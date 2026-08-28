// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package orchestrator owns the spawn loop + reaper goroutines.
//
// Spawn loop: every PollInterval, drain the queued-jobs queue. For
// each claimed job: look up its pool (job.PoolID was stamped at
// webhook time by the pool selector), mint a fresh HMAC-signed
// callback token (the per-job secret), then call either CreateFleet
// (default, "fleet" spawn method -- AWS picks an available type+AZ
// from every override combo) or RunInstances (legacy serial
// fallback, "run_instances" spawn method) referencing the pool's
// LT at $Default. The callback token rides as the gha:callback-token
// instance tag (stamped at-launch for RunInstances, post-launch for
// Fleet). The LT's baked user-data reads it via IMDS at boot. The
// LT itself is never mutated by the orchestrator -- it only changes
// when the operator saves the pool. Capacity-class failures (no
// capacity for any type+AZ combo) are RESCHEDULED rather than
// failing the job: a backoff is applied and the next tick after
// next_retry_at picks it up. Permanent errors (bad AMI, missing IAM
// role) still mark the job failed immediately.
//
// Reaper: every ReapInterval, sweep instances older than their
// pool's max_runtime_minutes and TerminateInstances them.
//
// The orchestrator is a single goroutine -- sqlite's MaxOpenConns(1)
// serializes writes anyway. Bumping throughput means moving to
// postgres + parallel claim workers. Not in scope for V1.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/callback"
	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	jobstore "github.com/yousysadmin/pacer/internal/domain/job"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/instance"
	"github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/models/pool"
)

const (
	PollInterval = 5 * time.Second
	// callbackTokenGrace pads the pool's max_runtime so the
	// /api/runner/complete callback can still authenticate after a
	// long-running job.
	callbackTokenGrace = 30 * time.Minute

	// MaxSpawnAttempts caps how many times the orchestrator reschedules
	// a single job for capacity-class failures before giving up.
	MaxSpawnAttempts = 12

	// staleClaimAge is how long a job may sit in claimed with no
	// instance before reclaimStale requeues it. Matches the bootstrap
	// window so a slow but live spawn is never yanked back.
	staleClaimAge = 15 * time.Minute

	// bookkeepingTimeout bounds the detached context used for DB
	// writes and rollback terminates after EC2 has launched.
	bookkeepingTimeout = 30 * time.Second

	// orchestratorHealthComponent is the Health key safeTick writes.
	orchestratorHealthComponent = "orchestrator"

	// SpawnMethodFleet uses CreateFleet(Type=instant) with multi-type
	// + multi-subnet overrides. AWS picks an available combo using a
	// price/capacity-aware allocation strategy. Recommended.
	SpawnMethodFleet = "fleet"
	// SpawnMethodRunInstances loops RunInstances over pool.InstanceTypes
	// against pool.SubnetIDs[0] only. Kept as opt-down.
	SpawnMethodRunInstances = "run_instances"
)

type Orchestrator struct {
	Runtime *env.Runtime
	HMACKey []byte
	// subnetAZ caches subnetID -> availabilityZone resolutions so we
	// hit DescribeSubnets at most once per subnet over the
	// orchestrator's lifetime. CreateFleet's response echoes the
	// chosen subnet but leaves AvailabilityZone empty for
	// subnet-keyed overrides. The cache lets snapshotPrice get an AZ
	// for the spot-pricing lookup without an API call per spawn.
	subnetAZ sync.Map // map[string]string
}

func New(rt *env.Runtime) *Orchestrator {
	return &Orchestrator{
		Runtime: rt,
		HMACKey: []byte(rt.Config.GitHub.CallbackHMACSecret),
	}
}

// Run blocks until ctx is cancelled, draining the queue every
// PollInterval. Errors are logged, never returned -- the loop must
// keep running.
func (o *Orchestrator) Run(ctx context.Context) {
	t := time.NewTicker(PollInterval)
	defer t.Stop()

	o.safeTick(ctx) // run once immediately so a clean restart picks up backlog
	for {
		select {
		case <-ctx.Done():
			slog.Info("orchestrator stopping")
			return
		case <-t.C:
			o.safeTick(ctx)
		}
	}
}

