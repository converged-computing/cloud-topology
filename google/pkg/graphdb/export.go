// Package graphdb exports folded resource graphs as tables for a graph database: one row
// per folded entry, containment edges carrying counts, and a shapes table that names each
// distinct subtree. Identical subtrees share a signature, so a graph of millions of
// vertices becomes tens of thousands of rows and a motif query is a pattern over them.
package graphdb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/converged-computing/google-cloud-topology/pkg/site"
)

// Graph is one folded graph to export.
type Graph struct {
	// Database, Level and Region identify the graph; Provider is the cloud (gcp).
	Database, Level, Region, Provider string
	Tree                              *site.Tree
}

// Vertex is one folded entry: a vertex of the resource graph standing for Count
// identical siblings, each with Vertices/Count vertices beneath it.
type Vertex struct {
	ID        int64  `parquet:"id,plain"`
	Provider  string `parquet:"provider,plain"`
	Graph     string `parquet:"graph,plain"`
	Level     string `parquet:"level,plain"`
	Region    string `parquet:"region,plain"`
	Zone      string `parquet:"zone,plain"`
	Offer     string `parquet:"offer,plain"`
	Type      string `parquet:"type,plain"`
	Name      string `parquet:"name,plain"`
	Path      string `parquet:"path,plain"`
	Depth     int32  `parquet:"depth,plain"`
	Count     int64  `parquet:"count,plain"`
	Vertices  int64  `parquet:"vertices,plain"`
	Size      int64  `parquet:"size,plain"`
	Unit      string `parquet:"unit,plain"`
	Exclusive bool   `parquet:"exclusive,plain"`
	Signature string `parquet:"signature,plain"`
	ParentID  int64  `parquet:"parent_id,plain"`
	// Properties of the vertex, as "k=v;k=v", and the ones queries want as columns.
	Properties  string `parquet:"properties,plain"`
	MachineType string `parquet:"machine_type,plain"`
	Family      string `parquet:"family,plain"`
	Model       string `parquet:"provisioning_model,plain"`
	VCPUs       int64  `parquet:"vcpus,plain"`
	GPUs        int64  `parquet:"gpus,plain"`
	EgressGbps  int64  `parquet:"egress_gbps,plain"`
	Kind        string `parquet:"kind,plain"`
	DiskType    string `parquet:"disk_type,plain"`
	Topology    string `parquet:"topology,plain"`
}

// Edge is a containment edge from a folded entry to one of its children; Count is how
// many children of that shape each parent has.
type Edge struct {
	From  int64 `parquet:"from_id,plain"`
	To    int64 `parquet:"to_id,plain"`
	Count int64 `parquet:"count,plain"`
}

// Shape names a distinct subtree: what a vertex of that signature holds.
type Shape struct {
	Signature   string `parquet:"signature,plain"`
	Type        string `parquet:"type,plain"`
	Description string `parquet:"description,plain"`
	Vertices    int64  `parquet:"vertices,plain"`
	// Occurrences counts folded entries with this signature across the exported graphs.
	Occurrences int64 `parquet:"occurrences,plain"`
}

// Export writes vertices.parquet, edges.parquet and shapes.parquet into dir.
func Export(dir string, graphs []Graph) (vertices, edges, shapes int, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, 0, 0, err
	}
	var vs []Vertex
	var es []Edge
	shapeRows := map[string]*Shape{}
	var next int64 = 1
	for _, g := range graphs {
		if g.Tree == nil {
			continue
		}
		graph := fmt.Sprintf("%s/%s/%s", g.Database, g.Level, g.Region)
		var walk func(t *site.Tree, parent int64, depth int32, path, zone, offer string)
		walk = func(t *site.Tree, parent int64, depth int32, path, zone, offer string) {
			id := next
			next++
			path = path + "/" + t.Name
			switch t.Type {
			case "zone":
				zone = t.Name
			case "offer":
				offer = t.Name
			}
			v := Vertex{ID: id, Provider: g.Provider, Graph: graph, Level: g.Level, Region: g.Region, Zone: zone, Offer: offer,
				Type: t.Type, Name: t.Name, Path: path, Depth: depth, Count: int64(t.Count), Vertices: int64(t.Vertices), Size: t.Size, Unit: t.Unit,
				Exclusive: t.Exclusive, Signature: t.Signature, ParentID: parent, Properties: props(t.Properties)}
			v.MachineType, v.Family, v.Model = t.Properties["machine_type"], t.Properties["family"], t.Properties["provisioning_model"]
			v.VCPUs, v.GPUs, v.EgressGbps = atoi(t.Properties["vcpus"]), gpuCount(t.Properties["accelerators"]), atoi(t.Properties["egress_gbps"])
			v.Kind, v.DiskType, v.Topology = t.Properties["kind"], t.Properties["disk_type"], t.Properties["topology"]
			vs = append(vs, v)
			if parent != 0 {
				es = append(es, Edge{From: parent, To: id, Count: int64(t.Count)})
			}
			s := shapeRows[t.Signature]
			if s == nil {
				s = &Shape{Signature: t.Signature, Type: t.Type, Description: site.Describe(t), Vertices: int64(t.Vertices / max(t.Count, 1))}
				shapeRows[t.Signature] = s
			}
			s.Occurrences += int64(t.Count)
			for _, c := range t.Children {
				walk(c, id, depth+1, path, zone, offer)
			}
		}
		walk(g.Tree, 0, 0, "", "", "")
	}
	var ss []Shape
	for _, s := range shapeRows {
		ss = append(ss, *s)
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].Signature < ss[j].Signature })
	if err := parquet.WriteFile(filepath.Join(dir, "vertices.parquet"), vs); err != nil {
		return 0, 0, 0, err
	}
	if err := parquet.WriteFile(filepath.Join(dir, "edges.parquet"), es); err != nil {
		return 0, 0, 0, err
	}
	if err := parquet.WriteFile(filepath.Join(dir, "shapes.parquet"), ss); err != nil {
		return 0, 0, 0, err
	}
	return len(vs), len(es), len(ss), nil
}

func props(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ";")
}

func atoi(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

// gpuCount sums "type:count,type:count".
func gpuCount(s string) int64 {
	var total int64
	for _, part := range strings.Split(s, ",") {
		if i := strings.LastIndex(part, ":"); i > 0 {
			total += atoi(part[i+1:])
		}
	}
	return total
}

// Folded is a folded graph written by another provider's graph command (aws/cmd/graph
// --fold) for export alongside the graphs built here.
type Folded struct {
	Provider string     `json:"provider"`
	Database string     `json:"database"`
	Level    string     `json:"level"`
	Region   string     `json:"region"`
	Tree     *site.Tree `json:"tree"`
}

// ReadFolded reads a folded graph file.
func ReadFolded(path string) (Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Graph{}, err
	}
	var f Folded
	if err := json.Unmarshal(data, &f); err != nil {
		return Graph{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.Tree == nil {
		return Graph{}, fmt.Errorf("%s: no tree", path)
	}
	return Graph{Database: f.Database, Level: f.Level, Region: f.Region, Provider: f.Provider, Tree: f.Tree}, nil
}
