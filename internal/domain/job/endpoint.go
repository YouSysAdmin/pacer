// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
)

// refreshThrottle bounds how often a single running job can trigger
// an upstream GitHub fetch. The modal polls the detail endpoint every
// 5s. Matching the throttle to the poll cadence means one tab hits
// GitHub once per cycle, multiple tabs viewing the same job collapse
// to the same rate, and an idle modal generates zero traffic.
const refreshThrottle = 5 * time.Second

type Handler struct {
	Runtime *env.Runtime

	// lastRefreshAt[jobID] = time.Time of last successful or attempted
	// upstream fetch. In-memory only -- on restart we just pay the cost
	// of one fresh round of GitHub calls bounded by client poll rate.
	lastRefreshAt sync.Map
}

// detail is the bundle returned by GET /api/jobs/:id. The list view
// stays slim (Job rows only). The detail view joins in the linked
// instance, the parsed webhook payload, and the per-job audit trail
// so the modal can render everything in one round trip.
//
// Payload is json.RawMessage so the original GitHub object passes
// through untouched -- the client decides which fields to surface
// (head_branch, head_sha, html_url, steps[], etc.) without us
// committing to a specific schema in Go.
type detail struct {
	Job *jobmodel.Job `json:"job"`
	// ProjectName + PoolName are joined in for the modal so the UI
	// can render human-readable labels instead of the UUIDs carried
	// on Job.ProjectID / Job.PoolID. Empty string when the source row
	// has been deleted out from under the job.
	ProjectName string                  `json:"project_name,omitempty"`
	PoolName    string                  `json:"pool_name,omitempty"`
	Payload     json.RawMessage         `json:"payload,omitempty"`
	Instance    *instancemodel.Instance `json:"instance,omitempty"`
	Audit       []*auditmodel.Entry     `json:"audit"`
}

