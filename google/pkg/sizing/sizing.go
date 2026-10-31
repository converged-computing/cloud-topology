// Package sizing lists the concrete machine types and node counts that can satisfy a
// request expressed as totals, so a lower-level agent can choose among them.
package sizing

import (
	"sort"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// Request is a total amount of resources, independent of machine size.
type Request struct {
	VCPUs    int `json:"vcpus"`
	GPUs     int `json:"gpus"`
	MemoryGB int `json:"memory_gb"`
	// MaxNodes rejects options needing more nodes; 0 means no limit.
	MaxNodes int `json:"max_nodes,omitempty"`
}

// Limits are the vCPUs and GPUs quota allows; -1 means unlimited.
type Limits struct {
	VCPUs int `json:"vcpus"`
	GPUs  int `json:"gpus"`
}

// Option is one way to satisfy a request: Count nodes of one machine type.
type Option struct {
	MachineType string `json:"machine_type"`
	Count       int    `json:"count"`
	VCPUs       int    `json:"vcpus"`
	GPUs        int    `json:"gpus"`
	MemoryGB    int    `json:"memory_gb"`
	// ExcessVCPUs is how many more vCPUs than requested the option provides.
	ExcessVCPUs int `json:"excess_vcpus"`
}

// Options returns the homogeneous options from a series that satisfy the request
// within limits, fewest nodes first, then least excess.
func Options(members []database.MachineType, req Request, lim Limits) []Option {
	var out []Option
	for _, mt := range members {
		v, g, m := mt.VCPUs, mt.GPUs(), mt.MemoryMB/1024
		if v <= 0 || (req.GPUs > 0 && g == 0) || (req.MemoryGB > 0 && m == 0) {
			continue
		}
		count := max(1, ceilDiv(req.VCPUs, v))
		if req.GPUs > 0 {
			count = max(count, ceilDiv(req.GPUs, g))
		}
		if req.MemoryGB > 0 {
			count = max(count, ceilDiv(req.MemoryGB, m))
		}
		if req.MaxNodes > 0 && count > req.MaxNodes {
			continue
		}
		if lim.VCPUs >= 0 && count*v > lim.VCPUs {
			continue
		}
		if g > 0 && lim.GPUs >= 0 && count*g > lim.GPUs {
			continue
		}
		out = append(out, Option{
			MachineType: mt.Name,
			Count:       count,
			VCPUs:       count * v,
			GPUs:        count * g,
			MemoryGB:    count * m,
			ExcessVCPUs: count*v - req.VCPUs,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count < out[j].Count
		}
		return out[i].ExcessVCPUs < out[j].ExcessVCPUs
	})
	return out
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
