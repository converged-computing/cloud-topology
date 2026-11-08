// Command prices writes hourly On-Demand and Spot prices for a region's machine types from
// the Cloud Billing Catalog (read-only; application default credentials).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
	"github.com/converged-computing/google-cloud-topology/pkg/prices"
)

func main() {
	dataDir := flag.String("data", "data", "database directory (its machine types are priced)")
	region := flag.String("region", "us-central1", "region")
	out := flag.String("out", "", "output file")
	flag.Parse()
	if *out == "" {
		log.Fatal("--out is required")
	}
	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	skus, err := prices.Fetch(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	p := prices.Build(skus, *region, db.Query(database.Filter{Region: *region}))
	if err := database.WriteJSON(*out, p); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d on-demand and %d spot prices for %s to %s (%d types unpriced)\n", len(p.OnDemand), len(p.Spot), *region, *out, len(p.Unpriced))
}