// List is GET /api/jobs. Optional query params:
//
//	status=<status>   filter by job status
//	limit=<n>         clamp at 500, default 100
//	offset=<n>        skip n rows for pagination, default 0
//
// Response envelope: {entries, total, limit, offset}. total is the
// matching-row count ignoring pagination so the UI can render
// "showing X-Y of Z" without a follow-up call.
func (h *Handler) List(c *fiber.Ctx) error {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}
	offset := 0
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	f := jobmodel.ListFilter{
		Status:    jobmodel.Status(c.Query("status")),
		ProjectID: c.Query("project_id"),
		PoolID:    c.Query("pool_id"),
		Repo:      c.Query("repo"),
		Limit:     limit,
		Offset:    offset,
	}
	js, err := h.Runtime.Store.Job.List(c.UserContext(), f)
	if err != nil {
		return response.Internal(c, err)
	}
	total, err := h.Runtime.Store.Job.Count(c.UserContext(), f)
	if err != nil {
		return response.Internal(c, err)
	}
	if js == nil {
		js = []*jobmodel.Job{}
	}
	return response.Success(c, fiber.Map{
		"entries": js,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// Get is GET /api/jobs/:id. Returns the enriched detail bundle: the
// job row, the parsed webhook payload (raw GitHub JSON), the linked
// instance row when one was spawned, and the audit trail filtered to
// this job. One endpoint, one round trip from the modal.
//
// While the job is `running`, this also opportunistically refreshes
// `payload` from GitHub's REST API on the way out so the modal's 5s
// poll cadence drives mid-run step updates without any background
// goroutine. See refreshStepsIfRunning for the throttle + race
// semantics.
func (h *Handler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx := c.UserContext()

	j, err := h.Runtime.Store.Job.Get(ctx, id)
	if err != nil {
		return response.Internal(c, err)
	}
	if j == nil {
		return response.NotFound(c, "job not found")
	}

	payload := j.Payload
	if fresh, ok := h.refreshStepsIfRunning(ctx, j); ok {
		payload = fresh
	}

	d := detail{Job: j, Audit: []*auditmodel.Entry{}}
	if len(payload) > 0 && json.Valid(payload) {
		d.Payload = json.RawMessage(payload)
	}

	if j.ProjectID != "" {
		if pr, err := h.Runtime.Store.Project.Get(ctx, j.ProjectID); err == nil && pr != nil {
			d.ProjectName = pr.Name
		}
	}
	if j.PoolID != "" {
		if po, err := h.Runtime.Store.Pool.Get(ctx, j.PoolID); err == nil && po != nil {
			d.PoolName = po.Name
		}
	}

	if j.InstanceID != "" {
		inst, err := h.Runtime.Store.Instance.Get(ctx, j.InstanceID)
		if err != nil {
			return response.Internal(c, err)
		}
		d.Instance = inst
	}

	entries, err := h.Runtime.Store.Audit.List(ctx, auditmodel.ListFilter{
		TargetType: "job",
		TargetID:   j.ID,
		Limit:      200,
	})
	if err != nil {
		return response.Internal(c, err)
	}
	if entries != nil {
		d.Audit = entries
	}

	return response.Success(c, d)
}

// refreshStepsIfRunning calls GitHub's workflow-job API, wraps the
// response into the same shape webhooks deliver
// ({"action":"in_progress","workflow_job":{...}}) so the frontend's
// payload.workflow_job.steps[] path keeps working, and persists via
// UpdatePayloadIfRunning.
//
// Returns (newPayload, true) only on a successful fetch + non-empty
// write. (nil, false) for every skip / error path so the caller falls
// back to whatever was already on the row.
//
// All failure modes (GHApp not configured, ctx-cancelled mid-flight,
// GitHub 404 / 5xx, malformed repo full_name, DB write error) log warn
// and return false -- the detail endpoint must never fail because
// the optional refresh did.
func (h *Handler) refreshStepsIfRunning(ctx context.Context, j *jobmodel.Job) ([]byte, bool) {
	if h.Runtime.GHApp == nil {
		return nil, false
	}
	if j.Status != jobmodel.StatusRunning || j.GHJobID == 0 {
		// Drop the throttle entry once the job leaves running -- the
		// map is keyed per viewed job and would otherwise grow for the
		// life of the process.
		h.lastRefreshAt.Delete(j.ID)
		return nil, false
	}
	if t, ok := h.lastRefreshAt.Load(j.ID); ok {
		if last, _ := t.(time.Time); time.Since(last) < refreshThrottle {
			return nil, false
		}
	}
	// Stamp the timestamp before the upstream call so concurrent
	// requests that race past the load above still get coalesced
	// to one round-trip per cycle. Errors below leave it stamped
	// too -- that's intentional, it backs off persistent failures.
	h.lastRefreshAt.Store(j.ID, time.Now())

	owner, name, err := splitRepoFullName(j.RepoFullName)
	if err != nil {
		slog.Warn("job.refresh: malformed repo", "job_id", j.ID, "err", err)
		return nil, false
	}
	raw, err := h.Runtime.GHApp.WorkflowJob(ctx, j.InstallationID, owner, name, j.GHJobID)
	if err != nil {
		slog.Warn("job.refresh: github fetch failed",
			"job_id", j.ID, "gh_job_id", j.GHJobID, "err", err)
		return nil, false
	}
	wrapped, err := json.Marshal(struct {
		Action      string          `json:"action"`
		WorkflowJob json.RawMessage `json:"workflow_job"`
	}{Action: "in_progress", WorkflowJob: raw})
	if err != nil {
		slog.Warn("job.refresh: marshal wrapped payload failed", "job_id", j.ID, "err", err)
		return nil, false
	}
	if err := h.Runtime.Store.Job.UpdatePayloadIfRunning(ctx, j.ID, wrapped); err != nil {
		slog.Warn("job.refresh: db write failed", "job_id", j.ID, "err", err)
		return nil, false
	}
	return wrapped, true
}

func splitRepoFullName(s string) (owner, name string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed repo full_name %q", s)
	}
	return parts[0], parts[1], nil
}
