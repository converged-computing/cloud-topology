// Package catalog exports Compute Engine machine types into the static database.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"

	"github.com/converged-computing/google-cloud-topology/pkg/database"
)

// Options selects which machine types to keep.
type Options struct {
	// Regions keeps zones in these regions; empty keeps all regions.
	Regions []string
	// Families keeps machine types whose family (name prefix) is listed; empty keeps all.
	Families []string
	// IncludeDeprecated keeps machine types marked deprecated.
	IncludeDeprecated bool
}

// Pair is one zone's list of machine types from an aggregated list call.
type Pair struct {
	Key          string
	MachineTypes []*computepb.MachineType
}

// Fetch lists machine types for a project across all zones.
func Fetch(ctx context.Context, project string) ([]Pair, error) {
	client, err := compute.NewMachineTypesRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	it := client.AggregatedList(ctx, &computepb.AggregatedListMachineTypesRequest{Project: project})
	var pairs []Pair
	for {
		pair, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing machine types: %w", err)
		}
		if pair.Value == nil {
			continue
		}
		pairs = append(pairs, Pair{Key: pair.Key, MachineTypes: pair.Value.GetMachineTypes()})
	}
	return pairs, nil
}

// Convert merges per-zone machine types into one record per machine type.
func Convert(pairs []Pair, opts Options) map[string]database.MachineType {
	regions := toSet(opts.Regions)
	families := toSet(opts.Families)
	out := map[string]database.MachineType{}
	for _, pair := range pairs {
		for _, m := range pair.MachineTypes {
			zone := lastSegment(m.GetZone())
			if zone == "" {
				zone = strings.TrimPrefix(pair.Key, "zones/")
			}
			if len(regions) > 0 && !regions[database.RegionOf(zone)] {
				continue
			}
			name := m.GetName()
			family := database.FamilyOf(name)
			if len(families) > 0 && !families[family] {
				continue
			}
			deprecated := m.GetDeprecated().GetState()
			if deprecated != "" && !opts.IncludeDeprecated {
				continue
			}
			mt, seen := out[name]
			if !seen {
				mt = database.MachineType{
					Name:         name,
					Family:       family,
					Description:  m.GetDescription(),
					Architecture: m.GetArchitecture(),
					VCPUs:        int(m.GetGuestCpus()),
					MemoryMB:     int(m.GetMemoryMb()),
					SharedCPU:    m.GetIsSharedCpu(),
					Deprecated:   deprecated,
				}
				if ssd := m.GetBundledLocalSsds(); ssd != nil {
					mt.LocalSSDPartitions = int(ssd.GetPartitionCount())
					mt.LocalSSDInterface = ssd.GetDefaultInterface()
				}
				for _, a := range m.GetAccelerators() {
					mt.Accelerators = append(mt.Accelerators, database.Accelerator{
						Type:  a.GetGuestAcceleratorType(),
						Count: int(a.GetGuestAcceleratorCount()),
					})
				}
			}
			mt.Zones = appendUnique(mt.Zones, zone)
			out[name] = mt
		}
	}
	return out
}

func toSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			set[v] = true
		}
	}
	return set
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
