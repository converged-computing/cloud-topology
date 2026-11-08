// Package site exports the databases, their captures and Fluxion graphs as a static web
// site: JSON for a browser-side interface that needs no server, so it can live on GitHub
// Pages.
package site

import (
	"bytes"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/topology"
)

//go:embed assets
var assets embed.FS

// Source is one database to export, under a label.
type Source struct {
	Label string
	Dir   string
}

// Options configures an export.
type Options struct {
	Sources []Source
	// Quota, when set, sizes graph offers by quota; otherwise each offer has Nodes nodes.
	Quota *quota.Snapshot
	Nodes int
	// Regions to build graphs for (default: every region in the databases).
	Regions []string
	// Levels of graph to build (default series, coarse and exact).
	Levels []string
	// Experiments are directories written by topology/experiment.sh; their report.json is
	// included and their arms are added as sources named <experiment>/<arm>.
	Experiments []string
	// Consistency is a study directory from experiments/scripts/consistency.sh; its rounds
	// are added as sources named round/<id> and its consistency.json, written by
	// experiments/cmd/consistency, is included.
	Consistency string
	// Kubernetes are runs of experiments/scripts/gke.sh, as name=dir: the run's gke/
	// directory is added as a database under the name, and its consistency.json and
	// latency.json (experiments/cmd/consistency and cmd/latency) are included.
	Kubernetes []string
	// Fidelity are scheduling-fidelity experiment directories (experiments/cmd/fidelity),
	// as name=dir; their fidelity.json is included.
	Fidelity []string
	// GraphSources limits graphs to these database labels (default: every database).
	GraphSources []string
	// Folded are graphs folded by another builder (aws/cmd/graph --fold), included as
	// they are under their own database label.
	Folded []ImportedGraph
	// Labels renames databases: a source, experiment arm or Kubernetes run whose label
	// is a key appears under the value, so the published site can call the deterministic
	// arm of the final experiment "compute-engine" next to "aws". GraphSources name the
	// new labels.
	Labels map[string]string
	// Hidden lists database labels (after Labels) kept out of the site's database lists:
	// an experiment arm that captured the same cloud again belongs on the experiment page,
	// not next to the clouds. Hidden databases stay reachable by label.
	Hidden []string
	// Single writes one self-contained index.html with the data and images inlined.
	Single bool
	// Log receives progress lines.
	Log func(string)
}

// ImportedGraph is a graph folded elsewhere, included in the export as it is.
type ImportedGraph struct {
	Database, Level, Region string
	Tree                    *Tree
}

// Manifest describes the export.
type Manifest struct {
	Generated string         `json:"generated"`
	Databases []DatabaseInfo `json:"databases"`
	Regions   []string       `json:"regions"`
	Levels    []string       `json:"levels"`
	Graphs    []GraphInfo    `json:"graphs"`
	// Merged holds, per level, every source's graph under one site root: what the graphs
	// page draws, with the provider and region subtrees to prune.
	Merged      []MergedGraph    `json:"merged,omitempty"`
	Experiments []ExperimentInfo `json:"experiments"`
	Kubernetes  []KubernetesInfo `json:"kubernetes,omitempty"`
	Fidelity    []FidelityInfo   `json:"fidelity,omitempty"`
	Quota       string           `json:"quota,omitempty"`
	// Consistency locates the consistency report, when a study or more than one database
	// with captures was exported.
	Consistency string `json:"consistency,omitempty"`
	// Motifs locates the motifs file, when graphs were exported.
	Motifs string `json:"motifs,omitempty"`
}

// DatabaseInfo summarizes one source.
type DatabaseInfo struct {
	Label        string `json:"label"`
	Dir          string `json:"dir"`
	MachineTypes int    `json:"machine_types"`
	Captures     int    `json:"captures"`
	Generated    string `json:"generated,omitempty"`
	// Hidden keeps the database out of the general lists (Options.Hidden).
	Hidden bool `json:"hidden,omitempty"`
	// Note is the database's NOTE.md, when present: what the reader should know about the
	// data, such as machine types that could not be captured and why.
	Note string `json:"note,omitempty"`
}

