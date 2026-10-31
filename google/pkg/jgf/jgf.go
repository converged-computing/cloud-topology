// Package jgf builds Fluxion JSON Graph Format (JGF) resource graphs from the database.
package jgf

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/topology"
)

// Metadata is the per-vertex metadata Fluxion reads from JGF.
type Metadata struct {
	Type       string            `json:"type"`
	Basename   string            `json:"basename"`
	Name       string            `json:"name"`
	ID         int               `json:"id"`
	UniqID     int               `json:"uniq_id"`
	Rank       int               `json:"rank"`
	Exclusive  bool              `json:"exclusive"`
	Unit       string            `json:"unit"`
	Size       int64             `json:"size"`
	Paths      map[string]string `json:"paths"`
	Properties map[string]string `json:"properties,omitempty"`
}

// Node is a JGF vertex.
type Node struct {
	ID       string   `json:"id"`
	Metadata Metadata `json:"metadata"`
}

// Edge is a JGF containment edge.
type Edge struct {
	Source   string            `json:"source"`
	Target   string            `json:"target"`
	Metadata map[string]string `json:"metadata"`
}

// Graph is a JGF document under construction.
type Graph struct {
	Nodes   []Node
	Edges   []Edge
	typeIDs map[string]int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{typeIDs: map[string]int{}}
}

// Vertex describes a vertex to add.
type Vertex struct {
	Type       string
	Path       string
	Size       int64
	Unit       string
	Exclusive  bool
	Properties map[string]string
}

// Add appends a vertex under parent ("" for the root) and returns its id.
func (g *Graph) Add(v Vertex, parent string) string {
	size := v.Size
	if size == 0 {
		size = 1
	}
	uid := len(g.Nodes)
	id := strconv.Itoa(uid)
	g.Nodes = append(g.Nodes, Node{ID: id, Metadata: Metadata{
		Type:       v.Type,
		Basename:   v.Type,
		Name:       v.Path[strings.LastIndex(v.Path, "/")+1:],
		ID:         g.typeIDs[v.Type],
		UniqID:     uid,
		Rank:       -1,
		Exclusive:  v.Exclusive,
		Unit:       v.Unit,
		Size:       size,
		Paths:      map[string]string{"containment": v.Path},
		Properties: v.Properties,
	}})
	g.typeIDs[v.Type]++
	if parent != "" {
		g.Edges = append(g.Edges, Edge{Source: parent, Target: id, Metadata: map[string]string{"subsystem": "containment"}})
	}
	return id
}

// MarshalJSON writes the graph as a JGF document.
func (g *Graph) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"graph": map[string]any{"nodes": g.Nodes, "edges": g.Edges}})
}

// Options selects what to put in a graph.
type Options struct {
	// Region to model, e.g. us-central1.
	Region string
	// MachineTypes to include; empty includes every machine type offered in the region.
	MachineTypes []string
	// Nodes is the number of potential nodes per offer (bounded in practice by quota).
	Nodes int
	// Models are provisioning models to create offers for, e.g. on-demand.
	Models []string
	// Capacity, when set, returns the node count quota allows per offer (-1 for no
	// limit); offers with no capacity are left out and Nodes is ignored.
	Capacity func(mt database.MachineType, model string) int
	// MaxNodes caps the nodes per offer when Capacity is set (default 64).
	MaxNodes int
}

func (o Options) withDefaults() Options {
	if o.Nodes <= 0 {
		o.Nodes = 1
	}
	if len(o.Models) == 0 {
		o.Models = []string{"on-demand"}
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = 64
	}
	return o
}

// nodesFor returns the number of potential nodes for an offer.
func (o Options) nodesFor(mt database.MachineType, model string) int {
	if o.Capacity == nil {
		return o.Nodes
	}
	n := o.Capacity(mt, model)
	if n < 0 || n > o.MaxNodes {
		return o.MaxNodes
	}
	return n
}

