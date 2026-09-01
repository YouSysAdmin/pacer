// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	jobstore "github.com/yousysadmin/pacer/internal/domain/job"
	"github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/job"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

type Handler struct {
	Runtime *env.Runtime
}

// Receive is POST /api/webhook.
// GitHub retries on non-2xx within a short window, so we only return 4xx for genuinely-unrecoverable
// inputs (bad signature, malformed JSON).
// Logical drops (unbound repo, no pool match, disabled project) reply 200 so GitHub stops resending.
func (h *Handler) Receive(c *fiber.Ctx) error {
	body := c.Body()
	sig := c.Get("X-Hub-Signature-256")
	if !verifySignature(h.Runtime.Config.GitHub.WebhookSecret, body, sig) {
		slog.Warn("webhook signature invalid", "delivery", c.Get("X-GitHub-Delivery"))
		return response.Unauthorized(c, "invalid signature")
	}

	event := c.Get("X-GitHub-Event")
	delivery := c.Get("X-GitHub-Delivery")
	hadDeliveryID := delivery != ""
	if !hadDeliveryID {
		// GitHub always sets this header. Missing means it isn't GitHub
		// (e.g. an operator curl).
		// Generate a synthetic id so the delivery row isn't all-NULL, but skip dedup - random UUIDs
		// won't collide so the check would be a no-op anyway.
		delivery = uuid.New().String()
	}

	ctx := c.UserContext()
	inserted, err := persistDelivery(ctx, h.Runtime, delivery, event, body)
	if err != nil {
		slog.Error("webhook persist failed", "err", err, "delivery", delivery)
		return response.Internal(c, err)
	}
	if hadDeliveryID && !inserted {
		// GitHub is retrying a delivery we already processed (or are
		// processing concurrently).
		// Reply 200 so it stops resending.
		// We deliberately don't re-dispatch - replaying workflow_job
		// "queued" would double-enqueue, and replaying "completed"
		// would re-audit a closed job.
		slog.Info("webhook drop: duplicate delivery", "event", event, "delivery", delivery)
		return response.Success(c, fiber.Map{"status": "duplicate", "delivery": delivery})
	}

	switch event {
	case "ping":
		return response.Success(c, fiber.Map{"pong": true})
	case "workflow_job":
		return h.handleWorkflowJob(c, body, delivery)
	default:
		slog.Info("webhook event ignored", "event", event, "delivery", delivery)
		return response.NoContent(c)
	}
}

