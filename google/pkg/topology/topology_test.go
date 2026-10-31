package topology

import (
	"strings"
	"testing"
)

func TestParseFixture(t *testing.T) {
	topo, err := ParseFile("../../testdata/topology/test-gpu-2g/hwloc.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(topo.Sockets) != 2 || topo.Cores() != 8 {
		t.Fatalf("want 2 sockets and 8 cores, got %d and %d", len(topo.Sockets), topo.Cores())
	}
	for _, s := range topo.Sockets {
		if len(s.GPUs) != 1 || s.GPUs[0].Vendor != "nvidia" {
			t.Errorf("socket %d: want one nvidia GPU, got %+v", s.Index, s.GPUs)
		}
		if s.MemoryBytes != 64<<30 {
			t.Errorf("socket %d: want 64GiB, got %d", s.Index, s.MemoryBytes)
		}
	}
	if len(topo.Sockets[0].NICs) != 1 || topo.NICCount() != 1 {
		t.Errorf("want one NIC on socket 0, got %+v", topo.Sockets[0].NICs)
	}
}

// hwloc 1.x layout: the NUMA node encloses the package, devices sit beside it, and
// a BMC display chip must not count as a GPU.
const hwloc1 = `<?xml version="1.0" encoding="UTF-8"?>
<topology>
  <object type="Machine" os_index="0">
    <object type="NUMANode" os_index="0" local_memory="1073741824">
      <object type="Group">
        <object type="PCIDev" pci_busid="0004:04:00.0" pci_type="0302 [10de:1db1] [10de:1212] a1" name="GPU"/>
        <object type="PCIDev" pci_busid="0002:02:00.0" pci_type="0300 [1a03:2000] [1a03:2000] 41" name="ASPEED"/>
      </object>
      <object type="Socket" os_index="0">
        <object type="Core" os_index="0"><object type="PU" os_index="0"/></object>
        <object type="Core" os_index="1"><object type="PU" os_index="1"/></object>
      </object>
    </object>
    <object type="NUMANode" os_index="255" local_memory="2147483648"/>
  </object>
</topology>`

func TestParseHwloc1Layout(t *testing.T) {
	topo, err := Parse(strings.NewReader(hwloc1))
	if err != nil {
		t.Fatal(err)
	}
	if len(topo.Sockets) != 1 || topo.Cores() != 2 {
		t.Fatalf("want 1 socket with 2 cores, got %+v", topo.Sockets)
	}
	s := topo.Sockets[0]
	if len(s.GPUs) != 1 || s.MemoryBytes != 1<<30 {
		t.Errorf("GPU and memory not assigned to the socket in the same NUMA node: %+v", s)
	}
	if topo.GPUCount() != 1 {
		t.Errorf("display chip counted as GPU: %d GPUs", topo.GPUCount())
	}
	if topo.MemoryBytes != 2<<30 {
		t.Errorf("memory outside any package should stay at node level, got %d", topo.MemoryBytes)
	}
}
