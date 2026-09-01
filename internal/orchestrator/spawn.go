// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"fmt"
	"github.com/yousysadmin/pacer/internal/models/job"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/yousysadmin/pacer/internal/core/ec2lt"
)

// spawnFleet runs CreateFleet(Type=instant) over every (instance_type
// x subnet) combo, letting AWS pick an available one using a
// price/capacity-aware allocation strategy:
//
//   - on-demand pools: AllocationStrategy=lowest-price (cheapest
//     available type across all combos)
//   - spot pools: AllocationStrategy=price-capacity-optimized
//     (AWS-recommended, balances price with interruption probability)
//
// Spot price never exceeds on-demand - AWS guarantees this since
// 2017, so we don't pass an explicit MaxPrice.
//
// The LT carries static user-data baked at pool-save time. Per-job
// state (the HMAC callback token) is served by /api/runner/bootstrap
// and never rides on the instance, so nothing here has to be minted
// per spawn: we reference Version=$Default and the LT only mutates
// when the operator saves the pool.
//
// Returns:
//   - (result, false, nil)              success
//   - (nil, true, summary err)          every override returned a
//     capacity-class error. Caller
//     should reschedule
//   - (nil, false, err)                 permanent failure
func (o *Orchestrator) spawnFleet(ctx context.Context, sc *spawnContext) (*spawnResult, bool, error) {
	priorityMode := sc.pool.AllocationStrategy == "priority"
	overrides := buildFleetOverrides(sc.pool.InstanceTypes, sc.pool.SubnetIDs, priorityMode)
	if len(overrides) == 0 {
		return nil, false, fmt.Errorf("pool %s has no instance_types x subnets to launch into", sc.pool.Name)
	}
	onDemandAlloc, spotAlloc := allocationStrategies(sc.pool.AllocationStrategy)

	in := &ec2.CreateFleetInput{
		Type: ec2types.FleetTypeInstant,
		LaunchTemplateConfigs: []ec2types.FleetLaunchTemplateConfigRequest{
			{
				LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
					LaunchTemplateId: aws.String(sc.pool.LaunchTemplateID),
					Version:          aws.String("$Default"),
				},
				Overrides: overrides,
			},
		},
		TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
			TotalTargetCapacity:       aws.Int32(1),
			DefaultTargetCapacityType: defaultCapacityType(sc.pool.Spot),
		},
		OnDemandOptions: &ec2types.OnDemandOptionsRequest{AllocationStrategy: onDemandAlloc},
		SpotOptions:     &ec2types.SpotOptionsRequest{AllocationStrategy: spotAlloc},
		// Tag the fleet resource itself so the IAM CreateFleet Sid's
		// aws:RequestTag/gha:managed-by condition fires. Also makes
		// the fleet visible in the EC2 console alongside our other
		// tagged resources.
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeFleet,
			Tags: []ec2types.Tag{
				{Key: aws.String(ec2lt.ManagedByTagKey), Value: aws.String(ec2lt.ManagedByTagValue)},
				{Key: aws.String("gha:project"), Value: aws.String(sc.project.Name)},
				{Key: aws.String("gha:pool"), Value: aws.String(sc.pool.Name)},
				{Key: aws.String("gha:job_id"), Value: aws.String(sc.job.ID)},
			},
		}},
	}

	// ClientToken makes the request idempotent: if the SDK times out
	// after AWS already fulfilled the fleet, a retry on the same
	// attempt returns the same instance instead of launching a second.
	// The claim timestamp keeps a reclaimed job from replaying the
	// token of a previous claim.
	in.ClientToken = aws.String(fleetClientToken(sc.job))

	out, err := o.Runtime.EC2.CreateFleet(ctx, in)
	if err != nil {
		return nil, false, fmt.Errorf("create fleet: %w", err)
	}
	if len(out.Instances) > 0 && len(out.Instances[0].InstanceIds) > 0 {
		instID := out.Instances[0].InstanceIds[0]
		instType := string(out.Instances[0].InstanceType)
		az := ""
		if lts := out.Instances[0].LaunchTemplateAndOverrides; lts != nil && lts.Overrides != nil {
			// Subnet-keyed overrides leave AvailabilityZone empty in
			// the echoed response. Resolve from the chosen subnet via
			// the orchestrator's per-subnet cache. Spot pricing needs
			// the AZ. Without it snapshotPrice degrades to NULL.
			az = aws.ToString(lts.Overrides.AvailabilityZone)
			if az == "" {
				az = o.resolveSubnetAZ(ctx, aws.ToString(lts.Overrides.SubnetId))
			}
		}
		// Post-launch tagging: per-job + repo user tags Fleet's API
		// surface can't carry on its own. The instance came up with
		// managed-by tag from the LT, so the IAM CreateTags Sid
		// (gated on managed-by) admits this call. Retry once to ride
		// over a transient rate limit.
		//
		// Best-effort by design. Nothing the runner needs travels
		// here - it authenticates against /api/runner/bootstrap using
		// the token baked into user-data - and gha:managed-by, the tag
		// the terminate policy is gated on, comes from the LT. So a
		// throttled CreateTags costs cost-attribution accuracy, not a
		// working runner, and killing a healthy instance over it would
		// fail a job that was about to run.
		bctx, cancel := detach(ctx)
		defer cancel()
		if err := o.postTagInstanceRetry(bctx, sc, instID); err != nil {
			slog.Warn("orchestrator: post-launch tagging failed; instance keeps its launch-template tags",
				"instance_id", instID, "job_id", sc.job.ID, "err", err)
		}
		return &spawnResult{InstanceID: instID, InstanceType: instType, AZ: az}, false, nil
	}

	// No instances launched - inspect Errors[]. If every entry is
	// capacity-class, signal exhaustion so the caller reschedules.
	if len(out.Errors) == 0 {
		// Theoretically unreachable per the CreateFleet contract -
		// log loudly so we notice if AWS ever changes the shape.
		slog.Error("orchestrator: create fleet returned 0 instances and 0 errors (AWS contract changed?)")
		return nil, false, fmt.Errorf("create fleet returned 0 instances and 0 errors (unexpected)")
	}
	allCapacity := true
	for _, e := range out.Errors {
		if !isCapacityErrorCode(aws.ToString(e.ErrorCode)) {
			allCapacity = false
			break
		}
	}
	last := out.Errors[len(out.Errors)-1]
	summary := fmt.Errorf("fleet exhausted %d combos (last: %s: %s)",
		len(out.Errors), aws.ToString(last.ErrorCode), aws.ToString(last.ErrorMessage))
	if allCapacity {
		return nil, true, summary
	}
	return nil, false, summary
}

