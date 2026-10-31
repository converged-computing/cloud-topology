package quota

import (
	"testing"

	"cloud.google.com/go/cloudquotas/apiv1/cloudquotaspb"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func used(v float64) *float64 { return &v }

func snapshot() *Snapshot {
	return &Snapshot{Project: "p", Regions: []string{"us-central1"}, Quotas: []Quota{
		{Metric: "N2_CPUS", Region: "us-central1", Limit: 24, Usage: used(8), Source: "compute"},
		{Metric: "CPUS", Region: "us-central1", Limit: 8, Usage: used(0), Source: "compute"},
		{Metric: "CPUS_ALL_REGIONS", Limit: 12, Usage: used(0), Source: "compute"},
		{Metric: "GPUS_ALL_REGIONS", Limit: 4, Usage: used(0), Source: "compute"},
		{Metric: "GPU_FAMILY", Region: "us-central1", Dimensions: map[string]string{"gpu_family": "NVIDIA_L4"}, Limit: 3, Source: "cloudquotas"},
		{Metric: "CPUS_PER_VM_FAMILY", Region: "us-central1", Dimensions: map[string]string{"vm_family": "C4"}, Limit: 64, Source: "cloudquotas"},
		{Metric: "PREEMPTIBLE_NVIDIA_L4_GPUS", Region: "us-central1", Limit: 8, Usage: used(0), Source: "compute"},
	}}
}

func mt(name string, vcpus int, accel string, count int) database.MachineType {
	m := database.MachineType{Name: name, Family: database.FamilyOf(name), VCPUs: vcpus}
	if accel != "" {
		m.Accelerators = []database.Accelerator{{Type: accel, Count: count}}
	}
	return m
}

func TestComputeCPU(t *testing.T) {
	s := snapshot()
	// N2: 16 available N2 CPUs / 4 = 4, but CPUS_ALL_REGIONS 12 / 4 = 3.
	c := Compute(s, mt("n2-standard-4", 4, "", 0), "on-demand", "us-central1")
	if c.Nodes != 3 || c.Binding != "CPUS_ALL_REGIONS" || !c.UsageKnown {
		t.Errorf("n2: %+v", c)
	}
	// E2 uses the shared CPUS pool: 8 / 2 = 4.
	if c := Compute(s, mt("e2-standard-2", 2, "", 0), "on-demand", "us-central1"); c.Nodes != 4 || c.Binding != "CPUS" {
		t.Errorf("e2: %+v", c)
	}
	// C4 uses CPUS_PER_VM_FAMILY (limit only, no usage): 64 / 8 = 8, then global 12 / 8 = 1.
	c = Compute(s, mt("c4-standard-8", 8, "", 0), "on-demand", "us-central1")
	if c.Nodes != 1 || c.Binding != "CPUS_ALL_REGIONS" {
		t.Errorf("c4: %+v", c)
	}
	// N2D has no quota in the snapshot, so nothing can run.
	if c := Compute(s, mt("n2d-standard-2", 2, "", 0), "on-demand", "us-central1"); c.Nodes != 0 || c.Binding != "N2D_CPUS" {
		t.Errorf("n2d: %+v", c)
	}
}

func TestComputeGPU(t *testing.T) {
	s := snapshot()
	// G2 needs GPU quota only: GPU_FAMILY NVIDIA_L4 3 / 1 = 3; no CPU quota applies.
	c := Compute(s, mt("g2-standard-8", 8, "nvidia-l4", 1), "on-demand", "us-central1")
	if c.Nodes != 3 || c.Binding != "GPU_FAMILY" || c.UsageKnown {
		t.Errorf("g2 on-demand: %+v", c)
	}
	// Flex-start uses preemptible quota when granted: 8 / 1, capped by GPUS_ALL_REGIONS 4.
	c = Compute(s, mt("g2-standard-8", 8, "nvidia-l4", 1), "flex-start", "us-central1")
	if c.Nodes != 4 || c.Binding != "GPUS_ALL_REGIONS" || c.Note != "" {
		t.Errorf("g2 flex-start: %+v", c)
	}
	// A3 High has no H100 quota in the snapshot.
	if c := Compute(s, mt("a3-highgpu-1g", 26, "nvidia-h100-80gb", 1), "on-demand", "us-central1"); c.Nodes != 0 {
		t.Errorf("a3: %+v", c)
	}
	// A3 Ultra (H200) has no standard quota, so on-demand is impossible.
	c = Compute(s, mt("a3-ultragpu-8g", 224, "nvidia-h200-141gb", 8), "on-demand", "us-central1")
	if c.Nodes != 0 || c.Note == "" || !c.Known {
		t.Errorf("a3 ultra: %+v", c)
	}
}

func TestPreemptibleFallback(t *testing.T) {
	s := snapshot()
	// No PREEMPTIBLE_CPUS granted, so spot falls back to N2_CPUS and says so.
	c := Compute(s, mt("n2-standard-4", 4, "", 0), "spot", "us-central1")
	if c.Nodes != 3 || c.Note == "" {
		t.Errorf("spot fallback: %+v", c)
	}
}

func TestUnknownFamily(t *testing.T) {
	c := Compute(snapshot(), mt("zz9-standard-4", 4, "", 0), "on-demand", "us-central1")
	if c.Known || c.Note == "" {
		t.Errorf("unknown family: %+v", c)
	}
}

func TestFromQuotaInfos(t *testing.T) {
	infos := []*cloudquotaspb.QuotaInfo{
		{QuotaId: "GPUS-PER-GPU-FAMILY-per-project-region", Dimensions: []string{"region", "gpu_family"},
			DimensionsInfos: []*cloudquotaspb.DimensionsInfo{
				{Dimensions: map[string]string{"gpu_family": "NVIDIA_L4"}, Details: &cloudquotaspb.QuotaDetails{Value: 1}},
				{Dimensions: map[string]string{"gpu_family": "NVIDIA_L4", "region": "us-central1"}, Details: &cloudquotaspb.QuotaDetails{Value: 16}},
				{Dimensions: map[string]string{"gpu_family": "NVIDIA_H100"}, Details: &cloudquotaspb.QuotaDetails{Value: 0},
					ApplicableLocations: []string{"us-east4"}},
			}},
		{QuotaId: "NETWORKS-per-project", Dimensions: nil},
	}
	got := FromQuotaInfos(infos, []string{"us-central1", "europe-west4"})
	s := &Snapshot{Quotas: got}
	if q, ok := s.Find("GPU_FAMILY", "us-central1", map[string]string{"gpu_family": "NVIDIA_L4"}); !ok || q.Limit != 16 {
		t.Errorf("region-specific entry should override the default: %+v", q)
	}
	if q, ok := s.Find("GPU_FAMILY", "europe-west4", map[string]string{"gpu_family": "NVIDIA_L4"}); !ok || q.Limit != 1 {
		t.Errorf("default should apply to other regions: %+v", q)
	}
	if _, ok := s.Find("GPU_FAMILY", "us-east4", map[string]string{"gpu_family": "NVIDIA_H100"}); ok {
		t.Error("entry for an unrequested region kept")
	}
	if len(got) != 2 {
		t.Errorf("unexpected entries %+v", got)
	}
}

func TestInferredRule(t *testing.T) {
	s := snapshot()
	s.Quotas = append(s.Quotas, Quota{Metric: "CPUS_PER_VM_FAMILY", Region: "us-central1",
		Dimensions: map[string]string{"vm_family": "C3A"}, Limit: -1, Source: "cloudquotas"})
	// C3A is not in Google's table; the snapshot's unlimited C3A quota leaves CPUS_ALL_REGIONS 12 / 4 = 3.
	c := Compute(s, mt("c3a-standard-4", 4, "", 0), "on-demand", "us-central1")
	if !c.Known || !c.Inferred || c.Nodes != 3 || c.Binding != "CPUS_ALL_REGIONS" {
		t.Errorf("c3a: %+v", c)
	}
	if got := s.Unlimited(); len(got) != 1 || got[0].Dimensions["vm_family"] != "C3A" {
		t.Errorf("unlimited: %+v", got)
	}
	// A family with no rule and no snapshot quota stays unknown.
	if c := Compute(s, mt("u4c-standard-4", 4, "", 0), "on-demand", "us-central1"); c.Known {
		t.Errorf("u4c should be unknown without a quota: %+v", c)
	}
}
