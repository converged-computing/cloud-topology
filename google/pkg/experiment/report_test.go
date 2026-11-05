package experiment

import (
	"os"
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/plan"
)

func TestBuild(t *testing.T) {
	dir := "../../testdata/experiment-example"
	f, err := os.Open(dir + "/captures.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	captures, err := plan.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Build("captures.txt", captures, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Planned != 3 || r.Either != 2 || r.Both != 1 || r.Neither != 1 || r.Only["agent"] != 0 || r.Only["deterministic"] != 1 {
		t.Errorf("totals: %+v", r)
	}
	agent, det := r.Arms[0], r.Arms[1]
	if a := agent.Agent; a == nil || a.Turns != 14 || a.APIMillis != 95000 || a.CostUSD != 2.5 || a.AttemptsUsed != 5 || a.IsError {
		t.Errorf("agent run: %+v", a)
	}
	if agent.Start != "2026-10-30T10:00:00Z" || agent.End != "2026-10-30T10:35:00Z" {
		t.Errorf("agent run times: %s %s", agent.Start, agent.End)
	}
	if agent.Captured != 1 || agent.Seconds != 2100 || *agent.Exit != 0 || agent.MeanProvisionSeconds != 120 || strings.Join(agent.Extra, ",") != "n1-standard-2" {
		t.Errorf("agent summary: %+v", agent)
	}
	if det.Captured != 2 || det.Seconds != 2520 || *det.Exit != 1 || det.MeanProvisionSeconds != 140 || len(det.Extra) != 0 {
		t.Errorf("deterministic summary: %+v", det)
	}
	want := map[string]map[string]Outcome{
		"e2-standard-32":  {"agent": {Status: "ok", Attempts: 2, Zone: "us-central1-b"}, "deterministic": {Status: "ok", Attempts: 1, Seconds: 131}},
		"n2-standard-128": {"agent": {Status: "stockout", Attempts: 2}, "deterministic": {Status: "ok", Attempts: 2, Seconds: 182}},
		"a2-megagpu-16g":  {"agent": {Status: "quota", Attempts: 1}, "deterministic": {Status: "quota", Attempts: 1, Seconds: 24}},
	}
	for _, row := range r.Rows {
		for arm, w := range want[row.MachineType] {
			got := row.Outcomes[arm]
			if got.Status != w.Status || got.Attempts != w.Attempts || got.Seconds != w.Seconds || (w.Zone != "" && got.Zone != w.Zone) {
				t.Errorf("%s %s: got %+v, want %+v", row.MachineType, arm, got, w)
			}
		}
	}
	md := r.Markdown()
	for _, want := range []string{"| agent | 1/3 | 33% | 35m0s | 0 | 2m0s |", "| either | 2/3 | 67% |", "Both arms 1, agent only 0, deterministic only 1, neither 1.",
		"| n2-standard-128 | spot | stockout (2 attempts, ZONE_RESOURCE_POOL_EXHAUSTED / in us-central1-a) | ok (2m30s provision, on-demand, us-central1-a, 2 attempts) |",
		"| a2-megagpu-16g | spot | quota (Quota NVIDIA_A100_GPUS exceeded) | quota (24s, Quota NVIDIA_A100_GPUS exceeded) |",
		"The agent took 14 turns, 1m35s of model time, and $2.50 in tokens, using 5 capture attempts.",
		"Captures outside the plan in the agent arm: n1-standard-2."} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if _, err := r.JSON(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBeforeAnyArm(t *testing.T) {
	captures, err := plan.Parse(strings.NewReader("make topology MACHINE_TYPE=e2-standard-32 ZONE=us-central1-a\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Build("plan", captures, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r.Neither != 1 || r.Rows[0].Outcomes["agent"].Status != "not run" || r.Rows[0].Outcomes["deterministic"].Status != "not run" {
		t.Errorf("%+v", r)
	}
}