func defaultCapacityType(spot bool) ec2types.DefaultTargetCapacityType {
	if spot {
		return ec2types.DefaultTargetCapacityTypeSpot
	}
	return ec2types.DefaultTargetCapacityTypeOnDemand
}

// buildFleetOverrides expands the (instance_type, subnet) cartesian
// product into FleetLaunchTemplateOverridesRequest entries. Fleet's
// allocation strategy picks one. When priorityMode is true, each
// override's Priority field is set to the instance type's index in
// the list (lower = preferred). Subnets within the same type share
// a priority so AWS can rotate AZs freely on capacity grounds.
func buildFleetOverrides(instanceTypes, subnetIDs []string, priorityMode bool) []ec2types.FleetLaunchTemplateOverridesRequest {
	out := make([]ec2types.FleetLaunchTemplateOverridesRequest, 0, len(instanceTypes)*len(subnetIDs))
	for i, t := range instanceTypes {
		for _, s := range subnetIDs {
			ov := ec2types.FleetLaunchTemplateOverridesRequest{
				InstanceType: ec2types.InstanceType(t),
				SubnetId:     aws.String(s),
			}
			if priorityMode {
				ov.Priority = aws.Float64(float64(i))
			}
			out = append(out, ov)
		}
	}
	return out
}

// allocationStrategies maps the pool's coarse strategy knob to AWS's
// per-market allocation strategy enums.
//
//	"cost"         -> lowest-price (on-demand) + price-capacity-optimized (spot)   [default]
//	"lowest_price" -> lowest-price (on-demand) + lowest-price (spot)               [pure cheapest, higher interruption]
//	"capacity"     -> lowest-price (on-demand) + capacity-optimized (spot)         [deepest pool, ignore price]
//	"priority"     -> prioritized  (on-demand) + capacity-optimized-prioritized (spot)
//
// On-demand has no "capacity" concept (it's always available), so the
// "capacity" preset still maps OD to lowest-price - the strategy is
// only meaningful for spot pools.
//
// "lowest_price" for spot is the deprecated AWS strategy: it picks the
// instantaneously cheapest pool with no capacity signal, so it'll
// happily land in shallow pools that interrupt soon after launch.
// Useful when cost trumps reliability (short throwaway jobs). Avoid
// for long-running workloads.
//
// For spot, capacity-optimized-prioritized respects the operator's
// list order on a best-effort basis but optimizes capacity first -
// using plain "prioritized" for spot is an anti-pattern (it ignores
// capacity signals and risks high-interruption pools).
//
// Unknown strategies fall back to "cost" so an inbound backup row
// with a typo still launches.
func allocationStrategies(strategy string) (ec2types.FleetOnDemandAllocationStrategy, ec2types.SpotAllocationStrategy) {
	switch strategy {
	case "lowest_price":
		return ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			ec2types.SpotAllocationStrategyLowestPrice
	case "capacity":
		return ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			ec2types.SpotAllocationStrategyCapacityOptimized
	case "priority":
		return ec2types.FleetOnDemandAllocationStrategyPrioritized,
			ec2types.SpotAllocationStrategyCapacityOptimizedPrioritized
	default: // "cost" or empty
		return ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			ec2types.SpotAllocationStrategyPriceCapacityOptimized
	}
}

