package prices

import (
	"testing"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func sku(desc, group, usage string, nanos int64, regions ...string) SKU {
	var s SKU
	s.Description, s.Category.ResourceGroup, s.Category.UsageType, s.ServiceRegions = desc, group, usage, regions
	s.PricingInfo = make([]struct {
		PricingExpression struct {
			UsageUnit   string `json:"usageUnit"`
			TieredRates []struct {
				UnitPrice struct {
					Units string `json:"units"`
					Nanos int64  `json:"nanos"`
				} `json:"unitPrice"`
			} `json:"tieredRates"`
		} `json:"pricingExpression"`
	}, 1)
	s.PricingInfo[0].PricingExpression.TieredRates = make([]struct {
		UnitPrice struct {
			Units string `json:"units"`
			Nanos int64  `json:"nanos"`
		} `json:"unitPrice"`
	}, 1)
	s.PricingInfo[0].PricingExpression.TieredRates[0].UnitPrice.Nanos = nanos
	return s
}

func TestBuild(t *testing.T) {
	uc := "us-central1"
	skus := []SKU{
		sku("N2 Instance Core running in Americas", "CPU", "OnDemand", 30000000, uc),
		sku("N2 Instance Ram running in Americas", "RAM", "OnDemand", 4000000, uc),
		sku("Spot Preemptible N2 Instance Core running in Americas", "CPU", "Preemptible", 10000000, uc),
		sku("Spot Preemptible N2 Instance Ram running in Americas", "RAM", "Preemptible", 1000000, uc),
		sku("N2 Instance Core running in Paris", "CPU", "OnDemand", 90000000, "europe-west9"),
		sku("DWS Defined Duration A3 Core running in Americas", "CPU", "OnDemand", 1, uc),
		sku("A3 Instance Core running in Americas", "CPU", "OnDemand", 20000000, uc),
		sku("A3 Instance Ram running in Americas", "RAM", "OnDemand", 2000000, uc),
		sku("Nvidia H100 80GB GPU running in Americas", "GPU", "OnDemand", 500000000, uc),
		sku("Compute optimized Core running in Americas", "CPU", "OnDemand", 30000000, uc),
		sku("Compute optimized Ram running in Americas", "RAM", "OnDemand", 4000000, uc),
	}
	types := []database.MachineType{
		{Name: "n2-standard-8", VCPUs: 8, MemoryMB: 32 * 1024},
		{Name: "a3-highgpu-1g", VCPUs: 26, MemoryMB: 234 * 1024, Accelerators: []database.Accelerator{{Type: "nvidia-h100-80gb", Count: 1}}},
		{Name: "c2-standard-4", VCPUs: 4, MemoryMB: 16 * 1024},
		{Name: "x4-megamem-960-metal", VCPUs: 960, MemoryMB: 1024},
	}
	p := Build(skus, uc, types)
	want := map[string]float64{"n2-standard-8": 8*0.03 + 32*0.004, "a3-highgpu-1g": 26*0.02 + 234*0.002 + 0.5, "c2-standard-4": 4*0.03 + 16*0.004}
	for name, w := range want {
		if got := p.OnDemand[name]; got < w-1e-4 || got > w+1e-4 {
			t.Errorf("%s: on-demand %v, want %v", name, got, w)
		}
	}
	if got := p.Spot["n2-standard-8"][""]; got < 0.111 || got > 0.113 {
		t.Errorf("n2-standard-8 spot %v, want 0.112", got)
	}
	if _, ok := p.Spot["a3-highgpu-1g"]; ok {
		t.Error("a3 spot priced without spot SKUs")
	}
	if len(p.Unpriced) != 1 || p.Unpriced[0] != "x4-megamem-960-metal" {
		t.Errorf("unpriced %v", p.Unpriced)
	}
}
