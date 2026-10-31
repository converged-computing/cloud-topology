package topology

import "testing"

func TestDerive(t *testing.T) {
	src, err := ParseFile("../../testdata/topology/test-gpu-2g/hwloc.xml")
	if err != nil {
		t.Fatal(err)
	}
	// 8 vCPUs at 2 per core = 4 cores: all of socket 0, with its GPU and NIC.
	d, err := Derive(src, 8, 1, 32<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sockets) != 1 || d.Cores() != 4 || d.GPUCount() != 1 || d.NICCount() != 1 || d.MemoryTotal() != 32<<30 {
		t.Errorf("8 vCPUs: %+v", d.Sockets)
	}
	// 10 vCPUs = 5 cores: socket 0 full plus one core of socket 1; memory split 4:1.
	d, err = Derive(src, 10, 2, 40<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Sockets) != 2 || d.Sockets[1].Cores != 1 || d.GPUCount() != 2 || d.Sockets[0].MemoryBytes != 32<<30 {
		t.Errorf("10 vCPUs: %+v", d.Sockets)
	}
	// 2 vCPUs with 2 GPUs: the second GPU's socket joins with no cores.
	d, err = Derive(src, 2, 2, 8<<30)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cores() != 1 || len(d.Sockets) != 2 || d.Sockets[1].Cores != 0 || len(d.Sockets[1].GPUs) != 1 {
		t.Errorf("2 vCPUs, 2 GPUs: %+v", d.Sockets)
	}
	if _, err := Derive(src, 32, 0, 0); err == nil {
		t.Error("more cores than the source should fail")
	}
	if _, err := Derive(src, 2, 3, 0); err == nil {
		t.Error("more GPUs than the source should fail")
	}
}
