// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/instance"
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
// For each instance in starting/running state: look up its pool's
// max_runtime_minutes, and TerminateInstances if the instance has
// been alive longer than that.
// Marks the instance reaped and the job reaped in lockstep.
//
// Crashed-runner / orphan handling (instance up but never registered)
// is covered by the same age check - starting-state rows with no
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
	for _, i := range insts {
		if err := r.maybeReap(ctx, i); err != nil {
			slog.Error("reaper: failed", "instance_id", i.ID, "err", err)
		}
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
