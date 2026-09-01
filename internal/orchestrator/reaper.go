// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	jobstore "github.com/yousysadmin/pacer/internal/domain/job"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

// healthComponent is the key the reaper uses on Runtime.Health so the
// /api/health endpoint and the UI banner can pick out the reaper's
// failures specifically. Centralized as a constant so the manual
// reconcile endpoint reads from the same key.
const healthComponent = "reaper"

// ec2API is the slice of the EC2 client surface the reaper uses.
// Narrow on purpose so tests can stub it without spinning up the full
// *ec2.Client. *ec2.Client satisfies this naturally.
type ec2API interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}

const ReapInterval = 60 * time.Second

type Reaper struct {
	Runtime *env.Runtime
	// mu serializes sweeps: the background ticker and the manual
	// /api/reconcile endpoint both call Tick, and two overlapping
	// sweeps over the same alive-instance snapshot would duplicate
	// side effects (TerminateInstances, audit rows, GitHub runner
	// deletes) and race their Health verdicts.
	mu sync.Mutex
}

func NewReaper(rt *env.Runtime) *Reaper {
	return &Reaper{Runtime: rt}
}

// Run sweeps every ReapInterval until ctx is cancelled.
//
// Each tick does two things, in this order:
//
//  1. EC2-side health check: ask AWS for the live state of every
//     instance the DB still considers alive. Any instance AWS reports
//     as terminated/stopping/stopped/shutting-down - or no longer
//     recognizes at all - gets marked lost: instance row to
//     "terminated", job to "failed" with stage="ec2". This catches
//     spot reclaims, host failures, and console-side terminations
//     where the runner died too abruptly to fire /api/runner/complete.
//     Without this pass the row sits "running" until the max-runtime
//     cutoff fires, which is typically minutes-to-an-hour past when
//     GitHub already marked the workflow_job as "lost communication".
//
//  2. Max-runtime check: for each instance still considered alive,
//     look up its pool's max_runtime_minutes. Past cutoff, hard-kill
//     via TerminateInstances and mark the job reaped. Covers the
//     stuck-but-alive case (runner hung, workflow looping forever).
//
// Crashed-runner / orphan handling (instance up but never registered)
// is covered by the same age check - starting-state rows with no
// registered_at hit the timeout and get terminated.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(ReapInterval)
	defer t.Stop()

	_, _ = r.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("reaper stopping")
			return
		case <-t.C:
			_, _ = r.Tick(ctx)
		}
	}
}

// Tick runs one sweep under panic recovery. A recovered panic flips
// Runtime.Health to "panic: ..." and is returned as an error so the
// HTTP reconcile path can surface it synchronously. The goroutine
// driving Run survives - the next ticker fire calls Tick again.
//
// Returns (checked, err) where checked is the number of alive rows
// inspected this sweep (zero on panic or ListAlive failure).
func (r *Reaper) Tick(ctx context.Context) (checked int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err = safeTick(r.Runtime, healthComponent, func() error {
		c, e := r.doTick(ctx)
		checked = c
		return e
	})
	return checked, err
}

// safeTick wraps do under recover(). On panic: log stack, write
// healthComponent on Runtime.Health, return the panic as an error.
// On clean exit: return do's error untouched. The Runtime + Health
// guards keep tests that pass a bare *Reaper (no Runtime) from
// crashing here before the panic-recovery path can fire.
func safeTick(rt *env.Runtime, component string, do func() error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			slog.Error(component+": panic recovered in tick", "panic", rec, "stack", string(stack))
			if rt != nil && rt.Health != nil {
				rt.Health.Set(component, fmt.Sprintf("panic: %v", rec))
			}
			err = fmt.Errorf("%s panic: %v", component, rec)
		}
	}()
	return do()
}

