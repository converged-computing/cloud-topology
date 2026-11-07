// Package database reads and writes the static-file database of Google Cloud
// machine types and their captured hardware topologies.
//
// Layout under the data directory:
//
//	index.json                           summary of everything below
//	machine-types/<name>.json            one machine type from the Compute Engine catalog
//	disk-types/<name>.json               one disk type and the zones that offer it
//	topology/<name>/hwloc.xml            hwloc topology captured on a real instance
//	topology/<name>/capture.json         how and when the topology was captured
package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Accelerator is a GPU or other accelerator attached to a machine type.
type Accelerator struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// MachineType is one Compute Engine machine type and the zones that offer it.
type MachineType struct {
	Name         string        `json:"name"`
	Family       string        `json:"family"`
	Description  string        `json:"description,omitempty"`
	Architecture string        `json:"architecture,omitempty"`
	VCPUs        int           `json:"vcpus"`
	MemoryMB     int           `json:"memory_mb"`
	SharedCPU    bool          `json:"shared_cpu,omitempty"`
	Accelerators []Accelerator `json:"accelerators,omitempty"`
	// LocalSSDPartitions is the number of bundled Local SSD partitions, and
	// LocalSSDInterface their interface (e.g. NVME), from the catalog.
	LocalSSDPartitions int      `json:"local_ssd_partitions,omitempty"`
	LocalSSDInterface  string   `json:"local_ssd_interface,omitempty"`
	Deprecated         string   `json:"deprecated,omitempty"`
	Zones              []string `json:"zones"`
}

// LocalSSD reports whether the machine type includes Local SSD, from the catalog or
// its name.
func (m MachineType) LocalSSD() bool {
	return m.LocalSSDPartitions > 0 || HasLocalSSD(m.Name)
}

// GPUs returns the total number of accelerators attached to the machine type.
func (m MachineType) GPUs() int {
	total := 0
	for _, a := range m.Accelerators {
		total += a.Count
	}
	return total
}

