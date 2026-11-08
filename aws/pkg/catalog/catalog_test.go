package catalog

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestConvert(t *testing.T) {
	data, err := os.ReadFile("testdata/instance-types.json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		InstanceTypes []types.InstanceTypeInfo
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	raw := &Raw{Region: "us-east-1", InstanceTypes: page.InstanceTypes, Offerings: map[string][]string{
		"p5.48xlarge": {"us-east-1b", "us-east-1a"}, "c7i.48xlarge": {"us-east-1a"}, "trn1.32xlarge": {"us-east-1a"}, "t3.micro": {"us-east-1a"},
		"m7g.metal": {"us-east-1a"}, "d3.8xlarge": {"us-east-1a"}, "hpc7g.16xlarge": {"us-east-1a"},
	}}
	got := Convert(raw, Options{})
	if _, ok := got["u-6tb1.112xlarge"]; ok {
		t.Error("u-6tb1.112xlarge is offered nowhere and should be left out")
	}
	p5 := got["p5.48xlarge"]
	if p5.VCPUs != 192 || p5.Cores != 96 || p5.ThreadsPerCore != 2 || p5.MemoryMB != 2097152 {
		t.Errorf("p5 cpu/memory: %+v", p5)
	}
	if p5.GPUs() != 8 || p5.Accelerators[0].Type != "nvidia-h100" || p5.Accelerators[0].MemoryMiB != 81920 {
		t.Errorf("p5 accelerators: %+v", p5.Accelerators)
	}
	if len(p5.Network.Cards) != 32 || p5.EgressGbps() != 3200 || !p5.Network.EFASupported || p5.Network.MaxEFAInterfaces != 32 || !p5.Network.Cards[0].EFA {
		t.Errorf("p5 network: %+v", p5.Network)
	}
	if p5.InstanceStore == nil || p5.InstanceStore.TotalGB != 30400 || p5.InstanceStore.Disks[0].Count != 8 || p5.InstanceStore.Disks[0].Type != "ssd" {
		t.Errorf("p5 instance store: %+v", p5.InstanceStore)
	}
	if !p5.Cluster() || !p5.Spot() || p5.Zones[0] != "us-east-1a" {
		t.Errorf("p5 placement/usage/zones: %v %v %v", p5.PlacementStrategies, p5.UsageClasses, p5.Zones)
	}
	trn := got["trn1.32xlarge"]
	if trn.GPUs() != 16 || trn.Accelerators[0].Type != "aws-trainium" {
		t.Errorf("trn1 accelerators: %+v", trn.Accelerators)
	}
	m7g := got["m7g.metal"]
	if !m7g.BareMetal || m7g.Architecture != "arm64" || m7g.ThreadsPerCore != 1 || m7g.Hypervisor != "" {
		t.Errorf("m7g.metal: %+v", m7g)
	}
	d3 := got["d3.8xlarge"]
	if d3.InstanceStore.Disks[0].Type != "hdd" || d3.InstanceStore.Disks[0].Count != 24 {
		t.Errorf("d3 disks: %+v", d3.InstanceStore)
	}
	if t3 := got["t3.micro"]; !t3.Burstable || t3.Cluster() || t3.EBS.MaxIOPS != 11800 {
		t.Errorf("t3.micro: %+v", t3)
	}
	if hpc := got["hpc7g.16xlarge"]; hpc.Spot() || hpc.EgressGbps() != 200 {
		t.Errorf("hpc7g: %+v", hpc)
	}
}
