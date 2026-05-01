// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package pricing fetches the at-launch USD/hour for an EC2 instance
// from the AWS Pricing API (on-demand) or DescribeSpotPriceHistory (spot).
// Best-effort -- pricing fetch errors are logged and the
// caller stamps a NULL price; nothing fails-closed.
// Spot fluctuates during a run, so the stamped value is a launch-time snapshot,
// not an authoritative bill.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

const (
	ModelOnDemand = "on_demand"
	ModelSpot     = "spot"

	// onDemandTTL is how long an on-demand quote stays cached.
	// AWS rarely changes published rates; a day is plenty.
	onDemandTTL = 24 * time.Hour
)

// Fetcher wraps the two AWS APIs we need plus a small in-process
// on-demand cache (keyed by region+type).
// Spot is not cached -- a stale spot price is meaningless.
type Fetcher struct {
	EC2     *ec2.Client
	Pricing *pricing.Client
	Region  string

	mu          sync.Mutex
	onDemandMem map[string]onDemandEntry
}

type onDemandEntry struct {
	usdPerHour float64
	at         time.Time
}

// New constructs a Fetcher.
// Both clients are required; passing nil for either results in AtLaunch
// returning (0, "", error) for the matching price model.
func New(ec2c *ec2.Client, pc *pricing.Client, region string) *Fetcher {
	return &Fetcher{
		EC2:         ec2c,
		Pricing:     pc,
		Region:      region,
		onDemandMem: map[string]onDemandEntry{},
	}
}

// AtLaunch returns the per-hour USD rate for the chosen instance
// type at launch.
// spot=true picks DescribeSpotPriceHistory in the
// instance's AZ; spot=false uses the cached on-demand rate.
// The model string returned is one of the Model* constants.
func (f *Fetcher) AtLaunch(ctx context.Context, instanceType, az string, spot bool) (float64, string, error) {
	if spot {
		p, err := f.Spot(ctx, instanceType, az)
		return p, ModelSpot, err
	}
	p, err := f.OnDemand(ctx, instanceType)
	return p, ModelOnDemand, err
}

// OnDemand returns the cached on-demand rate, fetching it from the
// AWS Pricing API on miss.
// Region comes from f.Region (the API isglobal; us-east-1 is the canonical endpoint,
// but we use whatever region the SDK was configured for).
func (f *Fetcher) OnDemand(ctx context.Context, instanceType string) (float64, error) {
	if f.Pricing == nil {
		return 0, fmt.Errorf("pricing client unavailable")
	}
	key := f.Region + "/" + instanceType

	f.mu.Lock()
	if e, ok := f.onDemandMem[key]; ok && time.Since(e.at) < onDemandTTL {
		f.mu.Unlock()
		return e.usdPerHour, nil
	}
	f.mu.Unlock()

	location, err := regionToLocation(f.Region)
	if err != nil {
		return 0, err
	}

	out, err := f.Pricing.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("instanceType"), Value: aws.String(instanceType)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("location"), Value: aws.String(location)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String("Shared")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String("Used")},
		},
		MaxResults: aws.Int32(1),
	})
	if err != nil {
		return 0, fmt.Errorf("pricing GetProducts: %w", err)
	}
	if len(out.PriceList) == 0 {
		return 0, fmt.Errorf("no on-demand price for %s in %s", instanceType, location)
	}
	usd, err := parseOnDemandPrice(out.PriceList[0])
	if err != nil {
		return 0, err
	}

	f.mu.Lock()
	f.onDemandMem[key] = onDemandEntry{usdPerHour: usd, at: time.Now()}
	f.mu.Unlock()
	return usd, nil
}

