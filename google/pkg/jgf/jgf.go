// Package jgf builds Fluxion JSON Graph Format (JGF) resource graphs from the database.
package jgf

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/network"
	"github.com/converged-computing/google-cloud-topology/pkg/storage"
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
	// Storage, when set, returns the GB quota allows for a disk type in the region (-1 for
	// no limit) and whether a quota bounds it; pools without one are sized at the disk
	// type's largest disk. Without Storage every pool is sized that way.
	Storage func(diskType string) (gb int64, ok bool)
	// PlacementDistance is the compact placement max distance that sizes the exact
	// graph's placement groups: 0 (the default) is an unspecified distance, which every
	// series with compact placement supports; 1, 2 and 3 are closer and support fewer.
	PlacementDistance int
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
				bw := network.Egress(mt)
				g.Add(Vertex{Type: "nic", Path: nodePath + "/nic0", Size: int64(bw.Gbps), Unit: "Gbps", Properties: map[string]string{"kinds": nicKinds(bw)}}, node)
				addLocalSSD(g, node, nodePath, mt)
			}
			// Storage the machine type can attach, offered in any of the offer's zones.
			seen := map[string]database.DiskType{}
			for _, z := range mt.Zones {
				if database.RegionOf(z) == opts.Region {
					for _, dt := range db.DiskTypesIn(z) {
						seen[dt.Name] = dt
					}
				}
			}
			var disks []database.DiskType
			for _, dt := range seen {
				disks = append(disks, dt)
			}
			sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
			addStoragePools(g, offer, offerPath, mt, disks, opts)
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
				bw := network.Egress(mt)
				props["egress_gbps"] = strconv.Itoa(bw.Gbps)
				if bw.Tier1Gbps > 0 {
					props["tier1_gbps"] = strconv.Itoa(bw.Tier1Gbps)
				}
				props["bandwidth_rule"] = bw.Rule
				// Compact placement: series Google lists get groups of the documented size;
				// others get groups of one, so a request for several nodes in one group can
				// only match where co-location is possible.
				groupSize := network.GroupSize(mt, opts.PlacementDistance)
				compact := groupSize > 0
				if !compact {
					groupSize = 1
					props["placement"] = "none"
				} else {
					props["placement"] = fmt.Sprintf("compact:max-distance=%d,max-nodes=%d", opts.PlacementDistance, groupSize)
				}
				offerPath := fmt.Sprintf("%s/%s.%s", zonePath, mt.Name, model)
				offer := g.Add(Vertex{Type: "offer", Path: offerPath, Properties: props}, zone)
				var group, groupPath string
				for n := 0; n < count; n++ {
					if n%groupSize == 0 {
						groupPath = fmt.Sprintf("%s/placement%d", offerPath, n/groupSize)
						gp := map[string]string{"compact": strconv.FormatBool(compact), "max_nodes": strconv.Itoa(groupSize)}
						if compact {
							gp["max_distance"] = strconv.Itoa(opts.PlacementDistance)
						}
						group = g.Add(Vertex{Type: "placement", Path: groupPath, Properties: gp}, offer)
					}
					nodePath := fmt.Sprintf("%s/node%d", groupPath, n)
					node := g.Add(Vertex{Type: "node", Path: nodePath, Exclusive: true,
						Properties: map[string]string{"state": "potential"}}, group)
					if t := topos[mt.Name]; t != nil {
						addHwlocNode(g, node, nodePath, t, bw)
					} else {
						addCatalogNode(g, node, nodePath, mt, bw)
					}
					addLocalSSD(g, node, nodePath, mt)
				}
				addStoragePools(g, offer, offerPath, mt, db.DiskTypesIn(z), opts)
			}
		}
	}
	return g, nil
}

