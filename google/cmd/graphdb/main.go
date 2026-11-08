// Command graphdb puts the folded resource graphs into a graph database for Cypher
// queries: export writes Parquet tables and a schema, load builds a LadybugDB database
// from them with the lbug shell, and query runs Cypher against it.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/graphdb"
	"github.com/converged-computing/google-cloud-topology/pkg/quota"
	"github.com/converged-computing/google-cloud-topology/pkg/site"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: graphdb export|load|query [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "export":
		export(os.Args[2:])
	case "load":
		load(os.Args[2:])
	case "query":
		query(os.Args[2:])
	default:
		log.Fatalf("unknown subcommand %q", os.Args[1])
	}
}

func export(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var data, regions, folded list
	fs.Var(&data, "data", "database directory, as label=dir or dir (repeatable)")
	fs.Var(&folded, "folded", "folded graph file from another provider (aws/cmd/graph --fold; repeatable)")
	fs.Var(&regions, "region", "region to build graphs for (repeatable; default every region in the databases)")
	quotaFile := fs.String("quota", "", "quota snapshot to size offers and storage by quota")
	levels := fs.String("levels", "exact", "comma-separated graph levels to export")
	out := fs.String("out", "graphdb", "output directory for the Parquet tables and schema")
	provider := fs.String("provider", "google-cloud", "cloud provider label, as in the graph's provider vertex")
	fs.Parse(args)
	if len(data) == 0 && len(folded) == 0 {
		log.Fatal("--data or --folded is required")
	}
	opts := site.Options{Nodes: 1}
	if *quotaFile != "" {
		snap, err := quota.Load(*quotaFile)
		if err != nil {
			log.Fatal(err)
		}
		opts.Quota = snap
	}
	var graphs []graphdb.Graph
	for _, d := range data {
		label, dir, ok := strings.Cut(d, "=")
		if !ok {
			label, dir = filepath.Base(filepath.Clean(d)), d
		}
		db, err := database.Load(dir)
		if err != nil {
			log.Fatal(err)
		}
		regs := regions
		if len(regs) == 0 {
			seen := map[string]bool{}
			for _, mt := range db.MachineTypes {
				for _, r := range mt.Regions() {
					if !seen[r] {
						seen[r] = true
						regs = append(regs, r)
					}
				}
			}
		}
		for _, region := range regs {
			for _, level := range strings.Split(*levels, ",") {
				tree, err := site.FoldedGraph(db, level, region, opts)
				if err != nil {
					log.Fatal(err)
				}
				if tree == nil {
					continue
				}
				graphs = append(graphs, graphdb.Graph{Database: label, Level: level, Region: region, Provider: *provider, Tree: tree})
				fmt.Fprintf(os.Stderr, "%s: %s graph for %s, %d vertices\n", label, level, region, tree.Vertices)
			}
		}
	}
	for _, path := range folded {
		g, err := graphdb.ReadFolded(path)
		if err != nil {
			log.Fatal(err)
		}
		graphs = append(graphs, g)
		fmt.Fprintf(os.Stderr, "%s (%s): %s graph for %s, %d vertices\n", g.Database, g.Provider, g.Level, g.Region, g.Tree.Vertices)
	}
	vs, es, ss, err := graphdb.Export(*out, graphs)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "schema.cypher"), []byte(graphdb.Schema), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d vertex rows, %d edges, %d shapes (for %d graphs)\n", *out, vs, es, ss, len(graphs))
}

func load(args []string) {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	in := fs.String("in", "graphdb", "directory with the exported tables")
	dbPath := fs.String("db", "graphdb/graph.lbug", "database to create")
	shell := fs.String("shell", "lbug", "LadybugDB shell")
	fs.Parse(args)
	if err := os.RemoveAll(*dbPath); err != nil {
		log.Fatal(err)
	}
	abs, err := filepath.Abs(*in)
	if err != nil {
		log.Fatal(err)
	}
	script := graphdb.Schema + graphdb.LoadScript(abs)
	out, err := run(*shell, *dbPath, script, "csv")
	if err != nil || strings.Contains(out, "Error:") {
		log.Fatalf("load failed: %v\n%s", err, out)
	}
	fmt.Printf("loaded %s into %s\n", *in, *dbPath)
}

func query(args []string) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	dbPath := fs.String("db", "graphdb/graph.lbug", "database")
	file := fs.String("file", "", "Cypher file to run (default: the query given as arguments, or stdin)")
	named := fs.String("named", "", "a shipped query to run: "+strings.Join(queryNames(), ", "))
	mode := fs.String("mode", "box", "output mode: box, csv, tsv, json, jsonlines, markdown, table")
	shell := fs.String("shell", "lbug", "LadybugDB shell")
	fs.Parse(args)
	var script string
	switch {
	case *named != "":
		q, ok := site.Queries[*named]
		if !ok {
			log.Fatalf("no query %q; have %s", *named, strings.Join(queryNames(), ", "))
		}
		script = q
	case *file != "":
		data, err := os.ReadFile(*file)
		if err != nil {
			log.Fatal(err)
		}
		script = string(data)
	case len(fs.Args()) > 0:
		script = strings.Join(fs.Args(), " ")
	default:
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		script = string(data)
	}
	if !strings.HasSuffix(strings.TrimSpace(script), ";") {
		script += ";"
	}
	out, err := run(*shell, *dbPath, script, *mode)
	fmt.Print(out)
	if err != nil {
		log.Fatal(err)
	}
}

func queryNames() []string {
	var names []string
	for n := range site.Queries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// run feeds a Cypher script to the shell and returns its output.
func run(shell, dbPath, script, mode string) (string, error) {
	cmd := exec.Command(shell, dbPath, "--mode", mode, "--no_progress_bar", "--no_stats")
	cmd.Stdin = strings.NewReader(script + "\n")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
