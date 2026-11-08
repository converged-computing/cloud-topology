package plan

import (
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/prices"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/quota"
)

func TestBuild(t *testing.T) {
	db, err := database.Load("../../testdata")
	if err != nil {
		t.Fatal(err)
	}
	std := "Standard (A, C, D, H, I, M, R, T, Z)"
	snap := &quota.Snapshot{Quotas: []quota.Quota{
		{Name: quota.QuotaName(std, "on-demand"), Region: "us-east-1", Value: 32768},
		{Name: quota.QuotaName(std, "spot"), Region: "us-east-1", Value: 640},
		{Name: quota.QuotaName("P", "on-demand"), Region: "us-east-1", Value: 480},
		{Name: quota.QuotaName("P", "spot"), Region: "us-east-1", Value: 0},
		{Name: quota.QuotaName("HPC", "on-demand"), Region: "us-east-1", Value: 32768},
	}}
	pr := &prices.Prices{OnDemand: map[string]float64{"c7i.48xlarge": 8.568, "p5.48xlarge": 55.04, "t3.micro": 0.0104, "d3.8xlarge": 3.99, "hpc7g.16xlarge": 1.68},
		Spot: map[string]map[string]float64{"c7i.48xlarge": {"us-east-1a": 3.0, "us-east-1b": 2.5}, "t3.micro": {"us-east-1a": 0.003}}}
	r := Build(db, snap, pr, Options{Region: "us-east-1", PriceCap: 25, LimitMinutes: 30})
	byType := map[string]Capture{}
	for _, c := range r.Captures {
		byType[c.MachineType] = c
	}
	// p5 is above the cap; the rest are in, cheapest first.
	if _, ok := byType["p5.48xlarge"]; ok || !strings.Contains(strings.Join(r.Excluded, ";"), "p5.48xlarge: $55.04/h is above") {
		t.Errorf("p5 should be excluded by the cap: %v %v", r.Captures, r.Excluded)
	}
	if len(r.Captures) != 4 || r.Captures[0].MachineType != "t3.micro" {
		t.Errorf("captures: %+v", r.Captures)
	}
	c7i := byType["c7i.48xlarge"]
	if c7i.Model != "spot" || c7i.Hourly != 2.5 || c7i.Zones[0] != "us-east-1b" || c7i.Zones[1] != "us-east-1a" {
		t.Errorf("c7i: %+v", c7i)
	}
	if !strings.HasPrefix(c7i.Command(), "make topology MACHINE_TYPE=c7i.48xlarge ZONE=us-east-1b,us-east-1a,") || !strings.HasSuffix(c7i.Command(), " SPOT=1") {
		t.Errorf("command: %s", c7i.Command())
	}
	// hpc7g has no spot usage class; d3 has a spot class but no Spot price in the table,
	// so it is priced On-Demand for the estimate yet still launched as Spot.
	if byType["hpc7g.16xlarge"].Model != "spot" && byType["hpc7g.16xlarge"].Model != "on-demand" {
		t.Errorf("hpc7g: %+v", byType["hpc7g.16xlarge"])
	}
	if byType["hpc7g.16xlarge"].Model == "spot" {
		t.Errorf("hpc7g is On-Demand only: %+v", byType["hpc7g.16xlarge"])
	}
	if d3 := byType["d3.8xlarge"]; d3.Model != "spot" || d3.Hourly != 3.99 {
		t.Errorf("d3: %+v", d3)
	}
	if r.SpotCount != 3 || r.OnDemandCount != 1 {
		t.Errorf("counts: %d spot %d on-demand", r.SpotCount, r.OnDemandCount)
	}
	want := (2.5 + 3.99 + 1.68 + 0.003) * 10 / 60
	if diff := r.ExpectedUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("expected $%.4f, want %.4f", r.ExpectedUSD, want)
	}
	if r.BoundedUSD < r.ExpectedUSD*2.9 {
		t.Errorf("bounded %.2f should be three times expected %.2f", r.BoundedUSD, r.ExpectedUSD)
	}
	// Allowing the expensive type by name brings it back; metal and on-demand options.
	r = Build(db, snap, pr, Options{Region: "us-east-1", PriceCap: 25, AllowExpensive: []string{"p5.48xlarge"}, LimitMinutes: 30, OnDemand: true})
	found := false
	for _, c := range r.Captures {
		if c.MachineType == "p5.48xlarge" {
			found = true
		}
		if c.Model != "on-demand" {
			t.Errorf("OnDemand option: %+v", c)
		}
	}
	if !found {
		t.Error("p5 should be allowed by name")
	}
	if !strings.Contains(strings.Join(r.Lines(), "\n"), "terminates itself after 30 minutes") {
		t.Errorf("lines: %v", r.Lines())
	}
}