// doTick is the original tick body, hoisted to a method so safeTick
// can call it under recover. Returns the number of alive rows seen +
// any fatal error (today only ListAlive can fatal-error, per-instance
// errors are logged and the sweep continues).
func (r *Reaper) doTick(ctx context.Context) (int, error) {
	insts, err := r.Runtime.Store.Instance.ListAlive(ctx)
	if err != nil {
		slog.Error("reaper: list alive failed", "err", err)
		return 0, err
	}
	if len(insts) == 0 {
		// Nothing alive means nothing to reconcile: not evidence the
		// EC2 path is healthy, so we don't touch Health here.
		return 0, nil
	}

	view := checkEC2HealthVia(ctx, r.Runtime.EC2, r.Runtime.Health, insts)

	// Stamp the per-row heartbeat for every instance AWS just
	// confirmed alive. This is the signal the UI uses to flag a
	// stale row - "running for 20m but last_seen_at = 20m ago"
	// means the reaper isn't visiting this row, regardless of
	// whether the global health banner shows green. Best-effort:
	// a Touch failure logs + we still run the maybeReap loop.
	if len(view.SeenAlive) > 0 {
		if err := r.Runtime.Store.Instance.Touch(ctx, view.SeenAlive, time.Now().UTC()); err != nil {
			slog.Warn("reaper: heartbeat touch failed",
				"count", len(view.SeenAlive), "err", err)
		}
	}

	for _, i := range insts {
		if d, isDead := view.Dead[i.ID]; isDead {
			r.markLost(ctx, i, d)
			continue
		}
		if err := r.maybeReap(ctx, i); err != nil {
			slog.Error("reaper: failed", "instance_id", i.ID, "err", err)
		}
	}
	return len(insts), nil
}

// deadState carries AWS's verdict for an instance that is no longer
// healthy. An empty StateName means AWS no longer returns the instance
// from DescribeInstances at all (the row was purged ~1h after
// termination). We still treat it as lost.
type deadState struct {
	StateName       string
	StateReasonCode string
	StateReason     string
}

// sweepView is the structured output of one EC2-side health pass.
// Dead is the subset AWS considers gone (terminated/stopping/...).
// SeenAlive is every ID AWS confirmed in any non-dead state. The
// reaper bumps last_seen_at on SeenAlive so the UI can show a
// per-row heartbeat - the signal the operator needs to spot
// "instance X hasn't been reconciled in 30 minutes" without
// trusting the absence of a global error banner.
//
// Dead-state rows already get their last_seen_at stamped through
// markLost -> UpdateState, so they're intentionally NOT in SeenAlive.
type sweepView struct {
	Dead      map[string]deadState
	SeenAlive []string
}

// checkEC2Health batches DescribeInstances over every alive instance
// and returns the subset AWS considers dead. Healthy (pending /
// running) instances are not in the map.
//
// Strategy: one batched call covers the common case (every ID known
// to AWS, mix of healthy + dead states). On an InvalidInstanceID.
// NotFound error - which fails the entire batch even if only one ID
// is bad - we parse the missing IDs out of the error message, mark
// them lost, and re-batch the survivors. On any other error we log
// and skip the health pass. The max-runtime check below still runs,
// so a flaky describe call doesn't strand a dead row.
// checkEC2HealthVia is the testable form of the EC2-side health check.
// Separated from the *Reaper method so tests can swap a fake ec2API
// without standing up a Runtime. The production path is the
// one-line *Reaper.checkEC2Health wrapper below.
//
// h is optional. When non-nil, this function is also the gate that
// keeps Runtime.Health in sync with the reaper's view of AWS: a
// successful describe call clears the "reaper" entry, a non-NotFound
// error writes it. NotFound is treated as a normal outcome (AWS
// purged a row we used to know about) and does NOT toggle Health.
//
// Returns a sweepView with both AWS's "dead" verdicts AND the IDs
// AWS confirmed are still alive - the caller stamps last_seen_at
// on the alive set so the UI gets a per-row heartbeat.
func checkEC2HealthVia(ctx context.Context, c ec2API, h *health.Health, insts []*instance.Instance) sweepView {
	view := sweepView{Dead: map[string]deadState{}}
	if len(insts) == 0 {
		return view
	}
	ids := make([]string, 0, len(insts))
	for _, i := range insts {
		ids = append(ids, i.ID)
	}

	resp, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	})
	if err == nil {
		mergeSweep(&view, resp)
		if h != nil {
			h.Clear(healthComponent)
		}
		return view
	}

	ae, ok := errors.AsType[smithy.APIError](err)
	if !ok || ae.ErrorCode() != "InvalidInstanceID.NotFound" {
		slog.Warn("reaper: describe instances failed; skipping ec2 health pass", "err", err)
		if h != nil {
			h.Set(healthComponent, fmt.Sprintf("describe instances failed: %v", err))
		}
		return view
	}

	missing := parseNotFoundIDs(ae.ErrorMessage())
	for _, id := range missing {
		view.Dead[id] = deadState{} // empty StateName - vanished from AWS
	}
	remaining := excludeIDs(ids, missing)
	if len(remaining) == 0 {
		if h != nil {
			h.Clear(healthComponent)
		}
		return view
	}
	resp2, err2 := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: remaining,
	})
	if err2 != nil {
		slog.Warn("reaper: describe instances retry failed; partial ec2 health pass",
			"err", err2, "missing", len(missing), "remaining", len(remaining))
		if h != nil {
			h.Set(healthComponent, fmt.Sprintf("describe instances retry failed: %v", err2))
		}
		return view
	}
	mergeSweep(&view, resp2)
	if h != nil {
		h.Clear(healthComponent)
	}
	return view
}