// safeTick runs tick under recover so a panic in the spawn path (an
// unexpected AWS response shape, a corrupt row) is logged and
// surfaced on Health instead of taking the process down.
func (o *Orchestrator) safeTick(ctx context.Context) {
	_ = safeTick(o.Runtime, orchestratorHealthComponent, func() error {
		o.tick(ctx)
		return nil
	})
}

// detach returns a context that survives ctx cancellation but is
// bounded by bookkeepingTimeout. Once EC2 has launched an instance,
// the DB writes and any rollback terminate must complete even when
// shutdown or a request cancellation races the spawn.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
}

// tick drains the queued-jobs queue. Stops at the first empty claim
// or a permanent claim error.
func (o *Orchestrator) tick(ctx context.Context) {
	o.reclaimStale(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		j, err := o.Runtime.Store.Job.Claim(ctx, time.Now().UTC())
		if err != nil {
			slog.Error("orchestrator: claim failed", "err", err)
			return
		}
		if j == nil {
			return // queue empty, all pools at cap, all projects disabled, or all jobs in backoff
		}
		spawnErr, capacityExhausted := o.spawn(ctx, j)
		if spawnErr == nil && !capacityExhausted {
			continue
		}
		// The job is already claimed, so the outcome must be written
		// even if ctx was cancelled mid-spawn. Otherwise the row sits
		// in claimed until reclaimStale picks it up 15 minutes later.
		wctx, cancel := detach(ctx)
		if capacityExhausted {
			o.reschedule(wctx, j, spawnErr)
			cancel()
			continue
		}
		// permanent failure
		slog.Error("orchestrator: spawn failed", "job_id", j.ID, "err", spawnErr)
		if err := o.Runtime.Store.Job.MarkFailed(wctx, j.ID, "spawn", spawnErr.Error(), time.Now().UTC()); err != nil {
			slog.Error("orchestrator: mark failed write failed, job may be stuck in claimed state",
				"job_id", j.ID, "err", err)
		}
		o.auditAction(wctx, audit.ActionJobFailed, "job", j.ID, map[string]any{
			"stage": "spawn",
			"err":   spawnErr.Error(),
		})
		cancel()
	}
}

// reclaimStale requeues jobs stuck in claimed with no instance for
// longer than staleClaimAge. These are left over from a crash or a
// cancelled shutdown between Claim and StampSpawn, and each one holds
// a concurrency slot in the Claim SQL until it is released.
func (o *Orchestrator) reclaimStale(ctx context.Context) {
	n, err := o.Runtime.Store.Job.ReclaimStale(ctx, time.Now().UTC().Add(-staleClaimAge))
	if err != nil {
		slog.Error("orchestrator: reclaim stale claims failed", "err", err)
		return
	}
	if n > 0 {
		slog.Warn("orchestrator: requeued stale claimed jobs", "count", n)
	}
}

// reschedule bumps the job's attempts counter and either pushes it
// back into the queue with a backoff or, after MaxSpawnAttempts,
// marks it permanently failed with a clear capacity-exhausted message.
func (o *Orchestrator) reschedule(ctx context.Context, j *job.Job, lastErr error) {
	attempts := j.Attempts + 1
	if attempts >= MaxSpawnAttempts {
		msg := fmt.Sprintf("EC2 capacity exhausted after %d attempts (last err: %v)", attempts, lastErr)
		slog.Warn("orchestrator: giving up after max attempts", "job_id", j.ID, "attempts", attempts, "err", lastErr)
		if err := o.Runtime.Store.Job.MarkFailed(ctx, j.ID, "spawn", msg, time.Now().UTC()); err != nil {
			slog.Error("orchestrator: mark failed write failed; job stuck in claimed state",
				"job_id", j.ID, "err", err)
		}
		o.auditAction(ctx, audit.ActionJobSpawnExhausted, "job", j.ID, map[string]any{
			"attempts": attempts,
			"err":      lastErr.Error(),
		})
		return
	}
	backoff := retryBackoff(j.Attempts)
	nextRetryAt := time.Now().UTC().Add(backoff)
	if err := o.Runtime.Store.Job.Reschedule(ctx, j.ID, attempts, nextRetryAt); err != nil {
		slog.Error("orchestrator: reschedule failed", "job_id", j.ID, "err", err)
		return
	}
	slog.Warn("orchestrator: capacity exhausted, rescheduled",
		"job_id", j.ID, "attempts", attempts, "next_retry_in", backoff.String(), "last_err", lastErr)
	o.auditAction(ctx, audit.ActionJobSpawnRetry, "job", j.ID, map[string]any{
		"attempts":      attempts,
		"next_retry_at": nextRetryAt.Format(time.RFC3339),
		"err":           lastErr.Error(),
	})
}