type workflowJobPayload struct {
	Action      string `json:"action"`
	WorkflowJob struct {
		ID         int64    `json:"id"`
		RunID      int64    `json:"run_id"`
		Status     string   `json:"status"`
		Conclusion string   `json:"conclusion"`
		Labels     []string `json:"labels"`
		Name       string   `json:"name"`
		// RunnerName is the only authoritative statement of which
		// machine ran this job. Null on the queued action, populated
		// from in_progress onwards. See reconcileRunner.
		RunnerID   int64  `json:"runner_id"`
		RunnerName string `json:"runner_name"`
	} `json:"workflow_job"`
	Repository struct {
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
			Type  string `json:"type"` // "User" | "Organization"
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func (h *Handler) handleWorkflowJob(c *fiber.Ctx, body []byte, delivery string) error {
	var p workflowJobPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return response.BadRequest(c, fmt.Sprintf("decode payload: %s", err))
	}
	ctx := c.UserContext()

	switch p.Action {
	case "queued":
		return h.enqueue(ctx, c, &p, body, delivery)
	case "in_progress":
		return h.markRunning(ctx, c, &p, body)
	case "completed":
		return h.markCompleted(ctx, c, &p, body)
	default:
		slog.Info("workflow_job action ignored", "action", p.Action, "delivery", delivery)
		return response.NoContent(c)
	}
}

func (h *Handler) enqueue(ctx context.Context, c *fiber.Ctx, p *workflowJobPayload, raw []byte, delivery string) error {
	// Drop GitHub-hosted jobs (ubuntu-latest, windows-latest, ...)
	// before any project / pool work. pool.RunnerLabels always
	// includes "self-hosted" as the first label, so a workflow_job
	// whose labels omit it can't match any pacer pool by definition.
	// Bouncing these here keeps the audit log free of "no_pool_match"
	// noise from every github-hosted workflow run in a bound repo.
	// 200 so GitHub stops retrying.
	if !hasSelfHostedLabel(p.WorkflowJob.Labels) {
		slog.Info("webhook drop: github-hosted job",
			"repo", p.Repository.FullName, "labels", p.WorkflowJob.Labels, "delivery", delivery)
		return response.Success(c, fiber.Map{"status": "ignored", "reason": "github-hosted runner"})
	}

	// Routing: try the per-repo binding first (most specific). When no
	// binding exists, fall back to an org-scoped project keyed off
	// repository.owner.login. This lets operators migrate gradually -
	// repo-scoped projects coexist with an org project that catches
	// every other repo in the same org.
	proj, err := h.routeProject(ctx, p)
	if err != nil {
		return response.Internal(c, err)
	}
	if proj == nil {
		slog.Info("webhook drop: no repo binding and no org-scoped project",
			"repo", p.Repository.FullName, "owner", p.Repository.Owner.Login, "delivery", delivery)
		return response.Success(c, fiber.Map{"status": "ignored", "reason": "repo not bound and no org-scoped project for owner"})
	}
	if proj.Disabled {
		slog.Info("webhook drop: project disabled", "project", proj.ID, "delivery", delivery)
		return response.Success(c, fiber.Map{"status": "ignored", "reason": "project disabled"})
	}

	// For pool match, org-scoped projects pass "" as the repo so
	// RunnerLabels drops the <owner>-<repo> narrowing label. Workflow
	// labels then match across every repo in the org.
	matchRepo := p.Repository.FullName
	if proj.Scope == projectmodel.ScopeOrg {
		matchRepo = ""
	}

	// Pool selection - match workflow_job.labels[] against each
	// pool's computed label set.  See domain/pool/pool.go::Match for
	// the algorithm.
	pools, err := h.Runtime.Store.Pool.ListByProject(ctx, proj.ID)
	if err != nil {
		return response.Internal(c, err)
	}
	if len(pools) == 0 {
		slog.Info("webhook drop: project has no pools",
			"project", proj.Name, "delivery", delivery)
		_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
			ID:         uuid.New().String(),
			Action:     audit.ActionJobNoPoolMatch,
			TargetType: "project",
			TargetID:   proj.ID,
			Detail: audit.Detail(map[string]any{
				"reason": "no_pools",
				"scope":  proj.Scope,
				"labels": p.WorkflowJob.Labels,
			}),
			ClientIP:   c.IP(),
			OccurredAt: time.Now().UTC(),
		})
		return response.Success(c, fiber.Map{"status": "ignored", "reason": "project has no pools"})
	}

	chosen := pool.Match(pools, p.WorkflowJob.Labels, proj.Name, matchRepo)
	if chosen == nil {
		slog.Info("webhook drop: no pool matches workflow labels",
			"project", proj.Name, "scope", proj.Scope, "labels", p.WorkflowJob.Labels, "delivery", delivery)
		_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
			ID:         uuid.New().String(),
			Action:     audit.ActionJobNoPoolMatch,
			TargetType: "project",
			TargetID:   proj.ID,
			Detail: audit.Detail(map[string]any{
				"reason": "no_match",
				"scope":  proj.Scope,
				"labels": p.WorkflowJob.Labels,
			}),
			ClientIP:   c.IP(),
			OccurredAt: time.Now().UTC(),
		})
		return response.Success(c, fiber.Map{"status": "ignored", "reason": "no pool matches workflow labels"})
	}

	// Delivery-id dedup only covers GitHub's automatic retries (same
	// GUID). A manual "Redeliver" from the App UI mints a fresh GUID,
	// so catch the duplicate at the job level too - otherwise Put hits
	// the jobs.gh_job_id unique index and the operator sees a 500 for
	// what is a logical drop.
	if existing, err := h.Runtime.Store.Job.GetByGHJobID(ctx, p.WorkflowJob.ID); err != nil {
		return response.Internal(c, err)
	} else if existing != nil {
		slog.Info("webhook drop: job already enqueued",
			"job_id", existing.ID, "gh_job_id", p.WorkflowJob.ID, "delivery", delivery)
		return response.Success(c, fiber.Map{"status": "duplicate", "job_id": existing.ID})
	}

	j := &job.Job{
		ID:             uuid.New().String(),
		GHJobID:        p.WorkflowJob.ID,
		GHRunID:        p.WorkflowJob.RunID,
		InstallationID: p.Installation.ID,
		RepoFullName:   p.Repository.FullName,
		ProjectID:      proj.ID,
		PoolID:         chosen.ID,
		Status:         job.StatusQueued,
		QueuedAt:       time.Now().UTC(),
		SenderLogin:    p.Sender.Login,
		Payload:        raw,
	}
	if err := h.Runtime.Store.Job.Put(ctx, j); err != nil {
		return response.Internal(c, err)
	}

	_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     audit.ActionJobEnqueued,
		TargetType: "job",
		TargetID:   j.ID,
		Detail: audit.Detail(map[string]any{
			"repo":      j.RepoFullName,
			"gh_job_id": j.GHJobID,
			"pool":      chosen.Name,
			"labels":    p.WorkflowJob.Labels,
		}),
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})

	slog.Info("job enqueued",
		"job_id", j.ID, "gh_job_id", j.GHJobID, "repo", j.RepoFullName,
		"project", proj.Name, "scope", proj.Scope, "pool", chosen.Name)
	return response.Success(c, fiber.Map{"status": "enqueued", "job_id": j.ID, "pool": chosen.Name})
}