// GraphInfo locates one folded graph.
type GraphInfo struct {
	Database string `json:"database"`
	Provider string `json:"provider,omitempty"`
	Level    string `json:"level"`
	Region   string `json:"region"`
	// File is the merged graph of the level that holds this graph.
	File     string `json:"file"`
	Vertices int    `json:"vertices"`
	Edges    int    `json:"edges"`
}

// MergedGraph is one level's graphs from every source under a single site root, the
// provider vertices of each source's graph as its children.
type MergedGraph struct {
	Level    string      `json:"level"`
	File     string      `json:"file"`
	Vertices int         `json:"vertices"`
	Edges    int         `json:"edges"`
	Parts    []GraphInfo `json:"parts"`
}

// ExperimentInfo locates one experiment report.
type ExperimentInfo struct {
	Name string   `json:"name"`
	File string   `json:"file"`
	Arms []string `json:"arms"`
}

// KubernetesInfo locates one Kubernetes run's database and reports.
type KubernetesInfo struct {
	Name        string `json:"name"`
	Database    string `json:"database"`
	Consistency string `json:"consistency"`
	Latency     string `json:"latency"`
}

// FidelityInfo locates one scheduling-fidelity report, with its size for the index.
type FidelityInfo struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Graphs   int    `json:"graphs"`
	Policies int    `json:"policies"`
	Sessions int    `json:"sessions"`
	Jobs     int    `json:"jobs"`
}

// MachineType is the union view of one machine type across the sources.
type MachineType struct {
	database.MachineType
	Series   string   `json:"series"`
	GPUs     int      `json:"gpus"`
	Regions  []string `json:"regions"`
	Metal    bool     `json:"metal,omitempty"`
	LocalSSD bool     `json:"local_ssd,omitempty"`
	// In lists the databases that hold the machine type, and Captured those with a capture.
	In       []string `json:"in"`
	Captured []string `json:"captured,omitempty"`
}

// Capture is one capture with a structured summary of its topology.
type Capture struct {
	database.Capture
	Summary *Summary `json:"summary,omitempty"`
	// Hash identifies the topology's shape, to tell captures apart across databases.
	Hash string `json:"hash,omitempty"`
	// SVG is the image path, or in a single-file export the image itself.
	SVG string `json:"svg,omitempty"`
	// Hwloc is the captured object tree, for drawing the topology in the browser.
	Hwloc *topology.Object `json:"hwloc,omitempty"`
}

// Summary is the reduced view of a captured topology.
type Summary struct {
	Sockets  []SocketSummary `json:"sockets"`
	Cores    int             `json:"cores"`
	PUs      int             `json:"pus"`
	MemoryGB float64         `json:"memory_gb"`
	GPUs     []Device        `json:"gpus,omitempty"`
	NICs     []Device        `json:"nics,omitempty"`
}

// SocketSummary is one socket.
type SocketSummary struct {
	Index    int      `json:"index"`
	Cores    int      `json:"cores"`
	PUs      int      `json:"pus"`
	MemoryGB float64  `json:"memory_gb"`
	GPUs     []Device `json:"gpus,omitempty"`
	NICs     []Device `json:"nics,omitempty"`
}

// Device is a PCI device.
type Device struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor,omitempty"`
	BusID  string `json:"bus_id,omitempty"`
}

type loaded struct {
	Source
	db *database.Database
}

