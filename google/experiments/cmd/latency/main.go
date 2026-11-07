// Command latency compares provisioning time across databases, one per provisioning
// path: how long each machine type took to come up as a VM against a GKE node pool, with
// the fixed costs a path adds, such as creating the cluster.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/converged-computing/google-cloud-topology/experiments/pkg/latency"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	var data list
	flag.Var(&data, "data", "database to compare, as label=dir or dir (repeatable)")
	phases := flag.String("phases", "", "phases.tsv with fixed costs named <label>/<phase> (name, start, end, seconds, exit)")
	format := flag.String("format", "markdown", "markdown or json")
	flag.Parse()
	var sources []latency.Source
	for _, d := range data {
		label, dir, ok := strings.Cut(d, "=")
		if !ok {
			label, dir = filepath.Base(filepath.Clean(d)), d
		}
		sources = append(sources, latency.Source{Label: label, Dir: dir})
	}
	if len(sources) == 0 {
		log.Fatal("--data is required")
	}
	r, err := latency.Compare(sources, *phases)
	if err != nil {
		log.Fatal(err)
	}
	if *format == "json" {
		out, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(append(out, '\n'))
		return
	}
	fmt.Print(r.Markdown())
}
