// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package response wraps Fiber JSON responses behind named helpers so
// handlers don't inline c.JSON(fiber.Map{...}) - keeps status codes
// and the error envelope consistent across the API.
package response

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

func Success(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusOK).JSON(data)
}

func Created(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusCreated).JSON(data)
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

func BadRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}

// BadRequestFields is the structured-error variant of BadRequest.
// Both the legacy "error" string (so existing api.js callers
// continue to surface a single-line message) and the new "fields"
// array land in the body, so the SPA can incrementally adopt
// field-level rendering without a synchronized backend cut-over.
//
// The summary string and the fields list are caller-supplied --
// validation.Humanize + validation.Summary are the canonical
// producers, but any handler that wants to surface multiple field
// errors (e.g. cross-field business rules) can build them directly.
func BadRequestFields(c *fiber.Ctx, summary string, fields any) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error":  summary,
		"fields": fields,
	})
}

func Unauthorized(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": msg})
}

func Forbidden(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": msg})
}

func NotFound(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

// Conflict is for state-conflict failures the operator can fix
// themselves (FK constraint violations on delete, unique-key
// collisions, etc.) - distinct from BadRequest (which connotes
// "your input is wrong") and Internal (which connotes "server bug").
func Conflict(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
}

// ServiceUnavailable is for features that are wired off in this
// deployment (reaper absent in UI-only mode) so the SPA can disable
// the control rather than treat it as a server bug.
func ServiceUnavailable(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": msg})
}

// Gone is for single-use resources that have already been consumed
// (or never existed) - the caller has no viable retry, unlike
// Conflict where the operator can resolve the state and try again.
func Gone(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": msg})
}

// FailedDependency is for a request we could not satisfy because an
// upstream we depend on (GitHub) refused, permanently: a revoked App
// installation, a repo the App can no longer see, a rejected runner
// registration. The operator has to change something at GitHub; no
// amount of retrying helps.
//
// Unlike Internal, the message IS echoed to the caller, and that is
// the point. The only caller is the runner bootstrap script, holding
// a per-job HMAC token, and the message it gets is the one that ends
// up in the job's failure_log where an operator reads it. A bare 500
// there means "go grep the server logs"; "Resource not accessible by
// integration" means "your App lost access to that repo". Pass only
// upstream-supplied text, never our own internal error strings.
//
// 424 rather than 502/503 on purpose: curl's --retry treats 5xx as
// transient and would spend the bootstrap's whole retry budget
// re-asking a question GitHub has already answered for good.
func FailedDependency(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusFailedDependency).JSON(fiber.Map{"error": msg})
}

// Internal logs the underlying error server-side and returns a generic
// 500 to the caller. The raw err is intentionally NOT echoed back - it
// commonly contains stack-revealing detail (file paths, SQL state,
// AWS request IDs) that's useful to attackers but not to operators
// using the UI. Operators see the full error in the access log. The
// caller sees a uniform message they can correlate by request_id (TBD)
// or timestamp.
//
// Pass nil err when the caller has already done its own logging or
// when invoked from the panic-recovery path.
func Internal(c *fiber.Ctx, err error) error {
	if err != nil {
		slog.Error("handler internal error",
			"err", err,
			"path", c.Path(),
			"method", c.Method(),
			"client_ip", c.IP(),
		)
	}
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
}
