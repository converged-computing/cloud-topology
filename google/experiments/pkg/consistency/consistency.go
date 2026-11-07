// Package consistency compares every capture of each machine type across databases: the
// rounds of a consistency study and any other databases given, reporting which machine
// types captured the same hardware every time and which did not.
package consistency

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/topology"
)

// Source is one database to compare, under a label.
type Source struct {
	Label string
	Dir   string
}

// Observation is one capture of a machine type in one database.
type Observation struct {
	Database   string               `json:"database"`
	Zone       string               `json:"zone"`
	Model      string               `json:"model"`
	CapturedAt string               `json:"captured_at"`
	ShapeID    string               `json:"shape"`
	Shape      topology.Fingerprint `json:"fingerprint"`
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

// Compare reads the databases and reports each machine type's captures.
func Compare(sources []Source) (*ConsistencyReport, error) {
	byType := map[string]*TypeConsistency{}
	r := &ConsistencyReport{}
	for _, s := range sources {
		r.Databases = append(r.Databases, s.Label)
		db, err := database.Load(s.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Label, err)
		}
		for name, c := range db.Captures {
			tree, err := topology.ReadObjects(filepath.Join(s.Dir, "topology", name, "hwloc.xml"))
			if err != nil || tree == nil {
				continue
			}
			fp := topology.FingerprintOf(tree)
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

func fieldValue(f topology.Fingerprint, field string) string {
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
