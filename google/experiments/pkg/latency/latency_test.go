package latency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	phases := filepath.Join(t.TempDir(), "phases.tsv")
	os.WriteFile(phases, []byte("gke/cluster_create\t2026-11-06T00:00:00Z\t2026-11-06T00:05:00Z\t300\t0\ngke/cluster_delete\tx\ty\t120\t0\n"), 0o644)
	r, err := Compare([]Source{{"vm", "../../testdata/experiment-example/deterministic"}, {"gke", "../../testdata/experiment-example/agent"}}, phases)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Paths) != 2 || r.Paths[0].Captures != 2 || r.Paths[0].Median != 150 || r.Paths[1].Captures != 2 || r.Paths[1].Fixed["cluster_create"] != 300 || r.Common != 1 {
		t.Errorf("report: %+v", r)
	}
	md := r.Markdown()
	for _, want := range []string{"| gke | 2 |", "cluster_create 5m00s", "| e2-standard-32 | 2m10s (spot) | 2m00s (spot) |", "| n2-standard-128 | 2m30s (on-demand) | — |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
