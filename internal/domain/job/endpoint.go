// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package job

import (
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	instancemodel "github.com/yousysadmin/pacer/internal/models/instance"
	jobmodel "github.com/yousysadmin/pacer/internal/models/job"
)

type Handler struct {
	Runtime *env.Runtime
}

// detail is the bundle returned by GET /api/jobs/:id. The list view
// stays slim (Job rows only); the detail view joins in the linked
// instance, the parsed webhook payload, and the per-job audit trail
// so the modal can render everything in one round trip.
//
// Payload is json.RawMessage so the original GitHub object passes
// through untouched -- the client decides which fields to surface
// (head_branch, head_sha, html_url, steps[], etc.) without us
// committing to a specific schema in Go.
type detail struct {
	Job      *jobmodel.Job           `json:"job"`
	Payload  json.RawMessage         `json:"payload,omitempty"`
	Instance *instancemodel.Instance `json:"instance,omitempty"`
	Audit    []*auditmodel.Entry     `json:"audit"`
}

// List is GET /api/jobs.
// Optional query params:
//
//	status=<status>   filter by job status
//	limit=<n>         clamp at 500; default 100
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
	f := jobmodel.ListFilter{
		Status: jobmodel.Status(c.Query("status")),
		Limit:  limit,
	}
	js, err := h.Runtime.Store.Job.List(c.UserContext(), f)
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, js)
}

// Get is GET /api/jobs/:id. Returns the enriched detail bundle: the
// job row, the parsed webhook payload (raw GitHub JSON), the linked
// instance row when one was spawned, and the audit trail filtered to
// this job. One endpoint, one round trip from the modal.
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

	d := detail{Job: j, Audit: []*auditmodel.Entry{}}

	if len(j.Payload) > 0 && json.Valid(j.Payload) {
		d.Payload = json.RawMessage(j.Payload)
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