func addCatalogNode(g *Graph, node, nodePath string, mt database.MachineType, bw network.Bandwidth) {
	sockPath := nodePath + "/socket0"
	sock := g.Add(Vertex{Type: "socket", Path: sockPath}, node)
	addRuleNICs(g, node, nodePath, bw, nil, 0)
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

func addHwlocNode(g *Graph, node, nodePath string, t *topology.Topology, bw network.Bandwidth) {
	gpu, nic := 0, 0
	// With a documented NIC layout, the rule's NICs replace the captured one, which is only
	// the instance's default interface; the first of them goes where the capture saw it.
	// Otherwise the captured NICs stay, sharing the rule's bandwidth.
	captured := len(t.NICs)
	for _, s := range t.Sockets {
		captured += len(s.NICs)
	}
	perNIC := int64(bw.Gbps)
	if captured > 1 {
		perNIC = int64(bw.Gbps / captured)
	}
	// GPUs go on the socket they are local to: GPU-near-cores is what this graph is for.
	// NICs always go on the node, with the socket hwloc saw them local to as a property,
	// so every node has the same shape for a jobspec to ask for (node > nic) whether or
	// not the hypervisor reported PCI locality.
	addDevices := func(parent, parentPath string, socket int, gpus, nics []topology.PCIDevice) {
		for _, d := range gpus {
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", parentPath, gpu), Exclusive: true,
				Properties: deviceProperties(d)}, parent)
			gpu++
		}
		for _, d := range nics {
			if bw.Known && nic > 0 {
				continue // the documented layout replaces the captured interfaces
			}
			props := deviceProperties(d)
			size := perNIC
			props["kind"] = "gvnic"
			if bw.Known && len(bw.NICs) > 0 {
				props["kind"], size = bw.NICs[0].Kind, int64(bw.NICs[0].Gbps)
			}
			if socket >= 0 {
				props["local_socket"] = strconv.Itoa(socket)
			}
			g.Add(Vertex{Type: "nic", Path: fmt.Sprintf("%s/nic%d", nodePath, nic), Exclusive: true, Size: size, Unit: "Gbps",
				Properties: props}, node)
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
		addDevices(sock, sockPath, s.Index, s.GPUs, s.NICs)
		if gb := s.MemoryBytes >> 30; gb > 0 {
			g.Add(Vertex{Type: "memory", Path: fmt.Sprintf("%s/memory%d", sockPath, s.Index), Size: gb, Unit: "GB"}, sock)
		}
	}
	addDevices(node, nodePath, -1, t.GPUs, t.NICs)
	if bw.Known {
		// The rest of the documented NICs, at the node: a capture cannot say which socket
		// they would be local to.
		layout := bw.NICs
		if nic > 0 && len(layout) > 0 {
			first := layout[0]
			first.Count--
			layout = append([]network.NIC{first}, layout[1:]...)
		}
		addRuleNICs(g, node, nodePath, bw, layout, nic)
	}
	if gb := t.MemoryBytes >> 30; gb > 0 {
		g.Add(Vertex{Type: "memory", Path: nodePath + "/memory", Size: gb, Unit: "GB"}, node)
	}
}

// addStoragePools adds one pool per disk type the machine type can attach among those
// offered where the offer is: a vertex typed after the disk type (pd_ssd, hyperdisk_balanced)
// whose size is the GB quota allows, or the largest single disk when no quota is known.
func addStoragePools(g *Graph, offer, offerPath string, mt database.MachineType, disks []database.DiskType, opts Options) {
	for _, dt := range storage.Compatible(mt, disks) {
		size, capacity := int64(dt.MaxGB), "largest disk (no quota snapshot)"
		if opts.Storage != nil {
			if metric := storage.QuotaMetric(dt.Name); metric != "" {
				capacity = "quota " + metric
			}
			if gb, ok := opts.Storage(dt.Name); ok {
				switch {
				case gb < 0:
					size = int64(dt.MaxGB)
					capacity += " (no limit)"
				case gb == 0:
					continue // no quota granted
				default:
					size = gb
				}
			} else {
				capacity = "largest disk (quota unknown)"
			}
		}
		if size <= 0 {
			continue
		}
		props := map[string]string{"disk_type": dt.Name, "min_gb": strconv.Itoa(dt.MinGB), "max_gb": strconv.Itoa(dt.MaxGB), "capacity": capacity}
		g.Add(Vertex{Type: storage.VertexType(dt.Name), Path: fmt.Sprintf("%s/%s0", offerPath, storage.VertexType(dt.Name)), Size: size, Unit: "GB", Properties: props}, offer)
	}
}

// addRuleNICs adds the documented NICs (bw's, or layout when given) under a node,
// numbering from next.
func addRuleNICs(g *Graph, node, nodePath string, bw network.Bandwidth, layout []network.NIC, next int) {
	if layout == nil {
		layout = bw.NICs
	}
	for _, n := range layout {
		for k := 0; k < n.Count; k++ {
			props := map[string]string{"kind": n.Kind, "source": "rule"}
			if n.Kind == "rdma" {
				props["rdma"] = "true"
			}
			g.Add(Vertex{Type: "nic", Path: fmt.Sprintf("%s/nic%d", nodePath, next), Exclusive: true, Size: int64(n.Gbps), Unit: "Gbps", Properties: props}, node)
			next++
		}
	}
}

// nicKinds summarizes a bandwidth's NICs, e.g. "gvnic:2,rdma:8".
func nicKinds(bw network.Bandwidth) string {
	var parts []string
	for _, n := range bw.NICs {
		parts = append(parts, fmt.Sprintf("%s:%d", n.Kind, n.Count))
	}
	return strings.Join(parts, ",")
}

func deviceProperties(d topology.PCIDevice) map[string]string {
	props := map[string]string{"pci_busid": d.BusID, "vendor_id": d.VendorID, "device_id": d.DeviceID}
	if d.Vendor != "" {
		props["vendor"] = d.Vendor
	}
	return props
}
