// Command fidelity reports on the scheduling-fidelity experiment: the resource-query
// sessions written by experiments/scripts/fidelity.sh, one per graph variant and match
// policy, become fidelity.json and fidelity.md.
//
//	fidelity report --runs DIR --jobs jobs.tsv --graphs DIR --out DIR
//	fidelity ablate --in graph.json --out graph.json --drop nic[,type...]
//
// ablate removes every vertex of the given types (and the edges to them) from a JGF
// graph, for the ablation variants.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Job is one line of jobs.tsv.
type Job struct {
	ID          string `json:"id"`
	Phase       string `json:"phase"`
	Family      string `json:"family"`
	Description string `json:"description"`
	Levels      string `json:"levels"`
}

// Match is one match attempt in a session.
type Match struct {
	Variant     string  `json:"variant"`
	Policy      string  `json:"policy"`
	ID          string  `json:"id"`
	Phase       string  `json:"phase"`
	Family      string  `json:"family"`
	Allocated   bool    `json:"allocated"`
	ElapseMs    float64 `json:"elapse_ms"`
	Preorder    int     `json:"preorder_visits"`
	Postorder   int     `json:"postorder_visits"`
	MachineType string  `json:"machine_type,omitempty"`
	Model       string  `json:"model,omitempty"`
	Zone        string  `json:"zone,omitempty"`
	Nodes       int     `json:"nodes,omitempty"`
}

// Session is one resource-query run: a graph variant with a match policy.
type Session struct {
	Variant     string  `json:"variant"`
	Level       string  `json:"level"`
	Policy      string  `json:"policy"`
	Vertices    int     `json:"vertices"`
	Edges       int     `json:"edges"`
	LoadSeconds float64 `json:"load_seconds"`
	RSSMB       float64 `json:"rss_mb"`
	WallSeconds float64 `json:"wall_seconds"`
	ExitCode    int     `json:"exit_code"`
	// Singles: every single job the level can express, matched on an empty graph.
	Singles          int     `json:"singles"`
	SinglesAllocated int     `json:"singles_allocated"`
	SingleMedianMs   float64 `json:"single_median_ms"`
	SingleMeanVisits float64 `json:"single_mean_preorder_visits"`
	FailMedianMs     float64 `json:"single_fail_median_ms"`
	DistinctTypes    int     `json:"distinct_machine_types"`
	// Stream: jobs matched in order and kept.
	Stream             int     `json:"stream"`
	StreamPlaced       int     `json:"stream_placed"`
	StreamFirstFail    int     `json:"stream_before_first_failure"`
	StreamFirstFailJob string  `json:"stream_first_failure,omitempty"`
	StreamMedianMs     float64 `json:"stream_median_ms"`
	StreamTotalSecond  float64 `json:"stream_total_seconds"`
}

// Graph summarizes one variant across its sessions.
type Graph struct {
	Variant     string  `json:"variant"`
	Level       string  `json:"level"`
	Vertices    int     `json:"vertices"`
	Edges       int     `json:"edges"`
	Bytes       int64   `json:"bytes"`
	LoadSeconds float64 `json:"load_seconds"` // fastest session
	RSSMB       float64 `json:"rss_mb"`       // largest session
}

// Agreement compares where two variants put the same jobs (policy first).
type Agreement struct {
	A        string `json:"a"`
	B        string `json:"b"`
	Policy   string `json:"policy"`
	Both     int    `json:"both_allocated"`
	SameType int    `json:"same_machine_type"`
	SameZone int    `json:"same_zone"`
	OnlyA    int    `json:"only_a"`
	OnlyB    int    `json:"only_b"`
}

// Report is fidelity.json.
type Report struct {
	Jobs        []Job                                   `json:"jobs"`
	Graphs      []Graph                                 `json:"graphs"`
	Sessions    []Session                               `json:"sessions"`
	Feasibility map[string]map[string]*Match            `json:"feasibility"` // job id -> variant -> match (policy first)
	Policies    map[string]map[string]map[string]*Match `json:"policies"`    // variant -> policy -> job id -> match
	Agreement   []Agreement                             `json:"agreement"`
	// Captured lists the machine types with a capture of their own.
	Captured map[string]bool `json:"captured"`
	// Topology is how the exact graph models each machine type, from an exact graph's
	// offers: hwloc (its own capture), derived:<type> (cut from a captured member of its
	// series) or catalog (one socket from the catalog; nothing in its series captured).
	Topology map[string]string `json:"topology,omitempty"`
	Matches  []Match           `json:"matches"`
}

