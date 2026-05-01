// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package audit

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	auditmodel "github.com/yousysadmin/pacer/internal/models/audit"
)

type Handler struct {
	Runtime *env.Runtime
}

// List is GET /api/audit?since=&until=&action=&actor=&target_type=&limit=&offset=.
// All params are optional. since/until accept RFC3339 or YYYY-MM-DD
// (UTC midnight); without them the response covers the whole log.
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

// parseWindow accepts either RFC3339 timestamps or YYYY-MM-DD dates;
// dates are interpreted as UTC midnight. Empty values disable that side.
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