// postTagInstance applies the per-spawn tags Fleet can't include in
// the CreateFleet call: gha:job_id, gha:repo, plus the repo user
// tags. The instance already carries managed-by + project + pool tags
// from the LT, so the IAM CreateTags Sid (gated on managed-by) admits
// this call.
//
// These tags are for the operator's benefit - cost allocation and
// console search. No part of the boot path reads them.
func (o *Orchestrator) postTagInstance(ctx context.Context, sc *spawnContext, instID string) error {
	tags := postLaunchTags(sc)
	if len(tags) == 0 {
		return nil
	}
	_, err := o.Runtime.EC2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{instID},
		Tags:      tags,
	})
	return err
}

// postTagInstanceRetry calls postTagInstance, then retries once after
// a short pause if the first attempt failed. CreateTags is the
// canonical EC2 throttling endpoint. One retry covers the common
// transient-throttle case without delaying the spawn loop noticeably.
func (o *Orchestrator) postTagInstanceRetry(ctx context.Context, sc *spawnContext, instID string) error {
	err := o.postTagInstance(ctx, sc, instID)
	if err == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return err
	case <-time.After(500 * time.Millisecond):
	}
	return o.postTagInstance(ctx, sc, instID)
}

func postLaunchTags(sc *spawnContext) []ec2types.Tag {
	tags := make([]ec2types.Tag, 0, 2+len(sc.repoTags))
	for k, v := range sc.repoTags {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	tags = append(tags,
		ec2types.Tag{Key: aws.String("gha:job_id"), Value: aws.String(sc.job.ID)},
		ec2types.Tag{Key: aws.String("gha:repo"), Value: aws.String(sc.job.RepoFullName)},
	)
	return tags
}

// spawnRunInstances is the legacy path: serial RunInstances over
// pool.InstanceTypes against pool.SubnetIDs[0]. Kept for operators
// who specifically want it. No multi-AZ - if SubnetIDs[0]'s AZ is
// dry, we never try the rest. Same return contract as spawnFleet.
//
// User-data is baked into the LT at materialize time, not passed
// per-call, so the LT version this references stays consecutive
// across spawns (same invariant as the Fleet path). Unlike Fleet,
// RunInstances carries the full tag set in TagSpecifications, so
// there is no post-launch CreateTags step.
func (o *Orchestrator) spawnRunInstances(ctx context.Context, sc *spawnContext) (*spawnResult, bool, error) {
	if len(sc.pool.InstanceTypes) == 0 || len(sc.pool.SubnetIDs) == 0 {
		return nil, false, fmt.Errorf("pool %s has no instance_types or subnets to launch into", sc.pool.Name)
	}
	tagSpec := buildTagSpecs(sc)

	var (
		runOut   *ec2.RunInstancesOutput
		typeUsed string
		runErr   error
	)
	for _, t := range sc.pool.InstanceTypes {
		runOut, runErr = o.Runtime.EC2.RunInstances(ctx, &ec2.RunInstancesInput{
			LaunchTemplate: &ec2types.LaunchTemplateSpecification{
				LaunchTemplateId: aws.String(sc.pool.LaunchTemplateID),
				Version:          aws.String("$Default"),
			},
			InstanceType:      ec2types.InstanceType(t),
			SubnetId:          aws.String(sc.pool.SubnetIDs[0]),
			MinCount:          aws.Int32(1),
			MaxCount:          aws.Int32(1),
			TagSpecifications: tagSpec,
		})
		if runErr == nil && runOut != nil && len(runOut.Instances) > 0 {
			typeUsed = t
			break
		}
		if runErr == nil {
			// Accepted the call but launched nothing. Not a documented
			// RunInstances outcome, so treat it as permanent rather
			// than burning the retry budget on it.
			return nil, false, fmt.Errorf("run instances (%s): no error and no instances", t)
		}
		if !isCapacityError(runErr) {
			return nil, false, fmt.Errorf("run instances (%s): %w", t, runErr)
		}
		slog.Warn("orchestrator: capacity insufficient, trying next type", "type", t, "err", runErr)
	}
	if runOut == nil || len(runOut.Instances) == 0 {
		return nil, true, fmt.Errorf("all instance types exhausted (last err: %v)", runErr)
	}

	instID := aws.ToString(runOut.Instances[0].InstanceId)
	az := ""
	if p := runOut.Instances[0].Placement; p != nil {
		az = aws.ToString(p.AvailabilityZone)
	}
	return &spawnResult{InstanceID: instID, InstanceType: typeUsed, AZ: az}, false, nil
}

// fleetClientToken derives the CreateFleet idempotency key. It is
// stable within one claim and attempt, and changes when either does.
func fleetClientToken(j *job.Job) string {
	var claimed int64
	if j.ClaimedAt != nil {
		claimed = j.ClaimedAt.Unix()
	}
	return fmt.Sprintf("%s-%d-%d", j.ID, j.Attempts, claimed)
}
