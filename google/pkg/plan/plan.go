// Package plan decides which topology captures to run and reports quota findings
// that affect planning.
package plan

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
)

// Options controls capture planning.
type Options struct {
	Region string
	// Verify also captures each series' smallest runnable size.
	Verify bool
	// Metal includes bare-metal series.
	Metal bool
	// SharedCore includes shared-core machine types (e.g. e2-micro).
	SharedCore bool
	// OnDemand captures on-demand instead of Spot.
	OnDemand bool
	// Recapture plans series that are already captured, as if the database held no captures.
	Recapture bool
}

// Capture is one planned topology capture.
type Capture struct {
	MachineType string   `json:"machine_type"`
	Series      string   `json:"series"`
	Zone        string   `json:"zone"`
	Model       string   `json:"model"`
	Purpose     string   `json:"purpose"`
	VCPUs       int      `json:"vcpus"`
	GPUs        int      `json:"gpus"`
	Args        []string `json:"args,omitempty"`
}

// Command returns the make command that runs the capture.
func (c Capture) Command() string {
	cmd := fmt.Sprintf("make topology MACHINE_TYPE=%s ZONE=%s", c.MachineType, c.Zone)
	if len(c.Args) > 0 {
		cmd += fmt.Sprintf(" ARGS=%q", strings.Join(c.Args, " "))
	}
	return cmd
}

