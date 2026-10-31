// Package topology reduces an hwloc XML topology to the sockets, cores, memory,
// GPUs and NICs used to build a resource graph.
package topology

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// GPU vendors recognized from PCI vendor IDs. Display devices from other vendors,
// such as BMC graphics chips, are not counted as GPUs.
var gpuVendors = map[string]string{"10de": "nvidia", "1002": "amd"}

// PCIDevice is a GPU or NIC found on the PCI bus.
type PCIDevice struct {
	BusID    string
	Vendor   string
	VendorID string
	DeviceID string
	Class    string
	Name     string
}

// Socket is one processor package with the devices local to it.
type Socket struct {
	Index       int
	Cores       int
	PUs         int
	MemoryBytes int64
	GPUs        []PCIDevice
	NICs        []PCIDevice
}

// Topology is the reduced view of one machine.
type Topology struct {
	Sockets []Socket
	// MemoryBytes, GPUs and NICs not local to any socket.
	MemoryBytes int64
	GPUs        []PCIDevice
	NICs        []PCIDevice

	pending []pendingItem
}

// pendingItem is memory or a device that belongs to a package not yet walked.
type pendingItem struct {
	socket int
	memory int64
	device PCIDevice
	kind   string
}

// Cores returns the total core count.
func (t *Topology) Cores() int {
	n := 0
	for _, s := range t.Sockets {
		n += s.Cores
	}
	return n
}

// GPUCount returns the total GPU count.
func (t *Topology) GPUCount() int {
	n := len(t.GPUs)
	for _, s := range t.Sockets {
		n += len(s.GPUs)
	}
	return n
}

// NICCount returns the total NIC count.
func (t *Topology) NICCount() int {
	n := len(t.NICs)
	for _, s := range t.Sockets {
		n += len(s.NICs)
	}
	return n
}

// MemoryTotal returns all memory in bytes.
func (t *Topology) MemoryTotal() int64 {
	n := t.MemoryBytes
	for _, s := range t.Sockets {
		n += s.MemoryBytes
	}
	return n
}

type object struct {
	Type        string   `xml:"type,attr"`
	OSIndex     string   `xml:"os_index,attr"`
	Name        string   `xml:"name,attr"`
	PCIBusID    string   `xml:"pci_busid,attr"`
	PCIType     string   `xml:"pci_type,attr"`
	LocalMemory string   `xml:"local_memory,attr"`
	Children    []object `xml:"object"`
}

type document struct {
	Objects []object `xml:"object"`
}

// ParseFile parses an hwloc XML file.
func ParseFile(path string) (*Topology, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads hwloc XML (1.x or 2.x layout) and assigns each core, memory node and
// device to the nearest enclosing package, or to the machine when there is none.
func Parse(r io.Reader) (*Topology, error) {
	var doc document
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing hwloc xml: %w", err)
	}
	t := &Topology{}
	for i := range doc.Objects {
		t.walk(&doc.Objects[i], -1, -1)
	}
	for _, p := range t.pending {
		target := p.socket
		if target >= len(t.Sockets) {
			target = -1
		}
		if p.kind == "" {
			t.addMemory(target, p.memory)
		} else {
			t.addDevice(target, p.device, p.kind)
		}
	}
	t.pending = nil
	return t, nil
}

// walk visits o. socket is the enclosing package index, or -1. numa is the package
// index owned by an enclosing NUMA node in hwloc 1.x layouts, or -1.
func (t *Topology) walk(o *object, socket, numa int) {
	switch o.Type {
	case "Package", "Socket":
		t.Sockets = append(t.Sockets, Socket{Index: len(t.Sockets)})
		socket = len(t.Sockets) - 1
	case "Core":
		if socket >= 0 {
			t.Sockets[socket].Cores++
		}
	case "PU":
		if socket >= 0 {
			t.Sockets[socket].PUs++
		}
	case "NUMANode":
		if socket < 0 && hasPackage(o) {
			// hwloc 1.x: the NUMA node encloses its package, which is walked next.
			numa = len(t.Sockets)
		}
		mem, _ := strconv.ParseInt(o.LocalMemory, 10, 64)
		if mem > 0 {
			t.place(socket, numa, pendingItem{memory: mem})
		}
	case "PCIDev":
		if dev, kind := classify(o); kind != "" {
			t.place(socket, numa, pendingItem{device: dev, kind: kind})
		}
	}
	for i := range o.Children {
		t.walk(&o.Children[i], socket, numa)
	}
}

// place assigns memory or a device to its package, deferring when the owning
// package has not been walked yet.
func (t *Topology) place(socket, numa int, item pendingItem) {
	target := socket
	if target < 0 {
		target = numa
	}
	if target >= len(t.Sockets) {
		item.socket = target
		t.pending = append(t.pending, item)
		return
	}
	if item.kind == "" {
		t.addMemory(target, item.memory)
	} else {
		t.addDevice(target, item.device, item.kind)
	}
}

func (t *Topology) addMemory(socket int, mem int64) {
	if socket >= 0 {
		t.Sockets[socket].MemoryBytes += mem
	} else {
		t.MemoryBytes += mem
	}
}

func hasPackage(o *object) bool {
	for i := range o.Children {
		c := &o.Children[i]
		if c.Type == "Package" || c.Type == "Socket" || hasPackage(c) {
			return true
		}
	}
	return false
}

func (t *Topology) addDevice(socket int, dev PCIDevice, kind string) {
	switch {
	case kind == "gpu" && socket >= 0:
		t.Sockets[socket].GPUs = append(t.Sockets[socket].GPUs, dev)
	case kind == "gpu":
		t.GPUs = append(t.GPUs, dev)
	case socket >= 0:
		t.Sockets[socket].NICs = append(t.Sockets[socket].NICs, dev)
	default:
		t.NICs = append(t.NICs, dev)
	}
}

// classify returns the device and "gpu", "nic" or "" from an hwloc pci_type such as
// "0302 [10de:2330] [10de:16c1] a1": class, then [vendor:device].
func classify(o *object) (PCIDevice, string) {
	fields := strings.Fields(o.PCIType)
	if len(fields) < 2 {
		return PCIDevice{}, ""
	}
	class := fields[0]
	ids := strings.Split(strings.Trim(fields[1], "[]"), ":")
	if len(ids) != 2 {
		return PCIDevice{}, ""
	}
	dev := PCIDevice{BusID: o.PCIBusID, VendorID: ids[0], DeviceID: ids[1], Class: class, Name: o.Name}
	switch {
	case strings.HasPrefix(class, "03"):
		vendor, ok := gpuVendors[ids[0]]
		if !ok {
			return PCIDevice{}, ""
		}
		dev.Vendor = vendor
		return dev, "gpu"
	case class == "0200" || class == "0207":
		return dev, "nic"
	}
	return PCIDevice{}, ""
}