// retryBackoff returns the wait before the (attempt+1)th retry. The
// schedule is 30s, 60s, 120s, 240s, then 5min capped -- ~50min
// budget over 12 attempts. Capacity returns are usually zonal so the
// later attempts catch the AZ rotation.
func retryBackoff(attempt int) time.Duration {
	schedule := []time.Duration{
		30 * time.Second,
		60 * time.Second,
		2 * time.Minute,
		4 * time.Minute,
	}
	if attempt < len(schedule) {
		return schedule[attempt]
	}
	return 5 * time.Minute
}

// spawnContext bundles the per-spawn inputs the backend impls need.
// Built once in spawn() and threaded into spawnFleet/spawnRunInstances.
//
// callbackToken is the raw HMAC token (`<job_id>.<exp>.<sig>`). The
// orchestrator stamps it on the instance as the gha:callback-token
// tag so the in-instance bootstrap script can read it via IMDS. Per
// CLAUDE.md, raw tokens never hit disk -- only the sha256 hash lives
// on the job row.
type spawnContext struct {
	job           *job.Job
	pool          *pool.Pool
	project       *projectInfo
	repoTags      map[string]string
	callbackToken string
	tokenHash     string
}

type projectInfo struct {
	Name string
	Tags map[string]string
}

// spawnResult is the per-method success payload.
type spawnResult struct {
	InstanceID   string
	InstanceType string
	AZ           string
}

// spawn coordinates one spawn attempt. Returns (err, capacityExhausted).
//   - err == nil, capacityExhausted == false: success
//   - capacityExhausted == true: every type+subnet combo returned a
//     capacity error. Caller should reschedule rather than fail
//   - err != nil, capacityExhausted == false: permanent failure
func (o *Orchestrator) spawn(ctx context.Context, j *job.Job) (error, bool) {
	if j.PoolID == "" {
		return fmt.Errorf("job %s has no pool_id (webhook routing failure)", j.ID), false
	}
	pl, err := o.Runtime.Store.Pool.Get(ctx, j.PoolID)
	if err != nil {
		return fmt.Errorf("get pool: %w", err), false
	}
	if pl == nil {
		return fmt.Errorf("pool %s no longer exists", j.PoolID), false
	}
	if pl.LaunchTemplateID == "" {
		return fmt.Errorf("pool %s has no launch template (re-save it)", pl.Name), false
	}
	proj, err := o.Runtime.Store.Project.Get(ctx, j.ProjectID)
	if err != nil {
		return fmt.Errorf("get project: %w", err), false
	}
	if proj == nil {
		return fmt.Errorf("project %s no longer exists", j.ProjectID), false
	}
	// Repo lookup is best-effort -- if it's gone, fall back to no
	// repo-level tags rather than failing the spawn.
	rp, _ := o.Runtime.Store.Repo.Get(ctx, j.RepoFullName)
	var repoTags map[string]string
	if rp != nil {
		repoTags = rp.Tags
	}

	maxRuntime := pl.EffectiveMaxRuntime()
	if maxRuntime != time.Duration(pl.MaxRuntimeMinutes)*time.Minute {
		slog.Warn("orchestrator: pool.max_runtime_minutes out of range, clamping",
			"pool", pl.Name, "configured", pl.MaxRuntimeMinutes, "clamped_to", pool.MaxRuntimeMinutesCap)
	}
	ttl := maxRuntime + callbackTokenGrace
	token, hash := callback.Mint(j.ID, o.HMACKey, ttl)

	sc := &spawnContext{
		job:           j,
		pool:          pl,
		project:       &projectInfo{Name: proj.Name, Tags: proj.Tags},
		repoTags:      repoTags,
		callbackToken: token,
		tokenHash:     hash,
	}

	method := pl.SpawnMethod
	if method == "" {
		method = SpawnMethodFleet
	}

	var (
		result  *spawnResult
		callErr error
		exhaust bool
	)
	switch method {
	case SpawnMethodFleet:
		result, exhaust, callErr = o.spawnFleet(ctx, sc)
	case SpawnMethodRunInstances:
		result, exhaust, callErr = o.spawnRunInstances(ctx, sc)
	default:
		return fmt.Errorf("invalid pool spawn_method %q (want %q or %q)",
			method, SpawnMethodFleet, SpawnMethodRunInstances), false
	}
	if exhaust {
		return callErr, true
	}
	if callErr != nil {
		return callErr, false
	}

	// EC2 has an instance for us now. Bookkeeping and any rollback
	// must not be cut short by ctx cancellation or the instance
	// becomes an untracked orphan.
	bctx, cancel := detach(ctx)
	defer cancel()
	return o.recordSpawn(bctx, sc, result), false
}

