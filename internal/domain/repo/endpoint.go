// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package repo

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/auditing"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/models/audit"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	repomodel "github.com/yousysadmin/pacer/internal/models/repo"
)

type Handler struct {
	Runtime *env.Runtime
}

// bindInput is the repo-bind DTO.
//
// full_name shape is "owner/name". The repo_full_name custom rule
// rejects malformed shapes up-front so the handler doesn't have to
// re-split. project_id length cap mirrors project.NameMax (uuid is
// 36 chars but we leave headroom in case the ID format changes).
// Tags follow the project / pool taxonomy: gha:* prefix reserved.
//
// max_concurrent_runners is floored at 0 (meaning no repo-level cap)
// because Job.Claim compares an in-flight count against it: a
// negative would make that comparison unsatisfiable and quietly park
// every job for the repo in the queue forever.
type bindInput struct {
	FullName             string            `json:"full_name"                  validate:"required,max=140,repo_full_name"`
	ProjectID            string            `json:"project_id"                 validate:"required,min=1,max=128"`
	MaxConcurrentRunners *int              `json:"max_concurrent_runners,omitempty" validate:"omitempty,min=0,max=10000"`
	Tags                 map[string]string `json:"tags,omitempty"             validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
}

func (h *Handler) Bind(c *fiber.Ctx) error {
	in, err := validation.BindAndValidate[bindInput](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}

	// Verify the project exists. FK would catch this too but the
	// error is clearer here.
	p, err := h.Runtime.Store.Project.Get(c.UserContext(), in.ProjectID)
	if err != nil {
		return response.Internal(c, err)
	}
	if p == nil {
		return response.BadRequest(c, "project_id does not exist")
	}
	if p.Scope == projectmodel.ScopeOrg {
		return response.BadRequest(c,
			"project is org-scoped; webhooks for the org route via repository.owner.login - no per-repo binding needed")
	}

	r := &repomodel.Repo{
		FullName:             in.FullName,
		ProjectID:            in.ProjectID,
		MaxConcurrentRunners: in.MaxConcurrentRunners,
		Tags:                 in.Tags,
		CreatedAt:            time.Now().UTC(),
	}
	if err := h.Runtime.Store.Repo.Put(c.UserContext(), r); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionRepoBound, "repo", r.FullName, audit.Detail(map[string]any{
		"project_id": r.ProjectID,
	}))
	return response.Created(c, r)
}

func (h *Handler) Get(c *fiber.Ctx) error {
	fullName := c.Params("owner") + "/" + c.Params("name")
	r, err := h.Runtime.Store.Repo.Get(c.UserContext(), fullName)
	if err != nil {
		return response.Internal(c, err)
	}
	if r == nil {
		return response.NotFound(c, "repo not bound")
	}
	return response.Success(c, r)
}

func (h *Handler) List(c *fiber.Ctx) error {
	rs, err := h.Runtime.Store.Repo.List(c.UserContext())
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, rs)
}

func (h *Handler) ListByProject(c *fiber.Ctx) error {
	projectID := c.Params("id")
	rs, err := h.Runtime.Store.Repo.ListByProject(c.UserContext(), projectID)
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, rs)
}

func (h *Handler) Unbind(c *fiber.Ctx) error {
	fullName := c.Params("owner") + "/" + c.Params("name")
	r, err := h.Runtime.Store.Repo.Get(c.UserContext(), fullName)
	if err != nil {
		return response.Internal(c, err)
	}
	if r == nil {
		return response.NotFound(c, "repo not bound")
	}
	if err := h.Runtime.Store.Repo.Delete(c.UserContext(), fullName); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionRepoUnbound, "repo", fullName, "")
	return response.NoContent(c)
}
