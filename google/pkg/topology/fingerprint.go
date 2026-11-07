package topology

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Fingerprint is what a capture says about a machine type's hardware, in enough detail
// to tell two captures of the same type apart: where devices attach, cache sizes and the
// CPU model, plus the environment the capture ran in.
type Fingerprint struct {
	Sockets  int     `json:"sockets"`
	Cores    int     `json:"cores"`
	PUs      int     `json:"pus"`
	NUMA     int     `json:"numa_nodes"`
	MemoryGB float64 `json:"memory_gb"`
	// L3MB and L2KB are the cache sizes seen (usually one value each).
	L3MB []int `json:"l3_mb,omitempty"`
	L2KB []int `json:"l2_kb,omitempty"`
	// CPUModels lists the CPU models of the packages (Arm instances report none).
	CPUModels []string `json:"cpu_models,omitempty"`
	// GPUs and NICs count devices by where they attach: per socket, and at the machine.
	GPUs DeviceSpread `json:"gpus"`
	NICs DeviceSpread `json:"nics"`
	// Kernel and Hwloc describe the capture environment.
	Kernel string `json:"kernel,omitempty"`
	Hwloc  string `json:"hwloc,omitempty"`
}

// DeviceSpread counts devices on each socket and at machine level.
type DeviceSpread struct {
	PerSocket []int `json:"per_socket"`
	Machine   int   `json:"machine"`
}

// Hardware is the fingerprint's hardware part as one string, so captures can be grouped;
// the capture environment (kernel, hwloc) is left out.
func (f Fingerprint) Hardware() string {
	return fmt.Sprintf("sockets=%d cores=%d pus=%d numa=%d mem=%.2g l3=%v l2=%v cpu=%v gpus=%v/%d nics=%v/%d",
		f.Sockets, f.Cores, f.PUs, f.NUMA, f.MemoryGB, f.L3MB, f.L2KB, f.CPUModels, f.GPUs.PerSocket, f.GPUs.Machine, f.NICs.PerSocket, f.NICs.Machine)
}

// ID is a short hash of the hardware part.
func (f Fingerprint) ID() string {
	h := sha1.Sum([]byte(f.Hardware()))
	return hex.EncodeToString(h[:])[:8]
}

// Fields names the hardware fields in which two fingerprints differ.
func (f Fingerprint) Fields(g Fingerprint) []string {
	var out []string
	add := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			out = append(out, name)
		}
	}
	add("sockets", f.Sockets, g.Sockets)
	add("cores", f.Cores, g.Cores)
	add("pus", f.PUs, g.PUs)
	add("numa", f.NUMA, g.NUMA)
	add("memory", fmt.Sprintf("%.2g", f.MemoryGB), fmt.Sprintf("%.2g", g.MemoryGB))
	add("l3", f.L3MB, g.L3MB)
	add("l2", f.L2KB, g.L2KB)
	add("cpu model", f.CPUModels, g.CPUModels)
	add("gpu placement", f.GPUs, g.GPUs)
	add("nic placement", f.NICs, g.NICs)
	return out
}

// FingerprintOf computes the fingerprint of an hwloc object tree.
func FingerprintOf(root *Object) Fingerprint {
	f := Fingerprint{}
	if root == nil {
		return f
	}
	f.Kernel, f.Hwloc = root.Attrs["OSRelease"], root.Attrs["hwlocVersion"]
	l3, l2, cpus := map[int]bool{}, map[int]bool{}, map[string]bool{}
	var memory int64
	var walk func(o *Object, socket int)
	walk = func(o *Object, socket int) {
		for _, c := range o.Children {
			s := socket
			switch c.Type {
			case "Package":
				s = f.Sockets
				f.Sockets++
				f.GPUs.PerSocket = append(f.GPUs.PerSocket, 0)
				f.NICs.PerSocket = append(f.NICs.PerSocket, 0)
				if m := strings.TrimSpace(c.Attrs["CPUModel"]); m != "" {
					cpus[m] = true
				}
			case "Core":
				f.Cores++
			case "PU":
				f.PUs++
			case "NUMANode":
				f.NUMA++
				memory += c.Size
			case "L3Cache":
				l3[int(c.Size>>20)] = true
			case "L2Cache":
				l2[int(c.Size>>10)] = true
			case "PCIDev":
				if kind := deviceKind(c); kind != "" {
					spread := &f.NICs
					if kind == "gpu" {
						spread = &f.GPUs
					}
					if s < 0 {
						spread.Machine++
					} else {
						spread.PerSocket[s]++
					}
				}
			}
			walk(c, s)
		}
	}
	walk(root, -1)
	if memory == 0 {
		memory = root.Size
	}
	f.MemoryGB = float64(memory*10/(1<<30)) / 10
	f.L3MB, f.L2KB = sortedInts(l3), sortedInts(l2)
	for m := range cpus {
		f.CPUModels = append(f.CPUModels, m)
	}
	sort.Strings(f.CPUModels)
	return f
}

// deviceKind classifies a PCI device by class: gpu, network or "".
func deviceKind(o *Object) string {
	class := strings.SplitN(o.Attrs["pci_type"], " ", 2)[0]
	switch {
	case strings.HasPrefix(class, "03"):
		return "gpu"
	case strings.HasPrefix(class, "02"):
		return "network"
	}
	return ""
}

func sortedInts(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
