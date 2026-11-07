// Command report compares the arms of a capture experiment (experiments/scripts/experiment.sh): which
// planned machine types each arm captured, how long it took, and what either arm got.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/converged-computing/google-cloud-topology/experiments/pkg/report"
	"github.com/converged-computing/google-cloud-topology/pkg/plan"
)

func main() {
	planFile := flag.String("plan", "", "plan file the arms ran (from make plan-topology)")
	dir := flag.String("dir", "", "experiment directory with an agent/ and deterministic/ database, deterministic-captures.log and phases.tsv")
	format := flag.String("format", "markdown", "markdown or json")
	flag.Parse()
	if *planFile == "" || *dir == "" {
		log.Fatal("--plan and --dir are required")
	}
	f, err := os.Open(*planFile)
	if err != nil {
		log.Fatal(err)
	}
	captures, err := plan.Parse(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	r, err := report.Build(*planFile, captures, *dir)
	if err != nil {
		log.Fatal(err)
	}
	switch *format {
	case "json":
		data, err := r.JSON()
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(append(data, '\n'))
	case "markdown":
		fmt.Print(r.Markdown())
	default:
		log.Fatalf("unknown format %q", *format)
	}
}
