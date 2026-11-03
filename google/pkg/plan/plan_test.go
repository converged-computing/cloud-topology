package plan

import (
	"strings"
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
)

func load(t *testing.T) (*database.Database, *quota.Snapshot) {
	t.Helper()
	db, err := database.Load("../../testdata/plan-example")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := quota.Load("../../testdata/plan-example/quota.json")
	if err != nil {
		t.Fatal(err)
	}
	return db, snap
}

func names(cs []Capture) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.MachineType)
	}
	return out
}

func TestCapturesDefaults(t *testing.T) {
	db, snap := load(t)
	got := Captures(db, snap, Options{Region: "us-central1"})
	want := "c3a-standard-4,e2-standard-4,c3-standard-176,c3d-highmem-360,g2-standard-8"
	if strings.Join(names(got), ",") != want {
		t.Fatalf("captures %v, want %s", names(got), want)
	}
	for _, c := range got {
		if c.Model != "spot" {
			t.Errorf("%s: want spot, got %s", c.MachineType, c.Model)
		}
		if c.MachineType == "g2-standard-8" && c.Command() != `make topology MACHINE_TYPE=g2-standard-8 ZONE=us-central1-a ARGS="--maintenance-policy=TERMINATE --provisioning-model=SPOT"` {
			t.Errorf("g2 command: %s", c.Command())
		}
	}
}

func TestCapturesOptions(t *testing.T) {
	db, snap := load(t)
	got := names(Captures(db, snap, Options{Region: "us-central1", Verify: true, Metal: true, SharedCore: true, OnDemand: true}))
	for _, want := range []string{"e2-micro", "c3-standard-192-metal", "c3-standard-8"} {
		if !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, c := range Captures(db, snap, Options{Region: "us-central1", OnDemand: true}) {
		if c.Model != "on-demand" || strings.Contains(c.Command(), "SPOT") {
			t.Errorf("%s should be on-demand: %s", c.MachineType, c.Command())
		}
	}
}

func TestCheck(t *testing.T) {
	db, snap := load(t)
	r := Check(db, snap, Options{Region: "us-central1"})
	if len(r.Unlimited) != 1 || r.Unknown["u4c"] != 1 || len(r.Inferred) != 1 || r.Inferred[0] != "c3a" {
		t.Errorf("report: %+v", r)
	}
	if r.Skipped["c3-standard-192-metal"] != "bare metal" || r.Skipped["e2-micro"] != "shared core" {
		t.Errorf("skipped: %+v", r.Skipped)
	}
	if !strings.HasPrefix(r.NotPlanned["ct5lp"], "Cloud TPU host") || r.NotPlanned["f1"] != "legacy shared-core" {
		t.Errorf("not planned: %+v", r.NotPlanned)
	}
	if _, ok := r.Unknown["ct5lp"]; ok {
		t.Error("TPU family reported as missing a quota rule")
	}
	if got := r.NoQuota["a3-highgpu"]; got != "GPU_FAMILY gpu_family=NVIDIA_H100 (no quota granted)" {
		t.Errorf("no quota for a3-highgpu: %q", got)
	}
	if len(r.NoQuota) != 1 {
		t.Errorf("only a3-highgpu should lack quota: %+v", r.NoQuota)
	}
	if lines := r.Lines(); len(lines) != 8 {
		t.Errorf("lines: %q", lines)
	}
}

func TestCapturesRecapture(t *testing.T) {
	db, snap := load(t)
	db.Captures["c3-standard-176"] = database.Capture{MachineType: "c3-standard-176"}
	db.Captures["c3-standard-8"] = database.Capture{MachineType: "c3-standard-8"}
	got := strings.Join(names(Captures(db, snap, Options{Region: "us-central1", Verify: true})), ",")
	if strings.Contains(got, "c3-standard-176") || strings.Contains(got, "c3-standard-8") {
		t.Fatalf("captured series planned again without Recapture: %s", got)
	}
	got = strings.Join(names(Captures(db, snap, Options{Region: "us-central1", Verify: true, Recapture: true})), ",")
	for _, want := range []string{"c3-standard-176", "c3-standard-8"} {
		if !strings.Contains(got, want) {
			t.Errorf("Recapture: missing %s in %s", want, got)
		}
	}
}

func TestParse(t *testing.T) {
	db, snap := load(t)
	want := Captures(db, snap, Options{Region: "us-central1", Verify: true})
	var text strings.Builder
	text.WriteString("# a comment\n\n")
	for _, c := range want {
		text.WriteString(c.Command() + "\n")
	}
	got, err := Parse(strings.NewReader(text.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d captures, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].MachineType != want[i].MachineType || got[i].Zone != want[i].Zone || got[i].Model != want[i].Model ||
			got[i].Series != want[i].Series || strings.Join(got[i].Args, " ") != strings.Join(want[i].Args, " ") {
			t.Errorf("parse(%s) = %+v, want %+v", want[i].Command(), got[i], want[i])
		}
	}
	for _, bad := range []string{"make topology MACHINE_TYPE=x", `make topology MACHINE_TYPE=x ZONE=y ARGS="oops`} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("Parse(%q): want error", bad)
		}
	}
}

func TestZonesForAndMaintenance(t *testing.T) {
	mt := database.MachineType{Name: "a2-highgpu-8g", Zones: []string{"europe-west4-a", "us-east1-b", "us-central1-f", "us-central1-a", "asia-east1-a"}}
	got := strings.Join(zonesFor(mt, "us-central1"), ",")
	if got != "us-central1-a,us-central1-f,us-east1-b,asia-east1-a,europe-west4-a" {
		t.Errorf("zonesFor: %s", got)
	}
	db, snap := load(t)
	for _, c := range Captures(db, snap, Options{Region: "us-central1", Verify: true, Metal: true}) {
		mt := db.MachineTypes[c.MachineType]
		want := mt.GPUs() > 0 || mt.LocalSSD()
		has := strings.Contains(strings.Join(c.Args, " "), "--maintenance-policy=TERMINATE")
		if has != want {
			t.Errorf("%s: maintenance policy %v, want %v", c.MachineType, has, want)
		}
	}
}
