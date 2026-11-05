// Command consistency compares every capture of each machine type across databases: the
// rounds of a consistency study (topology/consistency.sh) and any other databases given,
// reporting which machine types captured the same hardware every time and which did not.
package main

import (
	encjson "encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/converged-computing/google-cloud-topology/pkg/site"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func main() {
	var data list
	study := flag.String("study", "", "consistency study directory with rounds/<round>/ databases")
	flag.Var(&data, "data", "another database to include, as label=dir or dir (repeatable)")
	format := flag.String("format", "markdown", "markdown or json")
	flag.Parse()
	var sources []site.Source
	if *study != "" {
		rounds, err := site.RoundSources(*study)
		if err != nil {
			log.Fatal(err)
		}
		sources = append(sources, rounds...)
	}
	for _, d := range data {
		label, dir, ok := strings.Cut(d, "=")
		if !ok {
			label, dir = filepath.Base(filepath.Clean(d)), d
		}
		sources = append(sources, site.Source{Label: label, Dir: dir})
	}
	if len(sources) == 0 {
		log.Fatal("--study or --data is required")
	}
	r, err := site.Consistency(sources)
	if err != nil {
		log.Fatal(err)
	}
	if *format == "json" {
		data, err := json(r)
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(data)
		return
	}
	fmt.Print(r.Markdown())
}

func json(r *site.ConsistencyReport) ([]byte, error) {
	data, err := encjson.MarshalIndent(r, "", "  ")
	return append(data, '\n'), err
}