// routeProject resolves which project owns this workflow_job. Returns
// (project, nil) on success, (nil, nil) when no project owns this
// repo or its org, or (nil, err) on a store-level failure. Lookup
// order:
//
//  1. Per-repo binding - if a Repo row exists for the full_name, its
//     ProjectID wins (most specific, supports running repo-scoped and
//     org-scoped projects in the same org).
//  2. Org-scoped project for repository.owner.login.
func (h *Handler) routeProject(ctx context.Context, p *workflowJobPayload) (*projectmodel.Project, error) {
	r, err := h.Runtime.Store.Repo.Get(ctx, p.Repository.FullName)
	if err != nil {
		return nil, err
	}
	if r != nil {
		proj, err := h.Runtime.Store.Project.Get(ctx, r.ProjectID)
		if err != nil {
			return nil, err
		}
		return proj, nil
	}
	if p.Repository.Owner.Login == "" {
		return nil, nil
	}
	return h.Runtime.Store.Project.GetByOrgName(ctx, p.Repository.Owner.Login)
}

// runnerNamePrefix is what runner/endpoint.go::Register mints runner
// names with. It is the link back from GitHub's runner_name to the
// instance the runner is on.
const runnerNamePrefix = "ghr-"

// reconcileRunner corrects the job's instance binding from the runner
// GitHub says took the job.
//
// The binding written at spawn time pairs a job with the instance
// launched FOR it, and GitHub honours no such pairing: every runner in
// a pool advertises the same labels, so a queued job goes to whichever
// one is free. Run two jobs in a pool and the pairing is a coin flip.
//
// Left uncorrected, the reaper works from it: when an instance goes
// away it fails, reaps and deregisters the job the row names, which
// may be a job running happily on another host - and 'failed' is
// terminal, so the conclusion GitHub sends later is dropped.
//
// Only jobs.instance_id moves. instances.job_id keeps recording what
// the machine was LAUNCHED for, because that is what a callback from
// the machine itself can be matched on - the runner knows only the
// job id it booted with.
//
// Failures are logged, not surfaced: the webhook's own work (status,
// payload) still has to land, and a stale binding is what we already
// had.
func (h *Handler) reconcileRunner(ctx context.Context, j *job.Job, p *workflowJobPayload) {
	name := strings.TrimSpace(p.WorkflowJob.RunnerName)
	instID, ok := strings.CutPrefix(name, runnerNamePrefix)
	if name == "" || !ok || instID == "" {
		// Either GitHub sent no runner (queued, or a job that never
		// started) or the runner is not one of ours. A foreign runner
		// with matching labels poaching pacer jobs is worth saying out
		// loud - it means the labels are not as narrow as they look.
		if name != "" {
			slog.Warn("webhook: job ran on a runner pacer did not spawn",
				"job_id", j.ID, "runner", name, "gh_job_id", p.WorkflowJob.ID)
		}
		return
	}
	if instID == j.RunnerInstanceID {
		return
	}

	if err := h.Runtime.Store.Job.BindRunnerInstance(ctx, j.ID, instID); err != nil {
		slog.Warn("webhook: runner bind failed", "job_id", j.ID, "instance_id", instID, "err", err)
		return
	}
	j.RunnerInstanceID = instID

	// Only worth a line, and an audit row, when GitHub put the job
	// somewhere other than the machine launched for it. That is the
	// case the reaper used to get wrong.
	if instID == j.InstanceID {
		return
	}
	slog.Info("webhook: job ran on a different instance than the one spawned for it",
		"job_id", j.ID, "spawned_for", j.InstanceID, "ran_on", instID)
	_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     audit.ActionJobRunnerRebound,
		TargetType: "job",
		TargetID:   j.ID,
		Detail: audit.Detail(map[string]any{
			"ran_on":      instID,
			"spawned_for": j.InstanceID,
			"runner":      name,
		}),
		OccurredAt: time.Now().UTC(),
	})
}

