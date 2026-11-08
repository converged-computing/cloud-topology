// Command graph builds Fluxion JGF resource graphs from the EC2 database, and folds them
// for the graph database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/graphdb"
	"github.com/converged-computing/google-cloud-topology/pkg/site"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/jgf"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/quota"
)

func main() {
	dataDir := flag.String("data", "data", "database directory")
	level := flag.String("level", "coarse", "graph level: series (totals per instance family), coarse (per instance type) or exact (secretary)")
	series := flag.String("series", "", "comma-separated families for --level series, e.g. c7i,p5 (default all)")
	region := flag.String("region", "", "region to model, e.g. us-east-1")
	machineTypes := flag.String("machine-types", "", "comma-separated instance types (default all in the region)")
	nodes := flag.Int("nodes", 1, "potential nodes per offer when no quota snapshot is given")
	quotaFile := flag.String("quota", "", "quota snapshot (from quota fetch) to size offers by quota")
	maxNodes := flag.Int("max-nodes", 64, "cap on nodes per offer when sizing by quota")
	models := flag.String("models", "on-demand", "comma-separated purchasing models to create offers for: on-demand, spot")
	capturedOnly := flag.Bool("captured-only", false, "exact graph: leave out instance types whose series has no capture")
	previous := flag.Bool("include-previous", false, "include previous-generation instance types")
	out := flag.String("out", "", "output file for the JGF (default stdout unless --fold is given)")
	fold := flag.String("fold", "", "write the folded graph for the graph database to this file (label=path, or path)")
	flag.Parse()

	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	warn := func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	var snap *quota.Snapshot
	if *quotaFile != "" {
		if snap, err = quota.Load(*quotaFile); err != nil {
			log.Fatal(err)
		}
	}
	build := func(maxNodes int) (*jgf.Graph, error) {
		opts := jgf.Options{Region: *region, Nodes: *nodes, MachineTypes: split(*machineTypes), Models: split(*models), MaxNodes: maxNodes, CapturedOnly: *capturedOnly, IncludePrevious: *previous}
		if snap != nil {
			opts.Capacity = snap.NodesFunc(*region, warn)
			opts.Storage = snap.StorageFunc(*region)
		}
		switch *level {
		case "series":
			sopts := jgf.SeriesOptions{Region: *region, Series: split(*series), Models: split(*models), Nodes: *nodes, IncludePrevious: *previous}
			if snap != nil {
				sopts.Units = snap.UnitsFunc(*region, warn)
			}
			return jgf.BuildSeries(db, sopts)
		case "coarse":
			return jgf.BuildCoarse(db, opts)
		case "exact":
			return jgf.BuildExact(db, opts, jgf.HwlocLoader(db))
		}
		return nil, fmt.Errorf("unknown level %q", *level)
	}

	if *out != "" || *fold == "" {
		g, err := build(*maxNodes)
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
		} else {
			if err := write(*out, data); err != nil {
				log.Fatal(err)
			}
			fmt.Fprintf(os.Stderr, "wrote %d vertices to %s\n", len(g.Nodes), *out)
		}
	}
	if *fold != "" {
		// Fold from a graph with one node per offer; the fold restores the node count
		// quota allows, so the full graph is never materialized.
		label, path, ok := strings.Cut(*fold, "=")
		if !ok {
			label, path = filepath.Base(filepath.Clean(*dataDir)), *fold
		}
		one := *nodes
		if snap != nil {
			one = 1
		}
		warn = func(string) {}
		saved := *maxNodes
		*maxNodes = 1
		g, err := build(1)
		*maxNodes = saved
		if err != nil {
			log.Fatal(err)
		}
		var tree *site.Tree
		if *level == "series" {
			tree = site.Collapse(g, nil)
		} else {
			tree = site.Collapse(g, func(props map[string]string) int {
				if snap == nil {
					return one
				}
				mt, ok := db.MachineTypes[props["machine_type"]]
				if !ok {
					return 0
				}
				n := snap.NodesFunc(*region, nil)(mt, props["provisioning_model"])
				if n < 0 || n > saved {
					n = saved
				}
				return n
			})
		}
		f := graphdb.Folded{Provider: jgf.Provider, Database: label, Level: *level, Region: *region, Tree: tree}
		data, err := json.Marshal(f)
		if err != nil {
			log.Fatal(err)
		}
		if err := write(path, data); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "folded %s graph for %s: %d vertices, wrote %s\n", *level, *region, tree.Vertices, path)
	}
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