func selectMachineTypes(db *database.Database, opts Options) ([]database.MachineType, error) {
	if opts.Region == "" {
		return nil, fmt.Errorf("a region is required")
	}
	selected := db.Query(database.Filter{Region: opts.Region, Names: opts.MachineTypes})
	if len(selected) == 0 {
		return nil, fmt.Errorf("no machine types in region %s match", opts.Region)
	}
	if len(opts.MachineTypes) > 0 && len(selected) != len(opts.MachineTypes) {
		found := map[string]bool{}
		for _, mt := range selected {
			found[mt.Name] = true
		}
		var missing []string
		for _, n := range opts.MachineTypes {
			if !found[n] {
				missing = append(missing, n)
			}
		}
		return nil, fmt.Errorf("not offered in %s or not in database: %s", opts.Region, strings.Join(missing, ","))
	}
	return selected, nil
}

func offerProperties(mt database.MachineType, model string) map[string]string {
	props := map[string]string{
		"machine_type":       mt.Name,
		"family":             mt.Family,
		"provisioning_model": model,
		"vcpus":              strconv.Itoa(mt.VCPUs),
		"memory_mb":          strconv.Itoa(mt.MemoryMB),
	}
	var accels []string
	for _, a := range mt.Accelerators {
		accels = append(accels, fmt.Sprintf("%s:%d", a.Type, a.Count))
	}
	if len(accels) > 0 {
		props["accelerators"] = strings.Join(accels, ",")
	}
	if database.IsMetal(mt.Name) {
		props["metal"] = "true"
	}
	if mt.SharedCPU {
		props["shared_cpu"] = "true"
	}
	if mt.LocalSSD() {
		props["local_ssd"] = "true"
		if mt.LocalSSDPartitions > 0 {
			props["local_ssd_partitions"] = strconv.Itoa(mt.LocalSSDPartitions)
		}
		if mt.LocalSSDInterface != "" {
			props["local_ssd_interface"] = mt.LocalSSDInterface
		}
	}
	return props
}

// addLocalSSD adds one local_ssd vertex per bundled Local SSD partition under a node.
func addLocalSSD(g *Graph, node, nodePath string, mt database.MachineType) {
	for i := 0; i < mt.LocalSSDPartitions; i++ {
		var props map[string]string
		if mt.LocalSSDInterface != "" {
			props = map[string]string{"interface": mt.LocalSSDInterface}
		}
		g.Add(Vertex{Type: "local_ssd", Path: fmt.Sprintf("%s/local_ssd%d", nodePath, i), Exclusive: true, Properties: props}, node)
	}
}

func memoryGB(mb int) int64 {
	return int64(mb) / 1024
}

// BuildCoarse builds the hyperscheduler summary: site > region > offer > node, with
// each potential node holding pooled gpu, core and memory counts.
func BuildCoarse(db *database.Database, opts Options) (*Graph, error) {
	opts = opts.withDefaults()
	selected, err := selectMachineTypes(db, opts)
	if err != nil {
		return nil, err
	}
	g := New()
	root := g.Add(Vertex{Type: "cluster", Path: "/heuretes"}, "")
	site := g.Add(Vertex{Type: "site", Path: "/heuretes/gcp", Properties: map[string]string{"provider": "google-cloud"}}, root)
	regionPath := "/heuretes/gcp/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, site)
	for _, mt := range selected {
		var zones []string
		for _, z := range mt.Zones {
			if database.RegionOf(z) == opts.Region {
				zones = append(zones, z)
			}
		}
		sort.Strings(zones)
		for _, model := range opts.Models {
			count := opts.nodesFor(mt, model)
			if count == 0 {
				continue
			}
			props := offerProperties(mt, model)
			props["zones"] = strings.Join(zones, ",")
			offerPath := fmt.Sprintf("%s/%s.%s", regionPath, mt.Name, model)
			offer := g.Add(Vertex{Type: "offer", Path: offerPath, Properties: props}, region)
			for n := 0; n < count; n++ {
				nodePath := fmt.Sprintf("%s/node%d", offerPath, n)
				node := g.Add(Vertex{Type: "node", Path: nodePath}, offer)
				if gpus := mt.GPUs(); gpus > 0 {
					g.Add(Vertex{Type: "gpu", Path: nodePath + "/gpu0", Size: int64(gpus)}, node)
				}
				g.Add(Vertex{Type: "core", Path: nodePath + "/core0", Size: int64(mt.VCPUs)}, node)
				g.Add(Vertex{Type: "memory", Path: nodePath + "/memory0", Size: memoryGB(mt.MemoryMB), Unit: "GB"}, node)
				addLocalSSD(g, node, nodePath, mt)
			}
		}
	}
	return g, nil
}

