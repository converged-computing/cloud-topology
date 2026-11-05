package storage

import (
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func TestAttachable(t *testing.T) {
	cases := []struct {
		mt   string
		disk string
		want bool
	}{
		{"n2-standard-8", "pd-standard", true}, {"n2-standard-8", "pd-extreme", true}, {"n2-standard-8", "hyperdisk-balanced", false},
		{"c4-standard-288", "pd-ssd", false}, {"c4-standard-288", "hyperdisk-balanced", true}, {"c4-standard-288", "hyperdisk-ml", true},
		{"c3-standard-176", "pd-ssd", true}, {"c3-standard-176", "pd-standard", false}, {"c3-standard-176", "hyperdisk-extreme", true},
		{"h3-standard-88", "pd-balanced", true}, {"h3-standard-88", "pd-ssd", false},
		{"a3-highgpu-8g", "pd-ssd", true}, {"a3-ultragpu-8g", "pd-ssd", false}, {"a3-ultragpu-8g", "hyperdisk-balanced", true}, {"a3-ultragpu-8g", "hyperdisk-throughput", false},
		{"e2-standard-32", "hyperdisk-balanced", false}, {"n4-standard-80", "hyperdisk-throughput", true}, {"n4-standard-80", "pd-balanced", false},
		{"t2a-standard-48", "pd-ssd", true}, {"n2-standard-8", "local-ssd", false}, {"n2-standard-8", "unknown", false},
	}
	for _, c := range cases {
		mt := database.MachineType{Name: c.mt, Family: database.FamilyOf(c.mt)}
		if got := Attachable(mt, c.disk); got != c.want {
			t.Errorf("%s attaches %s: %v, want %v", c.mt, c.disk, got, c.want)
		}
	}
	if QuotaMetric("pd-ssd") != "SSD_TOTAL_GB" || QuotaMetric("pd-standard") != "DISKS_TOTAL_GB" || QuotaMetric("hyperdisk-balanced") != "" {
		t.Error("quota metrics")
	}
	if VertexType("hyperdisk-balanced") != "hyperdisk_balanced" || len(Known()) != 8 || Known()[0] != "hyperdisk-balanced" {
		t.Error("vertex types or known list")
	}
	got := Compatible(database.MachineType{Name: "c4-standard-8", Family: "c4"}, []database.DiskType{{Name: "pd-ssd"}, {Name: "hyperdisk-balanced"}, {Name: "local-ssd"}})
	if len(got) != 1 || got[0].Name != "hyperdisk-balanced" {
		t.Errorf("compatible: %+v", got)
	}
}
