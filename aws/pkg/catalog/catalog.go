// Package catalog exports EC2 instance types into the static database. Unlike the
// Compute Engine catalog, EC2 describes each type's cores, network cards, EFA support,
// instance store disks, EBS performance and placement strategies, so none of that has to
// be encoded from documentation.
package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
)

// Options selects which instance types to keep.
type Options struct {
	// Families keeps instance types whose family is listed; empty keeps all.
	Families []string
	// IncludePrevious keeps previous-generation instance types.
	IncludePrevious bool
}

// Raw is what one region's EC2 API returns: the instance types, the zones each is
// offered in, and the zone ids.
type Raw struct {
	Region        string
	InstanceTypes []types.InstanceTypeInfo
	// Offerings maps an instance type to the availability zones offering it.
	Offerings map[string][]string
	ZoneIDs   map[string]string
}

// Fetch describes the instance types, offerings and zones of one region.
func Fetch(ctx context.Context, client *ec2.Client, region string) (*Raw, error) {
	raw := &Raw{Region: region, Offerings: map[string][]string{}, ZoneIDs: map[string]string{}}
	p := ec2.NewDescribeInstanceTypesPaginator(client, &ec2.DescribeInstanceTypesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describing instance types: %w", err)
		}
		raw.InstanceTypes = append(raw.InstanceTypes, page.InstanceTypes...)
	}
	op := ec2.NewDescribeInstanceTypeOfferingsPaginator(client, &ec2.DescribeInstanceTypeOfferingsInput{LocationType: types.LocationTypeAvailabilityZone})
	for op.HasMorePages() {
		page, err := op.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describing instance type offerings: %w", err)
		}
		for _, o := range page.InstanceTypeOfferings {
			name := string(o.InstanceType)
			raw.Offerings[name] = append(raw.Offerings[name], aws.ToString(o.Location))
		}
	}
	zones, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{AllAvailabilityZones: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("describing availability zones: %w", err)
	}
	for _, z := range zones.AvailabilityZones {
		raw.ZoneIDs[aws.ToString(z.ZoneName)] = aws.ToString(z.ZoneId)
	}
	return raw, nil
}

