package network

import (
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func mt(name string, vcpus int, shared bool) database.MachineType {
	return database.MachineType{Name: name, Family: database.FamilyOf(name), VCPUs: vcpus, SharedCPU: shared}
}

func TestEgress(t *testing.T) {
	cases := []struct {
		mt          database.MachineType
		gbps, tier1 int
		nics        int
		known       bool
	}{
		{mt("n2-standard-8", 8, false), 16, 100, 1, true},
		{mt("n2-standard-128", 128, false), 32, 100, 1, true},
		{mt("c3-standard-176", 176, false), 100, 200, 1, true},
		{mt("c4n-standard-192", 192, false), 384, 0, 2, true},
		{mt("e2-micro", 2, true), 2, 0, 1, true},
		{mt("e2-standard-32", 32, false), 16, 0, 1, true},
		{mt("n1-standard-1", 1, false), 2, 0, 1, true},
		{mt("f1-micro", 1, true), 1, 0, 1, true},
		{mt("h3-standard-88", 88, false), 176, 0, 1, true},
		{mt("h4d-standard-192", 192, false), 200, 0, 2, true},
		{mt("a3-highgpu-8g", 208, false), 1000, 0, 5, true},
		{mt("a3-megagpu-8g", 208, false), 1800, 0, 9, true},
		{mt("a4-highgpu-8g", 224, false), 3600, 0, 10, true},
		{mt("a2-megagpu-16g", 96, false), 100, 0, 1, true},
		{mt("g4-standard-384", 384, false), 400, 0, 1, true},
		{mt("test-cpu-4", 4, false), 8, 0, 1, false},
		{mt("test-big-96", 96, false), 32, 0, 1, false},
	}
	for _, c := range cases {
		b := Egress(c.mt)
		nics := 0
		for _, n := range b.NICs {
			nics += n.Count
		}
		if b.Gbps != c.gbps || b.Tier1Gbps != c.tier1 || nics != c.nics || b.Known != c.known || b.Rule == "" {
			t.Errorf("%s: got %d Gbps (tier1 %d, %d NICs, known %v): %s; want %d/%d/%d/%v", c.mt.Name, b.Gbps, b.Tier1Gbps, nics, b.Known, b.Rule, c.gbps, c.tier1, c.nics, c.known)
		}
	}
	rdma := 0
	for _, n := range Egress(mt("a4-highgpu-8g", 224, false)).NICs {
		if n.Kind == "rdma" {
			rdma += n.Count
		}
	}
	if rdma != 8 {
		t.Errorf("a4-highgpu-8g: %d RDMA NICs, want 8", rdma)
	}
}

func TestCompactPlacement(t *testing.T) {
	cases := []struct {
		name           string
		d0, d1, d2, d3 int
	}{
		{"c3-standard-176", 1500, 22, 150, 1500},
		{"a3-highgpu-8g", 1500, 0, 256, 1500},
		{"a3-edgegpu-8g-nolssd", 1500, 22, 256, 1500},
		{"a3-ultragpu-8g", 1500, 0, 256, 0},
		{"a4x-highgpu-4g", 1500, 0, 0, 0},
		{"z4m-highmem-64", 1500, 16, 150, 1500},
		{"n2-standard-8", 1500, 0, 0, 0},
		{"e2-standard-32", 0, 0, 0, 0},
		{"n1-standard-96", 0, 0, 0, 0},
		{"z3-highmem-176-standardlssd", 0, 0, 0, 0},
		{"z3-highmem-192-metal", 1500, 22, 150, 1500},
		{"t2a-standard-48", 0, 0, 0, 0},
	}
	for _, c := range cases {
		m := mt(c.name, 8, false)
		got := [4]int{GroupSize(m, 0), GroupSize(m, 1), GroupSize(m, 2), GroupSize(m, 3)}
		if got != [4]int{c.d0, c.d1, c.d2, c.d3} {
			t.Errorf("%s: group sizes %v, want %v", c.name, got, [4]int{c.d0, c.d1, c.d2, c.d3})
		}
		if CompactPlacement(m).Supported != (c.d0 > 0) {
			t.Errorf("%s: supported %v", c.name, CompactPlacement(m).Supported)
		}
	}
}
