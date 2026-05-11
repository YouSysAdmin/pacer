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
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
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
		// GitHub always sets this header; missing means it isn't GitHub
		// (e.g. an operator curl).
		// Generate a synthetic id so the delivery row isn't all-NULL, but skip dedup -- random UUIDs
		// won't collide so the check would be a no-op anyway.
		delivery = uuid.NewString()
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
		// We deliberately don't re-dispatch -- replaying workflow_job
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
	// repository.owner.login. This lets operators migrate gradually --
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
			ID:         uuid.NewString(),
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
			ID:         uuid.NewString(),
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

	j := &job.Job{
		ID:             uuid.NewString(),
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
		ID:         uuid.NewString(),
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
//  1. Per-repo binding -- if a Repo row exists for the full_name, its
//     ProjectID wins (most specific; supports running repo-scoped and
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

func (h *Handler) markRunning(ctx context.Context, c *fiber.Ctx, p *workflowJobPayload, raw []byte) error {
	j, err := h.Runtime.Store.Job.GetByGHJobID(ctx, p.WorkflowJob.ID)
	if err != nil {
		return response.Internal(c, err)
	}
	if j == nil {
		return response.NoContent(c)
	}
	// The in_progress webhook arrives with steps[] partially populated
	// (set-up, checkout, ...). Refresh the payload column so the job
	// detail modal can render them. Best-effort -- a stale payload is
	// preferable to a 500 here.
	if err := h.Runtime.Store.Job.UpdatePayload(ctx, j.ID, raw); err != nil {
		slog.Warn("webhook.in_progress: payload refresh failed", "job_id", j.ID, "err", err)
	}
	if j.Status == job.StatusRunning || j.Status == job.StatusCompleted {
		return response.NoContent(c)
	}
	if err := h.Runtime.Store.Job.MarkRunning(ctx, j.ID, j.InstanceID, time.Now().UTC()); err != nil {
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
	// The completed webhook is the only one that carries the fully
	// populated steps[] (with conclusion + duration per step). Refresh
	// the payload column so the modal renders the final shape. Same
	// best-effort posture as markRunning.
	if err := h.Runtime.Store.Job.UpdatePayload(ctx, j.ID, raw); err != nil {
		slog.Warn("webhook.completed: payload refresh failed", "job_id", j.ID, "err", err)
	}
	now := time.Now().UTC()
	switch p.WorkflowJob.Conclusion {
	case "cancelled":
		if err := h.Runtime.Store.Job.MarkCancelled(ctx, j.ID, "github", "conclusion=cancelled", now); err != nil {
			return response.Internal(c, err)
		}
	case "failure", "timed_out":
		if err := h.Runtime.Store.Job.MarkFailed(ctx, j.ID, "github", "conclusion="+p.WorkflowJob.Conclusion, now); err != nil {
			return response.Internal(c, err)
		}
	default:
		if err := h.Runtime.Store.Job.MarkCompleted(ctx, j.ID, now); err != nil {
			return response.Internal(c, err)
		}
	}
	_ = h.Runtime.Store.Audit.Put(ctx, &audit.Entry{
		ID:         uuid.NewString(),
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
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
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
	res, err := rt.DB.DB().ExecContext(ctx, `
        INSERT INTO webhook_deliveries (id, event, payload)
        VALUES (?, ?, ?)
        ON CONFLICT(id) DO NOTHING
    `, deliveryID, event, string(body))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
