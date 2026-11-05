package site

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
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
	f.MemoryGB = gb(memory)
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

// Observation is one capture of a machine type in one database.
type Observation struct {
	Database   string      `json:"database"`
	Zone       string      `json:"zone"`
	Model      string      `json:"model"`
	CapturedAt string      `json:"captured_at"`
	ShapeID    string      `json:"shape"`
	Shape      Fingerprint `json:"fingerprint"`
}

// TypeConsistency is what repeated captures say about one machine type.
type TypeConsistency struct {
	MachineType  string        `json:"machine_type"`
	Captures     int           `json:"captures"`
	Shapes       int           `json:"shapes"`
	Varies       []string      `json:"varies,omitempty"`
	Zones        []string      `json:"zones"`
	Observations []Observation `json:"observations"`
}

// ConsistencyReport compares every capture of each machine type across databases.
type ConsistencyReport struct {
	Databases  []string          `json:"databases"`
	Types      []TypeConsistency `json:"types"`
	Consistent int               `json:"consistent"`
	Varying    int               `json:"varying"`
	Single     int               `json:"single"`
}

// Consistency reads the databases (label=dir) and reports each machine type's captures.
func Consistency(sources []Source) (*ConsistencyReport, error) {
	byType := map[string]*TypeConsistency{}
	r := &ConsistencyReport{}
	for _, s := range sources {
		r.Databases = append(r.Databases, s.Label)
		db, err := database.Load(s.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Label, err)
		}
		for name, c := range db.Captures {
			tree, err := ReadHwloc(filepath.Join(s.Dir, "topology", name, "hwloc.xml"))
			if err != nil || tree == nil {
				continue
			}
			fp := FingerprintOf(tree)
			t := byType[name]
			if t == nil {
				t = &TypeConsistency{MachineType: name}
				byType[name] = t
			}
			t.Observations = append(t.Observations, Observation{Database: s.Label, Zone: c.Zone, Model: c.ProvisioningModel, CapturedAt: c.CapturedAt, ShapeID: fp.ID(), Shape: fp})
		}
	}
	for _, t := range byType {
		sort.Slice(t.Observations, func(i, j int) bool { return t.Observations[i].CapturedAt < t.Observations[j].CapturedAt })
		shapes, zones, varies := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for i, o := range t.Observations {
			shapes[o.ShapeID] = true
			zones[o.Zone] = true
			if i > 0 {
				for _, f := range t.Observations[0].Shape.Fields(o.Shape) {
					varies[f] = true
				}
			}
		}
		t.Captures, t.Shapes = len(t.Observations), len(shapes)
		for z := range zones {
			t.Zones = append(t.Zones, z)
		}
		sort.Strings(t.Zones)
		for f := range varies {
			t.Varies = append(t.Varies, f)
		}
		sort.Strings(t.Varies)
		switch {
		case t.Captures < 2:
			r.Single++
		case t.Shapes == 1:
			r.Consistent++
		default:
			r.Varying++
		}
		r.Types = append(r.Types, *t)
	}
	sort.Slice(r.Types, func(i, j int) bool {
		if (r.Types[i].Shapes > 1) != (r.Types[j].Shapes > 1) {
			return r.Types[i].Shapes > 1
		}
		return r.Types[i].MachineType < r.Types[j].MachineType
	})
	return r, nil
}

// Markdown renders the consistency report.
func (r *ConsistencyReport) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Capture consistency across %d databases\n\n", len(r.Databases))
	fmt.Fprintf(&b, "%d machine types captured more than once: %d consistent, %d varying; %d captured once.\n\n", r.Consistent+r.Varying, r.Consistent, r.Varying, r.Single)
	b.WriteString("| Machine type | Captures | Shapes | Varies in | Zones | CPU model |\n|---|---|---|---|---|---|\n")
	for _, t := range r.Types {
		cpu := ""
		if len(t.Observations) > 0 {
			cpu = strings.Join(t.Observations[0].Shape.CPUModels, ", ")
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s | %s |\n", t.MachineType, t.Captures, t.Shapes, strings.Join(t.Varies, ", "), strings.Join(t.Zones, ", "), cpu)
	}
	for _, t := range r.Types {
		if t.Shapes < 2 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Captured | Database | Zone | Model | Shape | %s |\n|---|---|---|---|---|---|\n", t.MachineType, strings.Join(t.Varies, " | "))
		for _, o := range t.Observations {
			var vals []string
			for _, f := range t.Varies {
				vals = append(vals, fieldValue(o.Shape, f))
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", o.CapturedAt, o.Database, o.Zone, o.Model, o.ShapeID, strings.Join(vals, " | "))
		}
	}
	return b.String()
}

func fieldValue(f Fingerprint, field string) string {
	switch field {
	case "sockets":
		return fmt.Sprint(f.Sockets)
	case "cores":
		return fmt.Sprint(f.Cores)
	case "pus":
		return fmt.Sprint(f.PUs)
	case "numa":
		return fmt.Sprint(f.NUMA)
	case "memory":
		return fmt.Sprintf("%.4g GB", f.MemoryGB)
	case "l3":
		return fmt.Sprintf("%v MB", f.L3MB)
	case "l2":
		return fmt.Sprintf("%v KB", f.L2KB)
	case "cpu model":
		return strings.Join(f.CPUModels, ", ")
	case "gpu placement":
		return fmt.Sprintf("sockets %v, machine %d", f.GPUs.PerSocket, f.GPUs.Machine)
	case "nic placement":
		return fmt.Sprintf("sockets %v, machine %d", f.NICs.PerSocket, f.NICs.Machine)
	}
	return ""
}

// RoundSources lists the round databases under a consistency study directory, oldest
// first, labelled by their directory names.
func RoundSources(dir string) ([]Source, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "rounds"))
	if err != nil {
		return nil, err
	}
	var out []Source
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, Source{Label: "round/" + e.Name(), Dir: filepath.Join(dir, "rounds", e.Name())})
		}
	}
	return out, nil
}
