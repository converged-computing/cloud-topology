// Package experiment compares the arms of a capture experiment (topology/experiment.sh):
// the same plan worked by topology-agent's agent and by the deterministic runner, each
// into its own database.
package experiment

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/plan"
)

// Arms are the experiment's arms, in the order they run. Each is a directory under the
// experiment directory holding a database. The agent arm also leaves agent-result.json, and
// the deterministic arm a run-plan.sh log.
var Arms = []string{"agent", "deterministic"}

// Outcome is what one arm did with one planned machine type.
type Outcome struct {
	// Status is ok when the arm's database holds a capture. Otherwise it is the arm's
	// last recorded outcome (stockout, quota or error), "not captured" when the arm ran but
	// left no record of why, or "not attempted".
	Status string `json:"status"`
	// Attempts counts capture attempts: zone and model tries from the agent's result, or
	// runs of the plan line from the runner's log.
	Attempts int `json:"attempts,omitempty"`
	// Seconds is the runner's time over all attempts (deterministic arm only).
	Seconds int `json:"seconds,omitempty"`
	// Error is the agent's error message for a failed capture.
	Error string `json:"error,omitempty"`
	// Zone, ProvisioningModel, ProvisionSeconds and CapturedAt come from the capture.
	Zone              string  `json:"zone,omitempty"`
	ProvisioningModel string  `json:"provisioning_model,omitempty"`
	ProvisionSeconds  float64 `json:"provision_seconds,omitempty"`
	CapturedAt        string  `json:"captured_at,omitempty"`
}

// Row is one planned machine type across the arms.
type Row struct {
	MachineType string             `json:"machine_type"`
	Series      string             `json:"series"`
	Zone        string             `json:"zone"`
	Model       string             `json:"model"`
	Outcomes    map[string]Outcome `json:"outcomes"`
}

// Summary totals one arm.
type Summary struct {
	Arm      string `json:"arm"`
	Captured int    `json:"captured"`
	Planned  int    `json:"planned"`
	// Rate is Captured over Planned.
	Rate float64 `json:"rate"`
	// Start, End and Seconds are the arm's wall-clock run from phases.tsv, if it ran.
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	// Exit is the arm's exit status from phases.tsv.
	Exit *int `json:"exit,omitempty"`
	// MeanProvisionSeconds averages provision_seconds over the arm's captures.
	MeanProvisionSeconds float64 `json:"mean_provision_seconds,omitempty"`
	// Extra lists captures in the arm's database that are not in the plan.
	Extra []string `json:"extra,omitempty"`
	// Agent is the agent's own accounting, from agent-result.json (agent arm only).
	Agent *AgentRun `json:"agent,omitempty"`
}

// Report compares the arms over one plan.
type Report struct {
	Plan    string    `json:"plan"`
	Planned int       `json:"planned"`
	Arms    []Summary `json:"arms"`
	// Either counts machine types captured by at least one arm, Both by every arm,
	// Neither by none; Only counts those captured by one arm alone.
	Either  int            `json:"either"`
	Both    int            `json:"both"`
	Neither int            `json:"neither"`
	Only    map[string]int `json:"only"`
	Rows    []Row          `json:"rows"`
}