// recordSpawn persists the instance row, stamps the job, audit-logs,
// and best-effort post-tags the per-spawn metadata Fleet couldn't
// carry through CreateFleet's API surface.
func (o *Orchestrator) recordSpawn(ctx context.Context, sc *spawnContext, r *spawnResult) error {
	now := time.Now().UTC()

	// Best-effort pricing snapshot: stamp the launch-time USD/hour
	// so cost rollups can multiply by elapsed time at completion.
	// Failures here just leave the price NULL -- never abort spawn.
	pricePerHour, priceModel := o.snapshotPrice(ctx, r.InstanceType, r.AZ, sc.pool.Spot)

	if err := o.Runtime.Store.Instance.Put(ctx, &instance.Instance{
		ID:           r.InstanceID,
		JobID:        sc.job.ID,
		ProjectID:    sc.job.ProjectID,
		PoolID:       sc.pool.ID,
		InstanceType: r.InstanceType,
		AZ:           r.AZ,
		State:        instance.StateStarting,
		Spot:         sc.pool.Spot,
		PricePerHour: pricePerHour,
		PriceModel:   priceModel,
		LaunchedAt:   now,
	}); err != nil {
		// Bookkeeping lost: the instance is up and tagged but has no
		// DB row, so the reaper's ListAlive sweep won't touch it.
		// Best-effort terminate to avoid an EBS-burn orphan.
		o.rollbackOrphanInstance(ctx, sc, r, "instance_put", err)
		return fmt.Errorf("instance store put: %w", err)
	}

	if err := o.Runtime.Store.Job.StampSpawn(ctx, sc.job.ID, r.InstanceID, sc.tokenHash, sc.callbackToken); err != nil {
		// Without this stamp the runner can't authenticate against
		// /api/runner/register. The spawn would burn a full max_runtime
		// before the reaper noticed. Roll back: terminate the instance
		// and mark the local row terminated so the reaper skips it.
		slog.Error("orchestrator: job stamp spawn failed; rolling back spawn",
			"job_id", sc.job.ID, "instance_id", r.InstanceID, "err", err)
		o.rollbackInstance(ctx, r.InstanceID, sc.job.ID, "stamp_spawn", err)
		if errors.Is(err, jobstore.ErrJobMissing) {
			return fmt.Errorf("job stamp spawn: job row vanished mid-flight: %w", err)
		}
		return fmt.Errorf("job stamp spawn: %w", err)
	}

	o.auditAction(ctx, audit.ActionInstanceLaunched, "instance", r.InstanceID, map[string]any{
		"job_id":  sc.job.ID,
		"project": sc.project.Name,
		"pool":    sc.pool.Name,
		"type":    r.InstanceType,
		"spot":    sc.pool.Spot,
	})
	slog.Info("orchestrator: spawned",
		"job_id", sc.job.ID, "instance_id", r.InstanceID, "type", r.InstanceType,
		"project", sc.project.Name, "pool", sc.pool.Name)
	return nil
}