var (
	reTree   = regexp.MustCompile(`^\s+-+(.+)\[(\d+):[sx]\]$`)
	reOffer  = regexp.MustCompile(`^([a-z0-9-]+)\.(on-demand|spot)$`)
	reZone   = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]-[a-z]$`)
	reNode   = regexp.MustCompile(`^node\d+$`)
	reLoad   = regexp.MustCompile(`Graph Load Time: (\S+)`)
	reVertex = regexp.MustCompile(`Vertex Count: (\d+)`)
	reEdge   = regexp.MustCompile(`Edge Count: (\d+)`)
	reJobID  = regexp.MustCompile(`JOBID=(\d+)`)
	reElapse = regexp.MustCompile(`ELAPSE=(\S+)`)
	rePre    = regexp.MustCompile(`PREORDER VISIT COUNT=(\d+)`)
	rePost   = regexp.MustCompile(`POSTORDER VISIT COUNT=(\d+)`)
	reRSS    = regexp.MustCompile(`Maximum resident set size \(kbytes\): (\d+)`)
	reWall   = regexp.MustCompile(`Elapsed \(wall clock\) time \(h:mm:ss or m:ss\): (\S+)`)
)

func readJobs(path string) ([]Job, map[string]Job, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var jobs []Job
	byID := map[string]Job{}
	sc := bufio.NewScanner(f)
	var cols []string
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if cols == nil {
			cols = fields
			continue
		}
		row := map[string]string{}
		for i, c := range cols {
			if i < len(fields) {
				row[c] = fields[i]
			}
		}
		j := Job{ID: row["id"], Phase: row["phase"], Family: row["family"], Description: row["description"], Levels: row["levels"]}
		jobs = append(jobs, j)
		byID[j.ID] = j
	}
	return jobs, byID, sc.Err()
}

// matchOrder reads the job ids of a session's match lines, in order.
func matchOrder(in string) ([]string, error) {
	data, err := os.ReadFile(in)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "match allocate ") {
			ids = append(ids, strings.TrimSuffix(filepath.Base(strings.TrimPrefix(line, "match allocate ")), ".yaml"))
		}
	}
	return ids, nil
}

func parseWall(s string) float64 {
	parts := strings.Split(s, ":")
	total := 0.0
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		total = total*60 + v
	}
	return total
}

func parseSession(dir, variant, policy string, jobs map[string]Job) (*Session, []Match, error) {
	base := filepath.Join(dir, variant, policy)
	ids, err := matchOrder(base + ".in")
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(base + ".log")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	s := &Session{Variant: variant, Level: strings.SplitN(variant, "-", 2)[0], Policy: policy}
	if rc, err := os.ReadFile(base + ".log.rc"); err == nil {
		s.ExitCode, _ = strconv.Atoi(strings.TrimSpace(string(rc)))
	}
	if t, err := os.ReadFile(base + ".log.time"); err == nil {
		if m := reRSS.FindSubmatch(t); m != nil {
			kb, _ := strconv.ParseFloat(string(m[1]), 64)
			s.RSSMB = kb / 1024
		}
		if m := reWall.FindSubmatch(t); m != nil {
			s.WallSeconds = parseWall(string(m[1]))
		}
	}
	var matches []Match
	var tree []string
	noMatch, seen := false, 0
	var cur *Match
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if m := reTree.FindStringSubmatch(line); m != nil {
			tree = append(tree, m[1])
			continue
		}
		if m := reLoad.FindStringSubmatch(line); m != nil {
			s.LoadSeconds, _ = strconv.ParseFloat(m[1], 64)
		} else if m := reVertex.FindStringSubmatch(line); m != nil {
			s.Vertices, _ = strconv.Atoi(m[1])
		} else if m := reEdge.FindStringSubmatch(line); m != nil {
			s.Edges, _ = strconv.Atoi(m[1])
		} else if strings.Contains(line, "No matching resources found") {
			noMatch = true
		} else if reJobID.MatchString(line) {
			// The k-th JOBID line answers the k-th match sent. The id itself is not used:
			// a jobspec Fluxion rejects outright gets a JOBID line without advancing it.
			seen++
			if seen > len(ids) {
				return nil, nil, fmt.Errorf("%s: more JOBID lines than the %d matches sent", base, len(ids))
			}
			job := jobs[ids[seen-1]]
			matches = append(matches, Match{Variant: variant, Policy: policy, ID: job.ID, Phase: job.Phase, Family: job.Family, Allocated: !noMatch})
			cur = &matches[len(matches)-1]
			if !noMatch {
				for _, name := range tree {
					if om := reOffer.FindStringSubmatch(name); om != nil {
						cur.MachineType, cur.Model = om[1], om[2]
					} else if reZone.MatchString(name) {
						cur.Zone = name
					} else if reNode.MatchString(name) {
						cur.Nodes++
					}
				}
			}
			tree, noMatch = nil, false
		} else if cur != nil {
			if m := reElapse.FindStringSubmatch(line); m != nil {
				sec, _ := strconv.ParseFloat(m[1], 64)
				cur.ElapseMs = sec * 1000
			} else if m := rePre.FindStringSubmatch(line); m != nil {
				cur.Preorder, _ = strconv.Atoi(m[1])
			} else if m := rePost.FindStringSubmatch(line); m != nil {
				cur.Postorder, _ = strconv.Atoi(m[1])
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	// Summaries.
	var singleMs, failMs, streamMs []float64
	visits, types := 0.0, map[string]bool{}
	firstFail := -1
	for _, m := range matches {
		if m.Phase == "single" {
			s.Singles++
			if m.Allocated {
				s.SinglesAllocated++
				singleMs = append(singleMs, m.ElapseMs)
				types[m.MachineType] = true
			} else {
				failMs = append(failMs, m.ElapseMs)
			}
			visits += float64(m.Preorder)
		} else {
			s.Stream++
			streamMs = append(streamMs, m.ElapseMs)
			s.StreamTotalSecond += m.ElapseMs / 1000
			if m.Allocated {
				s.StreamPlaced++
			} else if firstFail < 0 {
				firstFail = s.Stream - 1
				s.StreamFirstFailJob = m.ID
			}
		}
	}
	if firstFail < 0 {
		firstFail = s.Stream
	}
	s.StreamFirstFail = firstFail
	s.SingleMedianMs, s.FailMedianMs, s.StreamMedianMs = median(singleMs), median(failMs), median(streamMs)
	if s.Singles > 0 {
		s.SingleMeanVisits = visits / float64(s.Singles)
	}
	s.DistinctTypes = len(types)
	return s, matches, nil
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	if len(ys)%2 == 1 {
		return ys[len(ys)/2]
	}
	return (ys[len(ys)/2-1] + ys[len(ys)/2]) / 2
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	runs := fs.String("runs", "", "sessions directory written by fidelity.sh")
	jobsFile := fs.String("jobs", "", "jobs.tsv from jobspecs")
	graphsDir := fs.String("graphs", "", "graphs directory (for file sizes)")
	out := fs.String("out", "", "output directory for fidelity.json and fidelity.md")
	captured := fs.String("captured", "", "database directory; machine types it has captures for are marked in the report")
	offers := fs.String("offers", "", "an exact graph (JGF) whose offers say how each machine type is modelled: hwloc, derived or catalog")
	fs.Parse(args)
	if *runs == "" || *jobsFile == "" || *out == "" {
		return fmt.Errorf("--runs, --jobs and --out are required")
	}
	jobs, byID, err := readJobs(*jobsFile)
	if err != nil {
		return err
	}
	r := &Report{Jobs: jobs, Feasibility: map[string]map[string]*Match{}, Policies: map[string]map[string]map[string]*Match{}}
	if *offers != "" {
		top, err := readTopologySources(*offers)
		if err != nil {
			return err
		}
		r.Topology = top
	}
	r.Captured = map[string]bool{}
	if *captured != "" {
		caps, _ := filepath.Glob(filepath.Join(*captured, "topology", "*", "capture.json"))
		for _, c := range caps {
			r.Captured[filepath.Base(filepath.Dir(c))] = true
		}
	}
	entries, err := os.ReadDir(*runs)
	if err != nil {
		return err
	}
	graphs := map[string]*Graph{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		variant := e.Name()
		logs, _ := filepath.Glob(filepath.Join(*runs, variant, "*.log"))
		for _, l := range logs {
			policy := strings.TrimSuffix(filepath.Base(l), ".log")
			s, matches, err := parseSession(*runs, variant, policy, byID)
			if err != nil {
				return err
			}
			r.Sessions = append(r.Sessions, *s)
			r.Matches = append(r.Matches, matches...)
			g := graphs[variant]
			if g == nil {
				g = &Graph{Variant: variant, Level: s.Level, Vertices: s.Vertices, Edges: s.Edges, LoadSeconds: s.LoadSeconds}
				if st, err := os.Stat(filepath.Join(*graphsDir, variant+".json")); err == nil {
					g.Bytes = st.Size()
				}
				graphs[variant] = g
			}
			if s.LoadSeconds > 0 && (s.LoadSeconds < g.LoadSeconds || g.LoadSeconds == 0) {
				g.LoadSeconds = s.LoadSeconds
			}
			g.RSSMB = math.Max(g.RSSMB, s.RSSMB)
			if r.Policies[variant] == nil {
				r.Policies[variant] = map[string]map[string]*Match{}
			}
			byJob := map[string]*Match{}
			for i := range matches {
				m := &matches[i]
				if m.Phase == "single" {
					byJob[m.ID] = m
					if policy == "first" {
						if r.Feasibility[m.ID] == nil {
							r.Feasibility[m.ID] = map[string]*Match{}
						}
						r.Feasibility[m.ID][variant] = m
					}
				}
			}
			r.Policies[variant][policy] = byJob
		}
	}
	for _, g := range graphs {
		r.Graphs = append(r.Graphs, *g)
	}
	sort.Slice(r.Graphs, func(i, j int) bool { return variantLess(r.Graphs[i].Variant, r.Graphs[j].Variant) })
	sort.Slice(r.Sessions, func(i, j int) bool {
		if r.Sessions[i].Variant != r.Sessions[j].Variant {
			return variantLess(r.Sessions[i].Variant, r.Sessions[j].Variant)
		}
		return policyRank(r.Sessions[i].Policy) < policyRank(r.Sessions[j].Policy)
	})
	// Agreement between the exact graph and the others, per policy.
	var variants []string
	for _, g := range r.Graphs {
		variants = append(variants, g.Variant)
	}
	for _, policy := range policies(r) {
		for _, a := range variants {
			if a == "exact" {
				continue
			}
			pa, pb := r.Policies[a][policy], r.Policies["exact"][policy]
			if pa == nil || pb == nil {
				continue
			}
			ag := Agreement{A: a, B: "exact", Policy: policy}
			for id, ma := range pa {
				mb := pb[id]
				switch {
				case mb == nil:
					continue
				case ma.Allocated && mb.Allocated:
					ag.Both++
					if ma.MachineType == mb.MachineType {
						ag.SameType++
					}
					if ma.Zone != "" && ma.Zone == mb.Zone {
						ag.SameZone++
					}
				case ma.Allocated:
					ag.OnlyA++
				case mb.Allocated:
					ag.OnlyB++
				}
			}
			r.Agreement = append(r.Agreement, ag)
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "fidelity.json"), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, "fidelity.md"), []byte(markdown(r)), 0o644)
}

var policyOrder = []string{"first", "low", "high", "lonode", "hinode", "lonodex", "hinodex", "firstnodex", "locality", "variation"}

func policyRank(p string) int {
	for i, q := range policyOrder {
		if p == q {
			return i
		}
	}
	return len(policyOrder)
}

func levelRank(v string) int {
	switch strings.SplitN(v, "-", 2)[0] {
	case "series":
		return 0
	case "coarse":
		return 1
	default:
		return 2
	}
}

func variantLess(a, b string) bool {
	if levelRank(a) != levelRank(b) {
		return levelRank(a) < levelRank(b)
	}
	// exact before exact-n1 before exact-n2 ... numerically.
	na, nb := scaleOf(a), scaleOf(b)
	if na != nb {
		return na < nb
	}
	return a < b
}

func scaleOf(v string) int {
	if i := strings.Index(v, "-n"); i >= 0 {
		n, _ := strconv.Atoi(strings.SplitN(v[i+2:], "-", 2)[0])
		return n
	}
	return 0
}

func policies(r *Report) []string {
	seen := map[string]bool{}
	for _, s := range r.Sessions {
		seen[s.Policy] = true
	}
	var out []string
	for _, p := range policyOrder {
		if seen[p] {
			out = append(out, p)
		}
	}
	return out
}

func fsec(s float64) string {
	if s >= 60 {
		return fmt.Sprintf("%dm%02ds", int(s)/60, int(s)%60)
	}
	return fmt.Sprintf("%.1fs", s)
}

func fms(ms float64) string {
	switch {
	case ms == 0:
		return "—"
	case ms >= 1000:
		return fmt.Sprintf("%.2f s", ms/1000)
	case ms >= 10:
		return fmt.Sprintf("%.0f ms", ms)
	default:
		return fmt.Sprintf("%.2f ms", ms)
	}
}

func markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Scheduling fidelity: what each graph level gets right, and what it costs\n\n")
	fmt.Fprintf(&b, "%d graph variants, %d match policies, %d single jobs and %d stream jobs, matched by Fluxion's resource-query. Nothing ran in the cloud.\n\n",
		len(r.Graphs), len(policies(r)), countPhase(r.Jobs, "single"), countPhase(r.Jobs, "stream"))

	fmt.Fprintf(&b, "## Graphs\n\nLoad time is the fastest of the variant's sessions; memory is the largest resident set of any session (the graph plus resource-query's indexes).\n\n")
	fmt.Fprintf(&b, "| Variant | Level | Vertices | Edges | JGF | Load | Memory |\n|---|---|---:|---:|---:|---:|---:|\n")
	for _, g := range r.Graphs {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %.0f MB | %s | %.0f MB |\n", g.Variant, g.Level, g.Vertices, g.Edges, float64(g.Bytes)/1e6, fsec(g.LoadSeconds), g.RSSMB)
	}

	// Feasibility matrix, policy first.
	var variants []string
	for _, g := range r.Graphs {
		variants = append(variants, g.Variant)
	}
	fmt.Fprintf(&b, "\n## Feasibility and placement (policy first)\n\nEach single job matched on an empty graph, then cancelled. A cell is the machine type Fluxion chose, ✗ when nothing matched, or n/a when the level has no vertex for what the job asks (the series graph has no node, NIC, local SSD or storage pool). A type marked ≈ is derived (cut from a captured type of its series); one marked ~ is catalog-only (nothing in its series captured, so the exact graph models it as one socket).\n\n")
	fmt.Fprintf(&b, "| Job | Request |")
	for _, v := range variants {
		fmt.Fprintf(&b, " %s |", v)
	}
	fmt.Fprintf(&b, "\n|---|---|")
	for range variants {
		fmt.Fprintf(&b, "---|")
	}
	fmt.Fprintf(&b, "\n")
	for _, j := range r.Jobs {
		if j.Phase != "single" {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s |", j.ID, j.Description)
		for _, v := range variants {
			m := r.Feasibility[j.ID][v]
			switch {
			case m == nil && !strings.Contains(j.Levels, strings.SplitN(v, "-", 2)[0]):
				fmt.Fprintf(&b, " n/a |")
			case m == nil:
				fmt.Fprintf(&b, " ? |")
			case m.Allocated:
				fmt.Fprintf(&b, " %s%s |", typeName(r, m), zoneSuffix(m))
			default:
				fmt.Fprintf(&b, " ✗ |")
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "\n## Agreement with the exact graph\n\nOver the single jobs both variants allocated (policy by row): how often the other level chose the same machine type as the exact graph, and the jobs only one of them could place.\n\n")
	fmt.Fprintf(&b, "| Variant | Policy | Both allocated | Same machine type | Same zone | Only this variant | Only exact |\n|---|---|---:|---:|---:|---:|---:|\n")
	for _, a := range r.Agreement {
		zone := "n/a"
		if strings.HasPrefix(a.A, "exact") {
			zone = fmt.Sprintf("%d", a.SameZone)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %d | %d |\n", a.A, a.Policy, a.Both, a.SameType, zone, a.OnlyA, a.OnlyB)
	}

	fmt.Fprintf(&b, "\n## Match policies\n\nPer variant and policy: single jobs allocated, the median match time of an allocated single job and of a failed one (a failure walks what the policy makes it walk), the mean preorder visits per single job, the distinct machine types the policy chose, and the stream: jobs placed before the first failure, placed in all, and the median match time with the graph filling up.\n\n")
	fmt.Fprintf(&b, "| Variant | Policy | Singles | Match (ok) | Match (fail) | Visits | Types | Placed on captured / derived / catalog-only | Stream before 1st fail | First failure | Stream placed | Stream match | Session |\n|---|---|---:|---:|---:|---:|---:|---|---:|---|---:|---:|---:|\n")
	byID := map[string]Job{}
	for _, j := range r.Jobs {
		byID[j.ID] = j
	}
	for _, s := range r.Sessions {
		counts := map[string]int{}
		for _, m := range r.Policies[s.Variant][s.Policy] {
			if m.Allocated {
				counts[source(r, m.MachineType)]++
			}
		}
		sources := fmt.Sprintf("%d / %d / %d", counts["hwloc"], counts["derived"], counts["catalog"])
		if s.Level == "series" {
			sources = "n/a (series pools)"
		}
		fmt.Fprintf(&b, "| %s | %s | %d/%d | %s | %s | %.0f | %d | %s | %d | %s | %d/%d | %s | %s |\n", s.Variant, s.Policy, s.SinglesAllocated, s.Singles,
			fms(s.SingleMedianMs), fms(s.FailMedianMs), s.SingleMeanVisits, s.DistinctTypes, sources, s.StreamFirstFail, byID[s.StreamFirstFailJob].Description, s.StreamPlaced, s.Stream, fms(s.StreamMedianMs), fsec(s.WallSeconds))
	}

	// Scale series: exact variants by size.
	fmt.Fprintf(&b, "\n## Scale (exact graphs, policy first)\n\nThe exact graph at 1, 2, 4 and 8 potential nodes per offer, and quota-sized. Match times are medians over the single jobs of the family.\n\n")
	fmt.Fprintf(&b, "| Variant | Vertices | Load | Memory | cores | gpu | nodes | failed matches |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, g := range r.Graphs {
		if g.Level != "exact" {
			continue
		}
		fam := func(f string, ok bool) float64 {
			var xs []float64
			for _, m := range r.Policies[g.Variant]["first"] {
				if (f == "" || m.Family == f) && m.Allocated == ok {
					xs = append(xs, m.ElapseMs)
				}
			}
			return median(xs)
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %.0f MB | %s | %s | %s | %s |\n", g.Variant, g.Vertices, fsec(g.LoadSeconds), g.RSSMB, fms(fam("cores", true)), fms(fam("gpu", true)), fms(fam("nodes", true)), fms(fam("", false)))
	}

	fmt.Fprintf(&b, "\n## Where the policies disagree (exact graph)\n\nMachine type chosen per policy for the single jobs the exact graph could place; a row with more than one type is a job whose placement the policy decides.\n\n")
	if ex := r.Policies["exact"]; ex != nil {
		ps := policies(r)
		fmt.Fprintf(&b, "| Job | Request |")
		for _, p := range ps {
			fmt.Fprintf(&b, " %s |", p)
		}
		fmt.Fprintf(&b, "\n|---|---|")
		for range ps {
			fmt.Fprintf(&b, "---|")
		}
		fmt.Fprintf(&b, "\n")
		for _, j := range r.Jobs {
			if j.Phase != "single" {
				continue
			}
			row, any := fmt.Sprintf("| %s | %s |", j.ID, j.Description), false
			for _, p := range ps {
				m := ex[p][j.ID]
				if m != nil && m.Allocated {
					row += fmt.Sprintf(" %s%s |", typeName(r, m), zoneSuffix(m))
					any = true
				} else {
					row += " ✗ |"
				}
			}
			if any {
				fmt.Fprintf(&b, "%s\n", row)
			}
		}
	}
	return b.String()
}

// typeName marks how the exact graph models the type: ~ for a catalog-only type (one
// socket from the catalog), ≈ for a derived one (cut from a captured type of its series),
// nothing for a captured type.
func typeName(r *Report, m *Match) string {
	switch source(r, m.MachineType) {
	case "catalog":
		return "~" + m.MachineType
	case "derived":
		return "≈" + m.MachineType
	}
	return m.MachineType
}

// source is hwloc, derived or catalog for a machine type, from the offers when known and
// otherwise from whether the type has its own capture.
func source(r *Report, mt string) string {
	if t, ok := r.Topology[mt]; ok {
		return strings.SplitN(t, ":", 2)[0]
	}
	if len(r.Captured) > 0 && !r.Captured[mt] {
		return "catalog"
	}
	return "hwloc"
}

// readTopologySources maps machine type to its offer's topology property in a JGF graph.
func readTopologySources(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Graph struct {
			Nodes []struct {
				Metadata struct {
					Type       string            `json:"type"`
					Properties map[string]string `json:"properties"`
				} `json:"metadata"`
			} `json:"nodes"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, n := range doc.Graph.Nodes {
		if n.Metadata.Type == "offer" && n.Metadata.Properties["machine_type"] != "" {
			out[n.Metadata.Properties["machine_type"]] = n.Metadata.Properties["topology"]
		}
	}
	return out, nil
}

