// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package runner

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/callback"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/ghapp"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/models/audit"
	"github.com/yousysadmin/pacer/internal/models/job"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

type Handler struct {
	Runtime *env.Runtime
	GHApp   *ghapp.Client
	HMACKey []byte
}

// registerInput is the runner self-registration body.
//
// Only the three fields the auth layer needs (job_id, instance_id,
// callback_token) are required -- this preserves the prior behavior
// where auth runs before shape validation, so a caller without a
// valid token gets 401 rather than a 400 that would leak the full
// expected shape. instance_type and az are validated for length
// but optional in practice (downstream stamps them as-is).
type registerInput struct {
	JobID         string `json:"job_id"         validate:"required,min=1,max=64"`
	InstanceID    string `json:"instance_id"    validate:"required,min=1,max=32"`
	InstanceType  string `json:"instance_type"  validate:"omitempty,max=64"`
	AZ            string `json:"az"             validate:"omitempty,max=32"`
	CallbackToken string `json:"callback_token" validate:"required,min=1,max=512"`
}

type completeInput struct {
	JobID         string `json:"job_id"         validate:"required,min=1,max=64"`
	CallbackToken string `json:"callback_token" validate:"required,min=1,max=512"`
	ExitCode      int    `json:"exit_code"`
}

// errorInput carries the bootstrap-failure log. Stage is free-form
// (operator-supplied bash labels: "imdsv2", "runner-download",
// "register", "run.sh", ...) so we cap length but don't constrain
// charset. Log has NO max tag because the existing handler-side
// truncation logic keeps the trailing 64 KiB; a hard reject here
// would lose the tail of a runaway script before the user could see
// what blew up. Content-Length pre-check (256 KiB) still bounds the
// inbound payload.
type errorInput struct {
	JobID         string `json:"job_id"         validate:"required,min=1,max=64"`
	CallbackToken string `json:"callback_token" validate:"required,min=1,max=512"`
	Stage         string `json:"stage"          validate:"omitempty,max=64"`
	ExitCode      int    `json:"exit_code"`
	Line          int    `json:"line" validate:"min=0"`
	Log           string `json:"log"`
}

const (
	failureLogMaxBytes   = 64 * 1024
	errorRequestMaxBytes = 256 * 1024
)

// Register is POST /api/runner/register.
// Returns the JIT runner
// configuration on success; the instance feeds it to
// `./run.sh --jitconfig <value>`.
func (h *Handler) Register(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[registerInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	j := h.authenticate(c, in.JobID, in.CallbackToken)
	if j == nil {
		return nil // response already written by authenticate
	}
	if j.Status != job.StatusClaimed {
		return response.BadRequest(c, fmt.Sprintf("job in status %q, expected claimed", j.Status))
	}

	// Look up project + pool to derive runner labels.
	// Both lookups are required - a missing pool means the binding was deleted
	// mid-flight; fail the registration rather than fall back to
	// generic labels.
	proj, err := h.Runtime.Store.Project.Get(c.UserContext(), j.ProjectID)
	if err != nil {
		return response.Internal(c, err)
	}
	if proj == nil {
		return response.Internal(c, fmt.Errorf("project %s missing for job %s", j.ProjectID, j.ID))
	}
	pl, err := h.Runtime.Store.Pool.Get(c.UserContext(), j.PoolID)
	if err != nil {
		return response.Internal(c, err)
	}
	if pl == nil {
		return response.Internal(c, fmt.Errorf("pool %s missing for job %s", j.PoolID, j.ID))
	}

	runnerName := "ghr-" + in.InstanceID

	// Org-scoped projects: drop the <owner>-<repo> narrowing label so
	// the runner can claim any matching job in the org / runner-group.
	// JIT config goes to /orgs/{org}/... rather than /repos/{owner}/{name}/...
	var (
		labels      []string
		jitConfig   string
		ghRunnerID  int64
	)
	if proj.Scope == projectmodel.ScopeOrg {
		labels = pool.RunnerLabels(proj.Name, pl.Name, "", pl.ExtraLabels)
		jitConfig, ghRunnerID, err = h.GHApp.JITConfigOrg(c.UserContext(), j.InstallationID, proj.OrgName, runnerName, labels, proj.RunnerGroupID)
	} else {
		owner, name, splitErr := splitRepoFullName(j.RepoFullName)
		if splitErr != nil {
			return response.Internal(c, splitErr)
		}
		labels = pool.RunnerLabels(proj.Name, pl.Name, j.RepoFullName, pl.ExtraLabels)
		jitConfig, ghRunnerID, err = h.GHApp.JITConfig(c.UserContext(), j.InstallationID, owner, name, runnerName, labels, 1)
	}
	if err != nil {
		return response.Internal(c, fmt.Errorf("jit config: %w", err))
	}

	now := time.Now().UTC()
	if err := h.Runtime.Store.Instance.StampRegistration(c.UserContext(), in.InstanceID, in.InstanceType, in.AZ, ghRunnerID, now); err != nil {
		return response.Internal(c, err)
	}
	if err := h.Runtime.Store.Job.MarkRunning(c.UserContext(), j.ID, in.InstanceID, now); err != nil {
		return response.Internal(c, err)
	}
	if err := h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		Action:     audit.ActionInstanceRegistered,
		TargetType: "instance",
		TargetID:   in.InstanceID,
		Detail: audit.Detail(map[string]any{
			"job_id": j.ID,
			"pool":   pl.Name,
			"type":   in.InstanceType,
			"az":     in.AZ,
		}),
		ClientIP:   c.IP(),
		OccurredAt: now,
	}); err != nil {
		// Audit is best-effort here -- the registration itself
		// succeeded and we don't want to fail the runner over a
		// missing log row. Surface the error so it isn't invisible.
		slog.Warn("runner.register: audit put failed", "job_id", j.ID, "err", err)
	}

	slog.Info("runner registered", "job_id", j.ID, "instance_id", in.InstanceID,
		"type", in.InstanceType, "pool", pl.Name)
	return response.Success(c, fiber.Map{"jit_config": jitConfig})
}