func mergeSweep(view *sweepView, resp *ec2.DescribeInstancesOutput) {
	for _, res := range resp.Reservations {
		for _, inst := range res.Instances {
			id := aws.ToString(inst.InstanceId)
			if id == "" {
				continue
			}
			if inst.State == nil {
				continue
			}
			switch inst.State.Name {
			case ec2types.InstanceStateNameTerminated,
				ec2types.InstanceStateNameStopping,
				ec2types.InstanceStateNameStopped,
				ec2types.InstanceStateNameShuttingDown:
				ds := deadState{StateName: string(inst.State.Name)}
				if inst.StateReason != nil {
					ds.StateReasonCode = aws.ToString(inst.StateReason.Code)
					ds.StateReason = aws.ToString(inst.StateReason.Message)
				}
				view.Dead[id] = ds
			default:
				// pending / running / anything else AWS confirms is
				// not dead is treated as alive for heartbeat purposes.
				view.SeenAlive = append(view.SeenAlive, id)
			}
		}
	}
}

// parseNotFoundIDs pulls instance IDs out of an InvalidInstanceID.
// NotFound error message. AWS formats it as:
//
//	The instance IDs 'i-abc, i-def' do not exist
//
// Best-effort: an unrecognized format returns nil and the caller
// degrades to "skip the health pass" cleanly.
func parseNotFoundIDs(msg string) []string {
	_, after, ok := strings.Cut(msg, "'")
	if !ok {
		return nil
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, "'")
	if !ok0 {
		return nil
	}
	var out []string
	for p := range strings.SplitSeq(before0, ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "i-") {
			out = append(out, p)
		}
	}
	return out
}

func excludeIDs(all, drop []string) []string {
	if len(drop) == 0 {
		return all
	}
	dropSet := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropSet[d] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, x := range all {
		if _, skip := dropSet[x]; !skip {
			out = append(out, x)
		}
	}
	return out
}

