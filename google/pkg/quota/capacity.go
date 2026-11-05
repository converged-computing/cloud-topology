package quota

import (
	"fmt"
	"math"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/storage"
)

// Check is one quota's contribution to a capacity result.
type Check struct {
	Metric     string            `json:"metric"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	PerNode    float64           `json:"per_node"`
	Limit      float64           `json:"limit"`
	Usage      *float64          `json:"usage,omitempty"`
	Found      bool              `json:"found"`
	Nodes      int               `json:"nodes"`
}

// Capacity is how many nodes of a machine type a project can run in a region.
type Capacity struct {
	MachineType string `json:"machine_type"`
	Model       string `json:"model"`
	Region      string `json:"region"`
	// Nodes is the node count the quotas allow; -1 means no quota limits it.
	Nodes int `json:"nodes"`
	// Binding is the quota that sets Nodes.
	Binding string `json:"binding,omitempty"`
	// Known is false when no rule covers the machine type.
	Known bool `json:"known"`
	// UsageKnown is false when a limiting quota has no usage data, so Nodes is an upper bound.
	UsageKnown bool `json:"usage_known"`
	// Inferred is true when the quota rule came from the snapshot, not Google's tables.
	Inferred bool    `json:"inferred,omitempty"`
	Note     string  `json:"note,omitempty"`
	Checks   []Check `json:"checks,omitempty"`
}

// Compute returns the capacity for one machine type, provisioning model and region.
// A regional quota missing from the snapshot counts as zero; a missing project-wide
// quota is ignored, since only some projects have one.
func Compute(snap *Snapshot, mt database.MachineType, model, region string) Capacity {
	c := Capacity{MachineType: mt.Name, Model: model, Region: region, Known: true, UsageKnown: true}

	usePreemptible := Preemptible(model) && hasPreemptible(snap, mt, region)
	if Preemptible(model) && !usePreemptible {
		c.Note = "no preemptible quota found; using standard quota"
	}
	reqs, known, reason, inferred := RequirementsFor(snap, mt, usePreemptible)
	c.Inferred = inferred
	if !known {
		c.Known, c.Note = false, reason
		return c
	}
	if len(reqs) == 0 {
		c.Note = reason
		return c
	}
	if inferred {
		c.Note = joinNote(c.Note, "quota rule inferred from snapshot")
	}

	nodes := math.MaxInt
	for _, r := range reqs {
		where := region
		if r.Global {
			where = ""
		}
		q, found := snap.Find(r.Metric, where, r.Dimensions)
		check := Check{Metric: r.Metric, Dimensions: r.Dimensions, PerNode: r.PerNode, Found: found, Limit: q.Limit, Usage: q.Usage}
		switch {
		case !found && r.Global:
			check.Nodes = -1
		case !found:
			check.Nodes = 0
		case q.Limit < 0 || r.PerNode <= 0:
			check.Nodes = -1
		default:
			available := q.Limit
			if q.Usage != nil {
				available -= *q.Usage
			}
			check.Nodes = max(0, int(math.Floor(available/r.PerNode)))
		}
		c.Checks = append(c.Checks, check)
		if check.Nodes >= 0 && check.Nodes < nodes {
			nodes = check.Nodes
			c.Binding = r.Metric
			c.UsageKnown = q.Usage != nil || !found
		}
	}
	if nodes == math.MaxInt {
		nodes = -1
	}
	c.Nodes = nodes
	return c
}

// hasPreemptible reports whether the snapshot holds a granted preemptible quota for
// the machine type's regional requirement.
func hasPreemptible(snap *Snapshot, mt database.MachineType, region string) bool {
	reqs, known, _, _ := RequirementsFor(snap, mt, true)
	if !known {
		return false
	}
	for _, r := range reqs {
		if r.Global {
			continue
		}
		if q, ok := snap.Find(r.Metric, region, r.Dimensions); ok && q.Limit != 0 {
			return true
		}
	}
	return false
}

// NodesFunc returns node counts for graph building in a region. Machine types with no
// quota rule get zero nodes and are reported to warn when it is set.
// StorageFunc returns a function giving the total GB quota allows for a disk type in a
// region (-1 for no limit), and whether a quota bounds it at all. Disk types without a
// known quota metric (the Hyperdisk types, whose quotas the snapshot does not fetch yet)
// report ok=false.
func (s *Snapshot) StorageFunc(region string) func(diskType string) (gb int64, ok bool) {
	return func(diskType string) (int64, bool) {
		metric := storage.QuotaMetric(diskType)
		if metric == "" {
			return 0, false
		}
		q, found := s.Find(metric, region, nil)
		if !found {
			return 0, true
		}
		if q.Limit < 0 || q.Limit > 1<<40 {
			return -1, true
		}
		usage := 0.0
		if q.Usage != nil {
			usage = *q.Usage
		}
		return int64(q.Limit - usage), true
	}
}

func (s *Snapshot) NodesFunc(region string, warn func(string)) func(database.MachineType, string) int {
	return func(mt database.MachineType, model string) int {
		c := Compute(s, mt, model, region)
		if !c.Known {
			if warn != nil {
				warn(fmt.Sprintf("%s: %s; left out", mt.Name, c.Note))
			}
			return 0
		}
		return c.Nodes
	}
}

// Units is how many vCPUs and GPUs quota allows for a machine series.
type Units struct {
	Series string `json:"series"`
	Model  string `json:"model"`
	Region string `json:"region"`
	// VCPUs and GPUs available; -1 means no quota limits them. For GPU series, which
	// need GPU quota only, VCPUs is -1 and vCPUs follow from the GPUs chosen.
	VCPUs      int    `json:"vcpus"`
	GPUs       int    `json:"gpus"`
	Known      bool   `json:"known"`
	UsageKnown bool   `json:"usage_known"`
	Note       string `json:"note,omitempty"`
}

// SeriesUnits returns the vCPUs and GPUs quota allows for a series, using one member
// machine type to find which quotas apply (all members of a series share them).
func SeriesUnits(s *Snapshot, series string, members []database.MachineType, model, region string) Units {
	u := Units{Series: series, Model: model, Region: region, VCPUs: -1, Known: true, UsageKnown: true}
	if len(members) == 0 {
		u.Known, u.Note = false, "no machine types in series"
		return u
	}
	mt := members[len(members)-1]
	usePreemptible := Preemptible(model) && hasPreemptible(s, mt, region)
	if Preemptible(model) && !usePreemptible {
		u.Note = "no preemptible quota found; using standard quota"
	}
	reqs, known, reason, inferred := RequirementsFor(s, mt, usePreemptible)
	if !known {
		u.Known, u.Note = false, reason
		return u
	}
	if inferred {
		u.Note = joinNote(u.Note, "quota rule inferred from snapshot")
	}
	if len(reqs) == 0 {
		u.GPUs, u.VCPUs, u.Note = 0, 0, reason
		return u
	}
	hasGPU := false
	for _, r := range reqs {
		if r.Kind == "gpu" {
			hasGPU = true
		}
	}
	if hasGPU {
		u.GPUs = -1
	}
	for _, r := range reqs {
		where := region
		if r.Global {
			where = ""
		}
		q, found := s.Find(r.Metric, where, r.Dimensions)
		avail := -1
		switch {
		case !found && r.Global:
		case !found:
			avail = 0
		case q.Limit >= 0:
			v := q.Limit
			if q.Usage != nil {
				v -= *q.Usage
			}
			avail = max(0, int(math.Floor(v)))
		}
		if avail < 0 {
			continue
		}
		target := &u.VCPUs
		if r.Kind == "gpu" {
			target = &u.GPUs
		}
		if *target < 0 || avail < *target {
			*target = avail
			u.UsageKnown = found && q.Usage != nil || !found
		}
	}
	return u
}

// UnitsFunc returns per-series vCPU and GPU limits for graph building in a region.
// Series with no quota rule are left out and reported to warn when it is set.
func (s *Snapshot) UnitsFunc(region string, warn func(string)) func(string, []database.MachineType, string) (int, int, bool) {
	return func(series string, members []database.MachineType, model string) (int, int, bool) {
		u := SeriesUnits(s, series, members, model, region)
		if !u.Known {
			if warn != nil {
				warn(fmt.Sprintf("%s: %s; left out", series, u.Note))
			}
			return 0, 0, false
		}
		return u.VCPUs, u.GPUs, true
	}
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
