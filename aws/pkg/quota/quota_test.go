package quota

import (
	"testing"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
)

func TestClass(t *testing.T) {
	cases := map[string]string{
		"c7i": standard, "m7g": standard, "r8g": standard, "t3": standard, "a1": standard, "d3": standard, "h1": standard,
		"i7ie": standard, "im4gn": standard, "is4gen": standard, "z1d": standard, "c8gn": standard,
		"hpc7a": "HPC", "hpc7g": "HPC", "inf2": "Inf", "trn1": "Trn", "trn2u": "Trn", "dl1": "DL", "dl2q": "DL",
		"g6e": "G and VT", "gr6": "G and VT", "vt1": "G and VT", "p5": "P", "p5en": "P", "f2": "F",
		"x2iedn": "X", "x8g": "X", "u-6tb1": "High Memory", "u7i-12tb": "High Memory", "mac2-m2": "", "mac1": "",
	}
	for family, want := range cases {
		got, _ := Class(database.MachineType{Family: family})
		if got != want {
			t.Errorf("Class(%s) = %q, want %q", family, got, want)
		}
	}
}

func TestCompute(t *testing.T) {
	snap := &Snapshot{Quotas: []Quota{
		{Name: QuotaName(standard, "on-demand"), Region: "us-east-1", Value: 32768},
		{Name: QuotaName(standard, "spot"), Region: "us-east-1", Value: 640},
		{Name: QuotaName("P", "on-demand"), Region: "us-east-1", Value: 480},
		{Name: "Storage for General Purpose SSD (gp3) volumes, in TiB", Region: "us-east-1", Value: 60},
	}}
	c7i := database.MachineType{Name: "c7i.48xlarge", Family: "c7i", VCPUs: 192, UsageClasses: []string{"on-demand", "spot"}}
	if c := Compute(snap, c7i, "on-demand", "us-east-1"); c.Nodes != 170 || c.UsageKnown {
		t.Errorf("c7i on-demand: %+v", c)
	}
	if c := Compute(snap, c7i, "spot", "us-east-1"); c.Nodes != 3 {
		t.Errorf("c7i spot: %+v", c)
	}
	p5 := database.MachineType{Name: "p5.48xlarge", Family: "p5", VCPUs: 192, UsageClasses: []string{"on-demand", "spot"}}
	if c := Compute(snap, p5, "on-demand", "us-east-1"); c.Nodes != 2 || c.Binding != "Running On-Demand P instances" {
		t.Errorf("p5 on-demand: %+v", c)
	}
	if c := Compute(snap, p5, "spot", "us-east-1"); c.Nodes != 0 || c.Note == "" {
		t.Errorf("p5 spot without a quota in the snapshot: %+v", c)
	}
	hpc := database.MachineType{Name: "hpc7a.96xlarge", Family: "hpc7a", VCPUs: 192, UsageClasses: []string{"on-demand"}}
	if c := Compute(snap, hpc, "spot", "us-east-1"); c.Nodes != 0 || c.Note != "no spot usage class" {
		t.Errorf("hpc7a spot: %+v", c)
	}
	if c := Compute(snap, database.MachineType{Name: "mac2.metal", Family: "mac2", VCPUs: 8}, "on-demand", "us-east-1"); c.Known {
		t.Errorf("mac2 should be unknown: %+v", c)
	}
	if gb, ok := snap.StorageFunc("us-east-1")("gp3"); !ok || gb != 60*1024 {
		t.Errorf("gp3 storage: %d %v", gb, ok)
	}
	if _, ok := snap.StorageFunc("us-east-1")("gp9"); ok {
		t.Error("unknown volume type should not be bounded")
	}
	u := SeriesUnits(snap, "c7i", []database.MachineType{c7i}, "on-demand", "us-east-1")
	if u.VCPUs != 32768 || u.GPUs != -1 {
		t.Errorf("c7i units: %+v", u)
	}
}
