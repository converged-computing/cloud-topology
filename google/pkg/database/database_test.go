package database

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const fixtures = "../../testdata"

func TestLoadFixtures(t *testing.T) {
	db, err := Load(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Names(); !reflect.DeepEqual(got, []string{"test-cpu-4", "test-gpu-2g"}) {
		t.Fatalf("unexpected names %v", got)
	}
	if _, ok := db.Captures["test-gpu-2g"]; !ok {
		t.Error("capture for test-gpu-2g not loaded")
	}
	if db.HwlocPath("test-cpu-4") != "" {
		t.Error("test-cpu-4 should have no topology")
	}
}

func TestQuery(t *testing.T) {
	db, err := Load(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		f    Filter
		want int
	}{
		{Filter{Region: "us-central1"}, 2},
		{Filter{Region: "europe-west4"}, 1},
		{Filter{Zone: "us-central1-b"}, 1},
		{Filter{MinGPUs: 1}, 1},
		{Filter{Accelerator: "nvidia-h100-80gb"}, 1},
		{Filter{Family: "test", Names: []string{"test-cpu-4"}}, 1},
		{Filter{Region: "asia-east1"}, 0},
	}
	for _, c := range cases {
		if got := len(db.Query(c.f)); got != c.want {
			t.Errorf("%+v: got %d, want %d", c.f, got, c.want)
		}
	}
}

func TestRegionAndFamily(t *testing.T) {
	if RegionOf("us-central1-a") != "us-central1" || RegionOf("europe-west4-c") != "europe-west4" {
		t.Error("RegionOf")
	}
	if FamilyOf("a3-highgpu-1g") != "a3" || FamilyOf("e2") != "e2" {
		t.Error("FamilyOf")
	}
}

func TestSeriesVariants(t *testing.T) {
	cases := []struct {
		name, series string
		metal, lssd  bool
	}{
		{"n2-standard-8", "n2-standard", false, false},
		{"c3-standard-192-metal", "c3-standard-metal", true, false},
		{"c3d-highmem-360-lssd", "c3d-highmem", false, true},
		{"z3-highmem-192-highlssd-metal", "z3-highmem-metal", true, true},
		{"a3-edgegpu-8g-nolssd", "a3-edgegpu", false, false},
		{"e2-micro", "e2-micro", false, false},
	}
	for _, c := range cases {
		if got := SeriesOf(c.name); got != c.series {
			t.Errorf("SeriesOf(%s) = %s, want %s", c.name, got, c.series)
		}
		if IsMetal(c.name) != c.metal || HasLocalSSD(c.name) != c.lssd {
			t.Errorf("%s: metal=%v lssd=%v", c.name, IsMetal(c.name), HasLocalSSD(c.name))
		}
	}
}

func TestWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mt := MachineType{Name: "x1-test", Family: "x1", VCPUs: 2, MemoryMB: 1024, Zones: []string{"b-z-b", "a-z-a"}}
	if err := WriteMachineType(dir, mt); err != nil {
		t.Fatal(err)
	}
	db, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := db.MachineTypes["x1-test"].Zones; !reflect.DeepEqual(got, []string{"a-z-a", "b-z-b"}) {
		t.Errorf("zones not sorted on write: %v", got)
	}
	if err := db.WriteIndex(time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	var idx Index
	if err := readJSON(filepath.Join(dir, "index.json"), &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.MachineTypes) != 1 || len(idx.Zones) != 2 || idx.Topologies == nil {
		t.Errorf("unexpected index %+v", idx)
	}
}
