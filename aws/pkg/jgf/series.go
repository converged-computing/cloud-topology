package jgf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	gjgf "github.com/converged-computing/google-cloud-topology/pkg/jgf"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
)

// UnitsFunc returns the vCPUs and accelerators quota allows for a series and purchasing
// model (-1 for no limit). ok is false when the series should be left out. EC2 quotas
// count vCPUs for accelerated classes too, so GPUs is normally -1 and accelerators follow
// from the vCPUs.
type UnitsFunc func(series string, members []database.MachineType, model string) (vcpus, gpus int, ok bool)

// SeriesOptions selects what to put in a series graph.
type SeriesOptions struct {
	Region string
	Series []string
	Models []string
	Units  UnitsFunc
	Nodes  int
	// MaxVCPUs and MaxGPUs cap each offer (defaults 1024 and 64).
	MaxVCPUs        int
	MaxGPUs         int
	IncludePrevious bool
}

// BuildSeries builds the hyperscheduler graph by instance family:
// site > region > <family>.<model> > one vertex per vCPU and accelerator plus memory chunks.
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
	groups := database.Series(db.Query(database.Filter{Region: opts.Region, CurrentOnly: !opts.IncludePrevious}))
	names := make([]string, 0, len(groups))
	for name := range groups {
		if len(opts.Series) == 0 || contains(opts.Series, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no instance series in region %s match", opts.Region)
	}
	g := gjgf.New()
	root := gjgf.AddRoot(g, Provider)
	regionPath := gjgf.ProviderPath(Provider) + "/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, root)

	for _, name := range names {
		members := groups[name]
		shape := shapeOf(members)
		for _, model := range opts.Models {
			if model == "spot" && !members[len(members)-1].Spot() {
				continue
			}
			vcpus, gpus, source := 0, 0, "nodes"
			if opts.Units != nil {
				v, gp, ok := opts.Units(name, members, model)
				if !ok {
					continue
				}
				vcpus, gpus, source = v, gp, "quota"
				if vcpus < 0 || vcpus > opts.MaxVCPUs {
					vcpus = opts.MaxVCPUs
				}
				if shape.maxGPUs > 0 {
					// Accelerators follow the vCPUs the class quota allows.
					byVCPU := vcpus / max(shape.vcpusPerGPU, 1)
					if gpus < 0 || gpus > byVCPU {
						gpus = byVCPU
					}
					if gpus > opts.MaxGPUs {
						gpus = opts.MaxGPUs
					}
					vcpus = min(vcpus, gpus*shape.vcpusPerGPU)
				} else {
					gpus = 0
				}
			} else {
				vcpus = shape.maxVCPUs * opts.Nodes
				gpus = shape.maxGPUs * opts.Nodes
			}
			if vcpus > opts.MaxVCPUs {
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
				"architecture":       members[0].Architecture,
			}
			if members[0].BareMetal {
				props["metal"] = "true"
			}
			var ssd []string
			for _, mt := range members {
				if mt.LocalSSD() {
					ssd = append(ssd, mt.Name)
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
		for _, z := range zonesIn(mt, region) {
			if !seen[z] {
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
