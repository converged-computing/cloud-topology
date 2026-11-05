// Command fluxion-go matches jobspecs against a JGF graph in-process with Fluxion's Go bindings.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/flux-framework/flux-sched/resource/reapi/bindings/go/src/fluxcli"
)

func main() {
	graphPath := flag.String("graph", "", "JGF graph file")
	jobspecPath := flag.String("jobspec", "", "jobspec YAML file")
	count := flag.Int("count", 1, "number of times to match the jobspec")
	reserve := flag.Bool("reserve", false, "reserve in the future when resources are busy now")
	flag.Parse()

	graph, err := os.ReadFile(*graphPath)
	if err != nil {
		log.Fatal(err)
	}
	jobspec, err := os.ReadFile(*jobspecPath)
	if err != nil {
		log.Fatal(err)
	}

	cli := fluxcli.NewReapiClient()
	defer cli.Destroy()
	if err := cli.InitContext(string(graph), `{"load_format": "jgf", "matcher_policy": "first"}`); err != nil {
		log.Fatalf("loading graph: %v: %s", err, cli.GetErrMsg())
	}

	for i := 0; i < *count; i++ {
		reserved, allocated, at, _, jobid, err := cli.MatchAllocate(*reserve, string(jobspec))
		switch {
		case err != nil:
			fmt.Printf("match %d: no match (%s)\n", i+1, cli.GetErrMsg())
			cli.ClearErrMsg()
		case reserved:
			fmt.Printf("match %d: job %d reserved at t=%d\n", i+1, jobid, at)
		default:
			fmt.Printf("match %d: job %d allocated now (%d bytes of R)\n", i+1, jobid, len(allocated))
		}
	}
}
