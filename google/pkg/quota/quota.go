// Package quota fetches a project's Compute Engine quotas and computes how many nodes
// of each machine type the project can run.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	cloudquotas "cloud.google.com/go/cloudquotas/apiv1"
	"cloud.google.com/go/cloudquotas/apiv1/cloudquotaspb"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

// Quota is one quota limit, optionally with current usage.
type Quota struct {
	// Metric is the quota name, e.g. N2_CPUS, CPUS_PER_VM_FAMILY or GPU_FAMILY.
	Metric string `json:"metric"`
	// Region is the region the quota applies to, or "" for project-wide quotas.
	Region string `json:"region,omitempty"`
	// Dimensions such as vm_family or gpu_family.
	Dimensions map[string]string `json:"dimensions,omitempty"`
	// Limit is the quota value; negative means unlimited.
	Limit float64 `json:"limit"`
	// Usage is current usage, or nil when the source does not report it.
	Usage *float64 `json:"usage,omitempty"`
	// Source is the API the value came from: compute or cloudquotas.
	Source string `json:"source"`
}

// Snapshot is a project's quotas at a point in time.
type Snapshot struct {
	Project string   `json:"project"`
	Regions []string `json:"regions"`
	Fetched string   `json:"fetched"`
	Quotas  []Quota  `json:"quotas"`
}

// Find returns the quota for a metric, region and dimensions, or false. Project-wide
// quotas match with region "".
func (s *Snapshot) Find(metric, region string, dims map[string]string) (Quota, bool) {
	for _, q := range s.Quotas {
		if q.Metric != metric || q.Region != region {
			continue
		}
		match := true
		for k, v := range dims {
			if q.Dimensions[k] != v {
				match = false
				break
			}
		}
		if match {
			return q, true
		}
	}
	return Quota{}, false
}

// hasMetric reports whether any region has a quota for metric with dims.
func (s *Snapshot) hasMetric(metric string, dims map[string]string) bool {
	for _, q := range s.Quotas {
		if q.Metric != metric {
			continue
		}
		match := true
		for k, v := range dims {
			if q.Dimensions[k] != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Unlimited returns quotas whose limit is negative (no limit).
func (s *Snapshot) Unlimited() []Quota {
	var out []Quota
	for _, q := range s.Quotas {
		if q.Limit < 0 {
			out = append(out, q)
		}
	}
	return out
}

// Load reads a snapshot file.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Fetch reads regional and project-wide quotas from the Compute Engine API, and
// per-VM-family and per-GPU-family limits from the Cloud Quotas API. The Cloud
// Quotas API reports limits only, so those entries have no usage.
func Fetch(ctx context.Context, project string, regions []string) (*Snapshot, error) {
	snap := &Snapshot{Project: project, Regions: regions, Fetched: time.Now().UTC().Format(time.RFC3339)}

	regionsClient, err := compute.NewRegionsRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	defer regionsClient.Close()
	for _, region := range regions {
		r, err := regionsClient.Get(ctx, &computepb.GetRegionRequest{Project: project, Region: region})
		if err != nil {
			return nil, fmt.Errorf("region %s quotas: %w", region, err)
		}
		snap.Quotas = append(snap.Quotas, fromCompute(r.GetQuotas(), region)...)
	}

	projectsClient, err := compute.NewProjectsRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	defer projectsClient.Close()
	p, err := projectsClient.Get(ctx, &computepb.GetProjectRequest{Project: project})
	if err != nil {
		return nil, fmt.Errorf("project quotas: %w", err)
	}
	snap.Quotas = append(snap.Quotas, fromCompute(p.GetQuotas(), "")...)

	quotasClient, err := cloudquotas.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	defer quotasClient.Close()
	it := quotasClient.ListQuotaInfos(ctx, &cloudquotaspb.ListQuotaInfosRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global/services/compute.googleapis.com", project),
	})
	var infos []*cloudquotaspb.QuotaInfo
	for {
		info, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing Cloud Quotas: %w", err)
		}
		infos = append(infos, info)
	}
	snap.Quotas = append(snap.Quotas, FromQuotaInfos(infos, regions)...)
	return snap, nil
}

func fromCompute(quotas []*computepb.Quota, region string) []Quota {
	var out []Quota
	for _, q := range quotas {
		usage := q.GetUsage()
		out = append(out, Quota{Metric: q.GetMetric(), Region: region, Limit: q.GetLimit(), Usage: &usage, Source: "compute"})
	}
	return out
}

// FromQuotaInfos keeps per-VM-family and per-GPU-family regional limits, named
// CPUS_PER_VM_FAMILY and GPU_FAMILY as in Google's allocation quota tables. Entries
// without a region apply to their applicable locations (or every requested region);
// an entry for a specific region overrides them.
func FromQuotaInfos(infos []*cloudquotaspb.QuotaInfo, regions []string) []Quota {
	type key struct{ metric, region, family string }
	values := map[key]float64{}
	specific := map[key]bool{}
	var order []key
	for _, info := range infos {
		var metric, dim string
		switch {
		case slices.Contains(info.GetDimensions(), "vm_family"):
			metric, dim = "CPUS_PER_VM_FAMILY", "vm_family"
		case slices.Contains(info.GetDimensions(), "gpu_family"):
			metric, dim = "GPU_FAMILY", "gpu_family"
		default:
			continue
		}
		for _, d := range info.GetDimensionsInfos() {
			family := d.GetDimensions()[dim]
			if family == "" {
				continue
			}
			region := d.GetDimensions()["region"]
			targets := []string{region}
			if region == "" {
				targets = d.GetApplicableLocations()
				if len(targets) == 0 {
					targets = regions
				}
			}
			for _, r := range targets {
				if len(regions) > 0 && !slices.Contains(regions, r) {
					continue
				}
				k := key{metric, r, family}
				if specific[k] && region == "" {
					continue
				}
				if _, seen := values[k]; !seen {
					order = append(order, k)
				}
				values[k] = float64(d.GetDetails().GetValue())
				if region != "" {
					specific[k] = true
				}
			}
		}
	}
	var out []Quota
	for _, k := range order {
		dim := "vm_family"
		if k.metric == "GPU_FAMILY" {
			dim = "gpu_family"
		}
		out = append(out, Quota{Metric: k.metric, Region: k.region, Dimensions: map[string]string{dim: k.family},
			Limit: values[k], Source: "cloudquotas"})
	}
	return out
}
