// Package storage holds what Google documents about block storage: which disk types each
// machine series can attach, and which quota bounds each disk type's capacity. The disk
// types API lists sizes and zones; it does not say what attaches to what.
//
// Sources, last checked 2026-11-05:
//   - https://docs.cloud.google.com/compute/docs/disks/persistent-disks
//     (Persistent Disk machine series support table)
//   - https://docs.cloud.google.com/compute/docs/disks/hyperdisks
//     (Hyperdisk machine series support; series marked "contact your account team" are
//     left out, since they are not available without asking)
//   - https://docs.cloud.google.com/compute/resource-usage
//     (DISKS_TOTAL_GB covers Standard PD, SSD_TOTAL_GB the SSD-backed PD types)
package storage

import (
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// support lists, per disk type, the machine families or series (where a family's series
// differ, such as A3 H100 versus A3 Ultra) that can attach it.
var support = map[string][]string{
	"pd-standard":          {"a2", "c2", "c2d", "e2", "m1", "m2", "n1", "n2", "n2d", "t2a", "t2d"},
	"pd-balanced":          {"a2", "a3-highgpu", "a3-megagpu", "a3-edgegpu", "c2", "c2d", "c3", "c3d", "e2", "g2", "h3", "m1", "m2", "m3", "n1", "n2", "n2d", "t2a", "t2d", "z3"},
	"pd-ssd":               {"a2", "a3-highgpu", "a3-megagpu", "a3-edgegpu", "c2", "c2d", "c3", "c3d", "e2", "g2", "m1", "m2", "m3", "n1", "n2", "n2d", "t2a", "t2d", "z3"},
	"pd-extreme":           {"m1", "m2", "m3", "n2"},
	"hyperdisk-balanced":   {"a3", "a4", "a4x", "c3", "c3d", "c4", "c4a", "c4d", "c4n", "g4", "h3", "h4d", "m1", "m2", "m3", "m4", "m4n", "n4", "n4a", "n4d", "x4", "x5", "z3", "z4d", "z4m"},
	"hyperdisk-extreme":    {"a3", "a4", "a4x", "c3", "c3d", "c4", "c4a", "c4d", "c4n", "g4", "m1", "m2", "m3", "m4", "m4n", "n2", "x4", "x5", "z3", "z4d", "z4m"},
	"hyperdisk-throughput": {"a3-highgpu", "a3-megagpu", "a3-edgegpu", "a4x-maxgpu", "c3", "c3d", "c4", "c4a", "c4d", "c4n", "g2", "g4", "h3", "m3", "m4n", "n2", "n2d", "n4", "n4a", "n4d", "t2d", "x5", "z3", "z4d", "z4m"},
	"hyperdisk-ml":         {"a2", "a3", "a4", "a4x", "c3", "c3d", "c4", "c4a", "c4d", "c4n", "g2", "g4", "m4n", "n4", "n4a", "n4d", "x5", "z4d", "z4m"},
}

// quotaMetric maps a disk type to the regional Compute Engine quota that bounds it. The
// Hyperdisk types have per-type quotas in the Cloud Quotas API that the snapshot does not
// fetch yet, so they have no entry.
var quotaMetric = map[string]string{
	"pd-standard": "DISKS_TOTAL_GB",
	"pd-balanced": "SSD_TOTAL_GB",
	"pd-ssd":      "SSD_TOTAL_GB",
	"pd-extreme":  "SSD_TOTAL_GB",
}

// Attachable reports whether a machine type can attach a disk type. Local SSD is part of
// the machine type and handled separately; unknown disk types attach nowhere.
func Attachable(mt database.MachineType, diskType string) bool {
	list, ok := support[diskType]
	if !ok {
		return false
	}
	family, series := database.FamilyOf(mt.Name), database.SeriesOf(mt.Name)
	for _, s := range list {
		if s == family || s == series {
			return true
		}
	}
	return false
}

// Compatible returns the disk types (of those given) a machine type can attach.
func Compatible(mt database.MachineType, diskTypes []database.DiskType) []database.DiskType {
	var out []database.DiskType
	for _, dt := range diskTypes {
		if Attachable(mt, dt.Name) {
			out = append(out, dt)
		}
	}
	return out
}

// QuotaMetric returns the regional quota metric bounding a disk type's total size, or "".
func QuotaMetric(diskType string) string {
	return quotaMetric[diskType]
}

// VertexType is the resource type a disk type becomes in a graph: pd-ssd is pd_ssd.
func VertexType(diskType string) string {
	return strings.ReplaceAll(diskType, "-", "_")
}

// Known lists the disk types the rules know, sorted.
func Known() []string {
	out := make([]string, 0, len(support))
	for k := range support {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