func zoneSuffix(m *Match) string {
	if m.Zone == "" {
		return ""
	}
	return " (" + strings.TrimPrefix(m.Zone, "us-central1-") + ")"
}

func countPhase(jobs []Job, phase string) int {
	n := 0
	for _, j := range jobs {
		if j.Phase == phase {
			n++
		}
	}
	return n
}

// ablate removes vertices of the given types and the edges to them.
func ablate(args []string) error {
	fs := flag.NewFlagSet("ablate", flag.ExitOnError)
	in := fs.String("in", "", "input JGF")
	out := fs.String("out", "", "output JGF")
	drop := fs.String("drop", "", "comma-separated vertex types to remove")
	fs.Parse(args)
	if *in == "" || *out == "" || *drop == "" {
		return fmt.Errorf("--in, --out and --drop are required")
	}
	types := map[string]bool{}
	for _, t := range strings.Split(*drop, ",") {
		types[t] = true
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var doc struct {
		Graph struct {
			Nodes []json.RawMessage `json:"nodes"`
			Edges []json.RawMessage `json:"edges"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	gone := map[string]bool{}
	var nodes []json.RawMessage
	for _, n := range doc.Graph.Nodes {
		var v struct {
			ID       string `json:"id"`
			Metadata struct {
				Type string `json:"type"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(n, &v); err != nil {
			return err
		}
		if types[v.Metadata.Type] {
			gone[v.ID] = true
			continue
		}
		nodes = append(nodes, n)
	}
	var edges []json.RawMessage
	for _, e := range doc.Graph.Edges {
		var v struct {
			Source string `json:"source"`
			Target string `json:"target"`
		}
		if err := json.Unmarshal(e, &v); err != nil {
			return err
		}
		if gone[v.Source] || gone[v.Target] {
			continue
		}
		edges = append(edges, e)
	}
	outDoc := map[string]any{"graph": map[string]any{"nodes": nodes, "edges": edges}}
	data, err = json.Marshal(outDoc)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ablate: dropped %d vertices of %s; %d vertices, %d edges remain\n", len(gone), *drop, len(nodes), len(edges))
	return os.WriteFile(*out, data, 0o644)
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: fidelity report|ablate ...")
	}
	var err error
	switch os.Args[1] {
	case "report":
		err = report(os.Args[2:])
	case "ablate":
		err = ablate(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
}