// rollbackOrphanInstance handles the case where the EC2 instance is
// up but Instance.Put failed: terminate it best-effort and audit the
// orphan so operators have a paper trail when the terminate also fails
// (rate limits, IAM hiccups). The instance has no DB row, so the
// reaper sweep can't help.
func (o *Orchestrator) rollbackOrphanInstance(ctx context.Context, sc *spawnContext, r *spawnResult, stage string, cause error) {
	slog.Error("orchestrator: instance store put failed; terminating orphan",
		"instance_id", r.InstanceID, "stage", stage, "err", cause)
	_, terr := o.Runtime.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{r.InstanceID},
	})
	if terr != nil {
		slog.Error("orchestrator: orphan terminate failed; clean up via EC2 console",
			"instance_id", r.InstanceID, "err", terr)
	}
	o.auditAction(ctx, audit.ActionInstanceLaunched, "instance", r.InstanceID, map[string]any{
		"orphan":       true,
		"stage":        stage,
		"job_id":       sc.job.ID,
		"pool":         sc.pool.Name,
		"type":         r.InstanceType,
		"terminate_ok": terr == nil,
	})
}

// rollbackInstance unwinds a spawn after Instance.Put already wrote
// the row but a subsequent step (StampSpawn) failed. Terminates the
// EC2 instance and marks the local row terminated so ListAlive
// doesn't keep handing it to the reaper.
func (o *Orchestrator) rollbackInstance(ctx context.Context, instanceID, jobID, stage string, cause error) {
	now := time.Now().UTC()
	if _, terr := o.Runtime.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}); terr != nil {
		slog.Error("orchestrator: rollback terminate failed; clean up via EC2 console",
			"instance_id", instanceID, "err", terr)
	}
	if err := o.Runtime.Store.Instance.UpdateState(ctx, instanceID, instance.StateTerminated, now); err != nil {
		slog.Error("orchestrator: rollback update instance state failed",
			"instance_id", instanceID, "err", err)
	}
	// terminated_at is stamped above. Finalize the job's cost
	// against the actual billable window even though this row never
	// successfully booked the spawn (the brief launch-then-rollback
	// window is still billable).
	if err := o.Runtime.Store.Job.FinalizeCost(ctx, instanceID); err != nil {
		slog.Warn("orchestrator: rollback finalize cost failed", "instance_id", instanceID, "err", err)
	}
	o.auditAction(ctx, audit.ActionInstanceTerminated, "instance", instanceID, map[string]any{
		"rollback": true,
		"stage":    stage,
		"job_id":   jobID,
		"err":      cause.Error(),
	})
}

// resolveSubnetAZ returns the availability zone for a subnet, hitting
// DescribeSubnets at most once per subnet (results are cached on the
// Orchestrator). Returns "" on any error so callers degrade to a
// NULL price_per_hour rather than failing the spawn.
//
// Why this exists: CreateFleet's response carries the chosen
// SubnetId in LaunchTemplateAndOverrides but leaves
// AvailabilityZone unset (since our overrides are subnet-keyed, not
// AZ-keyed). The pricing fetcher needs an AZ for spot lookups, so
// we map subnet -> AZ via DescribeSubnets. Subnet AZ is a stable
// property of the subnet itself, so caching is sound.
func (o *Orchestrator) resolveSubnetAZ(ctx context.Context, subnetID string) string {
	if subnetID == "" {
		return ""
	}
	if v, ok := o.subnetAZ.Load(subnetID); ok {
		return v.(string)
	}
	out, err := o.Runtime.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil || len(out.Subnets) == 0 {
		return ""
	}
	az := aws.ToString(out.Subnets[0].AvailabilityZone)
	if az != "" {
		o.subnetAZ.Store(subnetID, az)
	}
	return az
}

// snapshotPrice returns the launch-time USD/hour quote for the
// chosen (instanceType, az, spot) tuple. Errors are logged and the
// returned pointer is nil -- callers stamp NULL price_per_hour /
// price_model and the cost rollup later skips this instance.
func (o *Orchestrator) snapshotPrice(ctx context.Context, instanceType, az string, spot bool) (*float64, string) {
	if o.Runtime.Pricing == nil {
		return nil, ""
	}
	usd, model, err := o.Runtime.Pricing.AtLaunch(ctx, instanceType, az, spot)
	if err != nil {
		slog.Warn("orchestrator: pricing snapshot failed",
			"instance_type", instanceType, "az", az, "spot", spot, "err", err)
		return nil, ""
	}
	return &usd, model
}

