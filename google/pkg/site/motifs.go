package site

import (
	"fmt"
	"sort"
	"strings"
)

// Describe names a subtree by what it holds: "2 socket, 96 core, 8 gpu, 1 nic, 680 GB memory".
func Describe(t *Tree) string {
	counts := map[string]int64{}
	sizes := map[string]int64{}
	units := map[string]string{}
	var walk func(n *Tree, mult int64)
	walk = func(n *Tree, mult int64) {
		for _, c := range n.Children {
			k := mult * int64(c.Count)
			counts[c.Type] += k
			if c.Unit != "" {
				sizes[c.Type] += k * c.Size
				units[c.Type] = c.Unit
			}
			walk(c, k)
		}
	}
	walk(t, 1)
	order := []string{"region", "zone", "offer", "placement", "node", "socket", "core", "gpu", "nic", "memory", "local_ssd"}
	var parts []string
	seen := map[string]bool{}
	add := func(typ string) {
		if seen[typ] || counts[typ] == 0 {
			return
		}
		seen[typ] = true
		if sizes[typ] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s (%d %s)", counts[typ], typ, sizes[typ], units[typ]))
			return
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[typ], typ))
	}
	for _, typ := range order {
		add(typ)
	}
	var rest []string
	for typ := range counts {
		if !seen[typ] {
			rest = append(rest, typ)
		}
	}
	sort.Strings(rest)
	for _, typ := range rest {
		add(typ)
	}
	if len(parts) == 0 {
		if t.Size > 0 {
			return fmt.Sprintf("%s of %d %s", t.Type, t.Size, t.Unit)
		}
		return t.Type
	}
	return strings.Join(parts, ", ")
}

// Motif is one distinct subtree shape and where it occurs across the exported graphs.
type Motif struct {
	Signature   string `json:"signature"`
	Type        string `json:"type"`
	Description string `json:"description"`
	// Vertices is the number of vertices one occurrence stands for, subtree included.
	Vertices int `json:"vertices"`
	// Occurrences counts vertices with this shape across the graphs, parents' counts
	// included; MachineTypes, Zones, Regions and Graphs are where they are.
	Occurrences  int      `json:"occurrences"`
	MachineTypes []string `json:"machine_types"`
	Zones        []string `json:"zones"`
	Regions      []string `json:"regions"`
	Graphs       []string `json:"graphs"`
	// Levels are the graph levels (exact, coarse, series) the shape occurs in.
	Levels []string `json:"levels"`
	// Shape is the folded subtree itself, a few levels deep, for drawing.
	Shape *Glyph `json:"shape,omitempty"`
}

// Glyph is a compact folded subtree: type, count, size and children.
type Glyph struct {
	Type     string   `json:"t"`
	Count    int      `json:"c"`
	Size     int64    `json:"s,omitempty"`
	Unit     string   `json:"u,omitempty"`
	Children []*Glyph `json:"k,omitempty"`
}

// glyph copies a tree to the given depth.
func glyph(t *Tree, depth int) *Glyph {
	g := &Glyph{Type: t.Type, Count: t.Count, Size: t.Size, Unit: t.Unit}
	if depth > 0 {
		for _, c := range t.Children {
			g.Children = append(g.Children, glyph(c, depth-1))
		}
	}
	return g
}

// Motifs folds the shapes of every graph into one list: the same signature in two graphs
// is one motif. Only types that recur in interesting ways are kept (node and below, plus
// offers).
func Motifs(graphs map[string]*Tree, infos []GraphInfo) []Motif {
	byFile := map[string]GraphInfo{}
	for _, g := range infos {
		byFile[g.File] = g
	}
	keep := map[string]bool{"offer": true, "node": true, "socket": true}
	acc := map[string]*Motif{}
	sets := map[string]map[string]map[string]bool{}
	add := func(sig, kind, value string) {
		if sets[sig] == nil {
			sets[sig] = map[string]map[string]bool{}
		}
		if sets[sig][kind] == nil {
			sets[sig][kind] = map[string]bool{}
		}
		if value != "" {
			sets[sig][kind][value] = true
		}
	}
	for file, tree := range graphs {
		info := byFile[file]
		label := fmt.Sprintf("%s/%s/%s", info.Database, info.Level, info.Region)
		var walk func(t *Tree, mult int, zone, mt string)
		walk = func(t *Tree, mult int, zone, mt string) {
			k := mult * t.Count
			switch t.Type {
			case "zone":
				zone = t.Name
			case "offer":
				mt = t.Properties["machine_type"]
			}
			if keep[t.Type] {
				m := acc[t.Signature]
				if m == nil {
					m = &Motif{Signature: t.Signature, Type: t.Type, Description: Describe(t), Vertices: t.Vertices / max(t.Count, 1), Shape: glyph(t, 4)}
					acc[t.Signature] = m
				}
				m.Occurrences += k
				add(t.Signature, "mt", mt)
				add(t.Signature, "zone", zone)
				add(t.Signature, "region", info.Region)
				add(t.Signature, "graph", label)
				add(t.Signature, "level", info.Level)
			}
			for _, c := range t.Children {
				walk(c, k, zone, mt)
			}
		}
		walk(tree, 1, "", "")
	}
	var out []Motif
	for sig, m := range acc {
		m.MachineTypes, m.Zones, m.Regions, m.Graphs, m.Levels = keys(sets[sig]["mt"]), keys(sets[sig]["zone"]), keys(sets[sig]["region"]), keys(sets[sig]["graph"]), keys(sets[sig]["level"])
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].MachineTypes) != len(out[j].MachineTypes) {
			return len(out[i].MachineTypes) > len(out[j].MachineTypes)
		}
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MotifsFile is what the site's Motifs page loads: the motifs, and the Cypher queries that
// ask the same questions of the graph database (cmd/graphdb).
type MotifsFile struct {
	Motifs  []Motif           `json:"motifs"`
	Queries map[string]string `json:"queries"`
	// Note explains folding to the reader.
	Note string `json:"note"`
}
