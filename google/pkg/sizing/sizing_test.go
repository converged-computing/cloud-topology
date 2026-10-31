package sizing

import (
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func n2(v int) database.MachineType {
	return database.MachineType{Name: "n2-standard-" + itoa(v), Family: "n2", VCPUs: v, MemoryMB: v * 4096}
}

func g2(v, gpus int) database.MachineType {
	return database.MachineType{Name: "g2-standard-" + itoa(v), Family: "g2", VCPUs: v, MemoryMB: v * 4096,
		Accelerators: []database.Accelerator{{Type: "nvidia-l4", Count: gpus}}}
}

func itoa(v int) string {
	digits := ""
	for ; v > 0; v /= 10 {
		digits = string(rune('0'+v%10)) + digits
	}
	return digits
}

func TestOptionsCPU(t *testing.T) {
	members := []database.MachineType{n2(2), n2(4), n2(8), n2(16)}
	got := Options(members, Request{VCPUs: 16}, Limits{VCPUs: 20, GPUs: -1})
	want := []struct {
		name  string
		count int
	}{{"n2-standard-16", 1}, {"n2-standard-8", 2}, {"n2-standard-4", 4}, {"n2-standard-2", 8}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].MachineType != w.name || got[i].Count != w.count {
			t.Errorf("option %d: got %+v, want %s x%d", i, got[i], w.name, w.count)
		}
	}
	// 18 vCPUs: n2-standard-16 x2 needs 32 > 20 quota, so it is excluded.
	got = Options(members, Request{VCPUs: 18}, Limits{VCPUs: 20, GPUs: -1})
	for _, o := range got {
		if o.VCPUs > 20 {
			t.Errorf("option over quota: %+v", o)
		}
	}
	if len(got) != 2 || got[0].MachineType != "n2-standard-2" && got[0].MachineType != "n2-standard-4" {
		t.Errorf("unexpected options for 18 vCPUs: %+v", got)
	}
}

func TestOptionsGPUAndMemory(t *testing.T) {
	members := []database.MachineType{g2(4, 1), g2(24, 2), g2(48, 4)}
	got := Options(members, Request{GPUs: 4}, Limits{VCPUs: -1, GPUs: 4})
	if len(got) != 3 || got[0].MachineType != "g2-standard-48" || got[0].Count != 1 {
		t.Errorf("gpu options: %+v", got)
	}
	// Memory drives the count: 200 GB needs 2 x g2-standard-48 (8 GPUs) or 3 x g2-standard-24
	// (6 GPUs); 13 x g2-standard-4 would need 13 GPUs, over the quota of 8.
	got = Options(members, Request{GPUs: 1, MemoryGB: 200}, Limits{VCPUs: -1, GPUs: 8})
	if len(got) != 2 || got[0].MachineType != "g2-standard-48" || got[0].Count != 2 || got[1].Count != 3 {
		t.Errorf("memory-driven options: %+v", got)
	}
	// With only 4 GPUs of quota, no option can provide 200 GB.
	if got := Options(members, Request{GPUs: 1, MemoryGB: 200}, Limits{VCPUs: -1, GPUs: 4}); len(got) != 0 {
		t.Errorf("expected no options within 4 GPUs: %+v", got)
	}
	if got := Options(members, Request{GPUs: 2, MaxNodes: 1}, Limits{VCPUs: -1, GPUs: -1}); len(got) != 2 {
		t.Errorf("max nodes: %+v", got)
	}
	if got := Options([]database.MachineType{n2(4)}, Request{GPUs: 1}, Limits{VCPUs: -1, GPUs: -1}); len(got) != 0 {
		t.Errorf("CPU-only type offered for a GPU request: %+v", got)
	}
}
