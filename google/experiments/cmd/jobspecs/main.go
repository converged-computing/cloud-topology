// Command jobspecs generates the scheduling-fidelity workload: one Fluxion jobspec per job
// per graph level, since each level needs the containment hierarchy spelled to its depth
// (series: offer > slot; coarse: offer > node > slot; exact: zone > offer > placement >
// node > slot > socket). The workload is deterministic from the seed.
//
// Usage: jobspecs --out DIR [--seed N] [--stream N]
//
// Writes DIR/jobs.tsv (one job per line: id, phase, family, sizes, and the levels the job
// exists for) and DIR/<level>/<id>.yaml.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Job is one request. Zero fields are absent from the jobspec.
type Job struct {
	ID       string
	Phase    string // single (matched then cancelled) or stream (matched and kept)
	Family   string
	Cores    int
	MemoryGB int
	GPUs     int
	Nodes    int
	Sockets  int // cores, GPUs and memory are split evenly across this many sockets (exact)
	NICGbps  int
	LocalSSD int
	PDSSDGB  int
}

// Levels a job can be expressed at: the series graph has no node, NIC, local SSD or
// storage pool vertices.
func (j Job) Levels() []string {
	if j.NICGbps > 0 || j.LocalSSD > 0 || j.PDSSDGB > 0 {
		return []string{"coarse", "exact"}
	}
	return []string{"series", "coarse", "exact"}
}

// Describe is the human reading of the request.
func (j Job) Describe() string {
	var parts []string
	if j.Nodes > 1 {
		parts = append(parts, fmt.Sprintf("%d nodes, each", j.Nodes))
	}
	parts = append(parts, fmt.Sprintf("%d cores", j.Cores))
	if j.GPUs > 0 {
		parts = append(parts, fmt.Sprintf("%d GPUs", j.GPUs))
	}
	if j.MemoryGB > 0 {
		parts = append(parts, fmt.Sprintf("%d GB memory", j.MemoryGB))
	}
	if j.Sockets > 1 {
		parts = append(parts, fmt.Sprintf("across %d sockets", j.Sockets))
	} else if j.GPUs > 0 || j.Cores >= 64 {
		parts = append(parts, "on one socket")
	}
	if j.NICGbps > 0 {
		parts = append(parts, fmt.Sprintf("a %d Gbps NIC", j.NICGbps))
	}
	if j.LocalSSD > 0 {
		parts = append(parts, fmt.Sprintf("%d local SSD", j.LocalSSD))
	}
	if j.PDSSDGB > 0 {
		parts = append(parts, fmt.Sprintf("%d GB pd-ssd", j.PDSSDGB))
	}
	return strings.Join(parts, ", ")
}

type res struct {
	Type  string
	Count int
	Label string
	With  []res
}

func render(r res, indent int, b *strings.Builder) {
	pad := strings.Repeat(" ", indent)
	fmt.Fprintf(b, "%s- type: %s\n%s  count: %d\n", pad, r.Type, pad, r.Count)
	if r.Label != "" {
		fmt.Fprintf(b, "%s  label: %s\n", pad, r.Label)
	}
	if len(r.With) > 0 {
		fmt.Fprintf(b, "%s  with:\n", pad)
		for _, c := range r.With {
			render(c, indent+4, b)
		}
	}
}

// Spec renders the job for a level, or "" when the level cannot express it.
func Spec(j Job, level string) string {
	nodes, sockets := max(j.Nodes, 1), max(j.Sockets, 1)
	var root res
	switch level {
	case "series":
		if len(j.Levels()) < 3 {
			return ""
		}
		leaves := []res{{Type: "core", Count: j.Cores}}
		if j.GPUs > 0 {
			leaves = append(leaves, res{Type: "gpu", Count: j.GPUs})
		}
		if j.MemoryGB > 0 {
			leaves = append(leaves, res{Type: "memory", Count: j.MemoryGB})
		}
		slot := res{Type: "slot", Count: nodes, Label: "default", With: leaves}
		root = res{Type: "site", Count: 1, With: []res{{Type: "provider", Count: 1, With: []res{{Type: "region", Count: 1, With: []res{{Type: "offer", Count: 1, With: []res{slot}}}}}}}}
	case "coarse":
		leaves := []res{}
		if j.GPUs > 0 {
			leaves = append(leaves, res{Type: "gpu", Count: j.GPUs})
		}
		leaves = append(leaves, res{Type: "core", Count: j.Cores})
		if j.MemoryGB > 0 {
			leaves = append(leaves, res{Type: "memory", Count: j.MemoryGB})
		}
		if j.NICGbps > 0 {
			leaves = append(leaves, res{Type: "nic", Count: j.NICGbps})
		}
		if j.LocalSSD > 0 {
			leaves = append(leaves, res{Type: "local_ssd", Count: j.LocalSSD})
		}
		node := res{Type: "node", Count: nodes, With: []res{{Type: "slot", Count: 1, Label: "default", With: leaves}}}
		offer := []res{}
		if j.PDSSDGB > 0 {
			offer = append(offer, res{Type: "pd_ssd", Count: j.PDSSDGB})
		}
		offer = append(offer, node)
		root = res{Type: "site", Count: 1, With: []res{{Type: "provider", Count: 1, With: []res{{Type: "region", Count: 1, With: []res{{Type: "offer", Count: 1, With: offer}}}}}}}
	case "exact":
		per := []res{{Type: "core", Count: j.Cores / sockets}}
		if j.GPUs > 0 {
			per = append(per, res{Type: "gpu", Count: j.GPUs / sockets})
		}
		if j.MemoryGB > 0 {
			per = append(per, res{Type: "memory", Count: j.MemoryGB / sockets})
		}
		inSlot := []res{{Type: "socket", Count: sockets, With: per}}
		if j.NICGbps > 0 {
			inSlot = append(inSlot, res{Type: "nic", Count: j.NICGbps})
		}
		if j.LocalSSD > 0 {
			inSlot = append(inSlot, res{Type: "local_ssd", Count: j.LocalSSD})
		}
		node := res{Type: "node", Count: nodes, With: []res{{Type: "slot", Count: 1, Label: "default", With: inSlot}}}
		offer := []res{}
		if j.PDSSDGB > 0 {
			offer = append(offer, res{Type: "pd_ssd", Count: j.PDSSDGB})
		}
		offer = append(offer, res{Type: "placement", Count: 1, With: []res{node}})
		root = res{Type: "site", Count: 1, With: []res{{Type: "provider", Count: 1, With: []res{{Type: "region", Count: 1, With: []res{{Type: "zone", Count: 1, With: []res{{Type: "offer", Count: 1, With: offer}}}}}}}}}
	default:
		return ""
	}
	var b strings.Builder
	b.WriteString("version: 9999\nresources:\n")
	render(root, 2, &b)
	b.WriteString("attributes:\n  system:\n    duration: 3600\ntasks:\n  - command: [ \"app\" ]\n    slot: default\n    count:\n      per_slot: 1\n")
	return b.String()
}