// Complete is POST /api/runner/complete.
// Best-effort - the authoritative job-completion signal is the
// GitHub workflow_job webhook.
// This endpoint only marks the local instance row and audits.
func (h *Handler) Complete(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[completeInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	j := h.authenticate(c, in.JobID, in.CallbackToken)
	if j == nil {
		return nil
	}

	now := time.Now().UTC()
	if j.InstanceID != "" {
		if err := h.Runtime.Store.Instance.UpdateState(c.UserContext(), j.InstanceID, "terminated", now); err != nil {
			slog.Warn("runner.complete: instance update failed",
				"job_id", j.ID, "instance_id", j.InstanceID, "err", err)
		}
		// Refine the cost estimate with the actual billable window
		// (terminated_at - launched_at) now that terminated_at is
		// stamped. The earlier MarkCompleted/MarkFailed estimate
		// missed the runner-shutdown tail that runs between the
		// workflow_job webhook and this callback.
		if err := h.Runtime.Store.Job.FinalizeCost(c.UserContext(), j.InstanceID); err != nil {
			slog.Warn("runner.complete: cost finalize failed",
				"job_id", j.ID, "instance_id", j.InstanceID, "err", err)
		}
	}
	if err := h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		Action:     audit.ActionInstanceTerminated,
		TargetType: "instance",
		TargetID:   j.InstanceID,
		Detail: audit.Detail(map[string]any{
			"job_id":    j.ID,
			"exit_code": in.ExitCode,
		}),
		ClientIP:   c.IP(),
		OccurredAt: now,
	}); err != nil {
		slog.Warn("runner.complete: audit put failed", "job_id", j.ID, "err", err)
	}
	return response.NoContent(c)
}