// Build reads the experiment directory and compares the arms over the plan.
func Build(planFile string, captures []plan.Capture, dir string) (*Report, error) {
	r := &Report{Plan: planFile, Planned: len(captures), Only: map[string]int{}}
	phases, err := readPhases(filepath.Join(dir, "phases.tsv"))
	if err != nil {
		return nil, err
	}
	planned := map[string]bool{}
	for _, c := range captures {
		planned[c.MachineType] = true
		r.Rows = append(r.Rows, Row{MachineType: c.MachineType, Series: c.Series, Zone: c.Zone, Model: c.Model, Outcomes: map[string]Outcome{}})
	}

	for _, arm := range Arms {
		s := Summary{Arm: arm, Planned: len(captures)}
		armDir := filepath.Join(dir, arm)
		ran := false
		if p, ok := phases[arm]; ok {
			s.Start, s.End, s.Seconds, s.Exit = p.start, p.end, p.seconds, &p.exit
			ran = true
		}
		var db *database.Database
		if _, err := os.Stat(armDir); err == nil {
			ran = true
			if db, err = database.Load(armDir); err != nil {
				return nil, fmt.Errorf("%s arm: %w", arm, err)
			}
		}
		outcomes, err := readLog(filepath.Join(dir, arm+"-captures.log"))
		if err != nil {
			return nil, err
		}
		run, err := readAgentResult(filepath.Join(dir, arm+"-result.json"))
		if err != nil {
			return nil, err
		}
		if run != nil {
			s.Agent = run
			for _, a := range run.results {
				outcomes[a.MachineType] = logged{status: a.Outcome, attempts: len(a.Attempts), err: a.Error}
			}
		}
		if len(outcomes) > 0 {
			ran = true
		}
		var provision float64
		for i := range r.Rows {
			row := &r.Rows[i]
			o := Outcome{Status: "not attempted"}
			if !ran {
				o.Status = "not run"
			}
			if l, ok := outcomes[row.MachineType]; ok {
				o.Status, o.Attempts, o.Seconds, o.Error = l.status, l.attempts, l.seconds, l.err
			}
			if db != nil {
				if c, ok := db.Captures[row.MachineType]; ok {
					o.Status = "ok"
					o.Zone, o.ProvisioningModel, o.ProvisionSeconds, o.CapturedAt = c.Zone, c.ProvisioningModel, c.ProvisionSeconds, c.CapturedAt
					s.Captured++
					provision += c.ProvisionSeconds
				} else if o.Status == "not attempted" && (s.Exit != nil || len(db.Captures) > 0) {
					o.Status = "not captured"
				}
			}
			row.Outcomes[arm] = o
		}
		if s.Captured > 0 {
			s.Rate = float64(s.Captured) / float64(s.Planned)
			s.MeanProvisionSeconds = provision / float64(s.Captured)
		}
		if db != nil {
			for name := range db.Captures {
				if !planned[name] {
					s.Extra = append(s.Extra, name)
				}
			}
			sort.Strings(s.Extra)
		}
		r.Arms = append(r.Arms, s)
	}

	for _, row := range r.Rows {
		var ok []string
		for _, arm := range Arms {
			if row.Outcomes[arm].Status == "ok" {
				ok = append(ok, arm)
			}
		}
		switch len(ok) {
		case 0:
			r.Neither++
		case len(Arms):
			r.Both++
			r.Either++
		default:
			r.Either++
			if len(ok) == 1 {
				r.Only[ok[0]]++
			}
		}
	}
	return r, nil
}

type phase struct {
	start, end    string
	seconds, exit int
}

// readPhases reads experiment.sh's phases.tsv: arm, start, end, seconds, exit.
func readPhases(path string) (map[string]phase, error) {
	out := map[string]phase{}
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
		if len(fields) < 5 {
			continue
		}
		seconds, _ := strconv.Atoi(fields[3])
		exit, _ := strconv.Atoi(fields[4])
		out[fields[0]] = phase{start: fields[1], end: fields[2], seconds: seconds, exit: exit}
	}
	return out, sc.Err()
}

type logged struct {
	status, err       string
	attempts, seconds int
}

// readLog reads run-plan.sh's log: time, machine type, outcome, seconds (older logs end in
// "s"), pass and, for errors, the message. The last line for a machine type gives its status.
func readLog(path string) (map[string]logged, error) {
	out := map[string]logged{}
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
		seconds, _ := strconv.Atoi(strings.TrimSuffix(fields[3], "s"))
		l := out[fields[1]]
		l.status = fields[2]
		l.attempts++
		l.seconds += seconds
		l.err = ""
		if len(fields) > 5 {
			l.err = fields[5]
		}
		out[fields[1]] = l
	}
	return out, sc.Err()
}

