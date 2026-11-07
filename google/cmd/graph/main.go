// Command graph builds Fluxion JGF resource graphs from the static database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
)

func main() {
	dataDir := flag.String("data", "data", "database directory")
	level := flag.String("level", "coarse", "graph level: series (totals per machine series), coarse (per machine type) or exact (secretary)")
	series := flag.String("series", "", "comma-separated machine series for --level series, e.g. n2-standard,g2-standard (default all)")
	region := flag.String("region", "", "region to model, e.g. us-central1")
	machineTypes := flag.String("machine-types", "", "comma-separated machine types (default all in the region)")
	nodes := flag.Int("nodes", 1, "potential nodes per offer when no quota snapshot is given")
	quotaFile := flag.String("quota", "", "quota snapshot (from quota fetch) to size offers by quota")
	maxNodes := flag.Int("max-nodes", 64, "cap on nodes per offer when sizing by quota")
	placement := flag.Int("placement-distance", 0, "compact placement max distance sizing the exact graph's placement groups: 0 unspecified, 1, 2 or 3")
	models := flag.String("models", "on-demand", "comma-separated provisioning models to create offers for")
	capturedOnly := flag.Bool("captured-only", false, "exact graph: leave out machine types whose series has no capture")
	out := flag.String("out", "", "output file (default stdout)")
	flag.Parse()

	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	opts := jgf.Options{Region: *region, Nodes: *nodes, MachineTypes: split(*machineTypes), Models: split(*models), MaxNodes: *maxNodes, PlacementDistance: *placement, CapturedOnly: *capturedOnly}
	if *quotaFile != "" {
		snap, err := quota.Load(*quotaFile)
		if err != nil {
			log.Fatal(err)
		}
		opts.Capacity = snap.NodesFunc(*region, func(msg string) { fmt.Fprintln(os.Stderr, msg) })
		opts.Storage = snap.StorageFunc(*region)
	}

	var g *jgf.Graph
	switch *level {
	case "series":
		sopts := jgf.SeriesOptions{Region: *region, Series: split(*series), Models: split(*models), Nodes: *nodes}
		if *quotaFile != "" {
			snap, err := quota.Load(*quotaFile)
			if err != nil {
				log.Fatal(err)
			}
			sopts.Units = snap.UnitsFunc(*region, func(msg string) { fmt.Fprintln(os.Stderr, msg) })
		}
		g, err = jgf.BuildSeries(db, sopts)
	case "coarse":
		g, err = jgf.BuildCoarse(db, opts)
	case "exact":
		g, err = jgf.BuildExact(db, opts, jgf.HwlocLoader(db))
	default:
		err = fmt.Errorf("unknown level %q", *level)
	}
	if err != nil {
		log.Fatal(err)
	}

	data, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		log.Fatal(err)
	}
	data = append(data, '\n')
	if *out == "" {
		os.Stdout.Write(data)
		return
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d vertices to %s\n", len(g.Nodes), *out)
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
