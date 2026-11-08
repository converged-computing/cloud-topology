// Command prices writes the hourly On-Demand and Spot prices of a region's instance types.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/prices"
)

func main() {
	dataDir := flag.String("data", "data", "database directory (its instance types are priced)")
	region := flag.String("region", "us-east-1", "region")
	out := flag.String("out", "", "output file (default projects/<account>/prices.json next to --quota's directory)")
	project := flag.String("project", "", "projects/<project>/ to write into when --out is not given")
	flag.Parse()
	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	var names []string
	for _, mt := range db.Query(database.Filter{Region: *region}) {
		names = append(names, mt.Name)
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	p, err := prices.Fetch(ctx, cfg, *region, names)
	if err != nil {
		log.Fatal(err)
	}
	if *out == "" {
		if *project == "" {
			log.Fatal("--out or --project is required")
		}
		*out = filepath.Join("projects", *project, "prices.json")
	}
	if err := database.WriteJSON(*out, p); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d on-demand and %d spot prices for %s to %s\n", len(p.OnDemand), len(p.Spot), *region, *out)
}
