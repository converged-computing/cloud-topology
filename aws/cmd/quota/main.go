// Command quota fetches an account's EC2 and EBS quotas and plans what the account can
// run: node counts per instance type.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/plan"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/prices"
	"github.com/converged-computing/google-cloud-topology/aws/pkg/quota"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: quota fetch|plan [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fetch":
		fetch(os.Args[2:])
	case "plan":
		planCmd(os.Args[2:])
	default:
		log.Fatalf("unknown subcommand %q", os.Args[1])
	}
}

func fetch(args []string) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	regions := fs.String("regions", "", "comma-separated regions, e.g. us-east-1")
	out := fs.String("out", "", "output file (default projects/<account>/quota.json)")
	fs.Parse(args)
	if *regions == "" {
		log.Fatal("--regions is required")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	snap, err := quota.Fetch(ctx, cfg, strings.Split(*regions, ","))
	if err != nil {
		log.Fatal(err)
	}
	if *out == "" {
		*out = filepath.Join("projects", snap.Account, "quota.json")
	}
	if err := database.WriteJSON(*out, snap); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d quotas for account %s to %s\n", len(snap.Quotas), snap.Account, *out)
}

func planCmd(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	dataDir := fs.String("data", "data", "database directory")
	quotaFile := fs.String("quota", "", "quota snapshot from quota fetch")
	region := fs.String("region", "", "region to plan for")
	models := fs.String("models", "on-demand,spot", "comma-separated purchasing models")
	format := fs.String("format", "table", "table, json, or commands (the capture plan)")
	all := fs.Bool("all", false, "include instance types with no capacity")
	priceFile := fs.String("prices", "", "price table from the prices command, for --format commands")
	priceCap := fs.Float64("price-cap", 0, "with --format commands, leave out types above this many $/h (0: no cap)")
	allowExpensive := fs.String("allow-expensive", "", "comma-separated types to plan despite the cap")
	metal := fs.Bool("metal", false, "with --format commands, include bare-metal series")
	onDemand := fs.Bool("on-demand", false, "with --format commands, capture On-Demand instead of Spot")
	recapture := fs.Bool("recapture", false, "with --format commands, plan series that are already captured too")
	limit := fs.Int("limit-minutes", 30, "with --format commands, minutes after which each instance terminates itself")
	fs.Parse(args)
	if *quotaFile == "" || *region == "" {
		log.Fatal("--quota and --region are required")
	}
	db, err := database.Load(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	snap, err := quota.Load(*quotaFile)
	if err != nil {
		log.Fatal(err)
	}
	var results []quota.Capacity
	for _, mt := range db.Query(database.Filter{Region: *region}) {
		for _, model := range strings.Split(*models, ",") {
			c := quota.Compute(snap, mt, model, *region)
			if *all || c.Nodes != 0 {
				results = append(results, c)
			}
		}
	}
	if *format == "commands" {
		var pr *prices.Prices
		if *priceFile != "" {
			if pr, err = prices.Load(*priceFile); err != nil {
				log.Fatal(err)
			}
		}
		report := plan.Build(db, snap, pr, plan.Options{Region: *region, Metal: *metal, OnDemand: *onDemand, Recapture: *recapture,
			PriceCap: *priceCap, AllowExpensive: split(*allowExpensive), LimitMinutes: *limit})
		fmt.Println("# Topology captures: the largest size quota allows in each series, cheapest first,")
		fmt.Println("# Spot where the type and the Spot quota allow it. Each creates and terminates one instance.")
		for _, line := range report.Lines() {
			fmt.Println("# " + line)
		}
		for _, c := range report.Captures {
			fmt.Printf("%s  # $%.2f/h\n", c.Command(), c.Hourly)
		}
		return
	}
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(results)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "INSTANCE TYPE\tMODEL\tNODES\tLIMITED BY\tTOPOLOGY\tNOTE")
	for _, c := range results {
		nodes := fmt.Sprint(c.Nodes)
		switch {
		case !c.Known:
			nodes = "?"
		case c.Nodes < 0:
			nodes = "unlimited"
		default:
			nodes = "<=" + nodes // Service Quotas reports limits, not usage
		}
		topo := "-"
		if _, ok := db.Captures[c.MachineType]; ok {
			topo = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.MachineType, c.Model, nodes, c.Binding, topo, c.Note)
	}
	w.Flush()
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