// Error is POST /api/runner/error.
// The user-data ERR-trap fires here when bootstrap blows up
// (IMDSv2 failure, runner download failure, ./run.sh exits non-zero, etc.).
// Same callback-token auth as Register / Complete -- the token's hash was stamped on
// the job row at spawn time, so we can verify even when the runner
// never registered.
// Marks the job failed with the captured log attached.
// Truncates Log to failureLogMaxBytes to keep one runaway instance
// from filling the database.
func (h *Handler) Error(c *fiber.Ctx) error {
	// Endpoint-specific size cap, stricter than the global BodyLimit:
	// reject 256 KiB+ bodies before unmarshal to discourage chatty
	// runners from blasting multi-MB logs we'd just truncate below.
	// Checked via Content-Length so we don't have to read the body
	// first; a missing or malformed header falls through to the
	// global BodyLimit + truncation.
	if cl := c.Get("Content-Length"); cl != "" {
		if n, err := strconv.Atoi(cl); err == nil && n > errorRequestMaxBytes {
			return response.BadRequest(c, "request body too large")
		}
	}
	in, err := validation.BindAndValidate[errorInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	j := h.authenticate(c, in.JobID, in.CallbackToken)
	if j == nil {
		return nil
	}

	stage := strings.TrimSpace(in.Stage)
	if stage == "" {
		stage = "bootstrap"
	}
	logBody := in.Log
	if len(logBody) > failureLogMaxBytes {
		// Keep the tail -- the failure is almost always at the end
		// of the captured output.
		logBody = "...[truncated]...\n" + logBody[len(logBody)-failureLogMaxBytes:]
	}
	msg := fmt.Sprintf("bootstrap exit=%d line=%d", in.ExitCode, in.Line)

	now := time.Now().UTC()
	if err := h.Runtime.Store.Job.MarkFailedWithLog(c.UserContext(), j.ID, stage, msg, logBody, now); err != nil {
		return response.Internal(c, err)
	}
	if j.InstanceID != "" {
		if err := h.Runtime.Store.Instance.UpdateState(c.UserContext(), j.InstanceID, "terminated", now); err != nil {
			slog.Warn("runner.error: instance update failed",
				"job_id", j.ID, "instance_id", j.InstanceID, "err", err)
		}
		// See Complete: refine cost using the now-stamped
		// terminated_at. Bootstrap-fail instances usually terminate
		// almost immediately, so the change is small but consistent.
		if err := h.Runtime.Store.Job.FinalizeCost(c.UserContext(), j.InstanceID); err != nil {
			slog.Warn("runner.error: cost finalize failed",
				"job_id", j.ID, "instance_id", j.InstanceID, "err", err)
		}
	}
	if err := h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		Action:     audit.ActionJobFailed,
		TargetType: "job",
		TargetID:   j.ID,
		Detail: audit.Detail(map[string]any{
			"stage": stage,
			"exit":  in.ExitCode,
			"line":  in.Line,
		}),
		ClientIP:   c.IP(),
		OccurredAt: now,
	}); err != nil {
		slog.Warn("runner.error: audit put failed", "job_id", j.ID, "err", err)
	}
	slog.Warn("runner bootstrap failed", "job_id", j.ID, "stage", stage, "exit", in.ExitCode)
	return response.NoContent(c)
}

// authenticate is the security boundary for runner self-callbacks.
// It implements the three-layer contract documented in
// internal/core/callback/callback.go:
//
//  1. callback.Verify -- HMAC-SHA256 over <job_id>.<exp> proves the
//     token was minted by this server and is not yet expired. Constant
//     time inside hmac.Equal.
//  2. Hash(token) constant-time-compared (subtle.ConstantTimeCompare)
//     against the sha256 hex digest stored on the job row at spawn
//     time. Binds the token to a single job, so a leaked token cannot
//     be replayed against a different job.
//  3. Job-exists check (the Get above). Lifecycle-state checks
//     (e.g. j.Status == "claimed") happen at the call site after
//     authenticate returns the job.
//
// Contract for callers: on any auth failure the appropriate HTTP
// response is already written and authenticate returns nil. The
// caller MUST then `return nil` to Fiber rather than tuple-propagating
// the nil through error-returning helpers, otherwise the response is
// sent twice. See Register / Complete / Error above for the canonical
// pattern.
func (h *Handler) authenticate(c *fiber.Ctx, jobID, token string) *job.Job {
	parsedJobID, ok := callback.Verify(token, h.HMACKey)
	if !ok || parsedJobID != jobID {
		_ = response.Unauthorized(c, "invalid callback_token")
		return nil
	}
	j, err := h.Runtime.Store.Job.Get(c.UserContext(), jobID)
	if err != nil {
		_ = response.Internal(c, err)
		return nil
	}
	if j == nil {
		_ = response.NotFound(c, "job not found")
		return nil
	}
	want := callback.Hash(token)
	if subtle.ConstantTimeCompare([]byte(j.CallbackTokenHash), []byte(want)) != 1 {
		_ = response.Unauthorized(c, "callback_token mismatch")
		return nil
	}
	return j
}

func splitRepoFullName(s string) (owner, name string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed repo full_name %q", s)
	}
	return parts[0], parts[1], nil
}
