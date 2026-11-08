// Package plan decides which topology captures to run: one per series, the largest size
// the quota allows, Spot where the type and the Spot quota permit, cheapest first, under
// an hourly price ceiling.
package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/prices"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/quota"
)

// Options controls capture planning.
type Options struct {
	Region string
	// Metal includes bare-metal series.
	Metal bool
	// OnDemand captures On-Demand instead of Spot.
	OnDemand bool
	// Recapture plans series that are already captured.
	Recapture bool
	// PriceCap leaves out types above this many dollars an hour (0 for no cap) unless
	// named in AllowExpensive.
	PriceCap       float64
	AllowExpensive []string
	// LimitMinutes is the self-termination limit each instance carries.
	LimitMinutes int
}

// Capture is one planned topology capture.
type Capture struct {
	MachineType string   `json:"machine_type"`
	Series      string   `json:"series"`
	Zones       []string `json:"zones"`
	Model       string   `json:"model"`
	VCPUs       int      `json:"vcpus"`
	GPUs        int      `json:"gpus"`
	// Hourly is the price per hour under Model; -1 when unknown.
	Hourly float64 `json:"hourly"`
}

// Command returns the make command that runs the capture.
func (c Capture) Command() string {
	cmd := fmt.Sprintf("make topology MACHINE_TYPE=%s ZONE=%s", c.MachineType, strings.Join(c.Zones, ","))
	if c.Model == "spot" {
		cmd += " SPOT=1"
	}
	return cmd
}

// Report is what the planner left out and why, and the estimate.
type Report struct {
	Captures      []Capture
	Excluded      []string // "type: reason"
	ExpectedUSD   float64  // at ten minutes each
	BoundedUSD    float64  // at the self-termination limit
	LimitMinutes  int
	Unpriced      int
	SpotCount     int
	OnDemandCount int
}

// Lines renders the report as plan header comments.
func (r Report) Lines() []string {
	lines := []string{
		fmt.Sprintf("%d captures: %d Spot, %d On-Demand. Each instance terminates itself after %d minutes.", len(r.Captures), r.SpotCount, r.OnDemandCount, r.LimitMinutes),
		fmt.Sprintf("Estimated cost: $%.0f at ten minutes per capture; bounded at $%.0f if every instance ran to its %d-minute limit.", r.ExpectedUSD, r.BoundedUSD, r.LimitMinutes),
	}
	if r.Unpriced > 0 {
		lines = append(lines, fmt.Sprintf("%d type(s) have no price in the table and are not in the estimate.", r.Unpriced))
	}
	if len(r.Excluded) > 0 {
		lines = append(lines, "Left out: "+strings.Join(r.Excluded, "; "))
	}
	return lines
}

// Build returns the plan: per series, the largest member the On-Demand quota allows,
// on Spot when the type has the usage class and the Spot quota allows it (unless
// OnDemand), with zones ordered cheapest Spot first. Captures are ordered by GPUs, then
// vCPUs, so the cheapest come first.
func Build(db *database.Database, snap *quota.Snapshot, pr *prices.Prices, opts Options) Report {
	report := Report{LimitMinutes: opts.LimitMinutes}
	allow := map[string]bool{}
	for _, t := range opts.AllowExpensive {
		allow[t] = true
	}
	groups := database.Series(db.Query(database.Filter{Region: opts.Region, CurrentOnly: true}))
	names := make([]string, 0, len(groups))
	for s := range groups {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		members := groups[s]
		if !opts.Metal && members[0].BareMetal {
			continue
		}
		if !opts.Recapture {
			captured := false
			for _, m := range members {
				if _, ok := db.Captures[m.Name]; ok {
					captured = true
				}
			}
			if captured {
				continue
			}
		}
		var pick *database.MachineType
		for i := len(members) - 1; i >= 0; i-- {
			if c := quota.Compute(snap, members[i], "on-demand", opts.Region); c.Known && c.Nodes != 0 {
				pick = &members[i]
				break
			}
		}
		if pick == nil {
			c := quota.Compute(snap, members[len(members)-1], "on-demand", opts.Region)
			report.Excluded = append(report.Excluded, fmt.Sprintf("%s: %s", s, firstNonEmpty(c.Note, "no On-Demand quota for any size")))
			continue
		}
		model := "on-demand"
		if !opts.OnDemand && pick.Spot() {
			if c := quota.Compute(snap, *pick, "spot", opts.Region); c.Nodes != 0 {
				model = "spot"
			}
		}
		hourly := -1.0
		if pr != nil {
			if v, ok := pr.Hourly(pick.Name, model); ok {
				hourly = v
			} else if v, ok := pr.Hourly(pick.Name, "on-demand"); ok {
				hourly = v
			}
		}
		if opts.PriceCap > 0 && hourly > opts.PriceCap && !allow[pick.Name] {
			report.Excluded = append(report.Excluded, fmt.Sprintf("%s: $%.2f/h is above the $%.0f/h cap (ALLOW_EXPENSIVE=%s to include)", pick.Name, hourly, opts.PriceCap, pick.Name))
			continue
		}
		zones := zonesFor(*pick, model, pr, opts.Region)
		if len(zones) == 0 {
			report.Excluded = append(report.Excluded, pick.Name+": offered in no zone of the region")
			continue
		}
		report.Captures = append(report.Captures, Capture{MachineType: pick.Name, Series: s, Zones: zones, Model: model, VCPUs: pick.VCPUs, GPUs: pick.GPUs(), Hourly: hourly})
	}
	sort.SliceStable(report.Captures, func(i, j int) bool {
		a, b := report.Captures[i], report.Captures[j]
		if a.GPUs != b.GPUs {
			return a.GPUs < b.GPUs
		}
		if a.VCPUs != b.VCPUs {
			return a.VCPUs < b.VCPUs
		}
		return a.MachineType < b.MachineType
	})
	for _, c := range report.Captures {
		if c.Model == "spot" {
			report.SpotCount++
		} else {
			report.OnDemandCount++
		}
		if c.Hourly < 0 {
			report.Unpriced++
			continue
		}
		report.ExpectedUSD += c.Hourly * 10 / 60
		report.BoundedUSD += c.Hourly * float64(opts.LimitMinutes) / 60
	}
	return report
}

// zonesFor orders the type's zones in the region: for Spot, those with a current Spot
// price first, cheapest first, then the rest by name.
func zonesFor(mt database.MachineType, model string, pr *prices.Prices, region string) []string {
	var inRegion []string
	for _, z := range mt.Zones {
		if database.RegionOf(z) == region {
			inRegion = append(inRegion, z)
		}
	}
	sort.Strings(inRegion)
	if model != "spot" || pr == nil {
		return inRegion
	}
	offered := map[string]bool{}
	for _, z := range inRegion {
		offered[z] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, z := range pr.SpotZones(mt.Name) {
		if offered[z] {
			out = append(out, z)
			seen[z] = true
		}
	}
	for _, z := range inRegion {
		if !seen[z] {
			out = append(out, z)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
