package quota

import (
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// Rules come from the CPU and GPU quota tables in
// https://docs.cloud.google.com/compute/resource-usage (last updated 2026-10-26).

// cpuMetrics are machine families whose vCPUs count against a named regional metric.
var cpuMetrics = map[string]string{
	"n1": "CPUS", "e2": "CPUS",
	"n2": "N2_CPUS", "n2d": "N2D_CPUS",
	"t2d": "T2D_CPUS", "t2a": "T2A_CPUS",
	"m1": "M1_CPUS", "m2": "M2_CPUS", "m3": "M3_CPUS",
	"c2": "C2_CPUS", "c2d": "C2D_CPUS", "c3": "C3_CPUS",
}

// vmFamilies are machine families whose vCPUs count against CPUS_PER_VM_FAMILY.
var vmFamilies = map[string]bool{
	"n4": true, "n4a": true, "n4d": true,
	"z3": true, "z4d": true, "z4m": true,
	"m4": true, "m4n": true, "x5": true, "x4": true,
	"h4d": true, "h3": true,
	"c3d": true, "c4": true, "c4a": true, "c4d": true, "c4n": true,
}

// gpuRule names the GPU quotas for a GPU machine family or series. A standard quota
// with an empty Metric means Google offers no on-demand quota for that GPU.
type gpuRule struct {
	Standard    metricRef
	Preemptible string
}

type metricRef struct {
	Metric    string
	GPUFamily string
}

// gpuRules are keyed by machine series (the first two name segments, e.g. a3-megagpu)
// or by family. These machine types need GPU quota only, not CPU quota.
var gpuRules = map[string]gpuRule{
	"a3-highgpu":  {metricRef{"GPU_FAMILY", "NVIDIA_H100"}, "PREEMPTIBLE_NVIDIA_H100_GPUS"},
	"a3-edgegpu":  {metricRef{"GPU_FAMILY", "NVIDIA_H100"}, "PREEMPTIBLE_NVIDIA_H100_GPUS"},
	"a3-megagpu":  {metricRef{"GPU_FAMILY", "NVIDIA_H100_MEGA"}, "PREEMPTIBLE_NVIDIA_H100_MEGA_GPUS"},
	"a3-ultragpu": {metricRef{}, "PREEMPTIBLE_NVIDIA_H200_GPUS"},
	"a2-highgpu":  {metricRef{"NVIDIA_A100_GPUS", ""}, "PREEMPTIBLE_NVIDIA_A100_GPUS"},
	"a2-megagpu":  {metricRef{"NVIDIA_A100_GPUS", ""}, "PREEMPTIBLE_NVIDIA_A100_GPUS"},
	"a2-ultragpu": {metricRef{"NVIDIA_A100_80GB_GPUS", ""}, "PREEMPTIBLE_NVIDIA_A100_80GB_GPUS"},
	"a4":          {metricRef{}, "PREEMPTIBLE_NVIDIA_B200_GPUS"},
	"a4x":         {metricRef{}, "PREEMPTIBLE_NVIDIA_GB200_GPUS"},
	"g2":          {metricRef{"GPU_FAMILY", "NVIDIA_L4"}, "PREEMPTIBLE_NVIDIA_L4_GPUS"},
	"g4":          {metricRef{"GPU_FAMILY", "NVIDIA_RTX_PRO_6000"}, "PREEMPTIBLE_NVIDIA_RTX_PRO_6000_GPUS"},
}

// Requirement is the amount of one quota a single node consumes.
type Requirement struct {
	// Kind is "cpu" or "gpu": the unit PerNode counts.
	Kind       string
	Metric     string
	Dimensions map[string]string
	Global     bool
	PerNode    float64
}

// Preemptible reports whether a provisioning model consumes preemptible quota.
// Spot and flex-start VMs do, per Google's preemptible quota documentation.
func Preemptible(model string) bool {
	return model == "spot" || model == "flex-start"
}

// Requirements returns the quotas one node of a machine type consumes under a
// provisioning model. usePreemptible selects preemptible metrics; Google falls back
// to standard quota when a project has none. ok is false when no rule covers the
// machine type; reason explains why.
func Requirements(mt database.MachineType, usePreemptible bool) (reqs []Requirement, ok bool, reason string) {
	family := mt.Family
	vcpus := float64(mt.VCPUs)
	gpus := float64(mt.GPUs())

	if rule, found := gpuRuleFor(mt.Name, family); found {
		if usePreemptible {
			reqs = append(reqs, Requirement{Kind: "gpu", Metric: rule.Preemptible, PerNode: gpus})
		} else if rule.Standard.Metric == "" {
			return nil, true, "no standard (on-demand) quota exists for this GPU"
		} else {
			r := Requirement{Kind: "gpu", Metric: rule.Standard.Metric, PerNode: gpus}
			if rule.Standard.GPUFamily != "" {
				r.Dimensions = map[string]string{"gpu_family": rule.Standard.GPUFamily}
			}
			reqs = append(reqs, r)
		}
		reqs = append(reqs, Requirement{Kind: "gpu", Metric: "GPUS_ALL_REGIONS", Global: true, PerNode: gpus})
		return reqs, true, ""
	}
	switch {
	case usePreemptible:
		reqs = append(reqs, Requirement{Kind: "cpu", Metric: "PREEMPTIBLE_CPUS", PerNode: vcpus})
	case cpuMetrics[family] != "":
		reqs = append(reqs, Requirement{Kind: "cpu", Metric: cpuMetrics[family], PerNode: vcpus})
	case vmFamilies[family]:
		reqs = append(reqs, Requirement{Kind: "cpu", Metric: "CPUS_PER_VM_FAMILY",
			Dimensions: map[string]string{"vm_family": strings.ToUpper(family)}, PerNode: vcpus})
	default:
		return nil, false, "no quota rule for machine family " + family
	}
	if gpus > 0 {
		return nil, false, "attached GPUs on family " + family + " have no quota rule"
	}
	reqs = append(reqs, Requirement{Kind: "cpu", Metric: "CPUS_ALL_REGIONS", Global: true, PerNode: vcpus})
	return reqs, true, ""
}

func gpuRuleFor(name, family string) (gpuRule, bool) {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) >= 2 {
		if rule, ok := gpuRules[parts[0]+"-"+parts[1]]; ok {
			return rule, true
		}
	}
	rule, ok := gpuRules[family]
	return rule, ok
}