// Captures returns one capture per series: the largest machine type the project can
// run on-demand, since smaller sizes are derived from it, preferring a variant without
// Local SSD at the same size. Captures use Spot when quota allows it, unless OnDemand
// is set. Series already captured at that size are skipped. Results are ordered by
// GPUs, then vCPUs, so the cheapest captures come first.
func Captures(db *database.Database, snap *quota.Snapshot, opts Options) []Capture {
	runnable := map[string][]database.MachineType{}
	for _, mt := range db.Query(database.Filter{Region: opts.Region}) {
		if skip(mt, opts) != "" {
			continue
		}
		c := quota.Compute(snap, mt, "on-demand", opts.Region)
		if !c.Known || c.Nodes == 0 {
			continue
		}
		s := database.SeriesOf(mt.Name)
		runnable[s] = append(runnable[s], mt)
	}

	var out []Capture
	for series, list := range runnable {
		members := database.Series(list)[series]
		largest := pick(members, len(members)-1)
		if opts.Recapture || !capturedAtLeast(db, series, largest) {
			out = append(out, newCapture(snap, largest, series, "largest runnable size", opts))
		}
		if smallest := pick(members, 0); opts.Verify && smallest.Name != largest.Name {
			if _, ok := db.Captures[smallest.Name]; opts.Recapture || !ok {
				out = append(out, newCapture(snap, smallest, series, "smallest size, to check derived topology", opts))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GPUs != out[j].GPUs {
			return out[i].GPUs < out[j].GPUs
		}
		if out[i].VCPUs != out[j].VCPUs {
			return out[i].VCPUs < out[j].VCPUs
		}
		return out[i].MachineType < out[j].MachineType
	})
	return out
}

func newCapture(snap *quota.Snapshot, mt database.MachineType, series, purpose string, opts Options) Capture {
	c := Capture{MachineType: mt.Name, Series: series, Zone: strings.Join(zonesFor(mt, opts.Region), ","), Model: "on-demand",
		Purpose: purpose, VCPUs: mt.VCPUs, GPUs: mt.GPUs()}
	// GPU and Local SSD machine types cannot live-migrate, so Compute Engine requires
	// the TERMINATE maintenance policy for them.
	if mt.GPUs() > 0 || mt.LocalSSD() {
		c.Args = append(c.Args, "--maintenance-policy=TERMINATE")
	}
	if !opts.OnDemand && quota.Compute(snap, mt, "spot", opts.Region).Nodes != 0 {
		c.Model = "spot"
		c.Args = append(c.Args, "--provisioning-model=SPOT")
	}
	return c
}

// skip returns why a machine type is left out of captures, or "".
func skip(mt database.MachineType, opts Options) string {
	switch {
	case database.IsMetal(mt.Name) && !opts.Metal:
		return "bare metal"
	case mt.SharedCPU && !opts.SharedCore:
		return "shared core"
	}
	return ""
}

// pick returns the member at index i, or a same-sized member without Local SSD.
func pick(members []database.MachineType, i int) database.MachineType {
	target := members[i]
	if !target.LocalSSD() {
		return target
	}
	for _, mt := range members {
		if mt.VCPUs == target.VCPUs && mt.GPUs() == target.GPUs() && !mt.LocalSSD() {
			return mt
		}
	}
	return target
}

func capturedAtLeast(db *database.Database, series string, mt database.MachineType) bool {
	for name := range db.Captures {
		other, ok := db.MachineTypes[name]
		if ok && database.SeriesOf(name) == series && other.VCPUs >= mt.VCPUs && other.GPUs() >= mt.GPUs() {
			return true
		}
	}
	return false
}

// zonesFor orders a machine type's zones for a capture: the plan's region first, then
// other regions in the same geography (us-, europe-, ...), then the rest, each sorted.
func zonesFor(mt database.MachineType, region string) []string {
	geo := region[:strings.IndexByte(region+"-", '-')]
	rank := func(z string) int {
		switch r := database.RegionOf(z); {
		case r == region:
			return 0
		case strings.HasPrefix(r, geo+"-"):
			return 1
		}
		return 2
	}
	zones := append([]string(nil), mt.Zones...)
	sort.SliceStable(zones, func(i, j int) bool {
		if ri, rj := rank(zones[i]), rank(zones[j]); ri != rj {
			return ri < rj
		}
		return zones[i] < zones[j]
	})
	return zones
}

// Report collects quota findings that affect planning.
type Report struct {
	// Unlimited lists quotas with no limit.
	Unlimited []quota.Quota `json:"unlimited"`
	// Unknown maps machine families with no quota rule to their machine type count.
	Unknown map[string]int `json:"unknown"`
	// Inferred lists families whose quota rule came from the snapshot.
	Inferred []string `json:"inferred"`
	// NoQuota maps series that cannot run on-demand to the quota that blocks them.
	NoQuota map[string]string `json:"no_quota"`
	// NotPlanned maps families that are not ordinary VMs (TPU hosts, legacy
	// shared-core) to what they are; they have no quota rule and are not planned.
	NotPlanned map[string]string `json:"not_planned"`
	// Skipped maps machine types left out of captures to the reason.
	Skipped map[string]string `json:"skipped"`
}

// Check builds the report for a region.
func Check(db *database.Database, snap *quota.Snapshot, opts Options) Report {
	r := Report{Unlimited: snap.Unlimited(), Unknown: map[string]int{}, Skipped: map[string]string{},
		NoQuota: map[string]string{}, NotPlanned: map[string]string{}}
	inferred := map[string]bool{}
	runnable := map[string]bool{}
	blocked := map[string]string{}
	for _, mt := range db.Query(database.Filter{Region: opts.Region}) {
		c := quota.Compute(snap, mt, "on-demand", opts.Region)
		series := database.SeriesOf(mt.Name)
		switch {
		case !c.Known && kindOf(mt) != "":
			r.NotPlanned[mt.Family] = kindOf(mt)
		case !c.Known:
			r.Unknown[mt.Family]++
		case c.Nodes != 0:
			runnable[series] = true
		default:
			blocked[series] = blockedBy(c)
		}
		if c.Known && c.Inferred {
			inferred[mt.Family] = true
		}
		if why := skip(mt, opts); why != "" {
			r.Skipped[mt.Name] = why
		}
	}
	for f := range inferred {
		r.Inferred = append(r.Inferred, f)
	}
	sort.Strings(r.Inferred)
	for series, why := range blocked {
		if !runnable[series] {
			r.NoQuota[series] = why
		}
	}
	return r
}

// kindOf names machine families that are not ordinary Compute Engine VMs: Cloud TPU
// hosts (ct*, tpu*), which are created through Cloud TPU or GKE, and legacy shared-core
// types (f1, g1). It returns "" for other families.
func kindOf(mt database.MachineType) string {
	switch {
	case strings.HasPrefix(mt.Family, "ct") || strings.HasPrefix(mt.Family, "tpu"):
		return "Cloud TPU host, created through Cloud TPU or GKE"
	case mt.Family == "f1" || mt.Family == "g1":
		return "legacy shared-core"
	}
	return ""
}

// blockedBy describes the quota that leaves a machine type with no capacity.
func blockedBy(c quota.Capacity) string {
	if c.Binding == "" {
		return c.Note
	}
	for _, ch := range c.Checks {
		if ch.Metric != c.Binding {
			continue
		}
		desc := ch.Metric
		if len(ch.Dimensions) > 0 {
			desc += " " + dims(ch.Dimensions)
		}
		if !ch.Found {
			return desc + " (no quota granted)"
		}
		return fmt.Sprintf("%s (limit %g)", desc, ch.Limit)
	}
	return c.Binding
}

// Lines renders the report as human-readable lines.
func (r Report) Lines() []string {
	var lines []string
	for _, q := range r.Unlimited {
		where := q.Region
		if where == "" {
			where = "all regions"
		}
		lines = append(lines, fmt.Sprintf("unlimited quota: %s %s (%s)", q.Metric, dims(q.Dimensions), where))
	}
	for _, f := range r.Inferred {
		lines = append(lines, fmt.Sprintf("quota rule inferred from snapshot for family %s (not in Google's table)", f))
	}
	families := make([]string, 0, len(r.Unknown))
	for f := range r.Unknown {
		families = append(families, f)
	}
	sort.Strings(families)
	for _, f := range families {
		lines = append(lines, fmt.Sprintf("no quota rule for family %s (%d machine types left out)", f, r.Unknown[f]))
	}
	for _, f := range sortedKeys(r.NotPlanned) {
		lines = append(lines, fmt.Sprintf("not planned: family %s (%s)", f, r.NotPlanned[f]))
	}
	for _, series := range sortedKeys(r.NoQuota) {
		lines = append(lines, fmt.Sprintf("no on-demand quota for %s: %s", series, r.NoQuota[series]))
	}
	counts := map[string]int{}
	for _, why := range r.Skipped {
		counts[why]++
	}
	reasons := make([]string, 0, len(counts))
	for why := range counts {
		reasons = append(reasons, why)
	}
	sort.Strings(reasons)
	for _, why := range reasons {
		lines = append(lines, fmt.Sprintf("not captured: %d %s machine types (still in graphs)", counts[why], why))
	}
	return lines
}

func dims(d map[string]string) string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+d[k])
	}
	return strings.Join(parts, ",")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Parse reads a plan written by `quota plan --format commands`, one Capture per
