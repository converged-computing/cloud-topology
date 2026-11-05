package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
)

func TestExport(t *testing.T) {
	dir := t.TempDir()
	snap, err := quota.Load("../../testdata/quota-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Export(dir, Options{
		Sources: []Source{{Label: "fixtures", Dir: "../../testdata"}, {Label: "quota", Dir: "../../testdata/quota-example"}},
		Quota:   snap, Regions: []string{"us-central1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Databases) != 2 || len(m.Graphs) != 6 {
		t.Fatalf("manifest: %+v", m)
	}
	for _, f := range []string{"index.html", "app.js", "charts.js", "style.css", "vendor/d3.v7.min.js", "data/manifest.json", "data/machine-types.json", "data/captures.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	var types []MachineType
	readJSON(t, filepath.Join(dir, "data/machine-types.json"), &types)
	var gpu *MachineType
	for i := range types {
		if types[i].Name == "test-gpu-2g" {
			gpu = &types[i]
		}
	}
	if gpu == nil || gpu.GPUs != 2 || gpu.Series != "test-gpu" || strings.Join(gpu.In, ",") != "fixtures" || strings.Join(gpu.Captured, ",") != "fixtures" {
		t.Errorf("test-gpu-2g: %+v", gpu)
	}
	var captures map[string]map[string]Capture
	readJSON(t, filepath.Join(dir, "data/captures.json"), &captures)
	c := captures["fixtures"]["test-gpu-2g"]
	if c.Summary == nil || len(c.Summary.Sockets) != 2 || len(c.Summary.GPUs) != 2 || c.Hash == "" {
		t.Errorf("capture summary: %+v", c.Summary)
	}
	if c.Hwloc == nil || c.Hwloc.Type != "Machine" || len(c.Hwloc.Children) == 0 {
		t.Errorf("hwloc tree: %+v", c.Hwloc)
	} else {
		pus, gpus := 0, 0
		var walkObj func(o *Object)
		walkObj = func(o *Object) {
			if o.Type == "PU" {
				pus++
			}
			if o.Type == "PCIDev" && o.Attrs["pci_type"] != "" {
				gpus++
			}
			for _, k := range o.Children {
				walkObj(k)
			}
		}
		walkObj(c.Hwloc)
		if pus != c.Summary.PUs || gpus < 2 {
			t.Errorf("hwloc tree: %d PUs (summary %d), %d PCI devices", pus, c.Summary.PUs, gpus)
		}
	}
	for _, g := range m.Graphs {
		var tree Tree
		readJSON(t, filepath.Join(dir, g.File), &tree)
		if tree.Vertices != g.Vertices || tree.Vertices == 0 {
			t.Errorf("%s: %d vertices in file, %d in manifest", g.File, tree.Vertices, g.Vertices)
		}
		if g.Database == "quota" && g.Level == "exact" {
			// n2-standard-8 is allowed more than one node by the example quota: the fold
			// must restore that count although the graph was built with one node.
			found := false
			walk(&tree, func(n *Tree) {
				if n.Type == "offer" && n.Properties["machine_type"] == "n2-standard-8" {
					walk(n, func(k *Tree) {
						if k.Type == "node" && k.Count > 1 && k.Vertices > k.Count {
							found = true
						}
					})
				}
			})
			if !found {
				t.Errorf("exact graph: n2-standard-8 nodes were not restored from quota")
			}
		}
	}

	// A single-file export inlines everything.
	single := t.TempDir()
	if _, err := Export(single, Options{Sources: []Source{{Label: "f", Dir: "../../testdata"}}, Levels: []string{"coarse"}, Single: true}); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(single, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="site-data"`, `"data/manifest.json"`, `"data/graphs/f/coarse-us-central1.json"`, "<style>", "function"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("single-file export lacks %s", want)
		}
	}
	entries, _ := os.ReadDir(single)
	if len(entries) != 1 {
		t.Errorf("single-file export wrote more than index.html: %v", entries)
	}
}

func TestCollapse(t *testing.T) {
	g := jgf.New()
	cluster := g.Add(jgf.Vertex{Type: "cluster", Path: "/gcp"}, "")
	offer := g.Add(jgf.Vertex{Type: "offer", Path: "/gcp/o", Properties: map[string]string{"machine_type": "x"}}, cluster)
	for n := 0; n < 2; n++ {
		node := g.Add(jgf.Vertex{Type: "node", Path: fmt.Sprintf("/gcp/o/node%d", n)}, offer)
		for c := 0; c < 4; c++ {
			g.Add(jgf.Vertex{Type: "core", Path: fmt.Sprintf("/gcp/o/node%d/core%d", n, c)}, node)
		}
		g.Add(jgf.Vertex{Type: "memory", Path: fmt.Sprintf("/gcp/o/node%d/memory0", n), Size: 16, Unit: "GB"}, node)
	}
	// A node with a different shape does not fold with the others.
	odd := g.Add(jgf.Vertex{Type: "node", Path: "/gcp/o/node2"}, offer)
	g.Add(jgf.Vertex{Type: "core", Path: "/gcp/o/node2/core0"}, odd)

	tree := Collapse(g, nil)
	if tree.Vertices != len(g.Nodes) {
		t.Fatalf("vertices %d, want %d", tree.Vertices, len(g.Nodes))
	}
	o := tree.Children[0]
	if o.Type != "offer" || len(o.Children) != 2 {
		t.Fatalf("offer children: %+v", o.Children)
	}
	nodes := o.Children[0]
	if nodes.Count != 2 || nodes.Last != "node1" || nodes.Vertices != 12 || len(nodes.Children) != 2 || nodes.Children[0].Count != 4 || nodes.Children[1].Size != 16 {
		t.Errorf("folded nodes: %+v cores %+v", nodes, nodes.Children[0])
	}
	if o.Children[1].Count != 1 || o.Children[1].Name != "node2" {
		t.Errorf("odd node: %+v", o.Children[1])
	}

	// With a node count, the offer's folded nodes take it.
	tree = Collapse(g, func(props map[string]string) int { return 10 })
	nodes = tree.Children[0].Children[0]
	if nodes.Count != 10 || nodes.Vertices != 60 || nodes.Last != "node9" {
		t.Errorf("restored nodes: %+v", nodes)
	}
}

func walk(t *Tree, fn func(*Tree)) {
	fn(t)
	for _, c := range t.Children {
		walk(c, fn)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
