// Package database reads and writes the static-file database of EC2 instance types and
// their captured hardware topologies. The layout and the common fields are those of the
// Google database (google/pkg/database), so tools that read one can read the other:
//
//	index.json                           summary of everything below
//	zones.json                           availability zone name to zone id
//	machine-types/<name>.json            one instance type from the EC2 catalog
//	disk-types/<name>.json               one EBS volume type and the zones that offer it
//	topology/<name>/hwloc.xml            hwloc topology captured on a real instance
//	topology/<name>/capture.json         how and when the topology was captured
//
// EC2 calls them instance types; the files keep the Google name so the two databases
// have the same shape.
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

// Accelerator is a GPU or other accelerator (Trainium, Inferentia) on an instance type.
type Accelerator struct {
	// Type is the device name in the Google style, e.g. nvidia-h100, aws-trainium.
	Type         string `json:"type"`
	Count        int    `json:"count"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Name         string `json:"name,omitempty"`
	MemoryMiB    int    `json:"memory_mib,omitempty"`
}

// Processor is what the catalog says about the CPU.
type Processor struct {
	Manufacturer string  `json:"manufacturer,omitempty"`
	ClockGHz     float64 `json:"clock_ghz,omitempty"`
}

// NetworkCard is one physical network card; its NICs (ENIs) share its bandwidth.
type NetworkCard struct {
	Index        int     `json:"index"`
	BaselineGbps float64 `json:"baseline_gbps"`
	PeakGbps     float64 `json:"peak_gbps"`
	// EFA is true when the card accepts an Elastic Fabric Adapter interface.
	EFA bool `json:"efa,omitempty"`
}

// Network is the catalog's network description, which EC2 reports per instance type.
type Network struct {
	Performance      string        `json:"performance"`
	MaxInterfaces    int           `json:"max_interfaces"`
	EFASupported     bool          `json:"efa_supported,omitempty"`
	MaxEFAInterfaces int           `json:"max_efa_interfaces,omitempty"`
	ENASRD           bool          `json:"ena_srd,omitempty"`
	Cards            []NetworkCard `json:"cards"`
}

// Disk is a set of identical instance store volumes.
type Disk struct {
	SizeGB int    `json:"size_gb"`
	Count  int    `json:"count"`
	Type   string `json:"type"` // ssd or hdd
}

// InstanceStore is the local storage that comes with the instance type.
type InstanceStore struct {
	TotalGB int    `json:"total_gb"`
	NVMe    string `json:"nvme,omitempty"`
	Disks   []Disk `json:"disks"`
}

// EBS is the instance type's EBS-optimized performance.
type EBS struct {
	BaselineMbps   int `json:"baseline_mbps"`
	MaxMbps        int `json:"max_mbps"`
	BaselineIOPS   int `json:"baseline_iops"`
	MaxIOPS        int `json:"max_iops"`
	MaxAttachments int `json:"max_attachments,omitempty"`
}

// MachineType is one EC2 instance type and the zones that offer it.
type MachineType struct {
	Name         string `json:"name"`
	Family       string `json:"family"`
	Architecture string `json:"architecture,omitempty"`
	VCPUs        int    `json:"vcpus"`
	// Cores and ThreadsPerCore are the default CPU options: EC2 reports physical cores,
	// which the Google catalog does not.
	Cores          int           `json:"cores,omitempty"`
	ThreadsPerCore int           `json:"threads_per_core,omitempty"`
	MemoryMB       int           `json:"memory_mb"`
	Accelerators   []Accelerator `json:"accelerators,omitempty"`
	BareMetal      bool          `json:"bare_metal,omitempty"`
	Hypervisor     string        `json:"hypervisor,omitempty"`
	// CurrentGeneration is false for previous-generation types (m1, c3, ...).
	CurrentGeneration bool `json:"current_generation"`
	Burstable         bool `json:"burstable,omitempty"`
	// UsageClasses are the purchasing options: on-demand, spot, capacity-block.
	UsageClasses        []string       `json:"usage_classes"`
	Processor           Processor      `json:"processor"`
	Network             Network        `json:"network"`
	InstanceStore       *InstanceStore `json:"instance_store,omitempty"`
	EBS                 EBS            `json:"ebs"`
	PlacementStrategies []string       `json:"placement_strategies,omitempty"`
	Zones               []string       `json:"zones"`
}

// GPUs returns the total number of accelerators on the instance type.
func (m MachineType) GPUs() int {
	total := 0
	for _, a := range m.Accelerators {
		total += a.Count
	}
	return total
}

// LocalSSD reports whether the instance type has instance store volumes.
func (m MachineType) LocalSSD() bool {
	return m.InstanceStore != nil && len(m.InstanceStore.Disks) > 0
}

// Spot reports whether the instance type can be bought as Spot.
func (m MachineType) Spot() bool { return contains(m.UsageClasses, "spot") }

// EgressGbps is the sum of the network cards' peak bandwidth.
func (m MachineType) EgressGbps() float64 {
	total := 0.0
	for _, c := range m.Network.Cards {
		total += c.PeakGbps
	}
	return total
}

// Cluster reports whether the instance type can join a cluster placement group.
func (m MachineType) Cluster() bool { return contains(m.PlacementStrategies, "cluster") }

// Regions returns the sorted, unique regions of the instance type's zones.
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
	MachineType       string             `json:"machine_type"`
	Zone              string             `json:"zone"`
	Image             string             `json:"image"`
	CapturedAt        string             `json:"captured_at"`
	ProvisionSeconds  float64            `json:"provision_seconds"`
	ProvisioningModel string             `json:"provisioning_model,omitempty"`
	HwlocVersion      string             `json:"hwloc_version,omitempty"`
	Timings           map[string]float64 `json:"timings,omitempty"`
}

// DiskType is one EBS volume type and the zones that offer it.
type DiskType struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	MinGB       int      `json:"min_gb"`
	MaxGB       int      `json:"max_gb"`
	MaxIOPS     int      `json:"max_iops,omitempty"`
	MaxMiBps    int      `json:"max_mibps,omitempty"`
	Zones       []string `json:"zones"`
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
	// ZoneIDs maps an availability zone name, which differs by account, to its zone id.
	ZoneIDs map[string]string
}

// RegionOf returns the region of an availability zone: us-east-1a is in us-east-1.
// Local Zones (us-east-1-bos-1a) and Wavelength Zones belong to their parent region.
func RegionOf(zone string) string {
	parts := strings.Split(zone, "-")
	if len(parts) < 3 {
		return zone
	}
	region := parts[0] + "-" + parts[1] + "-" + strings.TrimRight(parts[2], "abcdefghijklmnopqrstuvwxyz")
	return region
}

// FamilyOf returns the instance family of an instance type: c7i for c7i.48xlarge,
// u-6tb1 for u-6tb1.112xlarge.
func FamilyOf(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// SizeOf returns the size of an instance type: 48xlarge for c7i.48xlarge.
func SizeOf(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[i+1:]
	}
	return ""
}

// IsMetal reports whether an instance type is bare metal by name (c7i.metal-24xl).
func IsMetal(name string) bool {
	return strings.HasPrefix(SizeOf(name), "metal")
}

// SeriesOf returns the series an instance type's sizes are cut from: the family, with
// bare-metal sizes in their own series (c7i.metal), since a VM is not a cut of a host.
func SeriesOf(name string) string {
	if IsMetal(name) {
		return FamilyOf(name) + ".metal"
	}
	return FamilyOf(name)
}

// Series groups instance types by series, each group sorted by vCPUs then name.
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

// Load reads every instance type, disk type and topology capture under dir.
func Load(dir string) (*Database, error) {
	db := &Database{Dir: dir, MachineTypes: map[string]MachineType{}, DiskTypes: map[string]DiskType{}, Captures: map[string]Capture{}, ZoneIDs: map[string]string{}}
	if err := loadDir(filepath.Join(dir, "machine-types"), func(path string) error {
		var mt MachineType
		if err := readJSON(path, &mt); err != nil {
			return err
		}
		db.MachineTypes[mt.Name] = mt
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadDir(filepath.Join(dir, "disk-types"), func(path string) error {
		var dt DiskType
		if err := readJSON(path, &dt); err != nil {
			return err
		}
		db.DiskTypes[dt.Name] = dt
		return nil
	}); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "zones.json"), &db.ZoneIDs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	topoDir := filepath.Join(dir, "topology")
	entries, err := os.ReadDir(topoDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var c Capture
		if err := readJSON(filepath.Join(topoDir, e.Name(), "capture.json"), &c); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		db.Captures[e.Name()] = c
	}
	return db, nil
}

func loadDir(dir string, each func(path string) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if err := each(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Names returns the sorted instance type names.
func (db *Database) Names() []string {
	out := make([]string, 0, len(db.MachineTypes))
	for n := range db.MachineTypes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HwlocPath returns the path to an instance type's hwloc XML, or "" if none was captured.
func (db *Database) HwlocPath(name string) string {
	path := filepath.Join(db.Dir, "topology", name, "hwloc.xml")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// DiskTypesIn returns the disk types offered in a zone, sorted by name.
func (db *Database) DiskTypesIn(zone string) []DiskType {
	var out []DiskType
	for _, dt := range db.DiskTypes {
		if contains(dt.Zones, zone) {
			out = append(out, dt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Filter selects instance types.
type Filter struct {
	Region  string
	Zone    string
	Family  string
	MinGPUs int
	Names   []string
	// CurrentOnly leaves out previous-generation types.
	CurrentOnly bool
}

// Query returns instance types matching the filter, sorted by name.
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
		if f.CurrentOnly && !mt.CurrentGeneration {
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
	idx := Index{Generated: now.UTC().Format(time.RFC3339), MachineTypes: db.Names(), DiskTypes: []string{}, Topologies: []string{}, Zones: []string{}}
	for n := range db.DiskTypes {
		idx.DiskTypes = append(idx.DiskTypes, n)
	}
	for n := range db.Captures {
		idx.Topologies = append(idx.Topologies, n)
	}
	for z := range zones {
		idx.Zones = append(idx.Zones, z)
	}
	sort.Strings(idx.DiskTypes)
	sort.Strings(idx.Topologies)
	sort.Strings(idx.Zones)
	return idx
}

// WriteMachineType writes one instance type file with deterministic formatting.
func WriteMachineType(dir string, mt MachineType) error {
	sort.Strings(mt.Zones)
	sort.Slice(mt.Accelerators, func(i, j int) bool { return mt.Accelerators[i].Type < mt.Accelerators[j].Type })
	return WriteJSON(filepath.Join(dir, "machine-types", mt.Name+".json"), mt)
}

// WriteDiskType writes one disk type file.
func WriteDiskType(dir string, dt DiskType) error {
	sort.Strings(dt.Zones)
	return WriteJSON(filepath.Join(dir, "disk-types", dt.Name+".json"), dt)
}

// WriteZones writes zones.json.
func WriteZones(dir string, ids map[string]string) error {
	return WriteJSON(filepath.Join(dir, "zones.json"), ids)
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
