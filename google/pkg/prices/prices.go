// Package prices builds hourly On-Demand and Spot prices for Compute Engine machine types
// from the Cloud Billing Catalog: Google prices a predefined type as vCPUs times its
// family's core rate, plus GB of memory times the RAM rate, plus attached GPUs. The
// output has the shape of aws/pkg/prices so ingest-agent reads both the same way.
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// ComputeService is the Cloud Billing service id of Compute Engine.
const ComputeService = "6F81-5844-456A"

// Prices is the snapshot: dollars per hour by machine type. Spot is regional on Google,
// so its zone key is "" (any zone in the region).
type Prices struct {
	Region   string                        `json:"region"`
	Fetched  string                        `json:"fetched"`
	Source   string                        `json:"source"`
	OnDemand map[string]float64            `json:"on_demand"`
	Spot     map[string]map[string]float64 `json:"spot"`
	// Unpriced lists types in the region with no complete rate (no core, RAM or GPU SKU
	// for their family), so a 0 price reads as unknown, not free.
	Unpriced []string `json:"unpriced,omitempty"`
}

// SKU is the part of a Cloud Billing SKU the prices come from.
type SKU struct {
	Description string `json:"description"`
	Category    struct {
		ResourceGroup string `json:"resourceGroup"`
		UsageType     string `json:"usageType"`
	} `json:"category"`
	ServiceRegions []string `json:"serviceRegions"`
	PricingInfo    []struct {
		PricingExpression struct {
			UsageUnit   string `json:"usageUnit"`
			TieredRates []struct {
				UnitPrice struct {
					Units string `json:"units"`
					Nanos int64  `json:"nanos"`
				} `json:"unitPrice"`
			} `json:"tieredRates"`
		} `json:"pricingExpression"`
	} `json:"pricingInfo"`
}

// Rate is the SKU's hourly unit price (the last tier, which for Compute is the only one).
func (s SKU) Rate() float64 {
	if len(s.PricingInfo) == 0 {
		return 0
	}
	tiers := s.PricingInfo[0].PricingExpression.TieredRates
	if len(tiers) == 0 {
		return 0
	}
	p := tiers[len(tiers)-1].UnitPrice
	var units float64
	fmt.Sscan(p.Units, &units)
	return units + float64(p.Nanos)/1e9
}

// skuFamily is the billing name of a machine type's core and RAM SKUs: "N2" for
// "N2 Instance Core running in Americas". Families with no entry are not priced.
func skuFamily(machineType string) string {
	family, _, _ := strings.Cut(machineType, "-")
	switch {
	case strings.HasPrefix(machineType, "a3-megagpu"):
		return "A3Plus"
	case strings.HasPrefix(machineType, "a3-ultragpu"):
		return "A3Ultra"
	}
	return map[string]string{
		"a2": "A2", "a3": "A3", "c2": "Compute optimized", "c2d": "C2D AMD", "c3": "C3", "c3d": "C3D",
		"c4": "C4", "c4a": "C4A Arm", "c4d": "C4D", "c4n": "C4N", "e2": "E2", "g2": "G2", "g4": "G4",
		"h3": "H3", "h4d": "H4D", "m1": "Memory-optimized", "m2": "Memory-optimized", "m3": "M3 Memory-optimized",
		"m4": "M4", "m4n": "M4N", "n1": "N1 Predefined", "n2": "N2", "n2d": "N2D AMD", "n4": "N4", "n4a": "N4A",
		"n4d": "N4D", "t2a": "T2A Arm", "t2d": "T2D AMD", "z3": "Z3", "z4m": "Z4M",
	}[family]
}

// gpuSKU is the billing name of an accelerator's per-GPU SKU. B200 is billed as a whole
// slice of the VM (cores and memory included), so a4 has no core or RAM rate.
var gpuSKU = map[string]string{
	"nvidia-tesla-a100":     "Nvidia Tesla A100 GPU",
	"nvidia-a100-80gb":      "Nvidia Tesla A100 80GB GPU",
	"nvidia-h100-80gb":      "Nvidia H100 80GB GPU",
	"nvidia-h100-mega-80gb": "Nvidia H100 80GB Plus GPU",
	"nvidia-h200-141gb":     "H200 141GB GPU",
	"nvidia-l4":             "Nvidia L4 GPU",
	"nvidia-rtx-pro-6000":   "RTX 6000 96GB",
	"nvidia-tesla-t4":       "Nvidia Tesla T4 GPU",
	"nvidia-tesla-v100":     "Nvidia Tesla V100 GPU",
	"nvidia-tesla-p100":     "Nvidia Tesla P100 GPU",
	"nvidia-tesla-p4":       "Nvidia Tesla P4 GPU",
	"nvidia-b200":           "A4 Nvidia B200 (1 gpu slice)",
}

