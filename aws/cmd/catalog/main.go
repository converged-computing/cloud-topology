// Command catalog exports EC2 instance types and EBS volume types into the static database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/catalog"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/storage"
)

func main() {
	region := flag.String("region", "", "region to export, e.g. us-east-1 (default the AWS CLI's)")
	families := flag.String("families", "", "comma-separated instance families to keep, e.g. c7i,p5 (default all)")
	dataDir := flag.String("data", "data", "database directory")
	previous := flag.Bool("include-previous", false, "keep previous-generation instance types")
	indexOnly := flag.Bool("index-only", false, "only rebuild index.json from files already in the database")
	flag.Parse()

	if *indexOnly {
		writeIndex(*dataDir)
		return
	}
	ctx := context.Background()
	var loadOpts []func(*config.LoadOptions) error
	if *region != "" {
		loadOpts = append(loadOpts, config.WithRegion(*region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Region == "" {
		log.Fatal("--region or a default region in the AWS config is required")
	}
	raw, err := catalog.Fetch(ctx, ec2.NewFromConfig(cfg), cfg.Region)
	if err != nil {
		log.Fatal(err)
	}
	machineTypes := catalog.Convert(raw, catalog.Options{Families: split(*families), IncludePrevious: *previous})
	for _, mt := range machineTypes {
		if err := database.WriteMachineType(*dataDir, mt); err != nil {
			log.Fatal(err)
		}
	}
	// EBS volume types are regional: every availability zone that offers an instance
	// type offers every volume type.
	zones := map[string]bool{}
	for _, mt := range machineTypes {
		for _, z := range mt.Zones {
			zones[z] = true
		}
	}
	for _, dt := range storage.VolumeTypes {
		for z := range zones {
			dt.Zones = append(dt.Zones, z)
		}
		if err := database.WriteDiskType(*dataDir, dt); err != nil {
			log.Fatal(err)
		}
	}
	if err := database.WriteZones(*dataDir, raw.ZoneIDs); err != nil {
		log.Fatal(err)
	}
	db := writeIndex(*dataDir)
	fmt.Printf("wrote %d instance types and %d volume types for %s to %s (database now has %d and %d)\n",
		len(machineTypes), len(storage.VolumeTypes), cfg.Region, *dataDir, len(db.MachineTypes), len(db.DiskTypes))
}

func writeIndex(dir string) *database.Database {
	db, err := database.Load(dir)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.WriteIndex(time.Now()); err != nil {
		log.Fatal(err)
	}
	return db
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