// RequirementsFor is Requirements with a fallback for CPU families missing from
// Google's tables: if the snapshot holds a CPUS_PER_VM_FAMILY quota for the family,
// that quota is used and inferred is true.
func RequirementsFor(s *Snapshot, mt database.MachineType, usePreemptible bool) (reqs []Requirement, ok bool, reason string, inferred bool) {
	reqs, ok, reason = Requirements(mt, usePreemptible)
	if ok || mt.GPUs() > 0 || s == nil {
		return reqs, ok, reason, false
	}
	dims := map[string]string{"vm_family": strings.ToUpper(mt.Family)}
	if !s.hasMetric("CPUS_PER_VM_FAMILY", dims) {
		return reqs, ok, reason, false
	}
	vcpus := float64(mt.VCPUs)
	if usePreemptible {
		reqs = []Requirement{{Kind: "cpu", Metric: "PREEMPTIBLE_CPUS", PerNode: vcpus}}
	} else {
		reqs = []Requirement{{Kind: "cpu", Metric: "CPUS_PER_VM_FAMILY", Dimensions: dims, PerNode: vcpus}}
	}
	reqs = append(reqs, Requirement{Kind: "cpu", Metric: "CPUS_ALL_REGIONS", Global: true, PerNode: vcpus})
	return reqs, true, "", true
}
