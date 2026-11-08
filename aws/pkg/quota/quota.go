// Package quota fetches an account's EC2 and EBS quotas from Service Quotas and computes
// how many nodes of each instance type the account can run. EC2 quotas are vCPU counts
// per instance class (Standard, P, G and VT, HPC, ...), one for On-Demand and one for
// Spot; EBS quotas are TiB per volume type.
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Quota is one applied quota value.
type Quota struct {
	Service string  `json:"service"`
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	Region  string  `json:"region,omitempty"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit,omitempty"`
	// Global is true for quotas that apply to the account, not a region.
	Global bool `json:"global,omitempty"`
}

// Snapshot is an account's quotas at a point in time. Service Quotas reports limits
// only; usage comes from CloudWatch and is not fetched, so counts derived from a
// snapshot are upper bounds.
type Snapshot struct {
	Account string   `json:"account"`
	Regions []string `json:"regions"`
	Fetched string   `json:"fetched"`
	Quotas  []Quota  `json:"quotas"`
}

// Find returns the quota with a name in a region (global quotas match any region).
func (s *Snapshot) Find(name, region string) (Quota, bool) {
	for _, q := range s.Quotas {
		if q.Name == name && (q.Region == region || q.Global) {
			return q, true
		}
	}
	return Quota{}, false
}

// Load reads a snapshot file.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Services are the Service Quotas service codes the snapshot covers.
var Services = []string{"ec2", "ebs"}

// Fetch lists the applied ec2 and ebs quotas in each region, with the account id.
func Fetch(ctx context.Context, cfg aws.Config, regions []string) (*Snapshot, error) {
	snap := &Snapshot{Regions: regions, Fetched: time.Now().UTC().Format(time.RFC3339)}
	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("caller identity: %w", err)
	}
	snap.Account = aws.ToString(id.Account)
	for _, region := range regions {
		rc := cfg.Copy()
		rc.Region = region
		client := servicequotas.NewFromConfig(rc)
		for _, service := range Services {
			p := servicequotas.NewListServiceQuotasPaginator(client, &servicequotas.ListServiceQuotasInput{ServiceCode: aws.String(service)})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, fmt.Errorf("listing %s quotas in %s: %w", service, region, err)
				}
				for _, q := range page.Quotas {
					snap.Quotas = append(snap.Quotas, Quota{Service: service, Code: aws.ToString(q.QuotaCode), Name: aws.ToString(q.QuotaName),
						Region: region, Value: aws.ToFloat64(q.Value), Unit: aws.ToString(q.Unit), Global: q.GlobalQuota})
				}
			}
		}
	}
	return snap, nil
}