// TopologyLoader returns the topology for a machine type and where it came from
// ("hwloc", or "derived:<machine type>"), or nil when there is none.
type TopologyLoader func(name string) (*topology.Topology, string, error)

// HwlocLoader loads captured hwloc topologies from the database; a capture needs both
// hwloc.xml and capture.json. A machine type without its own capture is derived from
// the smallest captured member of its series that is at least as large, assuming
// smaller sizes are cuts of larger ones.
func HwlocLoader(db *database.Database) TopologyLoader {
	parse := func(name string) (*topology.Topology, error) {
		path := db.HwlocPath(name)
		if path == "" {
			return nil, nil
		}
		return topology.ParseFile(path)
	}
	return func(name string) (*topology.Topology, string, error) {
		if _, ok := db.Captures[name]; ok {
			t, err := parse(name)
			return t, "hwloc", err
		}
		mt, ok := db.MachineTypes[name]
		if !ok {
			return nil, "", nil
		}
		var source *database.MachineType
		for _, other := range db.MachineTypes {
			if _, captured := db.Captures[other.Name]; !captured || database.SeriesOf(other.Name) != database.SeriesOf(name) {
				continue
			}
			if other.VCPUs < mt.VCPUs || other.GPUs() < mt.GPUs() {
				continue
			}
			if source == nil || other.VCPUs < source.VCPUs || other.VCPUs == source.VCPUs && other.Name < source.Name {
				o := other
				source = &o
			}
		}
		if source == nil {
			return nil, "", nil
		}
		src, err := parse(source.Name)
		if err != nil || src == nil {
			return nil, "", err
		}
		t, err := topology.Derive(src, mt.VCPUs, mt.GPUs(), int64(mt.MemoryMB)<<20)
		if err != nil {
			return nil, "", fmt.Errorf("deriving from %s: %w", source.Name, err)
		}
		return t, "derived:" + source.Name, nil
	}
}

// BuildExact builds the secretary's detailed graph: region > zone > offer > node >
// socket > cores, GPUs, NICs and memory. Nodes use the captured or derived hwloc
// topology when available, and otherwise one socket sized from the catalog (one core
// per vCPU).
func BuildExact(db *database.Database, opts Options, load TopologyLoader) (*Graph, error) {
	opts = opts.withDefaults()
	selected, err := selectMachineTypes(db, opts)
	if err != nil {
		return nil, err
	}
	topos := map[string]*topology.Topology{}
	sources := map[string]string{}
	for _, mt := range selected {
		if load == nil {
			continue
		}
		t, source, err := load(mt.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mt.Name, err)
		}
		if t != nil {
			topos[mt.Name], sources[mt.Name] = t, source
		}
	}

	g := New()
	root := g.Add(Vertex{Type: "cluster", Path: "/gcp", Properties: map[string]string{"provider": "google-cloud"}}, "")
	regionPath := "/gcp/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, root)

	zoneOffers := map[string][]database.MachineType{}
	for _, mt := range selected {
		for _, z := range mt.Zones {
			if database.RegionOf(z) == opts.Region {
				zoneOffers[z] = append(zoneOffers[z], mt)
			}
		}
	}
	zones := make([]string, 0, len(zoneOffers))
	for z := range zoneOffers {
		zones = append(zones, z)
	}
	sort.Strings(zones)

	for _, z := range zones {
		zonePath := regionPath + "/" + z
		zone := g.Add(Vertex{Type: "zone", Path: zonePath}, region)
		for _, mt := range zoneOffers[z] {
			for _, model := range opts.Models {
				count := opts.nodesFor(mt, model)
				if count == 0 {
					continue
				}
				props := offerProperties(mt, model)
				props["topology"] = "catalog"
				if src := sources[mt.Name]; src != "" {
					props["topology"] = src
				}
				offerPath := fmt.Sprintf("%s/%s.%s", zonePath, mt.Name, model)
				offer := g.Add(Vertex{Type: "offer", Path: offerPath, Properties: props}, zone)
				for n := 0; n < count; n++ {
					nodePath := fmt.Sprintf("%s/node%d", offerPath, n)
					node := g.Add(Vertex{Type: "node", Path: nodePath, Exclusive: true,
						Properties: map[string]string{"state": "potential"}}, offer)
					if t := topos[mt.Name]; t != nil {
						addHwlocNode(g, node, nodePath, t)
					} else {
						addCatalogNode(g, node, nodePath, mt)
					}
					addLocalSSD(g, node, nodePath, mt)
				}
			}
		}
	}
	return g, nil
}

