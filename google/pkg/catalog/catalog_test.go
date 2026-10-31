package catalog

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"
)

func machineType(name, zone string, cpus, memMB int32, accel string, count int32) *computepb.MachineType {
	m := &computepb.MachineType{
		Name:      proto.String(name),
		Zone:      proto.String(zone),
		GuestCpus: proto.Int32(cpus),
		MemoryMb:  proto.Int32(memMB),
	}
	if accel != "" {
		m.Accelerators = []*computepb.Accelerators{{
			GuestAcceleratorType:  proto.String(accel),
			GuestAcceleratorCount: proto.Int32(count),
		}}
	}
	return m
}

func TestConvertMergesZones(t *testing.T) {
	pairs := []Pair{
		{Key: "zones/us-central1-a", MachineTypes: []*computepb.MachineType{
			machineType("a3-highgpu-1g", "us-central1-a", 26, 239616, "nvidia-h100-80gb", 1),
			machineType("n2-standard-4", "us-central1-a", 4, 16384, "", 0),
		}},
		{Key: "zones/us-central1-b", MachineTypes: []*computepb.MachineType{
			machineType("a3-highgpu-1g", "us-central1-b", 26, 239616, "nvidia-h100-80gb", 1),
		}},
		{Key: "zones/europe-west4-a", MachineTypes: []*computepb.MachineType{
			machineType("a3-highgpu-1g", "europe-west4-a", 26, 239616, "nvidia-h100-80gb", 1),
		}},
	}
	got := Convert(pairs, Options{Regions: []string{"us-central1"}})
	if len(got) != 2 {
		t.Fatalf("expected 2 machine types, got %d", len(got))
	}
	a3 := got["a3-highgpu-1g"]
	if a3.Family != "a3" || a3.VCPUs != 26 || a3.GPUs() != 1 {
		t.Errorf("unexpected a3 record: %+v", a3)
	}
	if len(a3.Zones) != 2 {
		t.Errorf("expected zones from us-central1 only, got %v", a3.Zones)
	}
}

func TestConvertFiltersFamiliesAndDeprecated(t *testing.T) {
	old := machineType("n1-standard-1", "us-central1-a", 1, 3840, "", 0)
	old.Deprecated = &computepb.DeprecationStatus{State: proto.String("DEPRECATED")}
	pairs := []Pair{{Key: "zones/us-central1-a", MachineTypes: []*computepb.MachineType{
		old,
		machineType("g2-standard-8", "us-central1-a", 8, 32768, "nvidia-l4", 1),
		machineType("n2-standard-4", "us-central1-a", 4, 16384, "", 0),
	}}}
	got := Convert(pairs, Options{Families: []string{"g2", "n1"}})
	if _, ok := got["n2-standard-4"]; ok {
		t.Error("family filter did not remove n2")
	}
	if _, ok := got["n1-standard-1"]; ok {
		t.Error("deprecated machine type was kept")
	}
	if _, ok := got["g2-standard-8"]; !ok {
		t.Error("g2-standard-8 missing")
	}
	got = Convert(pairs, Options{Families: []string{"n1"}, IncludeDeprecated: true})
	if got["n1-standard-1"].Deprecated != "DEPRECATED" {
		t.Error("deprecated state not recorded")
	}
}

func TestConvertZoneFromKey(t *testing.T) {
	m := machineType("c3-standard-4", "", 4, 16384, "", 0)
	got := Convert([]Pair{{Key: "zones/us-east1-b", MachineTypes: []*computepb.MachineType{m}}}, Options{})
	if z := got["c3-standard-4"].Zones; len(z) != 1 || z[0] != "us-east1-b" {
		t.Errorf("zone not taken from key: %v", z)
	}
}

func TestConvertLocalSSD(t *testing.T) {
	m := machineType("c3d-standard-8-lssd", "us-central1-a", 8, 32768, "", 0)
	m.BundledLocalSsds = &computepb.BundledLocalSsds{PartitionCount: proto.Int32(1), DefaultInterface: proto.String("NVME")}
	got := Convert([]Pair{{Key: "zones/us-central1-a", MachineTypes: []*computepb.MachineType{m}}}, Options{})["c3d-standard-8-lssd"]
	if got.LocalSSDPartitions != 1 || got.LocalSSDInterface != "NVME" || !got.LocalSSD() {
		t.Errorf("local SSD not recorded: %+v", got)
	}
}
