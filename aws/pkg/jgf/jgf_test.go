package jgf

import (
	"fmt"
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/topology"
)

func load(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.Load("../../testdata")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func count(g *Graph, typ string) int {
	n := 0
	for _, v := range g.Nodes {
		if v.Metadata.Type == typ {
			n++
		}
	}
	return n
}

func find(g *Graph, path string) map[string]string {
	for _, v := range g.Nodes {
		if v.Metadata.Paths["containment"] == path {
			return v.Metadata.Properties
		}
	}
	return nil
}

func TestExact(t *testing.T) {
	db := load(t)
	g, err := BuildExact(db, Options{Region: "us-east-1", MachineTypes: []string{"p5.48xlarge", "t3.micro", "hpc7g.16xlarge"}, Nodes: 1}, HwlocLoader(db))
	if err != nil {
		t.Fatal(err)
	}
	// One offer per zone each type is in, one node each.
	want := 0
	for _, name := range []string{"p5.48xlarge", "t3.micro", "hpc7g.16xlarge"} {
		want += len(db.MachineTypes[name].Zones)
	}
	if n := count(g, "offer"); n != want {
		t.Errorf("offers = %d, want %d", n, want)
	}
	p5 := find(g, "/site0/aws/us-east-1/us-east-1a/p5.48xlarge.on-demand")
	if p5["cores"] != "96" || p5["threads_per_core"] != "2" || p5["egress_gbps"] != "3200" || p5["efa"] != "32" || p5["placement"] != "cluster" || p5["topology"] != "catalog" || p5["accelerators"] != "nvidia-h100:8" {
		t.Errorf("p5 offer properties: %v", p5)
	}
	// One p5 node: 96 cores, 8 GPUs, 32 EFA NICs of 100 Gbps, 8 local SSDs of 3800 GB.
	node := "/site0/aws/us-east-1/us-east-1a/p5.48xlarge.on-demand/placement0/node0"
	cores, gpus, nics, ssds := 0, 0, 0, 0
	for _, v := range g.Nodes {
		p := v.Metadata.Paths["containment"]
		if !strings.HasPrefix(p, node+"/") {
			continue
		}
		switch v.Metadata.Type {
		case "core":
			cores++
		case "gpu":
			gpus++
			if v.Metadata.Properties["memory_mib"] != "81920" {
				t.Errorf("gpu memory: %v", v.Metadata.Properties)
			}
		case "nic":
			nics++
			if v.Metadata.Properties["kind"] != "efa" || v.Metadata.Size != 100 {
				t.Errorf("nic: %v size %d", v.Metadata.Properties, v.Metadata.Size)
			}
		case "local_ssd":
			ssds++
			if v.Metadata.Size != 3800 {
				t.Errorf("local_ssd size %d", v.Metadata.Size)
			}
		}
	}
	if cores != 96 || gpus != 8 || nics != 32 || ssds != 8 {
		t.Errorf("p5 node: cores %d gpus %d nics %d ssds %d", cores, gpus, nics, ssds)
	}
	t3 := find(g, "/site0/aws/us-east-1/us-east-1a/t3.micro.on-demand")
	if t3["placement"] != "none" || t3["burstable"] != "true" || t3["cores"] != "1" {
		t.Errorf("t3 offer: %v", t3)
	}
	if gp3 := find(g, "/site0/aws/us-east-1/us-east-1a/t3.micro.on-demand/ebs_gp3_0"); gp3 != nil {
		t.Errorf("unexpected pool path")
	}
	if gp3 := find(g, "/site0/aws/us-east-1/us-east-1a/t3.micro.on-demand/ebs_gp30"); gp3["disk_type"] != "gp3" {
		t.Errorf("gp3 pool: %v", gp3)
	}
	if zone := find(g, "/site0/aws/us-east-1/us-east-1a"); zone["zone_id"] != "use1-az6" {
		t.Errorf("zone: %v", zone)
	}
	// Spot offers only where the usage class allows: hpc7g is on-demand only.
	g, err = BuildExact(db, Options{Region: "us-east-1", MachineTypes: []string{"hpc7g.16xlarge", "c7i.48xlarge"}, Models: []string{"spot"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range g.Nodes {
		if v.Metadata.Type == "offer" && v.Metadata.Properties["machine_type"] == "hpc7g.16xlarge" {
			t.Error("hpc7g must not have a spot offer")
		}
	}
	if count(g, "offer") == 0 {
		t.Error("c7i should have spot offers")
	}
}

// Accelerators a capture does not see (Inferentia, Trainium) come from the catalog.
func TestExactFillsUnseenAccelerators(t *testing.T) {
	db := load(t)
	saw := &topology.Topology{Sockets: []topology.Socket{{Index: 0, Cores: 48, PUs: 96}, {Index: 1, Cores: 48, PUs: 96}}}
	load := func(name string) (*topology.Topology, string, error) { return saw, "hwloc", nil }
	g, err := BuildExact(db, Options{Region: "us-east-1", MachineTypes: []string{"p5.48xlarge"}, Nodes: 1}, load)
	if err != nil {
		t.Fatal(err)
	}
	node := "/site0/aws/us-east-1/us-east-1a/p5.48xlarge.on-demand/placement0/node0"
	sockets, gpus := 0, 0
	for _, v := range g.Nodes {
		p := v.Metadata.Paths["containment"]
		if !strings.HasPrefix(p, node+"/") {
			continue
		}
		switch v.Metadata.Type {
		case "socket":
			sockets++
		case "gpu":
			gpus++
			if v.Metadata.Properties["source"] != "catalog" || v.Metadata.Properties["model"] != "nvidia-h100" || p != fmt.Sprintf("%s/gpu%d", node, gpus-1) {
				t.Errorf("gpu: %s %v", p, v.Metadata.Properties)
			}
		}
	}
	if sockets != 2 || gpus != 8 {
		t.Errorf("sockets %d gpus %d", sockets, gpus)
	}
}

func TestCoarseAndSeries(t *testing.T) {
	db := load(t)
	g, err := BuildCoarse(db, Options{Region: "us-east-1", Nodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(g, "offer"); n != 5 {
		t.Errorf("coarse offers = %d, want 5", n)
	}
	d3 := find(g, "/site0/aws/us-east-1/d3.8xlarge.on-demand")
	if d3["instance_store_gb"] != "47520" || !strings.Contains(d3["zones"], "us-east-1a") {
		t.Errorf("d3 offer: %v", d3)
	}
	if n := count(g, "local_hdd"); n != 48 {
		t.Errorf("local_hdd = %d, want 24 per node x 2", n)
	}
	s, err := BuildSeries(db, SeriesOptions{Region: "us-east-1", Units: func(series string, members []database.MachineType, model string) (int, int, bool) {
		return 480, -1, series != "t3"
	}})
	if err != nil {
		t.Fatal(err)
	}
	// p5: 480 vCPUs allow 480/24 = 20 GPUs, so 20 GPUs and 480 cores; t3 left out.
	p5 := find(s, "/site0/aws/us-east-1/p5.on-demand")
	if p5 == nil || p5["units"] != "quota" {
		t.Fatalf("p5 series offer: %v", p5)
	}
	if find(s, "/site0/aws/us-east-1/t3.on-demand") != nil {
		t.Error("t3 should be left out")
	}
	gpus := 0
	for _, v := range s.Nodes {
		if v.Metadata.Type == "gpu" && strings.HasPrefix(v.Metadata.Paths["containment"], "/site0/aws/us-east-1/p5.on-demand/") {
			gpus++
		}
	}
	if gpus != 20 {
		t.Errorf("p5 gpus = %d, want 20", gpus)
	}
}
