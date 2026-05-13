// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package systemhealth is the HTTP edge for the in-process health
// bus. GET /api/health returns the current issues snapshot;
// POST /api/reconcile forces an immediate reaper sweep so an operator
// who's just fixed an IAM perm doesn't have to wait for the next
// 60s tick.
//
// Named systemhealth (not "health") to keep the package import name
// distinct from internal/core/health which it depends on.
package systemhealth

import (
	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/core/response"
)

type Handler struct {
	Runtime *env.Runtime
}

// listResponse is the GET /api/health body. Issues is always a
// non-nil slice (possibly empty) so the SPA can iterate without a
// nil check.
type listResponse struct {
	Issues []health.Issue `json:"issues"`
}

// reconcileResponse is the POST /api/reconcile body. Checked is the
// count of alive instances the sweep inspected; Issue is the
// reaper's post-sweep verdict (the same Issue you'd see in /health)
// or nil when the sweep is clean.
type reconcileResponse struct {
	Checked int           `json:"checked"`
	Issue   *health.Issue `json:"issue,omitempty"`
}

// List returns the current health snapshot. Empty Issues means
// everything is happy.
func (h *Handler) List(c *fiber.Ctx) error {
	if h.Runtime.Health == nil {
		return response.Success(c, listResponse{Issues: []health.Issue{}})
	}
	return response.Success(c, listResponse{Issues: h.Runtime.Health.Snapshot()})
}

// Reconcile forces an immediate reaper tick. Returns 503 when the
// reaper isn't running (github.disabled=true UI-only dev mode) so the
// frontend can disable the button instead of guessing.
func (h *Handler) Reconcile(c *fiber.Ctx) error {
	if h.Runtime.Reaper == nil {
		return response.Internal(c, errReaperUnavailable)
	}
	checked, err := h.Runtime.Reaper.Tick(c.UserContext())
	if err != nil {
		// A panic-recovered tick still wrote Health and kept the
		// goroutine alive; we want the operator to see that the
		// sweep returned a verdict, so we don't 500. Return 200
		// with the issue surfaced in the body.
		return response.Success(c, reconcileResponse{
			Checked: checked,
			Issue:   currentReaperIssue(h.Runtime.Health),
		})
	}
	return response.Success(c, reconcileResponse{
		Checked: checked,
		Issue:   currentReaperIssue(h.Runtime.Health),
	})
}

func currentReaperIssue(h *health.Health) *health.Issue {
	if h == nil {
		return nil
	}
	for _, i := range h.Snapshot() {
		if i.Component == "reaper" {
			c := i // copy to take address safely
			return &c
		}
	}
	return nil
}

// errReaperUnavailable is sentinel for the 500 response when the
// reaper isn't wired (UI-only dev). Lowercase + var so it formats
// nicely through response.Internal's err.Error() path.
var errReaperUnavailable = &reaperUnavailableErr{}

type reaperUnavailableErr struct{}

func (*reaperUnavailableErr) Error() string {
	return "reaper not running (github.disabled is true)"
}
