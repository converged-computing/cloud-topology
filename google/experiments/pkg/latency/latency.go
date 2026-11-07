// Package latency compares provisioning time across databases: how long each capture
// took to get a machine type running on each provisioning path (a VM, a GKE node pool),
// with the fixed costs a path adds, such as creating the cluster.
package latency

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// Source is one database to compare, under a label.
type Source struct {
	Label string
	Dir   string
}

// Path summarizes one database: its captures' provisioning times and fixed costs.
type Path struct {
	Label    string             `json:"label"`
	Captures int                `json:"captures"`
	Mean     float64            `json:"mean_seconds"`
	Median   float64            `json:"median_seconds"`
	Max      float64            `json:"max_seconds"`
	Timings  map[string]float64 `json:"mean_timings,omitempty"`
	// Fixed are one-off costs from the phases file whose names start with the label
	// (gke/cluster_create), in seconds.
	Fixed map[string]float64 `json:"fixed_seconds,omitempty"`
}

// Row is one machine type across the paths.
type Row struct {
	MachineType string             `json:"machine_type"`
	Seconds     map[string]float64 `json:"seconds"`
	Model       map[string]string  `json:"model,omitempty"`
}

// Report compares provisioning paths.
type Report struct {
	Paths []Path `json:"paths"`
	Rows  []Row  `json:"rows"`
	// Common counts machine types captured on every path.
	Common int `json:"common"`
}

// Compare reads the databases and the phases file (name, start, end, seconds, exit).
func Compare(sources []Source, phasesFile string) (*Report, error) {
	fixed, err := readPhases(phasesFile)
	if err != nil {
		return nil, err
	}
	r := &Report{}
	byType := map[string]*Row{}
	for _, s := range sources {
		db, err := database.Load(s.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Label, err)
		}
		p := Path{Label: s.Label, Timings: map[string]float64{}, Fixed: map[string]float64{}}
		var secs []float64
		counts := map[string]int{}
		for name, c := range db.Captures {
			if c.ProvisionSeconds <= 0 {
				continue
			}
			secs = append(secs, c.ProvisionSeconds)
			for k, v := range c.Timings {
				p.Timings[k] += v
				counts[k]++
			}
			row := byType[name]
			if row == nil {
				row = &Row{MachineType: name, Seconds: map[string]float64{}, Model: map[string]string{}}
				byType[name] = row
			}
			row.Seconds[s.Label] = c.ProvisionSeconds
			row.Model[s.Label] = c.ProvisioningModel
		}
		for k := range p.Timings {
			p.Timings[k] = round1(p.Timings[k] / float64(counts[k]))
		}
		if len(p.Timings) == 0 {
			p.Timings = nil
		}
		for k, v := range fixed {
			if strings.HasPrefix(k, s.Label+"/") {
				p.Fixed[strings.TrimPrefix(k, s.Label+"/")] = v
			}
		}
		if len(p.Fixed) == 0 {
			p.Fixed = nil
		}
		sort.Float64s(secs)
		p.Captures = len(secs)
		if len(secs) > 0 {
			sum := 0.0
			for _, v := range secs {
				sum += v
			}
			p.Mean, p.Median, p.Max = round1(sum/float64(len(secs))), round1(secs[len(secs)/2]), secs[len(secs)-1]
		}
		r.Paths = append(r.Paths, p)
	}
	for _, row := range byType {
		r.Rows = append(r.Rows, *row)
		if len(row.Seconds) == len(sources) {
			r.Common++
		}
	}
	sort.Slice(r.Rows, func(i, j int) bool { return r.Rows[i].MachineType < r.Rows[j].MachineType })
	return r, nil
}

func readPhases(path string) (map[string]float64, error) {
	out := map[string]float64{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 4 {
			continue
		}
		secs, _ := strconv.ParseFloat(fields[3], 64)
		out[fields[0]] = secs
	}
	return out, sc.Err()
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func secs(v float64) string {
	if v <= 0 {
		return ""
	}
	if v < 60 {
		return fmt.Sprintf("%.0fs", v)
	}
	return fmt.Sprintf("%dm%02ds", int(v)/60, int(v)%60)
}

// Markdown renders the report.
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Provisioning latency by path\n\n| Path | Captures | Median | Mean | Max | Breakdown (mean) | Fixed costs |\n|---|---|---|---|---|---|---|\n")
	for _, p := range r.Paths {
		var parts, fixed []string
		for _, k := range sortedKeys(p.Timings) {
			parts = append(parts, fmt.Sprintf("%s %s", strings.TrimSuffix(k, "_seconds"), secs(p.Timings[k])))
		}
		for _, k := range sortedKeys(p.Fixed) {
			fixed = append(fixed, fmt.Sprintf("%s %s", k, secs(p.Fixed[k])))
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n", p.Label, p.Captures, secs(p.Median), secs(p.Mean), secs(p.Max), strings.Join(parts, ", "), strings.Join(fixed, ", "))
	}
	fmt.Fprintf(&b, "\n%d machine types captured on every path.\n\n| Machine type |", r.Common)
	for _, p := range r.Paths {
		fmt.Fprintf(&b, " %s |", p.Label)
	}
	b.WriteString("\n|---|")
	for range r.Paths {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "| %s |", row.MachineType)
		for _, p := range r.Paths {
			if v, ok := row.Seconds[p.Label]; ok {
				fmt.Fprintf(&b, " %s (%s) |", secs(v), row.Model[p.Label])
			} else {
				b.WriteString(" — |")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
