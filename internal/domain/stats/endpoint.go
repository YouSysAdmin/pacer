// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package stats

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	statsmodel "github.com/yousysadmin/pacer/internal/models/stats"
)

type Handler struct {
	Runtime *env.Runtime
}

// Timeseries is GET /api/stats/timeseries?from=&to=&project_id=.
// One row per UTC calendar day with terminal-status job counts -
// powers the Overview page's success/failed bar chart. Window
// defaults match Get (last 30 days when params are omitted).
// project_id is the console's scope selector; omitted means every
// project.
func (h *Handler) Timeseries(c *fiber.Ctx) error {
	from, to, err := parseWindow(c.Query("from"), c.Query("to"))
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	days, err := h.Runtime.Store.Job.StatusTimeseries(c.UserContext(), from, to, c.Query("project_id"))
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, statsmodel.TimeseriesResponse{
		Window: statsmodel.Window{From: from, To: to},
		Days:   days,
	})
}

// TopUsers is GET /api/stats/top-users?from=&to=&limit=&project_id=.
// Ranks GitHub senders by terminal-state job count in the requested
// window. Powers the stats page's top-N user panel. Limit defaults
// to 10 and is capped at 100. project_id is the console's scope
// selector; omitted means every project.
func (h *Handler) TopUsers(c *fiber.Ctx) error {
	from, to, err := parseWindow(c.Query("from"), c.Query("to"))
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	limit := 10
	if v := c.Query("limit"); v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = n
		}
	}
	users, err := h.Runtime.Store.Stats.TopUsers(c.UserContext(), from, to, limit, c.Query("project_id"))
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, statsmodel.TopUsersResponse{
		Window: statsmodel.Window{From: from, To: to},
		Limit:  limit,
		Users:  users,
	})
}

// Get is GET /api/stats?from=&to=&group_by=&project_id=.
// Every param is optional: from/to default to the last 30 days,
// group_by defaults to project, and project_id (the console's scope
// selector) defaults to every project.
// The response is a single JSON envelope with totals +
// per-bucket rows so the UI doesn't need to make multiple calls.
//
// Scoping to a project and grouping by project are not the same
// question and both are allowed: the first asks "what did THIS
// project cost", the second "how does spend split across projects".
// Combined they yield the one bucket, which is what a scoped stats
// page wants.
func (h *Handler) Get(c *fiber.Ctx) error {
	from, to, err := parseWindow(c.Query("from"), c.Query("to"))
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	by := statsmodel.GroupBy(c.Query("group_by", string(statsmodel.ByProject)))
	switch by {
	case statsmodel.ByProject, statsmodel.ByPool, statsmodel.ByRepo:
	default:
		return response.BadRequest(c, "group_by must be one of: project, pool, repo")
	}

	totals, buckets, err := h.Runtime.Store.Stats.Rollup(c.UserContext(), by, from, to, c.Query("project_id"))
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, statsmodel.Response{
		Window:  statsmodel.Window{From: from, To: to},
		GroupBy: by,
		Totals:  totals,
		Buckets: buckets,
	})
}

// parseWindow accepts either RFC3339 timestamps or YYYY-MM-DD dates.
// Dates are interpreted as UTC midnight.
// Empty values fall back to "last 30 days" - a sensible default for a UI that lands without
// any query string.
func parseWindow(fromS, toS string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-30 * 24 * time.Hour)
	to := now

	if fromS != "" {
		t, err := parseTime(fromS)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}
	if toS != "" {
		t, err := parseTime(toS)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errMsg("to must be after from")
	}
	return from, to, nil
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

type errString string

func (e errString) Error() string { return string(e) }
func errMsg(s string) error       { return errString(s) }
