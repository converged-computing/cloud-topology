package graphdb

import (
	"os"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/site"
)

func TestExport(t *testing.T) {
	db, err := database.Load("../../testdata/quota-example")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := quota.Load("../../testdata/quota-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := site.FoldedGraph(db, "exact", "us-central1", site.Options{Quota: snap, Nodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	vs, es, ss, err := Export(dir, []Graph{{Database: "q", Level: "exact", Region: "us-central1", Provider: "gcp", Tree: tree}})
	if err != nil {
		t.Fatal(err)
	}
	if vs == 0 || es != vs-1 || ss == 0 {
		t.Fatalf("rows %d, edges %d, shapes %d", vs, es, ss)
	}
	rows, err := parquet.ReadFile[Vertex](dir + "/vertices.parquet")
	if err != nil {
		t.Fatal(err)
	}
	var offers, nodes int
	for _, r := range rows {
		switch r.Type {
		case "offer":
			offers++
			if r.MachineType == "" || r.Zone == "" || r.Signature == "" || r.VCPUs == 0 || r.Graph != "q/exact/us-central1" {
				t.Errorf("offer row: %+v", r)
			}
		case "node":
			nodes++
			if r.Count < 1 || r.Vertices < r.Count || r.ParentID == 0 {
				t.Errorf("node row: %+v", r)
			}
		case "nic":
			if r.Kind == "" || r.Size == 0 {
				t.Errorf("nic row: %+v", r)
			}
		}
	}
	if offers == 0 || nodes == 0 {
		t.Errorf("offers %d nodes %d", offers, nodes)
	}
	shapes, err := parquet.ReadFile[Shape](dir + "/shapes.parquet")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range shapes {
		if s.Type == "node" && strings.Contains(s.Description, "core") && strings.Contains(s.Description, "Gbps)") && strings.Contains(s.Description, "GB)") && s.Occurrences > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("no node shape described: %+v", shapes)
	}
	if _, err := os.Stat(dir + "/edges.parquet"); err != nil {
		t.Error(err)
	}
	if !strings.Contains(LoadScript("/x"), "COPY Vertex FROM '/x/vertices.parquet'") || !strings.Contains(Schema, "CREATE REL TABLE IF NOT EXISTS Contains") {
		t.Error("schema or load script")
	}
}