func (h *Handler) markRunning(ctx context.Context, c *fiber.Ctx, p *workflowJobPayload, raw []byte) error {
	j, err := h.Runtime.Store.Job.GetByGHJobID(ctx, p.WorkflowJob.ID)
	if err != nil {
		return response.Internal(c, err)
	}
	if j == nil {
		return response.NoContent(c)
	}
	// Before the status write: this is the first moment GitHub tells
	// us which machine took the job, and the reaper may sweep at any
	// point after it.
	h.reconcileRunner(ctx, j, p)
	// The in_progress webhook arrives with steps[] partially populated
	// (set-up, checkout, ...). Refresh the payload column so the job
	// detail modal can render them. Best-effort - a stale payload is
	// preferable to a 500 here.
	if err := h.Runtime.Store.Job.UpdatePayload(ctx, j.ID, raw); err != nil {
		slog.Warn("webhook.in_progress: payload refresh failed", "job_id", j.ID, "err", err)
	}
	// Only pre-run states may transition to running. GitHub does not
	// guarantee webhook ordering and retries can delay in_progress by
	// minutes. A late one arriving after failed/cancelled/reaped must
	// not resurrect the job. The gate lives in MarkRunning's WHERE
	// clause so the check and the write are one statement.
	if err := h.Runtime.Store.Job.MarkRunning(ctx, j.ID, j.InstanceID, time.Now().UTC()); err != nil {
		if errors.Is(err, jobstore.ErrStatusConflict) {
			return response.NoContent(c)
		}
		return response.Internal(c, err)
	}
	return response.NoContent(c)
}

func (h *Handler) markCompleted(ctx context.Context, c *fiber.Ctx, p *workflowJobPayload, raw []byte) error {
	j, err := h.Runtime.Store.Job.GetByGHJobID(ctx, p.WorkflowJob.ID)
	if err != nil {
		return response.Internal(c, err)
	}
	if j == nil {
		return response.NoContent(c)
	}
	// A job short enough to finish between sweeps may have no
	// in_progress delivery on record yet, so bind here too before the
	// cost rollup reads instance_id.
	h.reconcileRunner(ctx, j, p)
	// The completed webhook is the only one that carries the fully
	// populated steps[] (with conclusion + duration per step). Refresh
	// the payload column so the modal renders the final shape. Same
	// best-effort posture as markRunning.
	if err := h.Runtime.Store.Job.UpdatePayload(ctx, j.ID, raw); err != nil {
		slog.Warn("webhook.completed: payload refresh failed", "job_id", j.ID, "err", err)
	}
	now := time.Now().UTC()
	var markErr error
	switch p.WorkflowJob.Conclusion {
	case "cancelled":
		markErr = h.Runtime.Store.Job.MarkCancelled(ctx, j.ID, "github", "conclusion=cancelled", now)
	case "failure", "timed_out":
		markErr = h.Runtime.Store.Job.MarkFailed(ctx, j.ID, "github", "conclusion="+p.WorkflowJob.Conclusion, now)
	default:
		markErr = h.Runtime.Store.Job.MarkCompleted(ctx, j.ID, now)
	}
	if markErr != nil {
		// A redelivery after the reaper or orchestrator finalized the
		// row must not rewrite status or cost. Ack so GitHub stops.
		if errors.Is(markErr, jobstore.ErrStatusConflict) {
			return response.NoContent(c)
		}
		return response.Internal(c, markErr)
	}
	_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     audit.ActionJobCompleted,
		TargetType: "job",
		TargetID:   j.ID,
		Detail: audit.Detail(map[string]any{
			"conclusion": p.WorkflowJob.Conclusion,
		}),
		OccurredAt: now,
	})
	return response.NoContent(c)
}

// hasSelfHostedLabel reports whether the workflow_job.labels array
// contains the GitHub-mandated "self-hosted" marker (case-insensitive,
// matching pool.SanitizeLabel's normalization). Workflows that omit
// it run on github-hosted runners and are not pacer's concern.
func hasSelfHostedLabel(labels []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool {
		return strings.EqualFold(l, "self-hosted")
	})
}

func verifySignature(secret string, body []byte, sigHeader string) bool {
	const prefix = "sha256="
	if len(sigHeader) <= len(prefix) || sigHeader[:len(prefix)] != prefix {
		return false
	}
	want, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// persistDelivery inserts the webhook envelope keyed on GitHub's
// X-GitHub-Delivery id.
// Returns inserted=true when this is a fresh id, inserted=false when the same id
// was already processed (the
// caller short-circuits dispatch on duplicates so GitHub retries
// don't double-enqueue).
func persistDelivery(ctx context.Context, rt *env.Runtime, deliveryID, event string, body []byte) (bool, error) {
	// received_at is supplied explicitly (UTC) so the driver stores
	// the same format as every other time column, instead of the
	// column's DEFAULT CURRENT_TIMESTAMP shape. The pruner compares
	// this column textually. One format keeps that comparison exact
	// rather than relying on prefix ordering across mixed shapes.
	res, err := rt.DB.DB().ExecContext(ctx, `
        INSERT INTO webhook_deliveries (id, event, payload, received_at)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(id) DO NOTHING
    `, deliveryID, event, string(body), time.Now().UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