// Export writes the site into dir.
func Export(dir string, opts Options) (*Manifest, error) {
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}
	if opts.Levels == nil {
		opts.Levels = []string{"series", "coarse", "exact"}
	}
	relabel := func(l string) string {
		if v, ok := opts.Labels[l]; ok {
			return v
		}
		return l
	}
	var sources []Source
	for _, s := range opts.Sources {
		sources = append(sources, Source{Label: relabel(s.Label), Dir: s.Dir})
	}
	var experiments []ExperimentInfo
	for i, e := range opts.Experiments {
		name, dir, ok := strings.Cut(e, "=")
		if !ok {
			name, dir = filepath.Base(filepath.Clean(e)), e
		}
		e = dir
		opts.Experiments[i] = dir
		info := ExperimentInfo{Name: name, File: "data/experiments/" + name + ".json"}
		for _, arm := range []string{"agent", "deterministic"} {
			if _, err := os.Stat(filepath.Join(e, arm, "topology")); err == nil {
				sources = append(sources, Source{Label: relabel(name + "/" + arm), Dir: filepath.Join(e, arm)})
				info.Arms = append(info.Arms, relabel(name+"/"+arm))
			}
		}
		experiments = append(experiments, info)
	}
	var kubernetes []KubernetesInfo
	for _, k := range opts.Kubernetes {
		name, dir, ok := strings.Cut(k, "=")
		if !ok {
			name, dir = filepath.Base(filepath.Clean(k)), k
		}
		if _, err := os.Stat(filepath.Join(dir, "gke", "topology")); err != nil {
			return nil, fmt.Errorf("kubernetes run %s: %w", dir, err)
		}
		sources = append(sources, Source{Label: relabel(name), Dir: filepath.Join(dir, "gke")})
		base := "data/kubernetes/" + strings.ReplaceAll(name, "/", "_")
		kubernetes = append(kubernetes, KubernetesInfo{Name: name, Database: relabel(name), Consistency: base + "/consistency.json", Latency: base + "/latency.json"})
		opts.Kubernetes[len(kubernetes)-1] = dir
	}
	if opts.Consistency != "" {
		entries, err := os.ReadDir(filepath.Join(opts.Consistency, "rounds"))
		if err != nil {
			return nil, fmt.Errorf("consistency study: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				sources = append(sources, Source{Label: "round/" + e.Name(), Dir: filepath.Join(opts.Consistency, "rounds", e.Name())})
			}
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no databases to export")
	}

	var dbs []loaded
	for _, s := range sources {
		db, err := database.Load(s.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Label, err)
		}
		dbs = append(dbs, loaded{s, db})
		log(fmt.Sprintf("%s: %d machine types, %d captures", s.Label, len(db.MachineTypes), len(db.Captures)))
	}

	// A single-file export writes index.html alone; otherwise the data sits beside it.
	write := func(path string, v any) error {
		if opts.Single {
			return nil
		}
		return writeJSON(filepath.Join(dir, path), v)
	}
	subdirs := []string{"data/graphs", "data/experiments", "svg"}
	if opts.Single {
		subdirs = nil
	}
	for _, sub := range append(subdirs, ".") {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	m := &Manifest{Generated: time.Now().UTC().Format(time.RFC3339), Levels: opts.Levels, Experiments: experiments, Kubernetes: kubernetes}
	if opts.Quota != nil {
		m.Quota = opts.Quota.Project
	}

	// Machine types: the union across sources.
	types := map[string]*MachineType{}
	regions := map[string]bool{}
	for _, l := range dbs {
		info := DatabaseInfo{Label: l.Label, Dir: l.Dir, MachineTypes: len(l.db.MachineTypes), Captures: len(l.db.Captures), Hidden: contains(opts.Hidden, l.Label)}
		if note, err := os.ReadFile(filepath.Join(l.Dir, "NOTE.md")); err == nil {
			info.Note = strings.TrimSpace(string(note))
		}
		m.Databases = append(m.Databases, info)
		for name, mt := range l.db.MachineTypes {
			t, ok := types[name]
			if !ok {
				t = &MachineType{MachineType: mt, Series: database.SeriesOf(name), GPUs: mt.GPUs(), Regions: mt.Regions(),
					Metal: database.IsMetal(name), LocalSSD: mt.LocalSSD()}
				types[name] = t
			}
			t.In = append(t.In, l.Label)
			if _, ok := l.db.Captures[name]; ok {
				t.Captured = append(t.Captured, l.Label)
			}
			for _, r := range t.Regions {
				regions[r] = true
			}
		}
	}
	var list []*MachineType
	for _, t := range types {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if err := write("data/machine-types.json", list); err != nil {
		return nil, err
	}
	if len(opts.Regions) > 0 {
		m.Regions = opts.Regions
	} else {
		for r := range regions {
			m.Regions = append(m.Regions, r)
		}
		sort.Strings(m.Regions)
	}

	// Captures, with topology summaries and images.
	captures := map[string]map[string]*Capture{}
	for _, l := range dbs {
		captures[l.Label] = map[string]*Capture{}
		for name, c := range l.db.Captures {
			out := &Capture{Capture: c}
			xmlPath := filepath.Join(l.Dir, "topology", name, "hwloc.xml")
			if t, err := topology.ParseFile(xmlPath); err == nil {
				out.Summary = summarize(t)
				out.Hash = hashSummary(out.Summary)
			} else {
				log(fmt.Sprintf("%s/%s: %v", l.Label, name, err))
			}
			if tree, err := topology.ReadObjects(xmlPath); err == nil && tree != nil {
				out.Hwloc = tree
				// The capture fingerprint is the one notion of shape everywhere.
				out.Hash = topology.FingerprintOf(tree).ID()
			}
			if svg, err := os.ReadFile(filepath.Join(l.Dir, "topology", name, "topology.svg")); err == nil {
				if opts.Single {
					out.SVG = string(svg)
				} else {
					rel := filepath.Join("svg", safe(l.Label), name+".svg")
					if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
						return nil, err
					}
					if err := os.WriteFile(filepath.Join(dir, rel), svg, 0o644); err != nil {
						return nil, err
					}
					out.SVG = filepath.ToSlash(rel)
				}
			}
			captures[l.Label][name] = out
		}
	}
	if err := write("data/captures.json", captures); err != nil {
		return nil, err
	}

	// Graphs, folded, then merged per level under one site root.
	graphs := map[string]*Tree{}
	type part struct {
		GraphInfo
		tree *Tree
	}
	var parts []part
	imported := map[string]bool{}
	for _, f := range opts.Folded {
		imported[f.Database] = true
	}
	for _, l := range dbs {
		// A database whose graphs were folded elsewhere is not built again here.
		if len(opts.GraphSources) > 0 && !contains(opts.GraphSources, l.Label) || imported[l.Label] {
			continue
		}
		for _, region := range m.Regions {
			for _, level := range opts.Levels {
				tree, err := buildTree(l.db, level, region, opts)
				if err != nil {
					return nil, fmt.Errorf("%s %s graph for %s: %w", l.Label, level, region, err)
				}
				if tree == nil {
					continue
				}
				// The fold keeps every vertex; the graph is a containment tree, so edges are
				// vertices less the root.
				parts = append(parts, part{GraphInfo{Database: l.Label, Provider: providerOf(tree), Level: level, Region: region, Vertices: tree.Vertices, Edges: tree.Vertices - 1}, tree})
				log(fmt.Sprintf("%s: %s graph for %s, %d vertices", l.Label, level, region, tree.Vertices))
			}
		}
	}
	for _, f := range opts.Folded {
		if f.Tree == nil {
			continue
		}
		parts = append(parts, part{GraphInfo{Database: f.Database, Provider: providerOf(f.Tree), Level: f.Level, Region: f.Region, Vertices: f.Tree.Vertices, Edges: f.Tree.Vertices - 1}, f.Tree})
		if !contains(m.Regions, f.Region) {
			m.Regions = append(m.Regions, f.Region)
			sort.Strings(m.Regions)
		}
		log(fmt.Sprintf("%s: %s graph for %s, %d vertices (folded elsewhere)", f.Database, f.Level, f.Region, f.Tree.Vertices))
	}
	for _, level := range []string{"series", "coarse", "exact"} {
		root := &Tree{Type: "site", Name: jgf.RootPath[1:], Count: 1, Vertices: 1}
		merged := MergedGraph{Level: level, File: "data/graphs/" + level + ".json"}
		seen := map[string]int{}
		for _, p := range parts {
			if p.Level != level {
				continue
			}
			for _, c := range providerSubtrees(p.tree) {
				// Two sources with the same provider (an experiment's arms, say) stay apart.
				if seen[c.Name] > 0 {
					c = &Tree{Type: c.Type, Name: c.Name + " (" + p.Database + ")", Count: c.Count, Vertices: c.Vertices, Properties: c.Properties, Children: c.Children, Signature: c.Signature}
				}
				seen[c.Name]++
				root.Children = append(root.Children, c)
				root.Vertices += c.Vertices
			}
			p.File = merged.File
			merged.Parts = append(merged.Parts, p.GraphInfo)
			m.Graphs = append(m.Graphs, p.GraphInfo)
		}
		if len(merged.Parts) == 0 {
			continue
		}
		merged.Vertices, merged.Edges = root.Vertices, root.Vertices-1
		m.Merged = append(m.Merged, merged)
		graphs[merged.File] = root
		if !opts.Single {
			if err := os.MkdirAll(filepath.Join(dir, "data/graphs"), 0o755); err != nil {
				return nil, err
			}
		}
		if err := write(merged.File, root); err != nil {
			return nil, err
		}
		log(fmt.Sprintf("merged %s graph: %d vertices from %d source(s)", level, root.Vertices, len(merged.Parts)))
	}
	// Graph files from an earlier export that this one did not write would be stale.
	if !opts.Single {
		if entries, err := os.ReadDir(filepath.Join(dir, "data/graphs")); err == nil {
			for _, e := range entries {
				if name := "data/graphs/" + e.Name(); graphs[name] == nil {
					if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	// Experiments.
	reports := map[string]json.RawMessage{}
	for i, e := range opts.Experiments {
		data, err := os.ReadFile(filepath.Join(e, "report.json"))
		if err != nil {
			return nil, fmt.Errorf("experiment %s: %w (run make report first)", e, err)
		}
		reports[experiments[i].File] = data
		if !opts.Single {
			if err := os.WriteFile(filepath.Join(dir, experiments[i].File), data, 0o644); err != nil {
				return nil, err
			}
		}
	}

	// Kubernetes runs' reports, written by experiments/cmd/consistency and cmd/latency.
	for i, k := range kubernetes {
		for _, f := range []struct{ src, dst string }{{"consistency.json", k.Consistency}, {"latency.json", k.Latency}} {
			data, err := os.ReadFile(filepath.Join(opts.Kubernetes[i], f.src))
			if err != nil {
				return nil, fmt.Errorf("kubernetes run %s: %w (run make gke-report first)", k.Name, err)
			}
			reports[f.dst] = data
			if !opts.Single {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f.dst)), 0o755); err != nil {
					return nil, err
				}
				if err := os.WriteFile(filepath.Join(dir, f.dst), data, 0o644); err != nil {
					return nil, err
				}
			}
		}
	}

	// Scheduling-fidelity reports, written by experiments/cmd/fidelity.
	for _, f := range opts.Fidelity {
		name, fdir, ok := strings.Cut(f, "=")
		if !ok {
			name, fdir = filepath.Base(filepath.Clean(f)), f
		}
		data, err := os.ReadFile(filepath.Join(fdir, "fidelity.json"))
		if err != nil {
			return nil, fmt.Errorf("fidelity %s: %w (run make fidelity-report first)", name, err)
		}
		info := FidelityInfo{Name: name, File: "data/fidelity/" + strings.ReplaceAll(name, "/", "_") + ".json"}
		var shape struct {
			Graphs   []json.RawMessage `json:"graphs"`
			Sessions []struct {
				Policy string `json:"policy"`
			} `json:"sessions"`
			Jobs []json.RawMessage `json:"jobs"`
		}
		if err := json.Unmarshal(data, &shape); err != nil {
			return nil, fmt.Errorf("fidelity %s: %w", name, err)
		}
		policies := map[string]bool{}
		for _, s := range shape.Sessions {
			policies[s.Policy] = true
		}
		info.Graphs, info.Policies, info.Sessions, info.Jobs = len(shape.Graphs), len(policies), len(shape.Sessions), len(shape.Jobs)
		m.Fidelity = append(m.Fidelity, info)
		reports[info.File] = data
		if !opts.Single {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, info.File)), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, info.File), data, 0o644); err != nil {
				return nil, err
			}
		}
	}

	// The consistency study's report, written by experiments/cmd/consistency.
	if opts.Consistency != "" {
		data, err := os.ReadFile(filepath.Join(opts.Consistency, "consistency.json"))
		if err != nil {
			return nil, fmt.Errorf("consistency study: %w (run make consistency-report first)", err)
		}
		m.Consistency = "data/consistency.json"
		reports[m.Consistency] = data
		if !opts.Single {
			if err := os.WriteFile(filepath.Join(dir, m.Consistency), data, 0o644); err != nil {
				return nil, err
			}
		}
	}

	if err := write("data/manifest.json", m); err != nil {
		return nil, err
	}
	return m, writeAssets(dir, opts.Single, m, list, captures, graphs, reports)
}