// Regions returns the sorted, unique regions derived from the machine type's zones.
func (m MachineType) Regions() []string {
	seen := map[string]bool{}
	for _, z := range m.Zones {
		seen[RegionOf(z)] = true
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Capture records how a topology was captured.
type Capture struct {
	MachineType      string  `json:"machine_type"`
	Zone             string  `json:"zone"`
	Image            string  `json:"image"`
	CapturedAt       string  `json:"captured_at"`
	ProvisionSeconds float64 `json:"provision_seconds"`
	// ProvisioningModel is spot or on-demand, as the capture instance actually ran.
	ProvisioningModel string `json:"provisioning_model,omitempty"`
	HwlocVersion      string `json:"hwloc_version,omitempty"`
	// Timings breaks ProvisionSeconds down where the provider reports it, for example
	// nodepool_seconds and pod_seconds on GKE.
	Timings map[string]float64 `json:"timings,omitempty"`
}

// Index summarizes the database contents.
type Index struct {
	Generated    string   `json:"generated"`
	MachineTypes []string `json:"machine_types"`
	DiskTypes    []string `json:"disk_types"`
	Topologies   []string `json:"topologies"`
	Zones        []string `json:"zones"`
}

// Database is the in-memory view of a data directory.
type Database struct {
	Dir          string
	MachineTypes map[string]MachineType
	DiskTypes    map[string]DiskType
	Captures     map[string]Capture
}

// DiskType is one Compute Engine disk type (pd-ssd, hyperdisk-balanced, ...) and the
// zones that offer it.
type DiskType struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	MinGB       int      `json:"min_gb"`
	MaxGB       int      `json:"max_gb"`
	DefaultGB   int      `json:"default_gb,omitempty"`
	Zones       []string `json:"zones"`
}

// WriteDiskType writes one disk type file.
func WriteDiskType(dir string, dt DiskType) error {
	sort.Strings(dt.Zones)
	return WriteJSON(filepath.Join(dir, "disk-types", dt.Name+".json"), dt)
}

// DiskTypesIn returns the disk types offered in a zone, sorted by name.
func (db *Database) DiskTypesIn(zone string) []DiskType {
	var out []DiskType
	for _, dt := range db.DiskTypes {
		for _, z := range dt.Zones {
			if z == zone {
				out = append(out, dt)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RegionOf returns the region for a zone name such as us-central1-a.
func RegionOf(zone string) string {
	i := strings.LastIndex(zone, "-")
	if i <= 0 {
		return zone
	}
	return zone[:i]
}

// FamilyOf returns the machine family prefix of a machine type name such as a3-highgpu-1g.
func FamilyOf(name string) string {
	if i := strings.Index(name, "-"); i > 0 {
		return name[:i]
	}
	return name
}

// SeriesOf returns the machine series of a machine type name: the first two name
// segments, such as n2-standard for n2-standard-8 or a3-highgpu for a3-highgpu-1g.
// Bare-metal types form their own series (c3-standard-metal), since a VM is not a cut
// of a bare-metal host. Local SSD variants (-lssd) stay in their series.
func SeriesOf(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return name
	}
	series := parts[0] + "-" + parts[1]
	if IsMetal(name) {
		series += "-metal"
	}
	return series
}

// IsMetal reports whether a machine type is bare metal (a "metal" name segment).
func IsMetal(name string) bool {
	parts := strings.Split(name, "-")
	for _, p := range parts[min(3, len(parts)):] {
		if p == "metal" {
			return true
		}
	}
	return false
}

// HasLocalSSD reports whether a machine type includes Local SSD by name, such as
// c3d-standard-360-lssd or z3-highmem-192-highlssd-metal ("nolssd" does not).
func HasLocalSSD(name string) bool {
	parts := strings.Split(name, "-")
	for _, p := range parts[min(3, len(parts)):] {
		if p != "nolssd" && strings.HasSuffix(p, "lssd") {
			return true
		}
	}
	return false
}

// Series groups machine types by series, each group sorted by vCPUs then name.
func Series(mts []MachineType) map[string][]MachineType {
	out := map[string][]MachineType{}
	for _, mt := range mts {
		s := SeriesOf(mt.Name)
		out[s] = append(out[s], mt)
	}
	for _, group := range out {
		sort.Slice(group, func(i, j int) bool {
			if group[i].VCPUs != group[j].VCPUs {
				return group[i].VCPUs < group[j].VCPUs
			}
			return group[i].Name < group[j].Name
		})
	}
	return out
}

// Load reads every machine type and topology capture under dir.
func Load(dir string) (*Database, error) {
	db := &Database{Dir: dir, MachineTypes: map[string]MachineType{}, DiskTypes: map[string]DiskType{}, Captures: map[string]Capture{}}
	mtDir := filepath.Join(dir, "machine-types")
	entries, err := os.ReadDir(mtDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var mt MachineType
		if err := readJSON(filepath.Join(mtDir, e.Name()), &mt); err != nil {
			return nil, err
		}
		db.MachineTypes[mt.Name] = mt
	}
	dtDir := filepath.Join(dir, "disk-types")
	entries, err = os.ReadDir(dtDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var dt DiskType
		if err := readJSON(filepath.Join(dtDir, e.Name()), &dt); err != nil {
			return nil, err
		}
		db.DiskTypes[dt.Name] = dt
	}
	topoDir := filepath.Join(dir, "topology")
	entries, err = os.ReadDir(topoDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(topoDir, e.Name(), "capture.json")
		var c Capture
		if err := readJSON(path, &c); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		db.Captures[e.Name()] = c
	}
	return db, nil
}

// Names returns the sorted machine type names.
func (db *Database) Names() []string {
	out := make([]string, 0, len(db.MachineTypes))
	for n := range db.MachineTypes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HwlocPath returns the path to a machine type's hwloc XML, or "" if none was captured.
func (db *Database) HwlocPath(name string) string {
	path := filepath.Join(db.Dir, "topology", name, "hwloc.xml")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Filter selects machine types.
type Filter struct {
	Region      string
	Zone        string
	Family      string
	Accelerator string
	MinGPUs     int
	Names       []string
}

// Query returns machine types matching the filter, sorted by name.
func (db *Database) Query(f Filter) []MachineType {
	want := map[string]bool{}
	for _, n := range f.Names {
		want[n] = true
	}
	var out []MachineType
	for _, name := range db.Names() {
		mt := db.MachineTypes[name]
		if len(want) > 0 && !want[name] {
			continue
		}
		if f.Family != "" && mt.Family != f.Family {
			continue
		}
		if f.MinGPUs > 0 && mt.GPUs() < f.MinGPUs {
			continue
		}
		if f.Accelerator != "" && !hasAccelerator(mt, f.Accelerator) {
			continue
		}
		if f.Zone != "" && !contains(mt.Zones, f.Zone) {
			continue
		}
		if f.Region != "" && !contains(mt.Regions(), f.Region) {
			continue
		}
		out = append(out, mt)
	}
	return out
}

// BuildIndex summarizes the database.
func (db *Database) BuildIndex(now time.Time) Index {
	zones := map[string]bool{}
	for _, mt := range db.MachineTypes {
		for _, z := range mt.Zones {
			zones[z] = true
		}
	}
	idx := Index{Generated: now.UTC().Format(time.RFC3339), MachineTypes: db.Names(), DiskTypes: []string{}}
	for n := range db.DiskTypes {
		idx.DiskTypes = append(idx.DiskTypes, n)
	}
	sort.Strings(idx.DiskTypes)
	for n := range db.Captures {
		idx.Topologies = append(idx.Topologies, n)
	}
	for z := range zones {
		idx.Zones = append(idx.Zones, z)
	}
	sort.Strings(idx.Topologies)
	sort.Strings(idx.Zones)
	if idx.MachineTypes == nil {
		idx.MachineTypes = []string{}
	}
	if idx.Topologies == nil {
		idx.Topologies = []string{}
	}
	if idx.Zones == nil {
		idx.Zones = []string{}
	}
	return idx
}

// WriteMachineType writes one machine type file with deterministic formatting.
func WriteMachineType(dir string, mt MachineType) error {
	sort.Strings(mt.Zones)
	sort.Slice(mt.Accelerators, func(i, j int) bool { return mt.Accelerators[i].Type < mt.Accelerators[j].Type })
	return WriteJSON(filepath.Join(dir, "machine-types", mt.Name+".json"), mt)
}

// WriteIndex writes index.json for the database.
func (db *Database) WriteIndex(now time.Time) error {
	return WriteJSON(filepath.Join(db.Dir, "index.json"), db.BuildIndex(now))
}

// WriteJSON writes v as indented JSON with a trailing newline, creating parent directories.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func hasAccelerator(mt MachineType, accel string) bool {
	for _, a := range mt.Accelerators {
		if a.Type == accel {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