// Convert turns the API's instance types into database records, one per type, with the
// zones that offer it. Types offered nowhere in the region are left out.
func Convert(raw *Raw, opts Options) map[string]database.MachineType {
	families := toSet(opts.Families)
	out := map[string]database.MachineType{}
	for _, it := range raw.InstanceTypes {
		name := string(it.InstanceType)
		family := database.FamilyOf(name)
		if len(families) > 0 && !families[family] {
			continue
		}
		if !aws.ToBool(it.CurrentGeneration) && !opts.IncludePrevious {
			continue
		}
		zones := append([]string(nil), raw.Offerings[name]...)
		if len(zones) == 0 {
			continue
		}
		sort.Strings(zones)
		mt := database.MachineType{
			Name:              name,
			Family:            family,
			VCPUs:             int(aws.ToInt32(it.VCpuInfo.DefaultVCpus)),
			Cores:             int(aws.ToInt32(it.VCpuInfo.DefaultCores)),
			ThreadsPerCore:    int(aws.ToInt32(it.VCpuInfo.DefaultThreadsPerCore)),
			MemoryMB:          int(aws.ToInt64(it.MemoryInfo.SizeInMiB)),
			BareMetal:         aws.ToBool(it.BareMetal),
			Hypervisor:        string(it.Hypervisor),
			CurrentGeneration: aws.ToBool(it.CurrentGeneration),
			Burstable:         aws.ToBool(it.BurstablePerformanceSupported),
			Zones:             zones,
		}
		if it.ProcessorInfo != nil {
			if len(it.ProcessorInfo.SupportedArchitectures) > 0 {
				mt.Architecture = string(it.ProcessorInfo.SupportedArchitectures[0])
			}
			mt.Processor = database.Processor{Manufacturer: aws.ToString(it.ProcessorInfo.Manufacturer), ClockGHz: aws.ToFloat64(it.ProcessorInfo.SustainedClockSpeedInGhz)}
		}
		for _, u := range it.SupportedUsageClasses {
			mt.UsageClasses = append(mt.UsageClasses, string(u))
		}
		sort.Strings(mt.UsageClasses)
		if it.GpuInfo != nil {
			for _, g := range it.GpuInfo.Gpus {
				mt.Accelerators = append(mt.Accelerators, database.Accelerator{
					Type: deviceType(aws.ToString(g.Manufacturer), aws.ToString(g.Name)), Count: int(aws.ToInt32(g.Count)),
					Manufacturer: aws.ToString(g.Manufacturer), Name: aws.ToString(g.Name), MemoryMiB: gpuMemory(g)})
			}
		}
		if it.NeuronInfo != nil {
			for _, d := range it.NeuronInfo.NeuronDevices {
				a := database.Accelerator{Type: deviceType("aws", aws.ToString(d.Name)), Count: int(aws.ToInt32(d.Count)), Manufacturer: "AWS", Name: aws.ToString(d.Name)}
				if d.MemoryInfo != nil {
					a.MemoryMiB = int(aws.ToInt32(d.MemoryInfo.SizeInMiB))
				}
				mt.Accelerators = append(mt.Accelerators, a)
			}
		}
		if n := it.NetworkInfo; n != nil {
			mt.Network = database.Network{Performance: aws.ToString(n.NetworkPerformance), MaxInterfaces: int(aws.ToInt32(n.MaximumNetworkInterfaces)),
				EFASupported: aws.ToBool(n.EfaSupported), ENASRD: aws.ToBool(n.EnaSrdSupported)}
			if n.EfaInfo != nil {
				mt.Network.MaxEFAInterfaces = int(aws.ToInt32(n.EfaInfo.MaximumEfaInterfaces))
			}
			for _, c := range n.NetworkCards {
				card := database.NetworkCard{Index: int(aws.ToInt32(c.NetworkCardIndex)), BaselineGbps: aws.ToFloat64(c.BaselineBandwidthInGbps), PeakGbps: aws.ToFloat64(c.PeakBandwidthInGbps)}
				for _, t := range c.InterfaceTypes {
					if t == types.NetworkCardInterfaceTypeEfa {
						card.EFA = true
					}
				}
				mt.Network.Cards = append(mt.Network.Cards, card)
			}
			sort.Slice(mt.Network.Cards, func(i, j int) bool { return mt.Network.Cards[i].Index < mt.Network.Cards[j].Index })
		}
		if s := it.InstanceStorageInfo; s != nil && aws.ToBool(it.InstanceStorageSupported) {
			store := &database.InstanceStore{TotalGB: int(aws.ToInt64(s.TotalSizeInGB)), NVMe: string(s.NvmeSupport)}
			for _, d := range s.Disks {
				store.Disks = append(store.Disks, database.Disk{SizeGB: int(aws.ToInt64(d.SizeInGB)), Count: int(aws.ToInt32(d.Count)), Type: string(d.Type)})
			}
			mt.InstanceStore = store
		}
		if e := it.EbsInfo; e != nil {
			mt.EBS.MaxAttachments = int(aws.ToInt32(e.MaximumEbsAttachments))
			if o := e.EbsOptimizedInfo; o != nil {
				mt.EBS.BaselineMbps, mt.EBS.MaxMbps = int(aws.ToInt32(o.BaselineBandwidthInMbps)), int(aws.ToInt32(o.MaximumBandwidthInMbps))
				mt.EBS.BaselineIOPS, mt.EBS.MaxIOPS = int(aws.ToInt32(o.BaselineIops)), int(aws.ToInt32(o.MaximumIops))
			}
		}
		if p := it.PlacementGroupInfo; p != nil {
			for _, s := range p.SupportedStrategies {
				mt.PlacementStrategies = append(mt.PlacementStrategies, string(s))
			}
			sort.Strings(mt.PlacementStrategies)
		}
		out[name] = mt
	}
	return out
}

// deviceType names an accelerator the way the Google catalog does: nvidia-h100,
// nvidia-a10g, amd-radeon-pro-v520, aws-trainium.
func deviceType(manufacturer, name string) string {
	s := strings.ToLower(strings.TrimSpace(manufacturer) + "-" + strings.TrimSpace(name))
	s = strings.NewReplacer(" ", "-", "_", "-").Replace(s)
	return s
}

func gpuMemory(g types.GpuDeviceInfo) int {
	if g.MemoryInfo == nil {
		return 0
	}
	return int(aws.ToInt32(g.MemoryInfo.SizeInMiB))
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