// FoldedGraph builds one graph for a database and folds it, as the site export does.
func FoldedGraph(db *database.Database, level, region string, opts Options) (*Tree, error) {
	return buildTree(db, level, region, opts)
}

// buildTree builds one graph and folds it. Exact and coarse graphs are built with one node
// per offer and the fold restores the node count quota allows.
func buildTree(db *database.Database, level, region string, opts Options) (*Tree, error) {
	nodes := opts.Nodes
	if nodes == 0 {
		nodes = 1
	}
	warn := func(string) {}
	var g *jgf.Graph
	var err error
	switch level {
	case "series":
		so := jgf.SeriesOptions{Region: region, Nodes: nodes}
		if opts.Quota != nil {
			so.Units = opts.Quota.UnitsFunc(region, warn)
		}
		g, err = jgf.BuildSeries(db, so)
		if err != nil {
			return nil, err
		}
		return Collapse(g, nil), nil
	case "coarse", "exact":
		o := jgf.Options{Region: region, Nodes: nodes, MaxNodes: 1}
		var capacity func(database.MachineType, string) int
		if opts.Quota != nil {
			capacity = opts.Quota.NodesFunc(region, warn)
			o.Capacity = capacity
			o.Storage = opts.Quota.StorageFunc(region)
		}
		if level == "coarse" {
			g, err = jgf.BuildCoarse(db, o)
		} else {
			g, err = jgf.BuildExact(db, o, jgf.HwlocLoader(db))
		}
		if err != nil {
			return nil, err
		}
		count := func(props map[string]string) int {
			if capacity == nil {
				return nodes
			}
			mt, ok := db.MachineTypes[props["machine_type"]]
			if !ok {
				return 0
			}
			n := capacity(mt, props["provisioning_model"])
			if n < 0 || n > 64 {
				n = 64
			}
			return n
		}
		return Collapse(g, count), nil
	}
	return nil, fmt.Errorf("unknown level %q", level)
}