// sliceBilled accelerators carry the whole VM price in the GPU SKU.
var sliceBilled = map[string]bool{"nvidia-b200": true}

// rates are the region's hourly rates by billing name: core, ram and gpu.
type rates struct{ core, ram, gpu map[string]float64 }

// Index sorts the region's SKUs into On-Demand and Spot rates. Commitments, sole tenancy,
// custom types and DWS (Dynamic Workload Scheduler) SKUs are left out.
func Index(skus []SKU, region string) (onDemand, spot rates) {
	onDemand = rates{map[string]float64{}, map[string]float64{}, map[string]float64{}}
	spot = rates{map[string]float64{}, map[string]float64{}, map[string]float64{}}
	for _, s := range skus {
		in := false
		for _, r := range s.ServiceRegions {
			in = in || r == region
		}
		if !in {
			continue
		}
		d, _, _ := strings.Cut(s.Description, " running in ")
		d, _, _ = strings.Cut(d, " in Americas")
		if strings.Contains(d, "DWS") || strings.Contains(d, "Sole Tenancy") || strings.Contains(d, "Custom") ||
			strings.Contains(d, "Commit") || strings.Contains(d, "Premium") {
			continue
		}
		into := onDemand
		switch s.Category.UsageType {
		case "OnDemand":
		case "Preemptible":
			into = spot
			d = strings.TrimPrefix(d, "Spot Preemptible ")
			d = strings.TrimSuffix(d, " attached to Spot Preemptible VMs")
		default:
			continue
		}
		rate := s.Rate()
		switch {
		case strings.HasSuffix(d, " Core"):
			into.core[billingName(d, "Core")] = rate
		case strings.HasSuffix(d, " Ram"):
			into.ram[billingName(d, "Ram")] = rate
		case s.Category.ResourceGroup == "GPU":
			into.gpu[d] = rate
		}
	}
	return onDemand, spot
}

// billingName is "N2" for "N2 Instance Core" and "Compute optimized" for "Compute optimized Core".
func billingName(d, unit string) string {
	d = strings.TrimSpace(strings.TrimSuffix(d, unit))
	return strings.TrimSpace(strings.TrimSuffix(d, "Instance"))
}

// Price is a machine type's hourly price under one set of rates, and whether every part
// of it had a rate.
func Price(mt database.MachineType, r rates) (float64, bool) {
	total := 0.0
	vmBilled := false
	for _, a := range mt.Accelerators {
		name, ok := gpuSKU[a.Type]
		rate := r.gpu[name]
		if !ok || rate == 0 {
			return 0, false
		}
		total += rate * float64(a.Count)
		vmBilled = vmBilled || sliceBilled[a.Type]
	}
	if !vmBilled {
		f := skuFamily(mt.Name)
		core, ram := r.core[f], r.ram[f]
		if f == "" || core == 0 || ram == 0 || mt.SharedCPU {
			return 0, false
		}
		total += core*float64(mt.VCPUs) + ram*float64(mt.MemoryMB)/1024
	}
	return math.Round(total*10000) / 10000, true
}

// Build prices every machine type offered in region.
func Build(skus []SKU, region string, types []database.MachineType) *Prices {
	od, sp := Index(skus, region)
	p := &Prices{Region: region, Fetched: time.Now().UTC().Format(time.RFC3339), Source: "cloudbilling.googleapis.com, " + ComputeService,
		OnDemand: map[string]float64{}, Spot: map[string]map[string]float64{}}
	for _, mt := range types {
		if v, ok := Price(mt, od); ok {
			p.OnDemand[mt.Name] = v
		} else {
			p.Unpriced = append(p.Unpriced, mt.Name)
		}
		if v, ok := Price(mt, sp); ok {
			p.Spot[mt.Name] = map[string]float64{"": v}
		}
	}
	sort.Strings(p.Unpriced)
	return p
}

// Fetch reads every Compute Engine SKU with application default credentials.
func Fetch(ctx context.Context) ([]SKU, error) {
	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-billing.readonly")
	if err != nil {
		return nil, err
	}
	var all []SKU
	token := ""
	for {
		q := url.Values{"currencyCode": {"USD"}, "pageSize": {"5000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://cloudbilling.googleapis.com/v1/services/"+ComputeService+"/skus?"+q.Encode(), nil)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var page struct {
			Skus          []SKU  `json:"skus"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("cloud billing catalog: %s", resp.Status)
		}
		all = append(all, page.Skus...)
		if page.NextPageToken == "" {
			return all, nil
		}
		token = page.NextPageToken
	}
}
