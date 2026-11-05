// Package network holds what Google documents about a machine type's network: per-VM
// egress bandwidth, the NICs a GPU machine type has and which of them are RDMA, and the
// compact placement groups a series supports. The Compute Engine API reports none of it,
// so it is encoded from the documentation, with the page each rule comes from.
//
// Sources, last checked 2026-11-05:
//   - https://docs.cloud.google.com/compute/docs/network-bandwidth
//     (per-series standard and Tier_1 egress caps; "generally 2 Gbps per vCPU")
//   - https://docs.cloud.google.com/compute/docs/gpus/gpu-network-bandwidth
//     (NIC counts and egress of accelerator-optimized machine types)
//   - https://docs.cloud.google.com/compute/docs/instances/placement-policies-overview
//     (compact placement: supported series and instances per policy by max distance)
package network

import (
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// NIC is one network interface, or a set of identical ones.
type NIC struct {
	// Kind is gvnic for ordinary NICs, rdma for RoCE NICs (A3 Ultra, A4, A4X, H4D),
	// tcpx and tcpxo for the GPU NICs of A3 High, A3 Edge and A3 Mega.
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	// Gbps is each NIC's egress bandwidth.
	Gbps int `json:"gbps"`
}

// Bandwidth is a machine type's documented egress.
type Bandwidth struct {
	// Gbps is the per-VM egress with standard networking; Tier1Gbps with Tier_1
	// networking (0 when the series has none).
	Gbps      int   `json:"gbps"`
	Tier1Gbps int   `json:"tier1_gbps,omitempty"`
	NICs      []NIC `json:"nics"`
	// Known is false when the series is not in the tables; Gbps is then the general
	// rule, 2 Gbps per vCPU, capped at 32.
	Known bool `json:"known"`
	// Rule says where the numbers come from.
	Rule string `json:"rule"`
}

// seriesCap is a series' per-VM egress caps from the network-bandwidth page.
type seriesCap struct {
	standard, tier1 int
	// nics is the physical NIC count for the dual-NIC fourth-generation series.
	nics int
}

var seriesCaps = map[string]seriesCap{
	"c4n": {400, 0, 2}, "c4": {100, 200, 1}, "c4d": {100, 200, 1}, "c4a": {50, 100, 1},
	"c3": {100, 200, 1}, "c3d": {100, 200, 1}, "c2": {32, 100, 1}, "c2d": {32, 100, 1},
	"e2": {16, 0, 1}, "h4d": {200, 0, 1}, "h3": {200, 0, 1}, "m4n": {400, 0, 2}, "m4": {100, 0, 1},
	"m3": {32, 100, 1}, "m2": {32, 0, 1}, "m1": {32, 0, 1}, "n4": {50, 0, 1}, "n4a": {50, 0, 1}, "n4d": {50, 0, 1},
	"n2": {32, 100, 1}, "n2d": {32, 100, 1}, "n1": {32, 0, 1}, "f1": {1, 0, 1}, "g1": {1, 0, 1}, "t2a": {32, 0, 1}, "t2d": {32, 0, 1},
	"x5": {200, 0, 1}, "x4": {100, 0, 1}, "z3": {100, 200, 1}, "z4d": {400, 0, 2}, "z4m": {400, 0, 2},
}

// gpuLayout is an accelerator-optimized machine type's NICs, from the GPU page.
var gpuLayouts = map[string][]NIC{
	"a3-highgpu-8g":        {{"gvnic", 1, 200}, {"tcpx", 4, 200}},
	"a3-edgegpu-8g":        {{"gvnic", 1, 80}, {"tcpx", 4, 80}},
	"a3-edgegpu-8g-nolssd": {{"gvnic", 1, 80}, {"tcpx", 4, 80}},
	"a3-megagpu-8g":        {{"gvnic", 1, 200}, {"tcpxo", 8, 200}},
	"a3-ultragpu-8g":       {{"gvnic", 2, 200}, {"rdma", 8, 400}},
	"a4-highgpu-8g":        {{"gvnic", 2, 200}, {"rdma", 8, 400}},
	"a4x-highgpu-4g":       {{"gvnic", 2, 200}, {"rdma", 4, 400}},
	"a4x-maxgpu-4g-metal":  {{"gvnic", 2, 200}, {"rdma", 4, 800}},
}

// gpuSingle is the egress of the single-NIC GPU machine types, by type.
var gpuSingle = map[string]int{
	"a3-highgpu-1g": 25, "a3-highgpu-2g": 50, "a3-highgpu-4g": 100,
	"a2-highgpu-1g": 24, "a2-highgpu-2g": 32, "a2-highgpu-4g": 50, "a2-highgpu-8g": 100, "a2-megagpu-16g": 100,
	"a2-ultragpu-1g": 24, "a2-ultragpu-2g": 32, "a2-ultragpu-4g": 50, "a2-ultragpu-8g": 100,
	"g2-standard-4": 10, "g2-standard-8": 16, "g2-standard-12": 16, "g2-standard-16": 32, "g2-standard-24": 32, "g2-standard-32": 32, "g2-standard-48": 50, "g2-standard-96": 100,
	"g4-standard-6": 20, "g4-standard-12": 20, "g4-standard-24": 20, "g4-standard-48": 50, "g4-standard-96": 100, "g4-standard-192": 200, "g4-standard-384": 400,
}

// Egress returns a machine type's documented network bandwidth and NICs.
func Egress(mt database.MachineType) Bandwidth {
	family := database.FamilyOf(mt.Name)
	if layout, ok := gpuLayouts[mt.Name]; ok {
		total := 0
		for _, n := range layout {
			total += n.Count * n.Gbps
		}
		return Bandwidth{Gbps: total, NICs: layout, Known: true, Rule: "gpu-network-bandwidth: NIC layout of " + mt.Name}
	}
	if gbps, ok := gpuSingle[mt.Name]; ok {
		return Bandwidth{Gbps: gbps, NICs: []NIC{{"gvnic", 1, gbps}}, Known: true, Rule: "gpu-network-bandwidth: egress of " + mt.Name}
	}
	if family == "h4d" {
		// Cloud RDMA and gVNIC share the 200 Gbps.
		return Bandwidth{Gbps: 200, NICs: []NIC{{"gvnic", 1, 200}, {"rdma", 1, 200}}, Known: true, Rule: "network-bandwidth: H4D, 200 Gbps shared by Cloud RDMA and gVNIC"}
	}
	c, ok := seriesCaps[family]
	if !ok {
		gbps := perVCPU(mt.VCPUs, 32)
		return Bandwidth{Gbps: gbps, NICs: []NIC{{"gvnic", 1, gbps}}, Rule: "general rule: 2 Gbps per vCPU, capped at 32 (series not in the table)"}
	}
	gbps := perVCPU(mt.VCPUs, c.standard)
	switch {
	case family == "e2" && mt.SharedCPU:
		gbps = 2
	case family == "n1" && mt.SharedCPU, family == "f1", family == "g1":
		gbps = 1
	case family == "n1" && mt.VCPUs == 1:
		gbps = 2
	}
	nics := []NIC{{"gvnic", 1, gbps}}
	if c.nics == 2 && gbps > c.standard/2 {
		nics = []NIC{{"gvnic", 2, gbps / 2}}
	}
	rule := "network-bandwidth: 2 Gbps per vCPU up to the " + strings.ToUpper(family) + " cap of " + strconv.Itoa(c.standard) + " Gbps"
	return Bandwidth{Gbps: gbps, Tier1Gbps: c.tier1, NICs: nics, Known: true, Rule: rule}
}

// perVCPU is the general rule: 2 Gbps per vCPU, at least 2, up to the series cap.
func perVCPU(vcpus, cap int) int {
	gbps := 2 * vcpus
	if gbps < 2 {
		gbps = 2
	}
	if gbps > cap {
		gbps = cap
	}
	return gbps
}

// Placement is what compact placement policies offer a series.
type Placement struct {
	// Supported is false for series the placement-policies page does not list.
	Supported bool `json:"supported"`
	// MaxInstances maps a max-distance value (1, 2 or 3) to the instances one policy can
	// hold at that distance; absent distances are not offered to the series.
	MaxInstances map[int]int `json:"max_instances,omitempty"`
}

// placementSeries lists, per max-distance, the families or series (where a family's
// series differ, such as A3) the page names. Distance 3 and "unspecified" hold 1,500;
// distance 2 holds 256 for A3 and 150 otherwise; distance 1 holds 22, or 16 for Z4M.
var placementSeries = map[int][]string{
	3: {"a4", "g4", "a3-megagpu", "a3-highgpu", "a3-edgegpu", "a2", "g2", "h4d", "h3", "c2d", "c2", "c4d", "c4", "c3d", "c3", "c4n", "m4n", "m4", "z4d", "z4m", "z3"},
	2: {"a4", "g4", "a3-ultragpu", "a3-megagpu", "a3-highgpu", "a3-edgegpu", "a2", "g2", "h4d", "h3", "c2d", "c2", "c4d", "c4", "c3d", "c3", "c4n", "m4n", "m4", "z4d", "z4m", "z3"},
	1: {"g4", "a3-edgegpu", "a2", "g2", "h4d", "h3", "c2d", "c2", "c4d", "c4", "c3d", "c3", "c4n", "m4n", "m4", "z4d", "z4m", "z3"},
}

// placementUnspecified are families supported only without a max distance (1,500 per policy).
var placementUnspecified = []string{"a4x", "a3-ultragpu", "n2d", "n2", "m3", "m2", "m1"}

// CompactPlacement returns the compact placement limits for a machine type's series.
func CompactPlacement(mt database.MachineType) Placement {
	family := database.FamilyOf(mt.Name)
	if family == "z3" && !database.IsMetal(mt.Name) {
		// Only Z3 metal is listed.
		return Placement{}
	}
	series := database.SeriesOf(mt.Name)
	listed := func(list []string) bool { return contains(list, family) || contains(list, series) }
	p := Placement{MaxInstances: map[int]int{}}
	for distance, families := range placementSeries {
		if !listed(families) {
			continue
		}
		switch {
		case distance == 3:
			p.MaxInstances[3] = 1500
		case distance == 2 && family == "a3":
			p.MaxInstances[2] = 256
		case distance == 2:
			p.MaxInstances[2] = 150
		case distance == 1 && family == "z4m":
			p.MaxInstances[1] = 16
		case distance == 1:
			p.MaxInstances[1] = 22
		}
	}
	if listed(placementUnspecified) {
		p.MaxInstances[0] = 1500
	}
	if len(p.MaxInstances) == 0 {
		return Placement{}
	}
	if _, ok := p.MaxInstances[3]; ok {
		p.MaxInstances[0] = 1500
	}
	p.Supported = true
	return p
}

// GroupSize returns how many nodes of a machine type one compact placement policy holds at
// the given max distance (0 for unspecified), or 0 when the series does not support it.
func GroupSize(mt database.MachineType, distance int) int {
	return CompactPlacement(mt).MaxInstances[distance]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