// JSON renders the report as indented JSON.
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Markdown renders the report as a Markdown document.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Capture experiment: %s\n\n", r.Plan)
	fmt.Fprintf(&b, "%d machine types planned.\n\n", r.Planned)
	b.WriteString("| Arm | Captured | Rate | Wall clock | Exit | Mean provision |\n|---|---|---|---|---|---|\n")
	for _, s := range r.Arms {
		exit := ""
		if s.Exit != nil {
			exit = strconv.Itoa(*s.Exit)
		}
		fmt.Fprintf(&b, "| %s | %d/%d | %s | %s | %s | %s |\n", s.Arm, s.Captured, s.Planned, percent(s.Rate),
			duration(s.Seconds), exit, duration(int(s.MeanProvisionSeconds+0.5)))
	}
	rate := 0.0
	if r.Planned > 0 {
		rate = float64(r.Either) / float64(r.Planned)
	}
	fmt.Fprintf(&b, "| either | %d/%d | %s | | | |\n\n", r.Either, r.Planned, percent(rate))
	var only []string
	for _, arm := range Arms {
		only = append(only, fmt.Sprintf("%s only %d", arm, r.Only[arm]))
	}
	fmt.Fprintf(&b, "Both arms %d, %s, neither %d.\n\n", r.Both, strings.Join(only, ", "), r.Neither)

	fmt.Fprintf(&b, "| Machine type | Model | %s |\n|---|---|%s\n", strings.Join(titled(Arms), " | "), strings.Repeat("---|", len(Arms)))
	for _, row := range r.Rows {
		var cells []string
		for _, arm := range Arms {
			cells = append(cells, describe(row.Outcomes[arm]))
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", row.MachineType, row.Model, strings.Join(cells, " | "))
	}
	for _, s := range r.Arms {
		if a := s.Agent; a != nil {
			fmt.Fprintf(&b, "\nThe agent took %d turns, %s of model time, and $%.2f in tokens", a.Turns, duration(a.APIMillis/1000), a.CostUSD)
			if a.AttemptsUsed > 0 {
				fmt.Fprintf(&b, ", using %d capture attempts", a.AttemptsUsed)
			}
			b.WriteString(".\n")
		}
	}
	for _, s := range r.Arms {
		if len(s.Extra) > 0 {
			fmt.Fprintf(&b, "\nCaptures outside the plan in the %s arm: %s.\n", s.Arm, strings.Join(s.Extra, ", "))
		}
	}
	return b.String()
}

// describe summarizes an outcome for the per-machine-type table.
func describe(o Outcome) string {
	var notes []string
	if o.Status == "ok" {
		if o.ProvisionSeconds > 0 {
			notes = append(notes, duration(int(o.ProvisionSeconds+0.5))+" provision")
		}
		if o.ProvisioningModel != "" {
			notes = append(notes, o.ProvisioningModel)
		}
		if o.Zone != "" {
			notes = append(notes, o.Zone)
		}
	}
	if o.Attempts > 1 {
		notes = append(notes, fmt.Sprintf("%d attempts", o.Attempts))
	}
	if o.Seconds > 0 && o.Status != "ok" {
		notes = append(notes, duration(o.Seconds))
	}
	if o.Error != "" && o.Status != "ok" {
		msg := strings.Join(strings.Fields(o.Error), " ")
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		notes = append(notes, strings.ReplaceAll(msg, "|", "/"))
	}
	if len(notes) == 0 {
		return o.Status
	}
	return o.Status + " (" + strings.Join(notes, ", ") + ")"
}

func percent(rate float64) string {
	return fmt.Sprintf("%.0f%%", rate*100)
}

func duration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	d := time.Duration(seconds) * time.Second
	if d < time.Hour {
		return d.String()
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func titled(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToUpper(n[:1]) + n[1:]
	}
	return out
}
