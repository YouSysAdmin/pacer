// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

const ReapInterval = 60 * time.Second

type Reaper struct {
	Runtime *env.Runtime
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
//     as terminated/stopping/stopped/shutting-down -- or no longer
//     recognizes at all -- gets marked lost: instance row to
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
// is covered by the same age check -- starting-state rows with no
// registered_at hit the timeout and get terminated.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(ReapInterval)
	defer t.Stop()

	r.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("reaper stopping")
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Reaper) tick(ctx context.Context) {
	insts, err := r.Runtime.Store.Instance.ListAlive(ctx)
	if err != nil {
		slog.Error("reaper: list alive failed", "err", err)
		return
	}
	if len(insts) == 0 {
		return
	}

	dead := r.checkEC2Health(ctx, insts)

	for _, i := range insts {
		if d, isDead := dead[i.ID]; isDead {
			r.markLost(ctx, i, d)
			continue
		}
		if err := r.maybeReap(ctx, i); err != nil {
			slog.Error("reaper: failed", "instance_id", i.ID, "err", err)
		}
	}
}

// deadState carries AWS's verdict for an instance that is no longer
// healthy. An empty StateName means AWS no longer returns the instance
// from DescribeInstances at all (the row was purged ~1h after
// termination); we still treat it as lost.
type deadState struct {
	StateName       string
	StateReasonCode string
	StateReason     string
}

// checkEC2Health batches DescribeInstances over every alive instance
// and returns the subset AWS considers dead. Healthy (pending /
// running) instances are not in the map.
//
// Strategy: one batched call covers the common case (every ID known
// to AWS, mix of healthy + dead states). On an InvalidInstanceID.
// NotFound error -- which fails the entire batch even if only one ID
// is bad -- we parse the missing IDs out of the error message, mark
// them lost, and re-batch the survivors. On any other error we log
// and skip the health pass; the max-runtime check below still runs,
// so a flaky describe call doesn't strand a dead row.
func (r *Reaper) checkEC2Health(ctx context.Context, insts []*instance.Instance) map[string]deadState {
	out := map[string]deadState{}
	if len(insts) == 0 {
		return out
	}
	ids := make([]string, 0, len(insts))
	for _, i := range insts {
		ids = append(ids, i.ID)
	}

	resp, err := r.Runtime.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	})
	if err == nil {
		mergeDead(out, resp)
		return out
	}

	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidInstanceID.NotFound" {
		slog.Warn("reaper: describe instances failed; skipping ec2 health pass", "err", err)
		return out
	}

	missing := parseNotFoundIDs(ae.ErrorMessage())
	for _, id := range missing {
		out[id] = deadState{} // empty StateName -- vanished from AWS
	}
	remaining := excludeIDs(ids, missing)
	if len(remaining) == 0 {
		return out
	}
	resp2, err2 := r.Runtime.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: remaining,
	})
	if err2 != nil {
		slog.Warn("reaper: describe instances retry failed; partial ec2 health pass",
			"err", err2, "missing", len(missing), "remaining", len(remaining))
		return out
	}
	mergeDead(out, resp2)
	return out
}