// markLost records that AWS killed the host before the runner could
// fire /api/runner/complete. Flips the instance row to terminated and,
// if the job is still in flight, marks it failed with stage="ec2" so
// the slot frees up and the workflow stops blocking on a runner that
// is never coming back.
//
// That verdict is final: 'failed' is a terminal status, so the
// workflow_job=completed webhook GitHub sends later is dropped by the
// Mark* guard rather than refining it. Waiting for GitHub instead is
// worse - the heartbeat timeout runs ~10 minutes, and the job holds a
// concurrency slot for all of it on a host that no longer exists.
func (r *Reaper) markLost(ctx context.Context, i *instance.Instance, d deadState) {
	now := time.Now().UTC()

	// markLost removes the row from ListAlive, so this is the last
	// time pacer will ever look at this instance. A stopping/stopped
	// host is NOT terminal in EC2 - it still exists and bills for its
	// EBS volumes - so terminate it for real before we forget it.
	if r.Runtime.EC2 != nil {
		terminateLostVia(ctx, r.Runtime.EC2, i.ID, d)
	}

	if err := r.Runtime.Store.Instance.UpdateState(ctx, i.ID, instance.StateTerminated, now); err != nil {
		slog.Error("reaper: mark instance terminated failed", "instance_id", i.ID, "err", err)
	}

	j := r.jobOnInstance(ctx, i)
	if j != nil && jobInFlight(j.Status) {
		msg := lostReasonMessage(d)
		if err := r.Runtime.Store.Job.MarkFailed(ctx, j.ID, "ec2", msg, now); err != nil {
			slog.Error("reaper: mark job failed write failed", "job_id", j.ID, "err", err)
		}
		_ = r.Runtime.Store.Audit.Put(ctx, &audit.Entry{
			ID:         uuid.New().String(),
			Action:     audit.ActionJobFailed,
			TargetType: "job",
			TargetID:   j.ID,
			Detail: audit.Detail(map[string]any{
				"stage":             "ec2",
				"aws_state":         d.StateName,
				"state_reason_code": d.StateReasonCode,
				"state_reason":      d.StateReason,
			}),
			OccurredAt: now,
		})
	}

	// Best-effort: deregister the runner from GitHub. With an active
	// workflow_job, GitHub aborts it immediately on delete - the user
	// sees the workflow fail in seconds rather than waiting on the
	// ~10-min heartbeat timeout.
	r.deleteGitHubRunner(ctx, i, j)

	// Refine cost from the now-stamped terminated_at. Best-effort: a
	// NULL price_per_hour leaves cost NULL.
	if err := r.Runtime.Store.Job.FinalizeCost(ctx, i.ID); err != nil {
		slog.Warn("reaper: finalize cost failed", "instance_id", i.ID, "err", err)
	}

	poolName := ""
	if i.PoolID != "" {
		if pl, perr := r.Runtime.Store.Pool.Get(ctx, i.PoolID); perr == nil && pl != nil {
			poolName = pl.Name
		}
	}
	_ = r.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     audit.ActionInstanceLost,
		TargetType: "instance",
		TargetID:   i.ID,
		Detail: audit.Detail(map[string]any{
			"job_id":            i.JobID,
			"pool":              poolName,
			"aws_state":         d.StateName,
			"state_reason_code": d.StateReasonCode,
			"state_reason":      d.StateReason,
		}),
		OccurredAt: now,
	})

	slog.Warn("reaper: instance lost",
		"instance_id", i.ID, "job_id", i.JobID,
		"aws_state", d.StateName, "reason_code", d.StateReasonCode)
}

// terminateLostVia issues a best-effort TerminateInstances for a lost
// instance that AWS reports in a stopping/stopped state. Those states
// are resumable, not terminal: the host keeps existing (and billing
// for EBS) until someone terminates it, and once markLost runs the
// reaper never revisits the row. terminated / shutting-down / vanished
// instances need no call. Returns whether a terminate was attempted so
// tests can pin the decision table. Failures are logged only - the
// DB-side cleanup must proceed regardless.
func terminateLostVia(ctx context.Context, c ec2API, id string, d deadState) bool {
	switch d.StateName {
	case string(ec2types.InstanceStateNameStopping), string(ec2types.InstanceStateNameStopped):
	default:
		return false
	}
	if _, err := c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{id},
	}); err != nil {
		slog.Warn("reaper: terminate of stopped instance failed; clean up via EC2 console",
			"instance_id", id, "aws_state", d.StateName, "err", err)
		return true
	}
	slog.Info("reaper: terminated stopped instance", "instance_id", id, "aws_state", d.StateName)
	return true
}

