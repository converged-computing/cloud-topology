// Command site exports databases, captures and Fluxion graphs as a static web site for
// browsing, searching and comparing them without a server (for example on GitHub Pages).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/site"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	var data, experiments, regions list
	out := flag.String("out", "site", "output directory")
	flag.Var(&data, "data", "database directory, as label=dir or dir (repeatable)")
	flag.Var(&experiments, "experiment", "experiment directory from topology/experiment.sh, as name=dir or dir; its arms are added as databases (repeatable)")
	var kubernetes list
	flag.Var(&kubernetes, "kubernetes", "Kubernetes run directory from experiments/scripts/gke.sh, as name=dir or dir; its gke/ database and reports are added (repeatable)")
	var graphData list
	flag.Var(&graphData, "graph-data", "database label to build graphs for (repeatable; default every database)")
	flag.Var(&regions, "region", "region to build graphs for (repeatable; default every region in the databases)")
	quotaFile := flag.String("quota", "", "quota snapshot (from quota fetch) to size graph offers by quota")
	consistency := flag.String("consistency", "", "consistency study directory from topology/consistency.sh; its rounds are added as databases")
	levels := flag.String("levels", "series,coarse,exact", "comma-separated graph levels to build, or none")
	nodes := flag.Int("nodes", 1, "potential nodes per offer without a quota snapshot")
	single := flag.Bool("single", false, "write one self-contained index.html with the data and images inlined")
	flag.Parse()

	opts := site.Options{GraphSources: graphData, Regions: regions, Experiments: experiments, Kubernetes: kubernetes, Consistency: *consistency, Nodes: *nodes, Single: *single, Log: func(s string) { fmt.Fprintln(os.Stderr, s) }}
	for _, d := range data {
		label, dir, ok := strings.Cut(d, "=")
		if !ok {
			dir = d
			if label = filepath.Base(filepath.Clean(d)); label == "." || label == "/" {
				label = "data"
			}
		}
		opts.Sources = append(opts.Sources, site.Source{Label: label, Dir: dir})
	}
	if *levels != "none" && *levels != "" {
		opts.Levels = strings.Split(*levels, ",")
	} else {
		opts.Levels = []string{}
	}
	if *quotaFile != "" {
		snap, err := quota.Load(*quotaFile)
		if err != nil {
			log.Fatal(err)
		}
		opts.Quota = snap
	}
	m, err := site.Export(*out, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d databases, %d graphs, %d experiments\n", *out, len(m.Databases), len(m.Graphs), len(m.Experiments))
}
