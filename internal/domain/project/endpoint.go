// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package project

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gofiber/fiber/v2"
	"uuid"

	"github.com/yousysadmin/pacer/internal/core/auditing"
	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/models/audit"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
)

type Handler struct {
	Runtime *env.Runtime
}

// input is the project create/update DTO.
//
// Tag layers, in apply order:
//
//   - normalize:"..." runs first (trim/lower) so a "  Foo  " becomes
//     "foo" before required / oneof see it.
//   - Normalize() runs after that for cross-field defaulting that
//     tag vocabulary can't express (scope coercion, conditional
//     org_name).
//   - validate:"..." runs last over the cleaned shape.
//
// MaxLen sources: Name -> 128 (AWS launch-template name cap.
// Project name flows into LT name downstream). OrgName -> 39
// (GitHub org-login cap). Scope is enum-bounded so no length tag.
type input struct {
	Name                 string            `json:"name"                   validate:"required,min=1,max=128"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners" validate:"min=0"`
	Tags                 map[string]string `json:"tags"                   validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
	Scope                string            `json:"scope"                  validate:"oneof=repo org"                                                  normalize:"normalize"`
	OrgName              string            `json:"org_name"               validate:"required_if=Scope org,omitempty,max=39,no_slash_or_space"        normalize:"trim"`
	RunnerGroupID        int               `json:"runner_group_id"        validate:"min=0"`
	Disabled             bool              `json:"disabled"`
}

// Normalize coerces blank scope to "repo" (back-compat with pre-org
// payloads), wipes org-only fields when scope reverts to repo, and
// nil-safes Tags so handlers don't have to nil-check before iterating.
// Runs after the normalize:"..." tag pass and before validate:"...".
func (in *input) Normalize() {
	in.Scope = cmp.Or(in.Scope, projectmodel.ScopeRepo)
	if in.Scope == projectmodel.ScopeRepo {
		in.OrgName = ""
		in.RunnerGroupID = 0
	}
	if in.MaxConcurrentRunners < 0 {
		in.MaxConcurrentRunners = 0
	}
	if in.Tags == nil {
		in.Tags = map[string]string{}
	}
}

func (h *Handler) Create(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[input](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}
	p := &projectmodel.Project{
		ID:                   uuid.New().String(),
		Name:                 in.Name,
		MaxConcurrentRunners: in.MaxConcurrentRunners,
		Tags:                 in.Tags,
		Scope:                in.Scope,
		OrgName:              in.OrgName,
		RunnerGroupID:        in.RunnerGroupID,
		Disabled:             in.Disabled,
	}
	if err := h.Runtime.Store.Project.Put(c.UserContext(), p); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionProjectCreated, "project", p.ID, audit.Detail(map[string]any{
		"name":     p.Name,
		"scope":    p.Scope,
		"org_name": p.OrgName,
	}))
	return response.Created(c, p)
}

// Update persists the project row.
// When the project's tags change, every pool's launch template is
// rematerialized (best-effort: per-pool failures are logged but don't
// fail the whole update) so newly-spawned instances AND console-spawn
// from the LT pick up the merged tag shape.
// Without this, only orchestrator-driven spawns reflected the new tags
// (the orchestrator re-merges per spawn), while the LT itself stayed
// stale until each pool was re-saved by hand.
func (h *Handler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	existing, err := h.Runtime.Store.Project.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if existing == nil {
		return response.NotFound(c, "project not found")
	}
	in, err := validation.BindAndValidate[input](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}
	// Block scope flips that would orphan existing state. Repo-bound
	// projects can't become org-scoped while bindings exist. Org
	// projects can't switch back to repo while pools have queued/active
	// jobs from the org webhook path.
	if (existing.Scope == "" || existing.Scope == projectmodel.ScopeRepo) && in.Scope == projectmodel.ScopeOrg {
		repos, err := h.Runtime.Store.Repo.ListByProject(c.UserContext(), existing.ID)
		if err != nil {
			return response.Internal(c, err)
		}
		if len(repos) > 0 {
			return response.BadRequest(c,
				fmt.Sprintf("project has %d bound repos, unbind them before switching to org scope", len(repos)))
		}
	}
	if existing.Scope == projectmodel.ScopeOrg && in.Scope == projectmodel.ScopeRepo {
		active, err := h.Runtime.Store.Project.ConcurrentRunnerCount(c.UserContext(), existing.ID)
		if err != nil {
			return response.Internal(c, err)
		}
		if active > 0 {
			return response.BadRequest(c,
				fmt.Sprintf("project has %d active org-routed jobs, wait for them to finish before switching to repo scope", active))
		}
	}

	p := &projectmodel.Project{
		ID:                   existing.ID,
		Name:                 in.Name,
		MaxConcurrentRunners: in.MaxConcurrentRunners,
		Tags:                 in.Tags,
		Scope:                in.Scope,
		OrgName:              in.OrgName,
		RunnerGroupID:        in.RunnerGroupID,
		Disabled:             in.Disabled,
		CreatedAt:            existing.CreatedAt,
	}
	if err := h.Runtime.Store.Project.Put(c.UserContext(), p); err != nil {
		return response.Internal(c, err)
	}

	tagsChanged := !reflect.DeepEqual(existing.Tags, p.Tags)
	rematerialized := 0
	if tagsChanged {
		rematerialized = h.rematerializePools(c.UserContext(), p)
	}

	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionProjectUpdated, "project", p.ID, audit.Detail(map[string]any{
		"scope":                p.Scope,
		"org_name":             p.OrgName,
		"tags_changed":         tagsChanged,
		"pools_rematerialized": rematerialized,
	}))
	return response.Success(c, p)
}

// rematerializePools bumps every pool's LT version to pick up the
// project's new tag shape. Returns the count of successful bumps.
// Per-pool failures are logged but never propagated - the project
// row is already persisted. We don't want a transient EC2 hiccup to
// look like the whole update failed.
func (h *Handler) rematerializePools(ctx context.Context, p *projectmodel.Project) int {
	if h.Runtime.EC2 == nil {
		return 0 // dev mode: no real LTs to bump
	}
	pools, err := h.Runtime.Store.Pool.ListByProject(ctx, p.ID)
	if err != nil {
		slog.Error("project: list pools for rematerialize failed", "project_id", p.ID, "err", err)
		return 0
	}
	var done int
	for _, pl := range pools {
		if err := h.rematerializeOne(ctx, pl, p); err != nil {
			slog.Error("project: pool rematerialize failed",
				"project_id", p.ID, "pool_id", pl.ID, "pool_name", pl.Name, "err", err)
			continue
		}
		done++
	}
	return done
}

func (h *Handler) rematerializeOne(ctx context.Context, pl *poolmodel.Pool, p *projectmodel.Project) error {
	runnerVersion := pl.RunnerVersion
	if h.Runtime.RunnerVersion != nil {
		runnerVersion = h.Runtime.RunnerVersion.Resolve(pl.RunnerVersion)
	}
	bootstrapToken, _ := h.Runtime.BootstrapAPIToken.Load().(string)
	if err := ec2lt.CreateOrUpdate(ctx, h.Runtime.EC2, h.Runtime.IAM, pl, p.Name, p.Tags,
		h.Runtime.Config.Server.PublicURL, runnerVersion, bootstrapToken); err != nil {
		return fmt.Errorf("ec2lt: %w", err)
	}
	if err := h.Runtime.Store.Pool.Put(ctx, pl); err != nil {
		return fmt.Errorf("pool put: %w", err)
	}
	return nil
}

func (h *Handler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	p, err := h.Runtime.Store.Project.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if p == nil {
		return response.NotFound(c, "project not found")
	}
	return response.Success(c, p)
}

func (h *Handler) List(c *fiber.Ctx) error {
	ps, err := h.Runtime.Store.Project.List(c.UserContext())
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, ps)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	p, err := h.Runtime.Store.Project.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if p == nil {
		return response.NotFound(c, "project not found")
	}

	// Each preflight query gates an irreversible delete. A swallowed
	// error here would let a transient DB failure look like "no
	// dependencies", and we'd happily delete a project that still has
	// repos / pools / running jobs underneath it. Treat all three as
	// hard failures.
	inflight, err := h.Runtime.Store.Project.ConcurrentRunnerCount(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if inflight > 0 {
		return response.BadRequest(c, fmt.Sprintf("project has %d active jobs; wait or cancel first", inflight))
	}
	repos, err := h.Runtime.Store.Repo.ListByProject(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if len(repos) > 0 {
		return response.BadRequest(c, fmt.Sprintf("project has %d bound repos; unbind them first", len(repos)))
	}
	pools, err := h.Runtime.Store.Pool.ListByProject(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if len(pools) > 0 {
		return response.BadRequest(c, fmt.Sprintf("project has %d pools; delete them first", len(pools)))
	}

	if err := h.Runtime.Store.Project.Delete(c.UserContext(), id); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionProjectDeleted, "project", id, "")
	return response.NoContent(c)
}