// `make topology` line; comments and other lines are skipped. The plan does not carry
// vCPU and GPU counts or the purpose, so those are left empty.
func Parse(r io.Reader) ([]Capture, error) {
	var out []Capture
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "make topology ") {
			continue
		}
		c, err := parseCommand(line)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// parseCommand is the inverse of Command.
func parseCommand(line string) (Capture, error) {
	c := Capture{Model: "on-demand"}
	rest := strings.TrimPrefix(line, "make topology ")
	for {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			return c, fmt.Errorf("plan line %q: expected KEY=value, got %q", line, rest)
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return c, fmt.Errorf("plan line %q: unterminated quote", line)
			}
			value, rest = rest[1:1+end], rest[end+2:]
		} else if sp := strings.IndexByte(rest, ' '); sp < 0 {
			value, rest = rest, ""
		} else {
			value, rest = rest[:sp], rest[sp:]
		}
		switch key {
		case "MACHINE_TYPE":
			c.MachineType = value
		case "ZONE":
			c.Zone = value
		case "ARGS":
			c.Args = strings.Fields(value)
		}
	}
	if c.MachineType == "" || c.Zone == "" {
		return c, fmt.Errorf("plan line %q: MACHINE_TYPE and ZONE are required", line)
	}
	c.Series = database.SeriesOf(c.MachineType)
	for _, a := range c.Args {
		if a == "--provisioning-model=SPOT" {
			c.Model = "spot"
		}
	}
	return c, nil
}
