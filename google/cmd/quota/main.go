// Command quota fetches a project's Compute Engine quotas and plans what the project
// can run: node counts per machine type and topology captures still missing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/plan"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/sizing"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: quota fetch|plan|sizes [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fetch":
		fetch(os.Args[2:])
	case "plan":
		planCmd(os.Args[2:])
	case "sizes":
		sizes(os.Args[2:])
	default:
		log.Fatalf("unknown subcommand %q", os.Args[1])
	}
}

func fetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	project := fs.String("project", os.Getenv("GOOGLE_CLOUD_PROJECT"), "Google Cloud project (default $GOOGLE_CLOUD_PROJECT)")
	regions := fs.String("regions", "", "comma-separated regions, e.g. us-central1")
	out := fs.String("out", "", "output file (default projects/<project>/quota.json)")
	fs.Parse(args)
	if *project == "" || *regions == "" {
		log.Fatal("--project and --regions are required")
	}
	if *out == "" {
		*out = filepath.Join("projects", *project, "quota.json")
	}
	snap, err := quota.Fetch(context.Background(), *project, strings.Split(*regions, ","))
	if err != nil {
		log.Fatal(err)
	}
	if err := database.WriteJSON(*out, snap); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d quotas to %s\n", len(snap.Quotas), *out)
}

func planCmd(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	dataDir := fs.String("data", "data", "database directory")
	quotaFile := fs.String("quota", "", "quota snapshot from quota fetch")
	region := fs.String("region", "", "region to plan for")
	models := fs.String("models", "on-demand", "comma-separated provisioning models")
	format := fs.String("format", "table", "table, json, or commands (topology captures still missing)")
	all := fs.Bool("all", false, "include machine types with no capacity")
	verify := fs.Bool("verify", false, "with --format commands, also capture each series' smallest runnable size, to check derived topologies")
	metal := fs.Bool("metal", false, "with --format commands, include bare-metal series")
	sharedCore := fs.Bool("shared-core", false, "with --format commands, include shared-core machine types")
	onDemand := fs.Bool("on-demand", false, "with --format commands, capture on-demand instead of Spot")
	recapture := fs.Bool("recapture", false, "with --format commands, plan series that are already captured too")
	fs.Parse(args)
	if *quotaFile == "" || *region == "" {
		log.Fatal("--quota and --region are required")
	}

	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	snap, err := quota.Load(*quotaFile)
	if err != nil {
		log.Fatal(err)
	}

	var results []quota.Capacity
	for _, mt := range db.Query(database.Filter{Region: *region}) {
		for _, model := range strings.Split(*models, ",") {
			c := quota.Compute(snap, mt, model, *region)
			if *all || c.Nodes != 0 {
				results = append(results, c)
			}
		}
	}

	popts := plan.Options{Region: *region, Verify: *verify, Metal: *metal, SharedCore: *sharedCore, OnDemand: *onDemand, Recapture: *recapture}
	report := plan.Check(db, snap, popts)
	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"capacity": results, "captures": plan.Captures(db, snap, popts), "report": report})
	case "commands":
		fmt.Println("# Topology captures: the largest on-demand size quota allows in each series,")
		fmt.Println("# cheapest first, on Spot where quota allows. Each creates and deletes one instance.")
		for _, line := range report.Lines() {
			fmt.Println("# " + line)
		}
		for _, c := range plan.Captures(db, snap, popts) {
			fmt.Println(c.Command())
		}
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "MACHINE TYPE\tMODEL\tNODES\tLIMITED BY\tTOPOLOGY\tNOTE")
		for _, c := range results {
			nodes := fmt.Sprint(c.Nodes)
			switch {
			case !c.Known:
				nodes = "?"
			case c.Nodes < 0:
				nodes = "unlimited"
			case !c.UsageKnown:
				nodes = "<=" + nodes
			}
			topo := "-"
			if _, ok := db.Captures[c.MachineType]; ok {
				topo = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.MachineType, c.Model, nodes, c.Binding, topo, c.Note)
		}
		w.Flush()
		if lines := report.Lines(); len(lines) > 0 {
			fmt.Println()
			for _, line := range lines {
				fmt.Println(line)
			}
		}
	}
}

// sizes lists machine types and node counts in a series that satisfy a request for
// total resources within quota, for a lower-level agent to choose from.
func sizes(args []string) {
	fs := flag.NewFlagSet("sizes", flag.ExitOnError)
	dataDir := fs.String("data", "data", "database directory")
	quotaFile := fs.String("quota", "", "quota snapshot (optional; without it quota is not checked)")
	region := fs.String("region", "", "region")
	series := fs.String("series", "", "machine series, e.g. n2-standard")
	model := fs.String("model", "on-demand", "provisioning model")
	var req sizing.Request
	fs.IntVar(&req.VCPUs, "vcpus", 0, "total vCPUs needed")
	fs.IntVar(&req.GPUs, "gpus", 0, "total GPUs needed")
	fs.IntVar(&req.MemoryGB, "memory-gb", 0, "total memory needed in GB")
	fs.IntVar(&req.MaxNodes, "max-nodes", 0, "reject options needing more nodes (0 for no limit)")
	fs.Parse(args)
	if *region == "" || *series == "" {
		log.Fatal("--region and --series are required")
	}
	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	members := database.Series(db.Query(database.Filter{Region: *region}))[*series]
	if len(members) == 0 {
		log.Fatalf("no machine types in series %s in %s", *series, *region)
	}
	lim := sizing.Limits{VCPUs: -1, GPUs: -1}
	if *quotaFile != "" {
		snap, err := quota.Load(*quotaFile)
		if err != nil {
			log.Fatal(err)
		}
		u := quota.SeriesUnits(snap, *series, members, *model, *region)
		lim = sizing.Limits{VCPUs: u.VCPUs, GPUs: u.GPUs}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "# quota limits: vcpus=%d gpus=%d (-1 = not limited)\n", lim.VCPUs, lim.GPUs)
	fmt.Fprintln(w, "MACHINE TYPE\tCOUNT\tVCPUS\tGPUS\tMEMORY GB\tEXCESS VCPUS")
	for _, o := range sizing.Options(members, req, lim) {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\n", o.MachineType, o.Count, o.VCPUs, o.GPUs, o.MemoryGB, o.ExcessVCPUs)
	}
	w.Flush()
}
