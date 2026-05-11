// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/auditing"
	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/models/audit"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
)

type Handler struct {
	Runtime *env.Runtime
}

// input is the pool create/update DTO.
//
// MaxLen sources track the AWS / GitHub natural caps:
//   - name                  -> 128 (LT name; runner_label rule rejects "---")
//   - ami_id                -> 32  (AWS AMI IDs are well under)
//   - instance type entries -> 64
//   - subnet / SG IDs       -> 32  (AWS resource IDs)
//   - iam_instance_profile  -> 128
//   - extra_labels entries  -> 64  (GitHub runner-label cap)
//   - runner_version        -> 32
//   - runner_user           -> 32  (POSIX cap)
//   - user_data_extra       -> 32 KiB (operator-supplied bash tail)
//
// Slice caps stop unbounded list payloads even when each entry is
// short (32 across InstanceTypes / SubnetIDs / SecurityGroupIDs /
// ExtraLabels; 50 for Tags).
type input struct {
	Name                 string            `json:"name"                   validate:"required,min=1,max=128,runner_label_strict"`
	IsDefault            bool              `json:"is_default"`
	Priority             int               `json:"priority"               validate:"min=0"`
	AMIID                string            `json:"ami_id"                 validate:"required,min=1,max=32"`
	InstanceTypes        []string          `json:"instance_types"         validate:"required,min=1,max=32,dive,min=1,max=64"`
	SubnetIDs            []string          `json:"subnet_ids"             validate:"required,min=1,max=32,dive,min=1,max=32"`
	SecurityGroupIDs     []string          `json:"security_group_ids"     validate:"required,min=1,max=32,dive,min=1,max=32"`
	IAMInstanceProfile   string            `json:"iam_instance_profile"   validate:"omitempty,max=128"                                            normalize:"trim"`
	RootVolumeGB         int               `json:"root_volume_gb"         validate:"min=0"`
	MaxRuntimeMinutes    int               `json:"max_runtime_minutes"    validate:"min=0"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners" validate:"min=0"`
	Spot                 bool              `json:"spot"`
	SpawnMethod          string            `json:"spawn_method"           validate:"oneof=fleet run_instances"                                    normalize:"normalize"`
	AllocationStrategy   string            `json:"allocation_strategy"    validate:"oneof=cost priority"                                          normalize:"normalize"`
	ExtraLabels          []string          `json:"extra_labels"           validate:"omitempty,max=32,dive,min=1,max=64,gha_safe,runner_label,not_self_hosted"`
	Tags                 map[string]string `json:"tags"                   validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
	RunnerVersion        string            `json:"runner_version"         validate:"omitempty,max=32"`
	RunnerUser           string            `json:"runner_user"            validate:"omitempty,max=32,posix_user"                                   normalize:"trim"`
	UserDataExtra        string            `json:"user_data_extra"        validate:"omitempty,max=32768"`
	Disabled             bool              `json:"disabled"`
}

// Normalize trims ExtraLabels entries (dropping blanks before
// validation runs), nil-safes Tags, and applies pool-specific
// defaults via the existing "<=0 means default" semantics
// (operator-friendly: omit the field OR pass 0 -> default kicks
// in). The sanitize+dedupe pass runs AFTER validation in the
// handler -- doing it here would mask the gha_safe / runner_label /
// not_self_hosted rules, which compute SanitizeLabel internally and
// expect to see the raw operator input.
func (in *input) Normalize() {
	if in.SpawnMethod == "" {
		in.SpawnMethod = "fleet"
	}
	if in.AllocationStrategy == "" {
		in.AllocationStrategy = "cost"
	}
	if in.MaxRuntimeMinutes <= 0 {
		in.MaxRuntimeMinutes = 60
	}
	if in.MaxConcurrentRunners <= 0 {
		in.MaxConcurrentRunners = 5
	}
	if in.RootVolumeGB < 0 {
		in.RootVolumeGB = 0
	}
	if in.Priority <= 0 {
		in.Priority = 100
	}
	if in.Tags == nil {
		in.Tags = map[string]string{}
	}
	// Trim every extra_labels entry and drop the ones that go empty
	// post-trim. Doing it here (rather than per-entry via dive,...)
	// keeps the validators that follow (gha_safe / runner_label /
	// not_self_hosted) operating on consistent, trimmed input.
	if len(in.ExtraLabels) > 0 {
		trimmed := make([]string, 0, len(in.ExtraLabels))
		for _, raw := range in.ExtraLabels {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			trimmed = append(trimmed, raw)
		}
		in.ExtraLabels = trimmed
	}
}

// finalizeLabels sanitizes + dedupes the post-validation ExtraLabels
// so the persisted pool row matches what the runner will register
// with (the Match algorithm consumes SanitizeLabel-ed values). Runs
// after BindAndValidate so the validators can still reject the raw
// input on charset / reserved-name grounds.
func (in *input) finalizeLabels() {
	if len(in.ExtraLabels) == 0 {
		return
	}
	cleaned := make([]string, 0, len(in.ExtraLabels))
	seen := make(map[string]bool, len(in.ExtraLabels))
	for _, raw := range in.ExtraLabels {
		s := SanitizeLabel(raw)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		cleaned = append(cleaned, s)
	}
	in.ExtraLabels = cleaned
}

// Create is POST /api/projects/:project_id/pools.
// Materializes the EC2 launch template before persisting the row
// so an inconsistent pool (DB row pointing at non-existent LT)
// is impossible - the failure mode flips to "orphan LT in EC2 console".
func (h *Handler) Create(c *fiber.Ctx) error {
	projectID := c.Params("project_id")
	proj, err := h.Runtime.Store.Project.Get(c.UserContext(), projectID)
	if err != nil {
		return response.Internal(c, err)
	}
	if proj == nil {
		return response.NotFound(c, "project not found")
	}

	in, err := validation.BindAndValidate[input](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}
	in.finalizeLabels()
	p := poolFromInput(&in, projectID)
	p.ID = uuid.NewString()

	if err := h.ensureSingleDefault(c, p); err != nil {
		return err
	}

	if err := h.materializeLT(c.UserContext(), p, proj.Name, proj.Tags); err != nil {
		return response.BadRequest(c, "ec2: "+err.Error())
	}
	if err := h.Runtime.Store.Pool.Put(c.UserContext(), p); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionPoolCreated, "pool", p.ID, poolDetailJSON(p))
	return response.Created(c, p)
}

// Update is PUT /api/pools/:id.
// Re-materializes the LT (bumps version + sets default) before persisting.
func (h *Handler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	existing, err := h.Runtime.Store.Pool.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if existing == nil {
		return response.NotFound(c, "pool not found")
	}
	proj, err := h.Runtime.Store.Project.Get(c.UserContext(), existing.ProjectID)
	if err != nil {
		return response.Internal(c, err)
	}
	if proj == nil {
		return response.Internal(c, fmt.Errorf("project %s missing for pool %s", existing.ProjectID, id))
	}

	in, err := validation.BindAndValidate[input](c)
	if err != nil {
		fes := validation.Humanize(err)
		return response.BadRequestFields(c, validation.Summary(fes), fes)
	}
	in.finalizeLabels()
	p := poolFromInput(&in, existing.ProjectID)
	p.ID = existing.ID
	p.CreatedAt = existing.CreatedAt
	// Carry forward LT identity so ec2lt takes the Update path.
	p.LaunchTemplateID = existing.LaunchTemplateID
	p.LaunchTemplateVersion = existing.LaunchTemplateVersion

	if err := h.ensureSingleDefault(c, p); err != nil {
		return err
	}

	if err := h.materializeLT(c.UserContext(), p, proj.Name, proj.Tags); err != nil {
		return response.BadRequest(c, "ec2: "+err.Error())
	}
	if err := h.Runtime.Store.Pool.Put(c.UserContext(), p); err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionPoolUpdated, "pool", p.ID, poolDetailJSON(p))
	return response.Success(c, p)
}

func (h *Handler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	p, err := h.Runtime.Store.Pool.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if p == nil {
		return response.NotFound(c, "pool not found")
	}
	return response.Success(c, p)
}

func (h *Handler) List(c *fiber.Ctx) error {
	ps, err := h.Runtime.Store.Pool.List(c.UserContext())
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, ps)
}

func (h *Handler) ListByProject(c *fiber.Ctx) error {
	projectID := c.Params("project_id")
	ps, err := h.Runtime.Store.Pool.ListByProject(c.UserContext(), projectID)
	if err != nil {
		return response.Internal(c, err)
	}
	return response.Success(c, ps)
}

func (h *Handler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	p, err := h.Runtime.Store.Pool.Get(c.UserContext(), id)
	if err != nil {
		return response.Internal(c, err)
	}
	if p == nil {
		return response.NotFound(c, "pool not found")
	}
	inflight, _ := h.Runtime.Store.Pool.ConcurrentRunnerCount(c.UserContext(), id)
	if inflight > 0 {
		return response.BadRequest(c, fmt.Sprintf("pool has %d active jobs; wait or cancel first", inflight))
	}
	if err := h.Runtime.Store.Pool.Delete(c.UserContext(), id); err != nil {
		return response.Internal(c, err)
	}
	ltDeleted := h.deleteLaunchTemplate(c.UserContext(), p)
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionPoolDeleted, "pool", id, audit.Detail(map[string]any{
		"project_id": p.ProjectID,
		"lt_id":      p.LaunchTemplateID,
		"lt_deleted": ltDeleted,
	}))
	return response.NoContent(c)
}

// deleteLaunchTemplate best-effort deletes the pool's EC2 launch
// template after the DB row is gone. Failures are logged but never
// surfaced -- the row is already deleted, so a transient EC2 hiccup
// or a manually-pre-deleted LT shouldn't make pool delete look failed.
// Returns whether the AWS call succeeded so the audit row records it.
func (h *Handler) deleteLaunchTemplate(ctx context.Context, p *poolmodel.Pool) bool {
	if h.Runtime.EC2 == nil {
		return false // dev mode: nothing in AWS to delete
	}
	if p.LaunchTemplateID == "" || strings.HasPrefix(p.LaunchTemplateID, "lt-dev-") {
		return false // never materialized for real (or dev placeholder)
	}
	_, err := h.Runtime.EC2.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
		LaunchTemplateId: aws.String(p.LaunchTemplateID),
	})
	if err != nil {
		slog.Warn("pool: launch-template cleanup failed; clean up via EC2 console",
			"pool_id", p.ID, "lt_id", p.LaunchTemplateID, "err", err)
		return false
	}
	return true
}

// materializeLT creates or bumps the EC2 launch template that backs
// this pool.
// In aws.disabled dev mode (Runtime.EC2 == nil) the AWS path is skipped
// and a placeholder LT id + version 1 is stamped so the row is internally
// consistent and the UI / orchestrator code paths still see
// "an LT exists" -- spawn would obviously fail
// against AWS, but the orchestrator isn't running in dev anyway.
func (h *Handler) materializeLT(ctx context.Context, p *poolmodel.Pool, projectName string, projectTags map[string]string) error {
	if h.Runtime.EC2 == nil {
		if p.LaunchTemplateID == "" {
			p.LaunchTemplateID = "lt-dev-" + uuid.NewString()[:8]
			p.LaunchTemplateVersion = 1
		} else {
			p.LaunchTemplateVersion++
		}
		return nil
	}
	return ec2lt.CreateOrUpdate(ctx, h.Runtime.EC2, h.Runtime.IAM, p, projectName, projectTags,
		h.Runtime.Config.Server.PublicURL, h.resolveRunnerVersion(p), h.bootstrapAPIToken())
}

// bootstrapAPIToken returns the current value from Runtime's atomic
// cache. The cache is loaded at startup and replaced on rotation, so
// pool save always bakes the latest token.
func (h *Handler) bootstrapAPIToken() string {
	v := h.Runtime.BootstrapAPIToken.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// resolveRunnerVersion returns the actions/runner tag to bake into
// the LT's user-data. Per-pool pin wins; otherwise the server's
// cached latest; otherwise empty (the script falls back to whatever
// the AMI baked).
//
// Resolved at LT-materialize time, frozen in the LT until the next
// pool save. To pick up an upstream runner release, re-save the pool.
func (h *Handler) resolveRunnerVersion(p *poolmodel.Pool) string {
	if h.Runtime.RunnerVersion == nil {
		return p.RunnerVersion
	}
	return h.Runtime.RunnerVersion.Resolve(p.RunnerVersion)
}

// ensureSingleDefault clears the is_default flag from any other pool
// in the same project before persisting `p` as default.
// Pool table has a partial unique index on (project_id) WHERE is_default=1 so
// the DB itself enforces this - the explicit clear avoids an INSERT
// failure when an admin edits another pool to be the new default.
func (h *Handler) ensureSingleDefault(c *fiber.Ctx, p *poolmodel.Pool) error {
	if !p.IsDefault {
		return nil
	}
	siblings, err := h.Runtime.Store.Pool.ListByProject(c.UserContext(), p.ProjectID)
	if err != nil {
		return response.Internal(c, err)
	}
	for _, s := range siblings {
		if s.ID == p.ID || !s.IsDefault {
			continue
		}
		s.IsDefault = false
		if err := h.Runtime.Store.Pool.Put(c.UserContext(), s); err != nil {
			return response.Internal(c, err)
		}
	}
	return nil
}

func poolFromInput(in *input, projectID string) *poolmodel.Pool {
	return &poolmodel.Pool{
		ProjectID:            projectID,
		Name:                 in.Name,
		IsDefault:            in.IsDefault,
		Priority:             in.Priority,
		AMIID:                in.AMIID,
		InstanceTypes:        in.InstanceTypes,
		SubnetIDs:            in.SubnetIDs,
		SecurityGroupIDs:     in.SecurityGroupIDs,
		IAMInstanceProfile:   in.IAMInstanceProfile,
		RootVolumeGB:         in.RootVolumeGB,
		MaxRuntimeMinutes:    in.MaxRuntimeMinutes,
		MaxConcurrentRunners: in.MaxConcurrentRunners,
		Spot:                 in.Spot,
		SpawnMethod:          in.SpawnMethod,
		AllocationStrategy:   in.AllocationStrategy,
		ExtraLabels:          in.ExtraLabels,
		Tags:                 in.Tags,
		RunnerVersion:        in.RunnerVersion,
		RunnerUser:           in.RunnerUser,
		UserDataExtra:        in.UserDataExtra,
		Disabled:             in.Disabled,
	}
}

// poolDetailJSON renders the operationally-meaningful pool fields for
// the audit log. Skips bulky/secret-adjacent fields (tags, user_data_extra)
// and per-row metadata (timestamps); the row itself is the source of truth
// for those.
func poolDetailJSON(p *poolmodel.Pool) string {
	b, _ := json.Marshal(struct {
		Name                 string   `json:"name"`
		AMIID                string   `json:"ami_id"`
		Spot                 bool     `json:"spot"`
		SpawnMethod          string   `json:"spawn_method"`
		AllocationStrategy   string   `json:"allocation_strategy"`
		ExtraLabels          []string `json:"extra_labels,omitempty"`
		MaxRuntimeMinutes    int      `json:"max_runtime_minutes"`
		MaxConcurrentRunners int      `json:"max_concurrent_runners"`
		IsDefault            bool     `json:"is_default"`
		Disabled             bool     `json:"disabled"`
		LTID                 string   `json:"lt_id"`
		LTVersion            int      `json:"lt_version"`
	}{
		Name:                 p.Name,
		AMIID:                p.AMIID,
		Spot:                 p.Spot,
		SpawnMethod:          p.SpawnMethod,
		AllocationStrategy:   p.AllocationStrategy,
		ExtraLabels:          p.ExtraLabels,
		MaxRuntimeMinutes:    p.MaxRuntimeMinutes,
		MaxConcurrentRunners: p.MaxConcurrentRunners,
		IsDefault:            p.IsDefault,
		Disabled:             p.Disabled,
		LTID:                 p.LaunchTemplateID,
		LTVersion:            p.LaunchTemplateVersion,
	})
	return string(b)
}