func mergeDead(out map[string]deadState, resp *ec2.DescribeInstancesOutput) {
	for _, res := range resp.Reservations {
		for _, inst := range res.Instances {
			if inst.State == nil {
				continue
			}
			id := aws.ToString(inst.InstanceId)
			if id == "" {
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
				out[id] = ds
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
	start := strings.Index(msg, "'")
	if start < 0 {
		return nil
	}
	rest := msg[start+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return nil
	}
	var out []string
	for _, p := range strings.Split(rest[:end], ",") {
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
// is never coming back. The eventual workflow_job=completed webhook
// from GitHub (heartbeat-timeout path) will overwrite the failure
// message with its own conclusion -- harmless, both signals agree the
// job failed.
func (r *Reaper) markLost(ctx context.Context, i *instance.Instance, d deadState) {
	now := time.Now().UTC()

	if err := r.Runtime.Store.Instance.UpdateState(ctx, i.ID, instance.StateTerminated, now); err != nil {
		slog.Error("reaper: mark instance terminated failed", "instance_id", i.ID, "err", err)
	}

	j, err := r.Runtime.Store.Job.Get(ctx, i.JobID)
	if err != nil {
		slog.Error("reaper: get job failed", "job_id", i.JobID, "err", err)
	}
	if j != nil && jobInFlight(j.Status) {
		msg := lostReasonMessage(d)
		if err := r.Runtime.Store.Job.MarkFailed(ctx, j.ID, "ec2", msg, now); err != nil {
			slog.Error("reaper: mark job failed write failed", "job_id", j.ID, "err", err)
		}
		_ = r.Runtime.Store.Audit.Put(ctx, &audit.Entry{
			ID:         uuid.NewString(),
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
	// workflow_job, GitHub aborts it immediately on delete -- the user
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
		ID:         uuid.NewString(),
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

// jobInFlight reports whether a job is still occupying a pool slot
// (i.e. has been claimed but not reached a terminal state yet). Used
// to gate MarkFailed in markLost so we don't regress an already-
// completed job back to failed if the webhook beat us.
func jobInFlight(s jobmodel.Status) bool {
	return s == jobmodel.StatusClaimed ||
		s == jobmodel.StatusStarting ||
		s == jobmodel.StatusRunning
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
	if i.PoolID == "" {
		// Pre-pools instance - skip; operator can clean up via console.
		return nil
	}
	pl, err := r.Runtime.Store.Pool.Get(ctx, i.PoolID)
	if err != nil {
		return err
	}
	if pl == nil {
		// Pool was deleted out from under the instance; skip the
		// reap pass and let an operator clean up via console.
		slog.Warn("reaper: pool missing for live instance; skipping",
			"instance_id", i.ID, "pool_id", i.PoolID)
		return nil
	}

	maxRuntime := time.Duration(pl.MaxRuntimeMinutes) * time.Minute
	age := time.Since(i.LaunchedAt)
	if age < maxRuntime {
		return nil
	}

	slog.Warn("reaper: terminating stuck instance",
		"instance_id", i.ID, "job_id", i.JobID, "pool", pl.Name,
		"age", age.String(), "max_runtime", maxRuntime.String())

	// Best-effort runner deregister BEFORE we hard-kill the host:
	// gives GitHub a clean abort signal so the workflow_job fails fast
	// instead of relying on heartbeat timeout.
	j, _ := r.Runtime.Store.Job.Get(ctx, i.JobID)
	r.deleteGitHubRunner(ctx, i, j)

	if _, err := r.Runtime.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{i.ID},
	}); err != nil {
		return fmt.Errorf("terminate %s: %w", i.ID, err)
	}

	now := time.Now().UTC()
	if err := r.Runtime.Store.Instance.UpdateState(ctx, i.ID, instance.StateReaped, now); err != nil {
		slog.Error("reaper: update instance state failed", "err", err)
	}
	if err := r.Runtime.Store.Job.MarkReaped(ctx, i.JobID, now); err != nil {
		slog.Error("reaper: mark job reaped failed", "err", err)
	}
	// Now that terminated_at is stamped, refine the cost stamp from
	// the workflow-completion estimate (which missed the time spent
	// stuck) to the actual billable window. Best-effort: a NULL
	// price_per_hour leaves cost NULL.
	if err := r.Runtime.Store.Job.FinalizeCost(ctx, i.ID); err != nil {
		slog.Warn("reaper: finalize cost failed", "instance_id", i.ID, "err", err)
	}
	if err := r.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.NewString(),
		Action:     audit.ActionInstanceReaped,
		TargetType: "instance",
		TargetID:   i.ID,
		Detail: audit.Detail(map[string]any{
			"job_id":      i.JobID,
			"pool":        pl.Name,
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
// GitHub aborts the job immediately on delete -- the user-visible
// "lost communication" hang shrinks from ~10 min to seconds.
//
// Best-effort end to end: missing GHRunnerID (runner never came online),
// missing job/project rows (deleted out from under us), or a 4xx/5xx
// from GitHub all log + return without surfacing -- the local cleanup
// the caller already did is the authoritative pacer-side state.
//
// j may be nil when the caller couldn't fetch the job row; we'll skip
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