// jobInFlight reports whether a job is still occupying a pool slot
// (i.e. has been claimed but not reached a terminal state yet). Used
// to gate MarkFailed in markLost so we don't regress an already-
// completed job back to failed if the webhook beat us.
func jobInFlight(s jobmodel.Status) bool {
	return s == jobmodel.StatusClaimed ||
		s == jobmodel.StatusStarting ||
		s == jobmodel.StatusRunning
}

// jobOnInstance answers "whose work am I about to end" - the only job
// the reaper may pass a verdict on when it kills or buries a host.
//
// The lookup deliberately does not go through instances.job_id. That
// column records which job the machine was LAUNCHED for, and GitHub
// does not honour that pairing: every runner in a pool advertises
// identical labels, so a queued job goes to whichever one is free.
// jobs.runner_instance_id carries GitHub's own account of where the
// job ran (see job.Store.GetByInstanceID for how the two are ranked).
//
// nil means nobody's work is on this host, and the caller then
// terminates it without touching any job's status.
func (r *Reaper) jobOnInstance(ctx context.Context, i *instance.Instance) *jobmodel.Job {
	j, err := r.Runtime.Store.Job.GetByInstanceID(ctx, i.ID)
	if err != nil {
		slog.Error("reaper: lookup job on instance failed", "instance_id", i.ID, "err", err)
		return nil
	}
	if j == nil && i.JobID != "" {
		slog.Info("reaper: instance holds no live job",
			"instance_id", i.ID, "launched_for", i.JobID)
	}
	return j
}

func lostReasonMessage(d deadState) string {
	switch {
	case d.StateReasonCode != "" && d.StateReason != "":
		return fmt.Sprintf("instance %s (%s)", d.StateReasonCode, d.StateReason)
	case d.StateReasonCode != "":
		return "instance " + d.StateReasonCode
	case d.StateName != "":
		return "instance state=" + d.StateName
	default:
		return "instance no longer recognized by EC2"
	}
}

func (r *Reaper) maybeReap(ctx context.Context, i *instance.Instance) error {
	return r.reapVia(ctx, r.Runtime.EC2, i)
}

// reapVia is maybeReap with the EC2 client injected, so the decision
// table (who gets terminated, whose job gets the verdict) is testable
// without a live client. Same split as checkEC2HealthVia.
func (r *Reaper) reapVia(ctx context.Context, c ec2API, i *instance.Instance) error {
	if i.PoolID == "" {
		// Pre-pools instance - skip. Operator can clean up via console.
		return nil
	}
	pl, err := r.Runtime.Store.Pool.Get(ctx, i.PoolID)
	if err != nil {
		return err
	}
	// A deleted pool must not leave its instances unreapable. Fall
	// back to the global cap so they are still terminated eventually.
	poolName := "<deleted>"
	if pl != nil {
		poolName = pl.Name
	} else {
		slog.Warn("reaper: pool missing for live instance, using max runtime cap",
			"instance_id", i.ID, "pool_id", i.PoolID)
	}

	maxRuntime := pl.EffectiveMaxRuntime()
	age := time.Since(i.LaunchedAt)
	if age < maxRuntime {
		return nil
	}

	slog.Warn("reaper: terminating stuck instance",
		"instance_id", i.ID, "job_id", i.JobID, "pool", poolName,
		"age", age.String(), "max_runtime", maxRuntime.String())

	// Best-effort runner deregister BEFORE we hard-kill the host:
	// gives GitHub a clean abort signal so the workflow_job fails fast
	// instead of relying on heartbeat timeout.
	j := r.jobOnInstance(ctx, i)
	r.deleteGitHubRunner(ctx, i, j)

	if _, err := c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{i.ID},
	}); err != nil {
		return fmt.Errorf("terminate %s: %w", i.ID, err)
	}

	now := time.Now().UTC()
	if err := r.Runtime.Store.Instance.UpdateState(ctx, i.ID, instance.StateReaped, now); err != nil {
		slog.Error("reaper: update instance state failed", "err", err)
	}
	// Only the job actually on this host gets reaped. jobOnInstance
	// returns nil when the host is running nobody's work, and then the
	// termination above stands on its own.
	if j == nil {
		slog.Warn("reaper: instance terminated with no job to reap", "instance_id", i.ID)
	} else if err := r.Runtime.Store.Job.MarkReaped(ctx, j.ID, now); err != nil {
		// A job the webhook already finalized keeps its status. The
		// instance row and cost are still updated below.
		if errors.Is(err, jobstore.ErrStatusConflict) {
			slog.Debug("reaper: job already terminal, instance reaped without status change", "job_id", i.JobID)
		} else {
			slog.Error("reaper: mark job reaped failed", "err", err)
		}
	}
	// Now that terminated_at is stamped, refine the cost stamp from
	// the workflow-completion estimate (which missed the time spent
	// stuck) to the actual billable window. Best-effort: a NULL
	// price_per_hour leaves cost NULL.
	if err := r.Runtime.Store.Job.FinalizeCost(ctx, i.ID); err != nil {
		slog.Warn("reaper: finalize cost failed", "instance_id", i.ID, "err", err)
	}
	if err := r.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     audit.ActionInstanceReaped,
		TargetType: "instance",
		TargetID:   i.ID,
		Detail: audit.Detail(map[string]any{
			"job_id":      i.JobID,
			"pool":        poolName,
			"age_seconds": int(age.Seconds()),
		}),
		OccurredAt: now,
	}); err != nil {
		slog.Warn("reaper: audit write failed", "instance_id", i.ID, "err", err)
	}
	return nil
}

