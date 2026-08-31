// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"uuid"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	authdomain "github.com/yousysadmin/pacer/internal/domain/auth"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
)

type Handler struct {
	Runtime *env.Runtime
}

// List is GET /api/audit?since=&until=&action=&actor=&target_type=&limit=&offset=.
// All params are optional. since/until accept RFC3339 or YYYY-MM-DD
// (UTC midnight). Without them the response covers the whole log.
// limit defaults to 100 and is capped at 1000 to keep payloads bounded.
func (h *Handler) List(c *fiber.Ctx) error {
	since, until, err := parseWindow(c.Query("since"), c.Query("until"))
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	limit, err := parsePositiveInt(c.Query("limit"), 100, 1000)
	if err != nil {
		return response.BadRequest(c, "limit: "+err.Error())
	}
	offset, err := parsePositiveInt(c.Query("offset"), 0, 1<<30)
	if err != nil {
		return response.BadRequest(c, "offset: "+err.Error())
	}

	f := auditmodel.ListFilter{
		Action:     c.Query("action"),
		Actor:      c.Query("actor"),
		TargetType: c.Query("target_type"),
		TargetID:   c.Query("target_id"),
		Q:          c.Query("q"),
		Since:      since,
		Until:      until,
		Limit:      limit,
		Offset:     offset,
	}

	entries, err := h.Runtime.Store.Audit.List(c.UserContext(), f)
	if err != nil {
		return response.Internal(c, err)
	}
	total, err := h.Runtime.Store.Audit.Count(c.UserContext(), f)
	if err != nil {
		return response.Internal(c, err)
	}
	if entries == nil {
		entries = []*auditmodel.Entry{}
	}
	return response.Success(c, fiber.Map{
		"entries": entries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// parseWindow accepts either RFC3339 timestamps or YYYY-MM-DD dates.
// Dates are interpreted as UTC midnight. Empty values disable that side.
func parseWindow(sinceS, untilS string) (time.Time, time.Time, error) {
	var since, until time.Time
	if sinceS != "" {
		t, err := parseTime(sinceS)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		since = t
	}
	if untilS != "" {
		t, err := parseTime(untilS)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		until = t
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return time.Time{}, time.Time{}, errMsg("until must be after since")
	}
	return since, until, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errMsg("invalid time " + s + " (expected RFC3339 or YYYY-MM-DD)")
}

func parsePositiveInt(s string, fallback, max int) (int, error) {
	if s == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errMsg("not an integer: " + s)
	}
	if n < 0 {
		return 0, errMsg("must be >= 0")
	}
	if n > max {
		return max, nil
	}
	return n, nil
}

type errString string

func (e errString) Error() string { return string(e) }
func errMsg(s string) error       { return errString(s) }

// pruneInput is the body of POST /api/audit/prune. OlderThanDays is
// the only knob: rows with occurred_at < (now - N*24h) are deleted.
// Range guard: at least 1 day so a misclick can't wipe today's
// activity. Capped at 3650 (10y) so the value stays sane.
type pruneInput struct {
	OlderThanDays int `json:"older_than_days" validate:"required,min=1,max=3650"`
}

// pruneResponse reports what happened. Cutoff is the absolute
// timestamp DeleteOlderThan was called with - echoed back so the
// UI can render "deleted N events older than YYYY-MM-DD HH:MM" with
// no client-side timezone math.
type pruneResponse struct {
	Deleted       int       `json:"deleted"`
	Cutoff        time.Time `json:"cutoff"`
	OlderThanDays int       `json:"older_than_days"`
}

// Prune is POST /api/audit/prune. Manual operator-driven cleanup
// of the audit log. The action itself is audited (with actor +
// detail showing the cutoff + delete count), so the log retains a
// self-documenting trace of who cleaned what - the prune-record
// row is necessarily after the cutoff it describes, so it survives.
func (h *Handler) Prune(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[pruneInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	cutoff := time.Now().UTC().Add(-time.Duration(in.OlderThanDays) * 24 * time.Hour)
	deleted, err := h.Runtime.Store.Audit.DeleteOlderThan(c.UserContext(), cutoff)
	if err != nil {
		return response.Internal(c, err)
	}

	// Audit the prune itself. Hand-written (not via auditing.PutCtx)
	// because we want to attach actor info - deleting audit rows
	// is the kind of action where "who did this" matters more than
	// the average state change.
	actorEmail, actorID := actorFromLocals(c)
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &auditmodel.Entry{
		ID:          uuid.New().String(),
		Action:      auditmodel.ActionAuditPruned,
		ActorEmail:  actorEmail,
		ActorUserID: actorID,
		ClientIP:    c.IP(),
		Detail: auditmodel.Detail(map[string]any{
			"older_than_days": in.OlderThanDays,
			"cutoff":          cutoff.Format(time.RFC3339),
			"deleted":         deleted,
		}),
		OccurredAt: time.Now().UTC(),
	})

	return response.Success(c, pruneResponse{
		Deleted:       deleted,
		Cutoff:        cutoff,
		OlderThanDays: in.OlderThanDays,
	})
}

// actorFromLocals reads the auth-middleware-populated user. Returns
// empty strings when auth.disabled=true (no user attached) - the
// audit row still lands with action + ip, just without attribution.
func actorFromLocals(c *fiber.Ctx) (email, userID string) {
	if u, ok := c.Locals(authdomain.UserLocalKey).(*usermodel.User); ok && u != nil {
		return u.Email, u.ID
	}
	return "", ""
}