func summarize(t *topology.Topology) *Summary {
	s := &Summary{MemoryGB: gb(t.MemoryBytes), GPUs: devices(t.GPUs), NICs: devices(t.NICs)}
	for _, sock := range t.Sockets {
		s.Sockets = append(s.Sockets, SocketSummary{Index: sock.Index, Cores: sock.Cores, PUs: sock.PUs, MemoryGB: gb(sock.MemoryBytes),
			GPUs: devices(sock.GPUs), NICs: devices(sock.NICs)})
		s.Cores += sock.Cores
		s.PUs += sock.PUs
		s.MemoryGB += gb(sock.MemoryBytes)
		s.GPUs = append(s.GPUs, devices(sock.GPUs)...)
		s.NICs = append(s.NICs, devices(sock.NICs)...)
	}
	return s
}

func devices(ds []topology.PCIDevice) []Device {
	var out []Device
	for _, d := range ds {
		out = append(out, Device{Name: d.Name, Vendor: d.Vendor, BusID: d.BusID})
	}
	return out
}

func gb(b int64) float64 {
	return float64(b*10/(1<<30)) / 10
}

// hashSummary identifies a topology's shape: sockets, cores, PUs, memory and device
// names, without bus ids, which change between instances. Memory is taken to two
// significant digits, since the kernel's view of it varies by tens of MB between
// instances of the same machine type.
func hashSummary(s *Summary) string {
	h := sha1.New()
	for _, sock := range s.Sockets {
		fmt.Fprintf(h, "socket%d:%d/%d/%.2g;", sock.Index, sock.Cores, sock.PUs, sock.MemoryGB)
		for _, d := range sock.GPUs {
			fmt.Fprintf(h, "gpu=%s;", d.Name)
		}
		for _, d := range sock.NICs {
			fmt.Fprintf(h, "nic=%s;", d.Name)
		}
	}
	fmt.Fprintf(h, "mem=%.2g;", s.MemoryGB)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// providerSubtrees returns a graph's provider vertices: the children of its site root, or
// the root itself for a graph built before the site root existed.
func providerSubtrees(t *Tree) []*Tree {
	if t.Type == "site" && len(t.Children) > 0 {
		return t.Children
	}
	return []*Tree{t}
}

// providerOf names the cloud a graph describes, from its provider vertex.
func providerOf(t *Tree) string {
	for _, c := range providerSubtrees(t) {
		if p := c.Properties["provider"]; p != "" {
			return p
		}
		return c.Name
	}
	return ""
}

func safe(label string) string {
	return strings.NewReplacer("/", "_", " ", "_").Replace(label)
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// writeAssets copies the interface in; a single-file export inlines the data as well.
func writeAssets(dir string, single bool, m *Manifest, types []*MachineType, captures map[string]map[string]*Capture,
	graphs map[string]*Tree, reports map[string]json.RawMessage) error {
	html, err := fs.ReadFile(assets, "assets/index.html")
	if err != nil {
		return err
	}
	// Asset links carry the export time, so a browser never shows a stale script with new data.
	stamp := []byte(strings.NewReplacer(":", "", "-", "").Replace(m.Generated))
	for _, name := range []string{"style.css", "vendor/d3.v7.min.js", "charts.js", "app.js"} {
		html = bytes.Replace(html, []byte(`"`+name+`"`), append(append([]byte(`"`+name+`?v=`), stamp...), '"'), 1)
	}
	if !single {
		for _, name := range []string{"app.js", "charts.js", "style.css", "vendor/d3.v7.min.js", "vendor/d3.LICENSE"} {
			data, err := fs.ReadFile(assets, "assets/"+name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(dir, "index.html"), html, 0o644)
	}
	d3, _ := fs.ReadFile(assets, "assets/vendor/d3.v7.min.js")
	charts, _ := fs.ReadFile(assets, "assets/charts.js")
	app, _ := fs.ReadFile(assets, "assets/app.js")
	js := append(append(append(append([]byte{}, d3...), '\n'), charts...), append([]byte{'\n'}, app...)...)
	css, _ := fs.ReadFile(assets, "assets/style.css")
	files := map[string]any{"data/manifest.json": m, "data/machine-types.json": types, "data/captures.json": captures}
	for k, v := range graphs {
		files[k] = v
	}
	for k, v := range reports {
		files[k] = v
	}
	data, err := json.Marshal(files)
	if err != nil {
		return err
	}
	// </script> inside the data would end the tag early.
	data = bytes.ReplaceAll(data, []byte("</"), []byte(`<\/`))
	v := string(stamp)
	out := bytes.Replace(html, []byte(`<link rel="stylesheet" href="style.css?v=`+v+`">`), append(append([]byte("<style>\n"), css...), []byte("\n</style>")...), 1)
	out = bytes.Replace(out, []byte(`<script src="vendor/d3.v7.min.js?v=`+v+`"></script>`), nil, 1)
	out = bytes.Replace(out, []byte(`<script src="charts.js?v=`+v+`"></script>`), nil, 1)
	out = bytes.Replace(out, []byte(`<script src="app.js?v=`+v+`"></script>`),
		append(append(append([]byte("<script type=\"application/json\" id=\"site-data\">"), data...), []byte("</script>\n<script>\n")...), append(js, []byte("\n</script>")...)...), 1)
	return os.WriteFile(filepath.Join(dir, "index.html"), out, 0o644)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
