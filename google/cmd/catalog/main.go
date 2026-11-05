// Command catalog exports Compute Engine machine types into the static database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/converged-computing/google-cloud-topology/pkg/catalog"
	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

func main() {
	project := flag.String("project", os.Getenv("GOOGLE_CLOUD_PROJECT"), "Google Cloud project to query (default $GOOGLE_CLOUD_PROJECT)")
	regions := flag.String("regions", "", "comma-separated regions to keep, e.g. us-central1 (default all)")
	families := flag.String("families", "", "comma-separated machine families to keep, e.g. a3,g2,c3 (default all)")
	dataDir := flag.String("data", "data", "database directory")
	deprecated := flag.Bool("include-deprecated", false, "keep machine types marked deprecated")
	indexOnly := flag.Bool("index-only", false, "only rebuild index.json from files already in the database")
	flag.Parse()

	if *indexOnly {
		writeIndex(*dataDir)
		return
	}
	if *project == "" {
		log.Fatal("--project or GOOGLE_CLOUD_PROJECT is required")
	}

	ctx := context.Background()
	pairs, err := catalog.Fetch(ctx, *project)
	if err != nil {
		log.Fatal(err)
	}
	machineTypes := catalog.Convert(pairs, catalog.Options{
		Regions:           split(*regions),
		Families:          split(*families),
		IncludeDeprecated: *deprecated,
	})
	for _, mt := range machineTypes {
		if err := database.WriteMachineType(*dataDir, mt); err != nil {
			log.Fatal(err)
		}
	}
	diskPairs, err := catalog.FetchDiskTypes(ctx, *project)
	if err != nil {
		log.Fatal(err)
	}
	diskTypes := catalog.ConvertDiskTypes(diskPairs, catalog.Options{Regions: split(*regions), IncludeDeprecated: *deprecated})
	for _, dt := range diskTypes {
		if err := database.WriteDiskType(*dataDir, dt); err != nil {
			log.Fatal(err)
		}
	}

	db := writeIndex(*dataDir)
	fmt.Printf("wrote %d machine types and %d disk types to %s (database now has %d and %d)\n", len(machineTypes), len(diskTypes), *dataDir, len(db.MachineTypes), len(db.DiskTypes))
}

// writeIndex reloads the database and rewrites index.json.
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