func addCatalogNode(g *Graph, node, nodePath string, mt database.MachineType) {
	sockPath := nodePath + "/socket0"
	sock := g.Add(Vertex{Type: "socket", Path: sockPath}, node)
	for c := 0; c < mt.VCPUs; c++ {
		g.Add(Vertex{Type: "core", Path: fmt.Sprintf("%s/core%d", sockPath, c), Exclusive: true}, sock)
	}
	gpu := 0
	for _, a := range mt.Accelerators {
		for i := 0; i < a.Count; i++ {
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", sockPath, gpu), Exclusive: true,
				Properties: map[string]string{"model": a.Type}}, sock)
			gpu++
		}
	}
	g.Add(Vertex{Type: "memory", Path: sockPath + "/memory0", Size: memoryGB(mt.MemoryMB), Unit: "GB"}, sock)
}

func addHwlocNode(g *Graph, node, nodePath string, t *topology.Topology) {
	gpu, nic := 0, 0
	addDevices := func(parent, parentPath string, gpus, nics []topology.PCIDevice) {
		for _, d := range gpus {
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", parentPath, gpu), Exclusive: true,
				Properties: deviceProperties(d)}, parent)
			gpu++
		}
		for _, d := range nics {
			g.Add(Vertex{Type: "nic", Path: fmt.Sprintf("%s/nic%d", parentPath, nic), Exclusive: true,
				Properties: deviceProperties(d)}, parent)
			nic++
		}
	}
	core := 0
	for _, s := range t.Sockets {
		sockPath := fmt.Sprintf("%s/socket%d", nodePath, s.Index)
		sock := g.Add(Vertex{Type: "socket", Path: sockPath}, node)
		for c := 0; c < s.Cores; c++ {
			g.Add(Vertex{Type: "core", Path: fmt.Sprintf("%s/core%d", sockPath, core), Exclusive: true}, sock)
			core++
		}
		addDevices(sock, sockPath, s.GPUs, s.NICs)
		if gb := s.MemoryBytes >> 30; gb > 0 {
			g.Add(Vertex{Type: "memory", Path: fmt.Sprintf("%s/memory%d", sockPath, s.Index), Size: gb, Unit: "GB"}, sock)
		}
	}
	addDevices(node, nodePath, t.GPUs, t.NICs)
	if gb := t.MemoryBytes >> 30; gb > 0 {
		g.Add(Vertex{Type: "memory", Path: nodePath + "/memory", Size: gb, Unit: "GB"}, node)
	}
}

func deviceProperties(d topology.PCIDevice) map[string]string {
	props := map[string]string{"pci_busid": d.BusID, "vendor_id": d.VendorID, "device_id": d.DeviceID}
	if d.Vendor != "" {
		props["vendor"] = d.Vendor
	}
	return props
}
