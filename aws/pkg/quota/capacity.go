package quota

import (
	"fmt"
	"math"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/storage"
)

// Capacity is how many nodes of an instance type an account can run in a region.
type Capacity struct {
	MachineType string `json:"machine_type"`
	Model       string `json:"model"`
	Region      string `json:"region"`
	// Nodes is the node count the quota allows; -1 means no quota limits it.
	Nodes   int    `json:"nodes"`
	Binding string `json:"binding,omitempty"`
	// Known is false when no class covers the instance type.
	Known bool `json:"known"`
	// UsageKnown is always false: Service Quotas reports limits, not usage.
	UsageKnown bool    `json:"usage_known"`
	Limit      float64 `json:"limit,omitempty"`
	Note       string  `json:"note,omitempty"`
}

// Compute returns the capacity for one instance type, purchasing model and region. A
// type whose usage classes lack the model gets zero nodes.
func Compute(snap *Snapshot, mt database.MachineType, model, region string) Capacity {
	c := Capacity{MachineType: mt.Name, Model: model, Region: region, Known: true}
	class, reason := Class(mt)
	if class == "" {
		c.Known, c.Note = false, reason
		return c
	}
	switch model {
	case "on-demand", "spot":
	default:
		c.Known, c.Note = false, "unknown purchasing model "+model
		return c
	}
	if model == "spot" && !mt.Spot() {
		c.Note = "no spot usage class"
		return c
	}
	name := QuotaName(class, model)
	q, found := snap.Find(name, region)
	if !found {
		c.Note = "quota " + name + " not in snapshot"
		return c
	}
	c.Binding, c.Limit = name, q.Value
	if mt.VCPUs <= 0 {
		c.Nodes = -1
		return c
	}
	c.Nodes = int(math.Floor(q.Value / float64(mt.VCPUs)))
	return c
}

// NodesFunc returns node counts for graph building in a region. Instance types with no
// quota class get zero nodes and are reported to warn when it is set.
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

// StorageFunc returns a function giving the total GB quota allows for a volume type in
// a region (-1 for no limit) and whether a quota bounds it.
func (s *Snapshot) StorageFunc(region string) func(diskType string) (gb int64, ok bool) {
	return func(diskType string) (int64, bool) {
		name := storage.QuotaName(diskType)
		if name == "" {
			return 0, false
		}
		q, found := s.Find(name, region)
		if !found {
			return 0, true
		}
		return int64(q.Value * 1024), true
	}
}

// Units is how many vCPUs and accelerators quota allows for a series.
type Units struct {
	Series string `json:"series"`
	Model  string `json:"model"`
	Region string `json:"region"`
	// VCPUs quota allows; -1 means no quota limits them. GPUs is -1: EC2 quotas count
	// vCPUs for accelerated classes too, so accelerators follow from the vCPUs.
	VCPUs int    `json:"vcpus"`
	GPUs  int    `json:"gpus"`
	Known bool   `json:"known"`
	Note  string `json:"note,omitempty"`
}

// SeriesUnits returns the vCPUs quota allows for a series, using its largest member to
// find the class (all members share it).
func SeriesUnits(s *Snapshot, series string, members []database.MachineType, model, region string) Units {
	u := Units{Series: series, Model: model, Region: region, VCPUs: -1, GPUs: -1, Known: true}
	if len(members) == 0 {
		u.Known, u.Note = false, "no instance types in series"
		return u
	}
	c := Compute(s, members[len(members)-1], model, region)
	if !c.Known {
		u.Known, u.Note = false, c.Note
		return u
	}
	u.Note = c.Note
	if c.Binding == "" {
		u.VCPUs = 0
		return u
	}
	u.VCPUs = int(c.Limit)
	return u
}

// UnitsFunc returns per-series vCPU limits for graph building in a region. Series with
// no quota class are left out and reported to warn when it is set.
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
