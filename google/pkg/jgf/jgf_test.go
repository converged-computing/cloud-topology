package jgf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
)

func load(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.Load("../../testdata")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func countTypes(g *Graph) map[string]int {
	counts := map[string]int{}
	for _, n := range g.Nodes {
		counts[n.Metadata.Type]++
	}
	return counts
}

func TestBuildCoarse(t *testing.T) {
	g, err := BuildCoarse(load(t), Options{Region: "us-central1", Nodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	c := countTypes(g)
	if c["offer"] != 2 || c["node"] != 6 || c["gpu"] != 3 || c["core"] != 6 {
		t.Errorf("unexpected counts %v", c)
	}
	if len(g.Edges) != len(g.Nodes)-1 {
		t.Errorf("graph is not a tree: %d nodes, %d edges", len(g.Nodes), len(g.Edges))
	}
	for _, n := range g.Nodes {
		if n.Metadata.Type == "offer" && n.Metadata.Name == "test-gpu-2g.on-demand" {
			if n.Metadata.Properties["zones"] != "us-central1-a,us-central1-b" {
				t.Errorf("offer zones: %q", n.Metadata.Properties["zones"])
			}
			return
		}
	}
	t.Error("test-gpu-2g offer missing")
}

func TestBuildExact(t *testing.T) {
	db := load(t)
	g, err := BuildExact(db, Options{Region: "us-central1", Nodes: 2}, HwlocLoader(db))
	if err != nil {
		t.Fatal(err)
	}
	c := countTypes(g)
	// test-gpu-2g: 2 zones x 2 nodes from hwloc (2 sockets, 8 cores, 2 GPUs, 1 NIC each).
	// test-cpu-4: 1 zone x 2 nodes from the catalog (1 socket, 4 vCPUs = 2 cores on x86).
	if c["zone"] != 2 || c["offer"] != 3 || c["node"] != 6 {
		t.Errorf("unexpected structure %v", c)
	}
	// NICs: the hwloc nodes keep their captured NIC (the test series is not in the
	// bandwidth tables); the catalog nodes get one from the rule. Placement: the test
	// series support no compact placement, so every node sits in a group of its own.
	if c["socket"] != 4*2+2*1 || c["core"] != 4*8+2*2 || c["gpu"] != 4*2 || c["nic"] != 4+2 || c["placement"] != 6 {
		t.Errorf("unexpected hardware counts %v", c)
	}
	for _, n := range g.Nodes {
		switch n.Metadata.Type {
		case "nic":
			if n.Metadata.Size <= 0 || n.Metadata.Unit != "Gbps" || n.Metadata.Properties["kind"] == "" {
				t.Errorf("nic without bandwidth: %+v", n.Metadata)
			}
			// NICs sit on the node, never on a socket; locality is a property.
			if p := n.Metadata.Paths["containment"]; strings.Contains(p, "/socket") {
				t.Errorf("nic under a socket: %s", p)
			}
		case "placement":
			if n.Metadata.Properties["compact"] != "false" || n.Metadata.Properties["max_nodes"] != "1" {
				t.Errorf("placement group: %+v", n.Metadata.Properties)
			}
		case "node":
			if !strings.Contains(n.Metadata.Paths["containment"], "/placement") {
				t.Errorf("node outside a placement group: %s", n.Metadata.Paths["containment"])
			}
		}
	}
	for _, n := range g.Nodes {
		if n.Metadata.Type == "offer" {
			want := "catalog"
			if n.Metadata.Properties["machine_type"] == "test-gpu-2g" {
				want = "hwloc"
			}
			if got := n.Metadata.Properties["topology"]; got != want {
				t.Errorf("%s: topology source %q, want %q", n.Metadata.Name, got, want)
			}
		}
	}
}

func TestModelsAndErrors(t *testing.T) {
	db := load(t)
	g, err := BuildCoarse(db, Options{Region: "us-central1", MachineTypes: []string{"test-gpu-2g"}, Models: []string{"on-demand", "flex-start"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := countTypes(g); c["offer"] != 2 {
		t.Errorf("want one offer per model, got %v", c)
	}
	if _, err := BuildCoarse(db, Options{}); err == nil {
		t.Error("missing region should fail")
	}
	if _, err := BuildCoarse(db, Options{Region: "europe-west4", MachineTypes: []string{"test-gpu-2g"}}); err == nil {
		t.Error("machine type not offered in region should fail")
	}
}

func TestJSONShape(t *testing.T) {
	g, err := BuildCoarse(load(t), Options{Region: "europe-west4"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Graph struct {
			Nodes []Node `json:"nodes"`
			Edges []Edge `json:"edges"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	root := doc.Graph.Nodes[0].Metadata
	if root.Paths["containment"] != "/heuretes" || root.Rank != -1 || root.Size != 1 {
		t.Errorf("unexpected root metadata %+v", root)
	}
}

func TestBuildWithQuotaCapacity(t *testing.T) {
	db, err := database.Load("../../testdata/quota-example")
	if err != nil {
		t.Fatal(err)
	}
	caps := map[string]int{"g2-standard-8": 2, "n2-standard-4": -1, "a3-ultragpu-8g": 0}
	opts := Options{Region: "us-central1", MaxNodes: 3,
		Capacity: func(mt database.MachineType, model string) int { return caps[mt.Name] }}
	g, err := BuildCoarse(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]int{}
	var offer string
	for _, n := range g.Nodes {
		switch n.Metadata.Type {
		case "offer":
			offer = n.Metadata.Properties["machine_type"]
		case "node":
			nodes[offer]++
		}
	}
	if nodes["g2-standard-8"] != 2 || nodes["n2-standard-4"] != 3 || nodes["a3-ultragpu-8g"] != 0 {
		t.Errorf("unexpected nodes per offer %v", nodes)
	}
}

func TestDerivedTopology(t *testing.T) {
	dir := t.TempDir()
	db := load(t)
	for _, mt := range db.MachineTypes {
		if err := database.WriteMachineType(dir, mt); err != nil {
			t.Fatal(err)
		}
	}
	small := database.MachineType{Name: "test-gpu-1g", Family: "test", VCPUs: 8, MemoryMB: 65536,
		Accelerators: []database.Accelerator{{Type: "nvidia-h100-80gb", Count: 1}}, Zones: []string{"us-central1-a"}}
	if err := database.WriteMachineType(dir, small); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hwloc.xml", "capture.json"} {
		data, err := os.ReadFile(filepath.Join("../../testdata/topology/test-gpu-2g", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "topology", "test-gpu-2g"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "topology", "test-gpu-2g", f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db2, err := database.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildExact(db2, Options{Region: "us-central1", MachineTypes: []string{"test-gpu-1g"}, Nodes: 1}, HwlocLoader(db2))
	if err != nil {
		t.Fatal(err)
	}
	c := countTypes(g)
	// 8 vCPUs at 2 per core from the 2-socket source: one socket, 4 cores, 1 GPU, 1 NIC.
	if c["socket"] != 1 || c["core"] != 4 || c["gpu"] != 1 || c["nic"] != 1 {
		t.Errorf("derived node: %v", c)
	}
	for _, n := range g.Nodes {
		if n.Metadata.Type == "offer" && n.Metadata.Properties["topology"] != "derived:test-gpu-2g" {
			t.Errorf("topology source %q", n.Metadata.Properties["topology"])
		}
	}
}

func TestVariantFeatures(t *testing.T) {
	db, err := database.Load("../../testdata/plan-example")
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildExact(db, Options{Region: "us-central1", MachineTypes: []string{"c3d-highmem-360-lssd", "c3-standard-192-metal", "e2-micro"}}, HwlocLoader(db))
	if err != nil {
		t.Fatal(err)
	}
	if c := countTypes(g); c["local_ssd"] != 32 {
		t.Errorf("want 32 local_ssd vertices, got %v", c)
	}
	props := map[string]map[string]string{}
	for _, n := range g.Nodes {
		if n.Metadata.Type == "offer" {
			props[n.Metadata.Properties["machine_type"]] = n.Metadata.Properties
		}
	}
	if p := props["c3d-highmem-360-lssd"]; p["local_ssd"] != "true" || p["local_ssd_partitions"] != "32" || p["local_ssd_interface"] != "NVME" {
		t.Errorf("lssd props: %v", p)
	}
	if props["c3-standard-192-metal"]["metal"] != "true" || props["e2-micro"]["shared_cpu"] != "true" {
		t.Errorf("metal/shared props: %v", props)
	}

	s, err := BuildSeries(db, SeriesOptions{Region: "us-central1", Series: []string{"c3d-highmem", "c3-standard-metal"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range s.Nodes {
		if n.Metadata.Type != "offer" {
			continue
		}
		switch n.Metadata.Properties["series"] {
		case "c3d-highmem":
			if n.Metadata.Properties["local_ssd_sizes"] != "c3d-highmem-360-lssd" {
				t.Errorf("series lssd sizes: %v", n.Metadata.Properties)
			}
		case "c3-standard-metal":
			if n.Metadata.Properties["metal"] != "true" {
				t.Errorf("metal series: %v", n.Metadata.Properties)
			}
		}
	}
}

func TestExactNetworkAndPlacement(t *testing.T) {
	// A real series: c3-standard-176 has 100 Gbps, Tier_1 200, and compact placement
	// groups of 150 at max distance 2 and 22 at distance 1.
	db, err := database.Load("../../testdata/plan-example")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ distance, nodes, groups int }{{2, 4, 1}, {1, 4, 1}, {1, 30, 2}} {
		g, err := BuildExact(db, Options{Region: "us-central1", Nodes: c.nodes, MachineTypes: []string{"c3-standard-176"}, PlacementDistance: c.distance}, nil)
		if err != nil {
			t.Fatal(err)
		}
		counts := countTypes(g)
		if counts["placement"] != c.groups || counts["node"] != c.nodes || counts["nic"] != c.nodes {
			t.Errorf("distance %d, %d nodes: %v", c.distance, c.nodes, counts)
		}
		for _, n := range g.Nodes {
			switch n.Metadata.Type {
			case "offer":
				if n.Metadata.Properties["egress_gbps"] != "100" || n.Metadata.Properties["tier1_gbps"] != "200" || !strings.HasPrefix(n.Metadata.Properties["placement"], "compact:") {
					t.Errorf("offer properties: %v", n.Metadata.Properties)
				}
			case "nic":
				if n.Metadata.Size != 100 || n.Metadata.Properties["kind"] != "gvnic" {
					t.Errorf("nic: %+v", n.Metadata)
				}
			case "placement":
				if n.Metadata.Properties["compact"] != "true" || n.Metadata.Properties["max_distance"] != strconv.Itoa(c.distance) {
					t.Errorf("placement: %v", n.Metadata.Properties)
				}
			}
		}
	}
	// A GPU type with a documented layout gets every NIC from the rule, RDMA ones marked.
	db, err = database.Load("../../testdata/quota-example")
	if err != nil {
		t.Fatal(err)
	}
	a3 := db.MachineTypes["a3-ultragpu-8g"]
	g, err := BuildExact(db, Options{Region: database.RegionOf(a3.Zones[0]), Nodes: 1, MachineTypes: []string{"a3-ultragpu-8g"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rdma, nics := 0, 0
	for _, n := range g.Nodes {
		if n.Metadata.Type == "nic" {
			nics++
			if n.Metadata.Properties["rdma"] == "true" && n.Metadata.Size == 400 {
				rdma++
			}
		}
	}
	if nics != 10 || rdma != 8 {
		t.Errorf("a3-ultragpu-8g: %d NICs, %d RDMA at 400 Gbps; want 10 and 8", nics, rdma)
	}
}

func TestStoragePools(t *testing.T) {
	db, err := database.Load("../../testdata/quota-example")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := quota.Load("../../testdata/quota-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	// n2-standard-8 attaches Standard and SSD PD but not Hyperdisk; Local SSD is not a pool.
	g, err := BuildExact(db, Options{Region: "us-central1", Nodes: 1, MachineTypes: []string{"n2-standard-8"}, Storage: snap.StorageFunc("us-central1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pools := map[string]Node{}
	for _, n := range g.Nodes {
		if n.Metadata.Properties["disk_type"] != "" {
			pools[n.Metadata.Type] = n
		}
	}
	if len(pools) != 2 || pools["pd_standard"].Metadata.Size != 4096 || pools["pd_ssd"].Metadata.Size != 800 || pools["pd_ssd"].Metadata.Unit != "GB" {
		t.Errorf("n2-standard-8 pools: %+v", pools)
	}
	if !strings.Contains(pools["pd_ssd"].Metadata.Paths["containment"], "/n2-standard-8.on-demand/pd_ssd0") || pools["pd_ssd"].Metadata.Properties["capacity"] != "quota SSD_TOTAL_GB" {
		t.Errorf("pd_ssd pool: %+v", pools["pd_ssd"].Metadata)
	}
	// a3-ultragpu-8g attaches only Hyperdisk, whose quota is unknown: sized at the largest disk.
	a3 := db.MachineTypes["a3-ultragpu-8g"]
	g, err = BuildExact(db, Options{Region: database.RegionOf(a3.Zones[0]), Nodes: 1, MachineTypes: []string{"a3-ultragpu-8g"}, Storage: snap.StorageFunc("us-central1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pools = map[string]Node{}
	for _, n := range g.Nodes {
		if n.Metadata.Properties["disk_type"] != "" {
			pools[n.Metadata.Type] = n
		}
	}
	if len(pools) != 1 || pools["hyperdisk_balanced"].Metadata.Size != 65536 || !strings.HasPrefix(pools["hyperdisk_balanced"].Metadata.Properties["capacity"], "largest disk") {
		t.Errorf("a3-ultragpu-8g pools: %+v", pools)
	}
	// The coarse graph gets the same pools under its offers.
	cg, err := BuildCoarse(db, Options{Region: "us-central1", Nodes: 1, MachineTypes: []string{"n2-standard-8"}, Storage: snap.StorageFunc("us-central1")})
	if err != nil {
		t.Fatal(err)
	}
	if c := countTypes(cg); c["pd_standard"] != 1 || c["pd_ssd"] != 1 || c["nic"] != 1 {
		t.Errorf("coarse pools: %v", c)
	}
}
