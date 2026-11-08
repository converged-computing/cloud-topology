// Package jgf builds Fluxion JSON Graph Format resource graphs from the EC2 database, at
// the same three levels and with the same vertex types and properties as the Google
// graphs (google/pkg/jgf), so one graph database and one set of jobspecs serve both:
//
//   - series: cluster > site > region > <family>.<model> offer > pooled core, gpu, memory
//   - coarse: ... > region > <type>.<model> offer > node > pooled core, gpu, memory, nic,
//     local_ssd; EBS pools on the offer
//   - exact: cluster > region > zone > offer > placement > node > socket > core, gpu,
//     memory; nic and local_ssd on the node; EBS pools on the offer
//
// EC2 reports cores and threads per core, so a catalog-built node has the right number
// of cores; the socket count still comes only from a capture.
package jgf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	gjgf "github.com/converged-computing/google-cloud-topology/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/pkg/topology"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/storage"
)

// Graph and Vertex are the shared JGF types.
type (
	Graph  = gjgf.Graph
	Vertex = gjgf.Vertex
)

// Provider is the site name and root of the paths.
const Provider = "aws"

// Options selects what to put in a coarse or exact graph.
type Options struct {
	Region       string
	MachineTypes []string
	// Nodes per offer when Capacity is not set.
	Nodes int
	// Models are purchasing models to create offers for: on-demand, spot.
	Models []string
	// Capacity, when set, returns the node count quota allows per offer (-1 for no
	// limit); offers with no capacity are left out and Nodes is ignored.
	Capacity func(mt database.MachineType, model string) int
	// Storage, when set, returns the GB quota allows for a volume type in the region
	// (-1 for no limit) and whether a quota bounds it; pools without one are sized at the
	// largest volume.
	Storage func(diskType string) (gb int64, ok bool)
	// MaxNodes caps the nodes per offer when Capacity is set (default 64).
	MaxNodes int
	// CapturedOnly leaves instance types whose series has no capture out of the exact graph.
	CapturedOnly bool
	// IncludePrevious keeps previous-generation instance types.
	IncludePrevious bool
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

func (o Options) nodesFor(mt database.MachineType, model string) int {
	if o.Capacity == nil {
		if model == "spot" && !mt.Spot() {
			return 0
		}
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
	selected := db.Query(database.Filter{Region: opts.Region, Names: opts.MachineTypes, CurrentOnly: !opts.IncludePrevious})
	if len(selected) == 0 {
		return nil, fmt.Errorf("no instance types in region %s match", opts.Region)
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
		"architecture":       mt.Architecture,
		"cores":              strconv.Itoa(cores(mt)),
		"threads_per_core":   strconv.Itoa(threads(mt)),
		"egress_gbps":        gbps(mt.EgressGbps()),
	}
	var accels []string
	for _, a := range mt.Accelerators {
		accels = append(accels, fmt.Sprintf("%s:%d", a.Type, a.Count))
	}
	if len(accels) > 0 {
		props["accelerators"] = strings.Join(accels, ",")
	}
	if mt.BareMetal {
		props["metal"] = "true"
	}
	if mt.Burstable {
		props["burstable"] = "true"
	}
	if mt.Hypervisor != "" {
		props["hypervisor"] = mt.Hypervisor
	}
	if mt.Processor.Manufacturer != "" {
		props["processor"] = mt.Processor.Manufacturer
	}
	if mt.Network.EFASupported {
		props["efa"] = strconv.Itoa(mt.Network.MaxEFAInterfaces)
	}
	if mt.Network.ENASRD {
		props["ena_srd"] = "true"
	}
	if mt.LocalSSD() {
		props["local_ssd"] = "true"
		props["instance_store_gb"] = strconv.Itoa(mt.InstanceStore.TotalGB)
	}
	return props
}

// cores is the catalog's physical cores, or vCPUs per thread when it has none.
func cores(mt database.MachineType) int {
	if mt.Cores > 0 {
		return mt.Cores
	}
	if c := mt.VCPUs / threads(mt); c > 0 {
		return c
	}
	return 1
}

func threads(mt database.MachineType) int {
	if mt.ThreadsPerCore > 0 {
		return mt.ThreadsPerCore
	}
	return 1
}

func gbps(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func memoryGB(mb int) int64 { return int64(mb) / 1024 }

// zonesIn returns the instance type's zones in the region, sorted.
func zonesIn(mt database.MachineType, region string) []string {
	var zones []string
	for _, z := range mt.Zones {
		if database.RegionOf(z) == region {
			zones = append(zones, z)
		}
	}
	sort.Strings(zones)
	return zones
}

// addNICs adds one nic per network card under a node, sized at the card's peak
// bandwidth. The first MaxEFAInterfaces cards that accept EFA are kind efa (RDMA-class,
// SRD transport), the rest ena.
func addNICs(g *Graph, node, nodePath string, mt database.MachineType) {
	efa := mt.Network.MaxEFAInterfaces
	if !mt.Network.EFASupported {
		efa = 0
	}
	for i, c := range mt.Network.Cards {
		props := map[string]string{"kind": "ena", "source": "catalog", "card": strconv.Itoa(c.Index), "baseline_gbps": gbps(c.BaselineGbps)}
		if efa > 0 && c.EFA {
			props["kind"], props["rdma"] = "efa", "true"
			efa--
		}
		g.Add(Vertex{Type: "nic", Path: fmt.Sprintf("%s/nic%d", nodePath, i), Exclusive: true, Size: int64(c.PeakGbps + 0.5), Unit: "Gbps", Properties: props}, node)
	}
}

// addInstanceStore adds one local_ssd (or local_hdd) vertex per instance store volume,
// sized in GB.
func addInstanceStore(g *Graph, node, nodePath string, mt database.MachineType) {
	if mt.InstanceStore == nil {
		return
	}
	n := 0
	for _, d := range mt.InstanceStore.Disks {
		typ := "local_ssd"
		if d.Type == "hdd" {
			typ = "local_hdd"
		}
		for i := 0; i < d.Count; i++ {
			props := map[string]string{"interface": strings.ToUpper(mt.InstanceStore.NVMe)}
			if mt.InstanceStore.NVMe == "" || mt.InstanceStore.NVMe == "unsupported" {
				props = nil
			} else {
				props["interface"] = "NVME"
			}
			g.Add(Vertex{Type: typ, Path: fmt.Sprintf("%s/%s%d", nodePath, typ, n), Exclusive: true, Size: int64(d.SizeGB), Unit: "GB", Properties: props}, node)
			n++
		}
	}
}

// addStoragePools adds one pool per EBS volume type the instance type can attach: a
// vertex typed ebs_<type> whose size is the GB quota allows, or the largest volume when
// no quota is known.
func addStoragePools(g *Graph, offer, offerPath string, mt database.MachineType, disks []database.DiskType, opts Options) {
	for _, dt := range storage.Compatible(mt, disks) {
		size, capacity := int64(dt.MaxGB), "largest volume (no quota snapshot)"
		if opts.Storage != nil {
			if name := storage.QuotaName(dt.Name); name != "" {
				capacity = "quota " + name
			}
			if gb, ok := opts.Storage(dt.Name); ok {
				switch {
				case gb < 0:
					size = int64(dt.MaxGB)
					capacity += " (no limit)"
				case gb == 0:
					continue
				default:
					size = gb
				}
			} else {
				capacity = "largest volume (quota unknown)"
			}
		}
		if size <= 0 {
			continue
		}
		props := map[string]string{"disk_type": dt.Name, "min_gb": strconv.Itoa(dt.MinGB), "max_gb": strconv.Itoa(dt.MaxGB), "capacity": capacity}
		if dt.MaxIOPS > 0 {
			props["max_iops"] = strconv.Itoa(dt.MaxIOPS)
		}
		g.Add(Vertex{Type: storage.VertexType(dt.Name), Path: fmt.Sprintf("%s/%s0", offerPath, storage.VertexType(dt.Name)), Size: size, Unit: "GB", Properties: props}, offer)
	}
}

// BuildCoarse builds the hyperscheduler summary: site > region > offer > node, with each
// potential node holding pooled gpu, core, memory, nic and instance store.
func BuildCoarse(db *database.Database, opts Options) (*Graph, error) {
	opts = opts.withDefaults()
	selected, err := selectMachineTypes(db, opts)
	if err != nil {
		return nil, err
	}
	g := gjgf.New()
	root := gjgf.AddRoot(g, Provider)
	regionPath := gjgf.ProviderPath(Provider) + "/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, root)
	for _, mt := range selected {
		zones := zonesIn(mt, opts.Region)
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
				kinds := "ena:" + strconv.Itoa(len(mt.Network.Cards))
				if mt.Network.EFASupported {
					kinds = fmt.Sprintf("ena:%d,efa:%d", len(mt.Network.Cards)-min(mt.Network.MaxEFAInterfaces, len(mt.Network.Cards)), min(mt.Network.MaxEFAInterfaces, len(mt.Network.Cards)))
				}
				g.Add(Vertex{Type: "nic", Path: nodePath + "/nic0", Size: int64(mt.EgressGbps() + 0.5), Unit: "Gbps", Properties: map[string]string{"kinds": kinds}}, node)
				addInstanceStore(g, node, nodePath, mt)
			}
			seen := map[string]database.DiskType{}
			for _, z := range zones {
				for _, dt := range db.DiskTypesIn(z) {
					seen[dt.Name] = dt
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

// TopologyLoader returns the topology for an instance type and where it came from
// ("hwloc", or "derived:<instance type>"), or nil when there is none.
type TopologyLoader func(name string) (*topology.Topology, string, error)

// HwlocLoader loads captured hwloc topologies from the database; an instance type
// without its own capture is derived from the smallest captured member of its series
// that is at least as large.
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
		// Accelerators hwloc cannot see (Inferentia and Trainium are not PCI display devices)
		// are absent from the capture; derive the ones it saw and let addHwlocNode fill the
		// rest from the catalog.
		t, err := topology.Derive(src, mt.VCPUs, min(mt.GPUs(), src.GPUCount()), int64(mt.MemoryMB)<<20)
		if err != nil {
			return nil, "", fmt.Errorf("deriving from %s: %w", source.Name, err)
		}
		return t, "derived:" + source.Name, nil
	}
}

// ClusterGroupNodes stands for the capacity of an EC2 cluster placement group, which
// EC2 does not cap; the fold restores an offer's quota-sized node count into one group.
const ClusterGroupNodes = 65536

// BuildExact builds the secretary's detailed graph: region > zone > offer > placement >
// node > socket > cores, GPUs and memory, with NICs and instance store on the node.
// Nodes use the captured or derived topology when there is one, otherwise one socket
// with the catalog's cores.
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
	g := gjgf.New()
	root := gjgf.AddRoot(g, Provider)
	regionPath := gjgf.ProviderPath(Provider) + "/" + opts.Region
	region := g.Add(Vertex{Type: "region", Path: regionPath}, root)

	zoneOffers := map[string][]database.MachineType{}
	for _, mt := range selected {
		for _, z := range zonesIn(mt, opts.Region) {
			zoneOffers[z] = append(zoneOffers[z], mt)
		}
	}
	zones := make([]string, 0, len(zoneOffers))
	for z := range zoneOffers {
		zones = append(zones, z)
	}
	sort.Strings(zones)
	for _, z := range zones {
		zonePath := regionPath + "/" + z
		zprops := map[string]string{}
		if id := db.ZoneIDs[z]; id != "" {
			zprops["zone_id"] = id
		}
		zone := g.Add(Vertex{Type: "zone", Path: zonePath, Properties: zprops}, region)
		for _, mt := range zoneOffers[z] {
			for _, model := range opts.Models {
				count := opts.nodesFor(mt, model)
				if count == 0 {
					continue
				}
				if opts.CapturedOnly && sources[mt.Name] == "" {
					continue
				}
				props := offerProperties(mt, model)
				props["topology"] = "catalog"
				if src := sources[mt.Name]; src != "" {
					props["topology"] = src
				}
				// Cluster placement groups: EC2 documents no instance cap, so an offer's
				// nodes share one group when the type supports the strategy; otherwise
				// groups of one, so a request for several nodes in one group only matches
				// where co-location is possible.
				groupSize := 1
				if mt.Cluster() {
					groupSize = count
					props["placement"] = "cluster"
				} else {
					props["placement"] = "none"
				}
				offerPath := fmt.Sprintf("%s/%s.%s", zonePath, mt.Name, model)
				offer := g.Add(Vertex{Type: "offer", Path: offerPath, Properties: props}, zone)
				var group, groupPath string
				for n := 0; n < count; n++ {
					if n%groupSize == 0 {
						groupPath = fmt.Sprintf("%s/placement%d", offerPath, n/groupSize)
						// max_nodes is the group's capacity, which the fold uses to restore the
						// quota's node count: unbounded for a cluster group, one otherwise.
						capacity := 1
						if mt.Cluster() {
							capacity = ClusterGroupNodes
						}
						group = g.Add(Vertex{Type: "placement", Path: groupPath, Properties: map[string]string{"compact": strconv.FormatBool(mt.Cluster()), "max_nodes": strconv.Itoa(capacity)}}, offer)
					}
					nodePath := fmt.Sprintf("%s/node%d", groupPath, n)
					node := g.Add(Vertex{Type: "node", Path: nodePath, Exclusive: true, Properties: map[string]string{"state": "potential"}}, group)
					if t := topos[mt.Name]; t != nil {
						addHwlocNode(g, node, nodePath, t, mt)
					} else {
						addCatalogNode(g, node, nodePath, mt)
					}
					addNICs(g, node, nodePath, mt)
					addInstanceStore(g, node, nodePath, mt)
				}
				addStoragePools(g, offer, offerPath, mt, db.DiskTypesIn(z), opts)
			}
		}
	}
	return g, nil
}

// addCatalogNode models an instance type without a capture: one socket holding the
// catalog's cores (EC2 reports them), its accelerators and its memory. The socket count
// is unknown from the catalog; the offer's topology=catalog property says so.
func addCatalogNode(g *Graph, node, nodePath string, mt database.MachineType) {
	sockPath := nodePath + "/socket0"
	sock := g.Add(Vertex{Type: "socket", Path: sockPath, Properties: map[string]string{"threads_per_core": strconv.Itoa(threads(mt))}}, node)
	for c := 0; c < cores(mt); c++ {
		g.Add(Vertex{Type: "core", Path: fmt.Sprintf("%s/core%d", sockPath, c), Exclusive: true}, sock)
	}
	gpu := 0
	for _, a := range mt.Accelerators {
		for i := 0; i < a.Count; i++ {
			props := map[string]string{"model": a.Type}
			if a.MemoryMiB > 0 {
				props["memory_mib"] = strconv.Itoa(a.MemoryMiB)
			}
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", sockPath, gpu), Exclusive: true, Properties: props}, sock)
			gpu++
		}
	}
	g.Add(Vertex{Type: "memory", Path: sockPath + "/memory0", Size: memoryGB(mt.MemoryMB), Unit: "GB"}, sock)
}

// addHwlocNode lays out sockets, cores, GPUs and memory as a capture saw them. NICs come
// from the catalog's network cards (addNICs), since a capture sees only the instance's
// default interface. Accelerators the capture did not see (AWS Inferentia and Trainium
// are not PCI display devices, so hwloc does not list them) are added on the node from
// the catalog, marked source=catalog, so the node holds what the catalog promises.
func addHwlocNode(g *Graph, node, nodePath string, t *topology.Topology, mt database.MachineType) {
	gpu, core := 0, 0
	addGPUs := func(parent, parentPath string, gpus []topology.PCIDevice) {
		for _, d := range gpus {
			props := map[string]string{"pci_busid": d.BusID, "vendor_id": d.VendorID, "device_id": d.DeviceID}
			if d.Vendor != "" {
				props["vendor"] = d.Vendor
			}
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", parentPath, gpu), Exclusive: true, Properties: props}, parent)
			gpu++
		}
	}
	for _, s := range t.Sockets {
		sockPath := fmt.Sprintf("%s/socket%d", nodePath, s.Index)
		sock := g.Add(Vertex{Type: "socket", Path: sockPath}, node)
		for c := 0; c < s.Cores; c++ {
			g.Add(Vertex{Type: "core", Path: fmt.Sprintf("%s/core%d", sockPath, core), Exclusive: true}, sock)
			core++
		}
		addGPUs(sock, sockPath, s.GPUs)
		if gb := s.MemoryBytes >> 30; gb > 0 {
			g.Add(Vertex{Type: "memory", Path: fmt.Sprintf("%s/memory%d", sockPath, s.Index), Size: gb, Unit: "GB"}, sock)
		}
	}
	addGPUs(node, nodePath, t.GPUs)
	missing := mt.GPUs() - t.GPUCount()
	for _, a := range mt.Accelerators {
		for i := 0; i < a.Count && missing > 0; i++ {
			props := map[string]string{"model": a.Type, "source": "catalog"}
			if a.MemoryMiB > 0 {
				props["memory_mib"] = strconv.Itoa(a.MemoryMiB)
			}
			g.Add(Vertex{Type: "gpu", Path: fmt.Sprintf("%s/gpu%d", nodePath, gpu), Exclusive: true, Properties: props}, node)
			gpu++
			missing--
		}
	}
	if gb := t.MemoryBytes >> 30; gb > 0 {
		g.Add(Vertex{Type: "memory", Path: nodePath + "/memory", Size: gb, Unit: "GB"}, node)
	}
}