// Workload is the single-job families: each size exercises one thing a level may or may
// not be able to see.
func Workload() []Job {
	var jobs []Job
	add := func(family string, j Job) {
		j.Family, j.Phase = family, "single"
		j.ID = fmt.Sprintf("s%03d-%s", len(jobs)+1, family)
		jobs = append(jobs, j)
	}
	for _, c := range []int{1, 4, 8, 16, 32, 64, 96, 176, 360} {
		add("cores", Job{Cores: c})
	}
	for _, c := range []int{64, 96, 176, 360} {
		add("cores-2s", Job{Cores: c, Sockets: 2})
	}
	for _, m := range []int{64, 256, 512, 1024, 2048, 3800} {
		add("memory", Job{Cores: 8, MemoryGB: m})
	}
	for _, g := range []int{1, 2, 4, 8} {
		add("gpu", Job{Cores: 4 * g, GPUs: g})
	}
	add("gpu-2s", Job{Cores: 32, GPUs: 8, Sockets: 2})
	for _, k := range []int{2, 4, 8, 16} {
		add("nodes", Job{Cores: 8, Nodes: k})
	}
	for _, b := range []int{32, 100, 200} {
		add("nic", Job{Cores: 8, NICGbps: b})
	}
	add("lssd", Job{Cores: 8, LocalSSD: 1})
	add("pdssd", Job{Cores: 8, PDSSDGB: 1000})
	return jobs
}

// Stream is n jobs drawn from the families every level can express, in a fixed order.
func Stream(n int, seed int64) []Job {
	r := rand.New(rand.NewSource(seed))
	pick := func(xs []int) int { return xs[r.Intn(len(xs))] }
	var jobs []Job
	for i := 1; i <= n; i++ {
		var j Job
		switch p := r.Float64(); {
		case p < 0.40:
			j = Job{Family: "cores", Cores: pick([]int{1, 4, 8, 16, 32, 64})}
		case p < 0.55:
			j = Job{Family: "memory", Cores: 8, MemoryGB: pick([]int{64, 256, 512, 1024})}
		case p < 0.75:
			g := pick([]int{1, 2, 4, 8})
			j = Job{Family: "gpu", Cores: 4 * g, GPUs: g}
		default:
			j = Job{Family: "nodes", Cores: 8, Nodes: pick([]int{2, 4, 8})}
		}
		j.Phase, j.ID = "stream", fmt.Sprintf("t%03d-%s", i, j.Family)
		jobs = append(jobs, j)
	}
	return jobs
}

func main() {
	out := flag.String("out", "", "output directory")
	seed := flag.Int64("seed", 1, "seed for the stream")
	stream := flag.Int("stream", 200, "jobs in the stream")
	flag.Parse()
	if *out == "" {
		log.Fatal("--out is required")
	}
	jobs := append(Workload(), Stream(*stream, *seed)...)
	var tsv strings.Builder
	tsv.WriteString("id\tphase\tfamily\tcores\tmemory_gb\tgpus\tnodes\tsockets\tnic_gbps\tlocal_ssd\tpd_ssd_gb\tdescription\tlevels\n")
	for _, j := range jobs {
		for _, level := range j.Levels() {
			dir := filepath.Join(*out, level)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, j.ID+".yaml"), []byte(Spec(j, level)), 0o644); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Fprintf(&tsv, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n", j.ID, j.Phase, j.Family, j.Cores, j.MemoryGB, j.GPUs,
			max(j.Nodes, 1), max(j.Sockets, 1), j.NICGbps, j.LocalSSD, j.PDSSDGB, j.Describe(), strings.Join(j.Levels(), ","))
	}
	if err := os.WriteFile(filepath.Join(*out, "jobs.tsv"), []byte(tsv.String()), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "jobspecs: %d jobs (%d single, %d stream) into %s\n", len(jobs), len(jobs)-*stream, *stream, *out)
}
