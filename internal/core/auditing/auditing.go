// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package auditing centralizes the construction + write of an
// audit_log entry from an HTTP handler. Every domain endpoint used to
// carry its own four-line `audit(c, action, targetID, detail)`
// method, varying only by a fixed TargetType constant. This package
// exposes a single helper so call sites read as:
//
//	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionXxxYyy,
//	                "project", p.ID, detail)
//
// Errors are logged at warn level rather than returned. The audit log
// is structurally important but not load-bearing for any caller -
// every handler that calls Put has already finished its real work,
// and a 500 over a missing log row would be worse than the row gap.
package auditing

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"uuid"

	"github.com/yousysadmin/pacer/internal/models/audit"
)

// Store is the subset of audit-store behavior this package needs.
// Kept narrow so the audit_log domain isn't pulled wholesale into
// every handler that wants to write a row.
type Store interface {
	Put(ctx context.Context, e *audit.Entry) error
}

// Put writes a single entry to the audit store. Errors are logged
// (warn level) but never returned - see package doc.
func Put(ctx context.Context, s Store, clientIP, action, targetType, targetID, detail string) {
	err := s.Put(ctx, &audit.Entry{
		ID:         uuid.New().String(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     detail,
		ClientIP:   clientIP,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		slog.Warn("audit put failed",
			"action", action,
			"target_type", targetType,
			"target_id", targetID,
			"err", err,
		)
	}
}

// PutCtx is the Fiber-flavored convenience wrapper: pulls request
// context + client IP from the *fiber.Ctx so handlers don't have to
// thread them through. The vast majority of production call sites
// use this form. The bare Put exists for tests and any non-Fiber
// caller (e.g. orchestrator background goroutines).
func PutCtx(c *fiber.Ctx, s Store, action, targetType, targetID, detail string) {
	Put(c.UserContext(), s, c.IP(), action, targetType, targetID, detail)
}