// buildTagSpecs assembles the per-spawn TagSpecifications used by
// the RunInstances path. Layered later-overrides-earlier: project ->
// pool -> repo -> gha:* tool taxonomy. The gha:* tags are stamped
// last so user tags can never accidentally shadow them.
//
// Note: with the Fleet path, instance/volume tags come from the LT
// (project + pool + gha:{managed-by,project,pool}). Per-job + repo
// tags + the callback-token tag are added by spawnFleet via a
// post-launch CreateTags call (see postLaunchTags).
func buildTagSpecs(sc *spawnContext) []ec2types.TagSpecification {
	tags := buildAllTags(sc)
	return []ec2types.TagSpecification{
		{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
		{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
	}
}

// buildAllTags returns the merged tag set as a flat []ec2types.Tag.
// Used by buildTagSpecs (RunInstances) -- the full set is stamped
// atomically at RunInstances time. Per-job state (the HMAC callback
// token) does NOT travel here: the in-instance bootstrap script
// fetches it from /api/runner/bootstrap using the global bootstrap
// API token baked into user-data.
func buildAllTags(sc *spawnContext) []ec2types.Tag {
	merged := ec2lt.MergeTags(sc.project.Tags, sc.pool.Tags, sc.repoTags)
	ghaTags := []ec2types.Tag{
		{Key: aws.String(ec2lt.ManagedByTagKey), Value: aws.String(ec2lt.ManagedByTagValue)},
		{Key: aws.String("gha:project"), Value: aws.String(sc.project.Name)},
		{Key: aws.String("gha:pool"), Value: aws.String(sc.pool.Name)},
		{Key: aws.String("gha:job_id"), Value: aws.String(sc.job.ID)},
		{Key: aws.String("gha:repo"), Value: aws.String(sc.job.RepoFullName)},
	}
	tags := make([]ec2types.Tag, 0, len(merged)+len(ghaTags))
	for k, v := range merged {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	tags = append(tags, ghaTags...)
	return tags
}

// isCapacityError detects transient EC2 capacity-related failures.
// Prefers smithy.APIError typed-code matching when AWS surfaces one.
// Falls back to substring matching for paths where the SDK only
// returns a wrapped *smithy.GenericAPIError or a plain string (notably
// CreateFleet's Errors[] response array, which is just text codes).
// Add new substrings to isCapacityErrorString when AWS surfaces a new
// fallback-worthy error.
func isCapacityError(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		if isCapacityErrorString(apiErr.ErrorCode()) {
			return true
		}
	}
	return isCapacityErrorString(err.Error())
}

// capacityErrorCodes are the exact EC2 error codes that mean "no
// capacity right now, try again later". Fleet's Errors[] carries bare
// codes, so exact matching keeps permanent codes such as
// UnsupportedOperation from being mistaken for Unsupported.
var capacityErrorCodes = map[string]bool{
	"InsufficientInstanceCapacity": true,
	"Unsupported":                  true,
	"SpotMaxPriceTooLow":           true,
	"InstanceLimitExceeded":        true,
	"UnfulfillableCapacity":        true,
}

// isCapacityErrorCode is the exact-match check for bare EC2 error codes.
func isCapacityErrorCode(code string) bool {
	return capacityErrorCodes[code]
}

// isCapacityErrorString is the substring fallback for wrapped error
// text where the code is embedded in a longer message.
func isCapacityErrorString(s string) bool {
	for code := range capacityErrorCodes {
		if code == "Unsupported" {
			continue
		}
		if strings.Contains(s, code) {
			return true
		}
	}
	// "Unsupported" alone is too broad as a substring. Match the
	// message shape RunInstances uses for it.
	return strings.Contains(s, "Unsupported:") || strings.HasSuffix(s, "Unsupported")
}

// auditAction is the orchestrator's best-effort audit helper. detail
// is a map so callers can express the structured shape directly.
// audit.Detail handles the JSON encoding (proper escaping for the
// occasional Unicode in instance types or error strings).
func (o *Orchestrator) auditAction(ctx context.Context, action, targetType, targetID string, detail map[string]any) {
	if err := o.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.NewString(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     audit.Detail(detail),
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		slog.Warn("orchestrator: audit write failed", "action", action, "target_id", targetID, "err", err)
	}
}