// Spot fetches the most recent spot price for (type, az).
// No cache: spot prices change every few minutes.
func (f *Fetcher) Spot(ctx context.Context, instanceType, az string) (float64, error) {
	if f.EC2 == nil {
		return 0, fmt.Errorf("ec2 client unavailable")
	}
	if az == "" {
		return 0, fmt.Errorf("spot price needs an AZ")
	}
	out, err := f.EC2.DescribeSpotPriceHistory(ctx, &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       []ec2types.InstanceType{ec2types.InstanceType(instanceType)},
		ProductDescriptions: []string{"Linux/UNIX"},
		AvailabilityZone:    aws.String(az),
		StartTime:           aws.Time(time.Now().Add(-30 * time.Minute)),
		MaxResults:          aws.Int32(1),
	})
	if err != nil {
		return 0, fmt.Errorf("spot price history: %w", err)
	}
	if len(out.SpotPriceHistory) == 0 {
		return 0, fmt.Errorf("no spot price history for %s in %s", instanceType, az)
	}
	usd, err := strconv.ParseFloat(aws.ToString(out.SpotPriceHistory[0].SpotPrice), 64)
	if err != nil {
		return 0, fmt.Errorf("parse spot price: %w", err)
	}
	return usd, nil
}

// parseOnDemandPrice cracks open the deeply-nested JSON the Pricing
// API returns and digs out the per-hour USD rate from the first
// (and typically only) on-demand term + price-dimension.
func parseOnDemandPrice(s string) (float64, error) {
	var doc struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return 0, fmt.Errorf("decode pricing payload: %w", err)
	}
	for _, term := range doc.Terms.OnDemand {
		for _, dim := range term.PriceDimensions {
			if v, ok := dim.PricePerUnit["USD"]; ok {
				p, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return 0, fmt.Errorf("parse USD price %q: %w", v, err)
				}
				return p, nil
			}
		}
	}
	return 0, fmt.Errorf("no USD on-demand term in pricing payload")
}

// regionToLocation maps a region code (us-east-1) to the verbose
// "location" the AWS Pricing API expects ("US East (N. Virginia)").
// The API only takes the human-readable form; this is the AWS-
// published mapping.
func regionToLocation(region string) (string, error) {
	loc, ok := regionLocations[region]
	if !ok {
		return "", fmt.Errorf("no Pricing API location mapping for region %q", region)
	}
	return loc, nil
}

var regionLocations = map[string]string{
	"us-east-1":      "US East (N. Virginia)",
	"us-east-2":      "US East (Ohio)",
	"us-west-1":      "US West (N. California)",
	"us-west-2":      "US West (Oregon)",
	"af-south-1":     "Africa (Cape Town)",
	"ap-east-1":      "Asia Pacific (Hong Kong)",
	"ap-south-1":     "Asia Pacific (Mumbai)",
	"ap-south-2":     "Asia Pacific (Hyderabad)",
	"ap-southeast-1": "Asia Pacific (Singapore)",
	"ap-southeast-2": "Asia Pacific (Sydney)",
	"ap-southeast-3": "Asia Pacific (Jakarta)",
	"ap-southeast-4": "Asia Pacific (Melbourne)",
	"ap-northeast-1": "Asia Pacific (Tokyo)",
	"ap-northeast-2": "Asia Pacific (Seoul)",
	"ap-northeast-3": "Asia Pacific (Osaka)",
	"ca-central-1":   "Canada (Central)",
	"ca-west-1":      "Canada West (Calgary)",
	"eu-central-1":   "EU (Frankfurt)",
	"eu-central-2":   "Europe (Zurich)",
	"eu-west-1":      "EU (Ireland)",
	"eu-west-2":      "EU (London)",
	"eu-west-3":      "EU (Paris)",
	"eu-north-1":     "EU (Stockholm)",
	"eu-south-1":     "EU (Milan)",
	"eu-south-2":     "Europe (Spain)",
	"il-central-1":   "Israel (Tel Aviv)",
	"me-central-1":   "Middle East (UAE)",
	"me-south-1":     "Middle East (Bahrain)",
	"sa-east-1":      "South America (Sao Paulo)",
}
