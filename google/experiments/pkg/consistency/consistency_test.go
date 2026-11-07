package consistency

import (
	"strings"
	"testing"
)

func TestConsistency(t *testing.T) {
	sources, err := RoundSources("../../testdata/consistency-example")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0].Label != "round/r1" {
		t.Fatalf("sources: %+v", sources)
	}
	r, err := Compare(sources)
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent != 1 || r.Varying != 1 || r.Single != 0 || len(r.Types) != 2 || r.Types[0].MachineType != "test-cpu-4" {
		t.Fatalf("report: consistent %d varying %d single %d types %+v", r.Consistent, r.Varying, r.Single, r.Types)
	}
	v := r.Types[0]
	// Round 2 moved the bridge holding a GPU and the NIC from socket 0 to the machine.
	if v.Captures != 2 || v.Shapes != 2 || strings.Join(v.Varies, ",") != "gpu placement,nic placement" || strings.Join(v.Zones, ",") != "us-central1-a,us-central1-b" {
		t.Errorf("test-cpu-4: %+v", v)
	}
	a, b := v.Observations[0].Shape, v.Observations[1].Shape
	if a.Sockets != 2 || a.Cores == 0 || a.PUs == 0 || a.NICs.PerSocket[0] != 1 || a.NICs.Machine != 0 || b.NICs.PerSocket[0] != 0 || b.NICs.Machine != 1 || a.ID() == b.ID() {
		t.Errorf("fingerprints: %+v vs %+v", a, b)
	}
	c := r.Types[1]
	if c.MachineType != "test-gpu-2g" || c.Shapes != 1 || len(c.Varies) != 0 || c.Observations[0].Shape.GPUs.PerSocket[0] != 1 {
		t.Errorf("test-gpu-2g: %+v", c)
	}
	md := r.Markdown()
	for _, want := range []string{"2 machine types captured more than once: 1 consistent, 1 varying; 0 captured once.", "| test-cpu-4 | 2 | 2 | gpu placement, nic placement |", "## test-cpu-4", "sockets [1 0], machine 0", "sockets [0 0], machine 1"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
