package jgf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// UnitsFunc returns the vCPUs and GPUs quota allows for a series and provisioning
// model (-1 for no limit; vCPUs -1 for a GPU series means they follow the GPUs).
// ok is false when the series should be left out.
type UnitsFunc func(series string, members []database.MachineType, model string) (vcpus, gpus int, ok bool)

// SeriesOptions selects what to put in a series graph.
type SeriesOptions struct {
	Region string
	// Series to include; empty includes every series offered in the region.
	Series []string
	Models []string
	// Units sizes each offer from quota; without it each offer holds Nodes of the
	// series' largest machine type.
	Units UnitsFunc
	Nodes int
	// MaxVCPUs and MaxGPUs cap each offer (defaults 1024 and 64).
	MaxVCPUs int
	MaxGPUs  int
}

// BuildSeries builds the hyperscheduler graph by machine series:
// site > region > <series>.<model> > one vertex per vCPU and GPU plus memory chunks.
// Requests match on totals; a lower level chooses the concrete machine types.
func BuildSeries(db *database.Database, opts SeriesOptions) (*Graph, error) {
	if opts.Region == "" {
		return nil, fmt.Errorf("a region is required")
	}
	if len(opts.Models) == 0 {
		opts.Models = []string{"on-demand"}
	}
	if opts.Nodes <= 0 {
		opts.Nodes = 1
	}
	if opts.MaxVCPUs <= 0 {
		opts.MaxVCPUs = 1024
	}
	if opts.MaxGPUs <= 0 {
		opts.MaxGPUs = 64
	}
	groups := database.Series(db.Query(database.Filter{Region: opts.Region}))
	names := make([]string, 0, len(groups))
	for name := range groups {
		if len(opts.Series) == 0 || contains(opts.Series, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no machine series in region %s match", opts.Region)
	}

	g := New()
	root := AddRoot(g, ProviderName)
	regionPath := ProviderPath(ProviderName) + "/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, root)

	for _, name := range names {
		members := groups[name]
		shape := shapeOf(members)
		for _, model := range opts.Models {
			vcpus, gpus, source := 0, 0, "nodes"
			if opts.Units != nil {
				v, gp, ok := opts.Units(name, members, model)
				if !ok {
					continue
				}
				vcpus, gpus, source = v, gp, "quota"
				if gpus < 0 || gpus > opts.MaxGPUs {
					gpus = opts.MaxGPUs
				}
				if shape.maxGPUs == 0 {
					gpus = 0
				}
				if shape.maxGPUs > 0 && vcpus < 0 {
					vcpus = gpus * shape.vcpusPerGPU
				}
			} else {
				vcpus = shape.maxVCPUs * opts.Nodes
				gpus = shape.maxGPUs * opts.Nodes
			}
			if vcpus < 0 || vcpus > opts.MaxVCPUs {
				vcpus = opts.MaxVCPUs
			}
			if vcpus == 0 || (shape.maxGPUs > 0 && gpus == 0) {
				continue
			}

			props := map[string]string{
				"series":             name,
				"family":             members[0].Family,
				"provisioning_model": model,
				"sizes":              strings.Join(shape.names, ","),
				"max_vcpus_per_node": strconv.Itoa(shape.maxVCPUs),
				"max_gpus_per_node":  strconv.Itoa(shape.maxGPUs),
				"zones":              strings.Join(shape.zones(opts.Region), ","),
				"units":              source,
			}
			if database.IsMetal(members[0].Name) {
				props["metal"] = "true"
			}
			var ssd []string
			for _, mt := range members {
				if mt.LocalSSD() {
					ssd = append(ssd, mt.Name)
				}
				if mt.SharedCPU {
					props["shared_cpu"] = "true"
				}
			}
			if len(ssd) > 0 {
				props["local_ssd_sizes"] = strings.Join(ssd, ",")
			}
			offerPath := fmt.Sprintf("%s/%s.%s", regionPath, name, model)
			offer := g.Add(Vertex{Type: "offer", Path: offerPath, Properties: props}, region)
			for i := 0; i < vcpus; i++ {
				g.Add(Vertex{Type: "core", Path: fmt.Sprintf("%s/core%d", offerPath, i), Exclusive: true}, offer)
			}
			for i := 0; i < gpus; i++ {
				g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", offerPath, i), Exclusive: true}, offer)
			}
			chunk := max(1, shape.minGBPerVCPU)
			for i := 0; i < vcpus*shape.maxGBPerVCPU/chunk; i++ {
				g.Add(Vertex{Type: "memory", Path: fmt.Sprintf("%s/memory%d", offerPath, i), Size: int64(chunk), Unit: "GB", Exclusive: true}, offer)
			}
		}
	}
	return g, nil
}

type seriesShape struct {
	names        []string
	maxVCPUs     int
	maxGPUs      int
	vcpusPerGPU  int
	minGBPerVCPU int
	maxGBPerVCPU int
	members      []database.MachineType
}

func shapeOf(members []database.MachineType) seriesShape {
	s := seriesShape{members: members, minGBPerVCPU: -1}
	for _, mt := range members {
		s.names = append(s.names, mt.Name)
		s.maxVCPUs = max(s.maxVCPUs, mt.VCPUs)
		s.maxGPUs = max(s.maxGPUs, mt.GPUs())
		if g := mt.GPUs(); g > 0 {
			s.vcpusPerGPU = max(s.vcpusPerGPU, mt.VCPUs/g)
		}
		if mt.VCPUs > 0 {
			gb := mt.MemoryMB / 1024 / mt.VCPUs
			s.maxGBPerVCPU = max(s.maxGBPerVCPU, gb)
			if s.minGBPerVCPU < 0 || gb < s.minGBPerVCPU {
				s.minGBPerVCPU = gb
			}
		}
	}
	return s
}

func (s seriesShape) zones(region string) []string {
	seen := map[string]bool{}
	var out []string
	for _, mt := range s.members {
		for _, z := range mt.Zones {
			if database.RegionOf(z) == region && !seen[z] {
				seen[z] = true
				out = append(out, z)
			}
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
