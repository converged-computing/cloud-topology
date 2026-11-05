package site

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/jgf"
)

// Tree is a Fluxion graph folded for display: identical sibling subtrees are merged into
// one entry with a count, so a node of 96 cores is one "core" entry with Count 96.
type Tree struct {
	Type string `json:"type"`
	// Name is the first merged vertex's name; Last the last one's when Count > 1.
	Name  string `json:"name"`
	Last  string `json:"last,omitempty"`
	Count int    `json:"count"`
	// Vertices is the number of JGF vertices this entry stands for, subtree included.
	Vertices   int               `json:"vertices"`
	Size       int64             `json:"size,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Exclusive  bool              `json:"exclusive,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Children   []*Tree           `json:"children,omitempty"`
	// Signature identifies the subtree's shape: entries with equal signatures are
	// interchangeable, which is what makes motif queries cheap.
	Signature string `json:"signature,omitempty"`
	key       string
}

type rawNode struct {
	meta     jgf.Metadata
	children []*rawNode
}

// Collapse folds a graph by its containment paths. nodes, when set, replaces the count
// of node vertices under an offer with the number quota allows (the graph may have been
// built with one node per offer to keep it small).
func Collapse(g *jgf.Graph, nodes func(offer map[string]string) int) *Tree {
	byPath := map[string]*rawNode{}
	var roots []*rawNode
	for _, n := range g.Nodes {
		path := n.Metadata.Paths["containment"]
		rn := &rawNode{meta: n.Metadata}
		byPath[path] = rn
		if parent, ok := byPath[path[:strings.LastIndex(path, "/")]]; ok && strings.Contains(path[1:], "/") {
			parent.children = append(parent.children, rn)
		} else {
			roots = append(roots, rn)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	root := roots[0]
	if len(roots) > 1 {
		root = &rawNode{meta: jgf.Metadata{Type: "graph", Name: "graph"}, children: roots}
	}
	return fold(root, nodes)
}

func fold(n *rawNode, nodes func(map[string]string) int) *Tree {
	t := &Tree{Type: n.meta.Type, Name: n.meta.Name, Count: 1, Vertices: 1, Size: n.meta.Size, Unit: n.meta.Unit,
		Exclusive: n.meta.Exclusive, Properties: n.meta.Properties}
	if t.Size == 1 {
		t.Size = 0
	}
	var kids []*Tree
	index := map[string]int{}
	for _, c := range n.children {
		ct := fold(c, nodes)
		if i, ok := index[ct.key]; ok {
			kids[i].Count += ct.Count
			kids[i].Last = ct.Name
			kids[i].Vertices += ct.Vertices
			continue
		}
		index[ct.key] = len(kids)
		kids = append(kids, ct)
	}
	if t.Type == "offer" && nodes != nil {
		if want := nodes(t.Properties); want > 0 {
			restoreNodes(kids, want)
		}
	}
	for _, k := range kids {
		t.Vertices += k.Vertices
	}
	t.Children = kids
	t.key = signature(t)
	t.Signature = t.key[:12]
	return t
}

// restoreNodes gives an offer built with one node the count quota allows: nodes directly
// under the offer, or under placement groups, which hold max_nodes each.
func restoreNodes(kids []*Tree, want int) {
	for _, k := range kids {
		switch k.Type {
		case "node":
			setCount(k, want, "node")
		case "placement":
			size, _ := strconv.Atoi(k.Properties["max_nodes"])
			if size < 1 {
				size = 1
			}
			groups := (want + size - 1) / size
			perGroup := want
			if perGroup > size {
				perGroup = size
			}
			for _, n := range k.Children {
				if n.Type == "node" {
					setCount(n, perGroup, "node")
				}
			}
			setCount(k, groups, "placement")
		}
	}
}

// setCount sets a folded entry's count and recomputes its vertices.
func setCount(t *Tree, count int, prefix string) {
	t.Count = count
	if count > 1 {
		t.Last = fmt.Sprintf("%s%d", prefix, count-1)
	} else {
		t.Last = ""
	}
	recompute(t)
}

// recompute sets an entry's vertices from its count and children.
func recompute(t *Tree) int {
	per := 1
	for _, c := range t.Children {
		per += recompute(c)
	}
	t.Vertices = t.Count * per
	return t.Vertices
}

// signature identifies a subtree's shape: type, size and properties, and the children's
// signatures with their counts. Names are left out so node0 and node1 fold together.
func signature(t *Tree) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%d|%s|%v|", t.Type, t.Size, t.Unit, t.Exclusive)
	keys := make([]string, 0, len(t.Properties))
	for k := range t.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s;", k, t.Properties[k])
	}
	for _, c := range t.Children {
		fmt.Fprintf(h, "[%s*%d]", c.key, c.Count)
	}
	return hex.EncodeToString(h.Sum(nil))
}
