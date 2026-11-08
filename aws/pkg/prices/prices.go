// Package prices fetches hourly EC2 prices: On-Demand from the Pricing API and the
// current Spot price per availability zone. They size the price ceiling and the estimate
// printed with a capture plan; nothing is bought here.
package prices

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	ptypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// Prices is the hourly price table for one region.
type Prices struct {
	Region  string `json:"region"`
	Fetched string `json:"fetched"`
	// OnDemand is the Linux, shared-tenancy On-Demand price per instance type, $/h.
	OnDemand map[string]float64 `json:"on_demand"`
	// Spot is the current Linux Spot price per instance type and availability zone, $/h.
	Spot map[string]map[string]float64 `json:"spot"`
}

// Load reads a price file.
func Load(path string) (*Prices, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Prices
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// Hourly returns what one hour of an instance type costs under a purchasing model: the
// lowest zone's Spot price for spot, the On-Demand price otherwise. ok is false when the
// table has no price for it.
func (p *Prices) Hourly(instanceType, model string) (price float64, ok bool) {
	if model == "spot" {
		zones := p.Spot[instanceType]
		if len(zones) == 0 {
			return 0, false
		}
		price = -1
		for _, v := range zones {
			if price < 0 || v < price {
				price = v
			}
		}
		return price, true
	}
	price, ok = p.OnDemand[instanceType]
	return price, ok
}

// SpotZones returns an instance type's zones with a Spot price, cheapest first.
func (p *Prices) SpotZones(instanceType string) []string {
	zones := p.Spot[instanceType]
	out := make([]string, 0, len(zones))
	for z := range zones {
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool {
		if zones[out[i]] != zones[out[j]] {
			return zones[out[i]] < zones[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Locations maps a region to the Pricing API's location name.
var Locations = map[string]string{
	"us-east-1": "US East (N. Virginia)", "us-east-2": "US East (Ohio)", "us-west-1": "US West (N. California)", "us-west-2": "US West (Oregon)",
	"eu-west-1": "EU (Ireland)", "eu-west-2": "EU (London)", "eu-central-1": "EU (Frankfurt)", "ap-southeast-1": "Asia Pacific (Singapore)",
	"ap-southeast-2": "Asia Pacific (Sydney)", "ap-northeast-1": "Asia Pacific (Tokyo)", "ca-central-1": "Canada (Central)", "sa-east-1": "South America (Sao Paulo)",
}

// Fetch reads the On-Demand prices of the given instance types (all of the region's
// when empty) and the current Spot prices. The Pricing API lives in us-east-1.
func Fetch(ctx context.Context, cfg aws.Config, region string, instanceTypes []string) (*Prices, error) {
	location, ok := Locations[region]
	if !ok {
		return nil, fmt.Errorf("no Pricing API location known for %s; add it to prices.Locations", region)
	}
	p := &Prices{Region: region, Fetched: time.Now().UTC().Format(time.RFC3339), OnDemand: map[string]float64{}, Spot: map[string]map[string]float64{}}
	pc := cfg.Copy()
	pc.Region = "us-east-1"
	client := pricing.NewFromConfig(pc)
	filters := []ptypes.Filter{
		{Type: ptypes.FilterTypeTermMatch, Field: aws.String("location"), Value: aws.String(location)},
		{Type: ptypes.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
		{Type: ptypes.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String("Shared")},
		{Type: ptypes.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
		{Type: ptypes.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String("Used")},
	}
	pg := pricing.NewGetProductsPaginator(client, &pricing.GetProductsInput{ServiceCode: aws.String("AmazonEC2"), Filters: filters, MaxResults: aws.Int32(100)})
	want := map[string]bool{}
	for _, t := range instanceTypes {
		want[t] = true
	}
	for pg.HasMorePages() {
		page, err := pg.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("pricing: %w", err)
		}
		for _, item := range page.PriceList {
			t, price, ok := parseProduct(item)
			if !ok || (len(want) > 0 && !want[t]) {
				continue
			}
			if cur, seen := p.OnDemand[t]; !seen || price < cur {
				p.OnDemand[t] = price
			}
		}
	}
	rc := cfg.Copy()
	rc.Region = region
	ec := ec2.NewFromConfig(rc)
	in := &ec2.DescribeSpotPriceHistoryInput{ProductDescriptions: []string{"Linux/UNIX"}, StartTime: aws.Time(time.Now())}
	if len(instanceTypes) > 0 {
		for _, t := range instanceTypes {
			in.InstanceTypes = append(in.InstanceTypes, types.InstanceType(t))
		}
	}
	sp := ec2.NewDescribeSpotPriceHistoryPaginator(ec, in)
	for sp.HasMorePages() {
		page, err := sp.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("spot price history: %w", err)
		}
		for _, h := range page.SpotPriceHistory {
			v, err := strconv.ParseFloat(aws.ToString(h.SpotPrice), 64)
			if err != nil {
				continue
			}
			t := string(h.InstanceType)
			if p.Spot[t] == nil {
				p.Spot[t] = map[string]float64{}
			}
			// The history is newest first; keep the first price seen per zone.
			if _, seen := p.Spot[t][aws.ToString(h.AvailabilityZone)]; !seen {
				p.Spot[t][aws.ToString(h.AvailabilityZone)] = v
			}
		}
	}
	return p, nil
}

// parseProduct reads one Pricing API product: its instance type and the lowest positive
// On-Demand hourly USD price.
func parseProduct(item string) (instanceType string, price float64, ok bool) {
	var prod struct {
		Product struct {
			Attributes map[string]string `json:"attributes"`
		} `json:"product"`
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(item), &prod); err != nil {
		return "", 0, false
	}
	instanceType = prod.Product.Attributes["instanceType"]
	best := -1.0
	for _, term := range prod.Terms.OnDemand {
		for _, d := range term.PriceDimensions {
			v, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64)
			if err == nil && v > 0 && (best < 0 || v < best) {
				best = v
			}
		}
	}
	return instanceType, best, instanceType != "" && best > 0
}