// deleteGitHubRunner asks GitHub to deregister the runner backed by
// this instance. With an active workflow_job assigned to that runner,
// GitHub aborts the job immediately on delete - the user-visible
// "lost communication" hang shrinks from ~10 min to seconds.
//
// Best-effort end to end: missing GHRunnerID (runner never came online),
// missing job/project rows (deleted out from under us), or a 4xx/5xx
// from GitHub all log + return without surfacing - the local cleanup
// the caller already did is the authoritative pacer-side state.
//
// j may be nil when the caller couldn't fetch the job row. We'll skip
// silently in that case rather than try to recover.
func (r *Reaper) deleteGitHubRunner(ctx context.Context, i *instance.Instance, j *jobmodel.Job) {
	if i.GHRunnerID == 0 || j == nil || r.Runtime.GHApp == nil {
		return
	}
	proj, err := r.Runtime.Store.Project.Get(ctx, j.ProjectID)
	if err != nil || proj == nil {
		if err != nil {
			slog.Warn("reaper: project lookup for runner-delete failed",
				"instance_id", i.ID, "project_id", j.ProjectID, "err", err)
		}
		return
	}
	if proj.Scope == projectmodel.ScopeOrg {
		if err := r.Runtime.GHApp.DeleteRunnerOrg(ctx, j.InstallationID, proj.OrgName, i.GHRunnerID); err != nil {
			slog.Warn("reaper: delete runner (org) failed",
				"instance_id", i.ID, "gh_runner_id", i.GHRunnerID, "org", proj.OrgName, "err", err)
		}
		return
	}
	owner, name, splitErr := splitRepoFullName(j.RepoFullName)
	if splitErr != nil {
		slog.Warn("reaper: malformed repo full_name; skipping runner-delete",
			"instance_id", i.ID, "repo", j.RepoFullName, "err", splitErr)
		return
	}
	if err := r.Runtime.GHApp.DeleteRunnerRepo(ctx, j.InstallationID, owner, name, i.GHRunnerID); err != nil {
		slog.Warn("reaper: delete runner (repo) failed",
			"instance_id", i.ID, "gh_runner_id", i.GHRunnerID, "repo", j.RepoFullName, "err", err)
	}
}

func splitRepoFullName(s string) (owner, name string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed repo full_name %q", s)
	}
	return parts[0], parts[1], nil
}
