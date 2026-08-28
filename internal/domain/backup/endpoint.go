// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package backup

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"uuid"

	"github.com/yousysadmin/pacer/internal/core/auditing"
	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/core/validation"
	pooldomain "github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/models/audit"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	repomodel "github.com/yousysadmin/pacer/internal/models/repo"
)

type Handler struct {
	Runtime *env.Runtime
}

// formatVersion is the schema version stamped into every export.
// Bump when the snapshot shape changes incompatibly. Import refuses
// payloads from a different version so an upgrade can't silently
// truncate fields the operator doesn't realize are gone.
const formatVersion = 1

// snapshot is the on-the-wire JSON document. Pools and repos nest
// under their project so the relationship is preserved without
// carrying brittle UUIDs across systems.
type snapshot struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Projects   []project `json:"projects"`
}

// project, pool, and repo carry the same validate / normalize tags
// the live CRUD DTOs in domain/project, domain/pool, and domain/repo
// use. The Import handler runs validation.NormalizeAndValidate on
// every row before persisting, so an imported snapshot cannot bypass
// the same reserved-namespace / shell-charset / shape rules a regular
// API client has to satisfy. Keep these tag lists in sync with the
// endpoint input DTOs (project/endpoint.go::input,
// pool/endpoint.go::input, repo/endpoint.go::bindInput).
type project struct {
	Name                 string            `json:"name"                   validate:"required,min=1,max=128"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners" validate:"min=0"`
	Tags                 map[string]string `json:"tags,omitempty"         validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
	Scope                string            `json:"scope,omitempty"        validate:"oneof=repo org"                                                  normalize:"normalize"`
	OrgName              string            `json:"org_name,omitempty"     validate:"required_if=Scope org,omitempty,max=39,no_slash_or_space"        normalize:"trim"`
	RunnerGroupID        int               `json:"runner_group_id,omitempty" validate:"min=0"`
	Disabled             bool              `json:"disabled,omitempty"`
	Pools                []pool            `json:"pools,omitempty"`
	Repos                []repo            `json:"repos,omitempty"`
}

// Normalize mirrors project/endpoint.go::input.Normalize so the
// import path applies the same default-and-coerce pass before the
// validator runs (e.g. empty scope -> "repo"). Pools and Repos are
// validated per row in their own handlers so they're skipped here.
func (p *project) Normalize() {
	p.Scope = cmp.Or(p.Scope, projectmodel.ScopeRepo)
	if p.Scope == projectmodel.ScopeRepo {
		p.OrgName = ""
		p.RunnerGroupID = 0
	}
	if p.MaxConcurrentRunners < 0 {
		p.MaxConcurrentRunners = 0
	}
	if p.Tags == nil {
		p.Tags = map[string]string{}
	}
}

type pool struct {
	Name                 string            `json:"name"                   validate:"required,min=1,max=128,runner_label_strict"`
	IsDefault            bool              `json:"is_default,omitempty"`
	Priority             int               `json:"priority"               validate:"min=0"`
	AMIID                string            `json:"ami_id"                 validate:"required,min=1,max=32"`
	InstanceTypes        []string          `json:"instance_types"         validate:"required,min=1,max=32,dive,min=1,max=64"`
	SubnetIDs            []string          `json:"subnet_ids"             validate:"required,min=1,max=32,dive,min=1,max=32"`
	SecurityGroupIDs     []string          `json:"security_group_ids"     validate:"required,min=1,max=32,dive,min=1,max=32"`
	IAMInstanceProfile   string            `json:"iam_instance_profile,omitempty" validate:"omitempty,max=128"                                    normalize:"trim"`
	RootVolumeGB         int               `json:"root_volume_gb"         validate:"min=0"`
	MaxRuntimeMinutes    int               `json:"max_runtime_minutes"    validate:"min=0"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners" validate:"min=0"`
	Spot                 bool              `json:"spot,omitempty"`
	SpawnMethod          string            `json:"spawn_method,omitempty"     validate:"oneof=fleet run_instances"                                    normalize:"normalize"`
	AllocationStrategy   string            `json:"allocation_strategy,omitempty" validate:"oneof=cost lowest_price capacity priority"                    normalize:"normalize"`
	ExtraLabels          []string          `json:"extra_labels,omitempty"         validate:"omitempty,max=32,dive,min=1,max=64,gha_safe,runner_label,not_self_hosted"`
	Tags                 map[string]string `json:"tags,omitempty"                 validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
	RunnerVersion        string            `json:"runner_version,omitempty"       validate:"omitempty,max=32"`
	RunnerUser           string            `json:"runner_user,omitempty"          validate:"omitempty,max=32,posix_user"                                   normalize:"trim"`
	UserDataExtra        string            `json:"user_data_extra,omitempty"      validate:"omitempty,max=32768"`
	Disabled             bool              `json:"disabled,omitempty"`
}

// Normalize mirrors pool/endpoint.go::input.Normalize. Defaults that
// SpawnMethod / AllocationStrategy / MaxRuntimeMinutes etc fall back
// to come from the live pool handler so a backup row missing those
// fields validates the same way a partial UI submission would.
func (p *pool) Normalize() {
	p.SpawnMethod = cmp.Or(p.SpawnMethod, "fleet")
	if p.AllocationStrategy == "" {
		p.AllocationStrategy = "cost"
	}
	if p.MaxRuntimeMinutes <= 0 {
		p.MaxRuntimeMinutes = 60
	}
	if p.MaxConcurrentRunners <= 0 {
		p.MaxConcurrentRunners = 5
	}
	if p.RootVolumeGB < 0 {
		p.RootVolumeGB = 0
	}
	if p.Priority <= 0 {
		p.Priority = 100
	}
	if p.Tags == nil {
		p.Tags = map[string]string{}
	}
	if len(p.ExtraLabels) > 0 {
		trimmed := make([]string, 0, len(p.ExtraLabels))
		for _, raw := range p.ExtraLabels {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			trimmed = append(trimmed, raw)
		}
		p.ExtraLabels = trimmed
	}
}

type repo struct {
	FullName             string            `json:"full_name"                  validate:"required,max=140,repo_full_name"`
	MaxConcurrentRunners *int              `json:"max_concurrent_runners,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"             validate:"omitempty,max=50,dive,keys,required,min=1,max=128,gha_safe,endkeys,max=256"`
}

// Export is GET /api/backup/export.
// Walks every project + its pools + its repo bindings and returns a
// single JSON document with Content-Disposition set so the browser
// downloads it as `pacer-backup-<date>.json`.
func (h *Handler) Export(c *fiber.Ctx) error {
	ctx := c.UserContext()
	projects, err := h.Runtime.Store.Project.List(ctx)
	if err != nil {
		return response.Internal(c, err)
	}
	snap := snapshot{
		Version:    formatVersion,
		ExportedAt: time.Now().UTC(),
		Projects:   make([]project, 0, len(projects)),
	}
	for _, p := range projects {
		bp := projectFromModel(p)
		pools, err := h.Runtime.Store.Pool.ListByProject(ctx, p.ID)
		if err != nil {
			return response.Internal(c, err)
		}
		for _, pl := range pools {
			bp.Pools = append(bp.Pools, poolFromModel(pl))
		}
		repos, err := h.Runtime.Store.Repo.ListByProject(ctx, p.ID)
		if err != nil {
			return response.Internal(c, err)
		}
		for _, r := range repos {
			bp.Repos = append(bp.Repos, repoFromModel(r))
		}
		snap.Projects = append(snap.Projects, bp)
	}

	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return response.Internal(c, err)
	}
	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionConfigExported, "config", "", audit.Detail(map[string]any{
		"projects": len(snap.Projects),
		"version":  formatVersion,
	}))
	filename := fmt.Sprintf("pacer-backup-%s.json", time.Now().UTC().Format("2006-01-02"))
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Status(fiber.StatusOK).Send(body)
}

// importResult is the per-section count surfaced back to the operator.
// errors collects any per-row failures so a partial import can be
// reasoned about without parsing the audit log.
type importResult struct {
	Projects countResult `json:"projects"`
	Pools    countResult `json:"pools"`
	Repos    countResult `json:"repos"`
	Errors   []string    `json:"errors,omitempty"`
}

type countResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
}

// Import is POST /api/backup/import.
// Body is the same JSON shape produced by Export. Rows are upserted
// by name (project name, pool (project_name, name), repo full_name).
// Each pool row is materialized through ec2lt.CreateOrUpdate before
// the row is persisted, exactly mirroring the pool handler's save
// path - so an inconsistent pool (DB row pointing at non-existent LT)
// is impossible.
//
// Errors per row are collected into result.Errors instead of aborting
// the whole import, so an operator can fix the offending entries and
// re-import without losing the rows that did succeed (the upsert
// semantics make re-runs safe).
func (h *Handler) Import(c *fiber.Ctx) error {
	if len(c.Body()) == 0 {
		return response.BadRequest(c, "empty body")
	}
	var snap snapshot
	if err := json.Unmarshal(c.Body(), &snap); err != nil {
		return response.BadRequest(c, "invalid JSON: "+err.Error())
	}
	if snap.Version != formatVersion {
		return response.BadRequest(c, fmt.Sprintf("unsupported backup version %d (this server expects %d)", snap.Version, formatVersion))
	}

	ctx := c.UserContext()
	var result importResult
	for i := range snap.Projects {
		bp := &snap.Projects[i]
		h.applyProject(ctx, bp, &result)
	}

	auditing.PutCtx(c, h.Runtime.Store.Audit, audit.ActionConfigImported, "config", "", audit.Detail(map[string]any{
		"projects_created": result.Projects.Created,
		"projects_updated": result.Projects.Updated,
		"pools_created":    result.Pools.Created,
		"pools_updated":    result.Pools.Updated,
		"repos_created":    result.Repos.Created,
		"repos_updated":    result.Repos.Updated,
		"errors":           len(result.Errors),
	}))
	return response.Success(c, result)
}

// applyProject upserts one project + cascades to its pools + repos.
// Errors at any level append to result.Errors and the inner loop
// short-circuits to the next sibling - never to a sibling project,
// since a partial pool list under one project is preferable to
// abandoning the rest of the import.
func (h *Handler) applyProject(ctx context.Context, bp *project, result *importResult) {
	// Mirror the CRUD endpoint's validate pass so an imported row
	// can't bypass rules like gha_safe / no_slash_or_space / scope
	// enum. Normalize first so default-coerced fields (e.g. empty
	// scope -> "repo") pass oneof.
	if err := validation.NormalizeAndValidate(bp); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %q: %s", bp.Name, validation.Summary(validation.Humanize(err))))
		return
	}
	defaults := 0
	for j := range bp.Pools {
		if bp.Pools[j].IsDefault {
			defaults++
		}
	}
	if defaults > 1 {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: %d pools marked is_default, at most one allowed", bp.Name, defaults))
		return
	}

	existing, err := h.Runtime.Store.Project.GetByName(ctx, bp.Name)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: %v", bp.Name, err))
		return
	}
	pmodel := projectModelFor(existing, bp)
	if existing == nil {
		result.Projects.Created++
	} else {
		result.Projects.Updated++
	}
	if err := h.Runtime.Store.Project.Put(ctx, pmodel); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: %v", bp.Name, err))
		return
	}

	existingPools, err := h.Runtime.Store.Pool.ListByProject(ctx, pmodel.ID)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: list pools: %v", bp.Name, err))
		return
	}
	poolByName := make(map[string]*poolmodel.Pool, len(existingPools))
	for _, pl := range existingPools {
		poolByName[pl.Name] = pl
	}
	for j := range bp.Pools {
		ip := &bp.Pools[j]
		h.applyPool(ctx, pmodel, ip, poolByName, result)
	}

	for j := range bp.Repos {
		ir := &bp.Repos[j]
		h.applyRepo(ctx, pmodel, ir, result)
	}
}

func (h *Handler) applyPool(ctx context.Context, pmodel *projectmodel.Project, ip *pool, poolByName map[string]*poolmodel.Pool, result *importResult) {
	if err := validation.NormalizeAndValidate(ip); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s pool %q: %s", pmodel.Name, ip.Name, validation.Summary(validation.Humanize(err))))
		return
	}
	// Mirror pool/endpoint.go::input.finalizeLabels: sanitize + dedupe
	// extra_labels after validation so the persisted row matches what
	// the runner will actually register with. Run after Validate so
	// the raw operator input is still rejected on charset / reserved
	// grounds.
	ip.ExtraLabels = sanitizeAndDedupeLabels(ip.ExtraLabels)
	existing := poolByName[ip.Name]
	plmodel := poolModelFor(existing, pmodel.ID, ip)
	if existing == nil {
		result.Pools.Created++
	} else {
		result.Pools.Updated++
	}
	if err := h.materializeLT(ctx, plmodel, pmodel.Name, pmodel.Tags); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s pool %s: ec2lt: %v", pmodel.Name, ip.Name, err))
		return
	}
	// Mirror pool/endpoint.go::ensureSingleDefault: the partial unique
	// index on (project_id) WHERE is_default=1 rejects a second default,
	// so flip the live sibling first. Otherwise Put fails after the LT
	// was already created and the version is burned.
	if plmodel.IsDefault {
		for name, sib := range poolByName {
			if name == ip.Name || !sib.IsDefault {
				continue
			}
			sib.IsDefault = false
			if err := h.Runtime.Store.Pool.Put(ctx, sib); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("project %s pool %s: clear default on %s: %v", pmodel.Name, ip.Name, name, err))
				return
			}
		}
	}
	if err := h.Runtime.Store.Pool.Put(ctx, plmodel); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s pool %s: %v", pmodel.Name, ip.Name, err))
		return
	}
	poolByName[ip.Name] = plmodel
}

func (h *Handler) applyRepo(ctx context.Context, pmodel *projectmodel.Project, ir *repo, result *importResult) {
	if err := validation.NormalizeAndValidate(ir); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s repo %q: %s", pmodel.Name, ir.FullName, validation.Summary(validation.Humanize(err))))
		return
	}
	// Same rule as repo/endpoint.go::Bind: org-scoped projects route
	// by owner login and take no per-repo bindings.
	if pmodel.Scope == projectmodel.ScopeOrg {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s repo %s: project is org-scoped, repos cannot be bound", pmodel.Name, ir.FullName))
		return
	}
	existing, err := h.Runtime.Store.Repo.Get(ctx, ir.FullName)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("repo %s: %v", ir.FullName, err))
		return
	}
	r := &repomodel.Repo{
		FullName:             ir.FullName,
		ProjectID:            pmodel.ID,
		MaxConcurrentRunners: ir.MaxConcurrentRunners,
		Tags:                 ir.Tags,
	}
	if existing != nil {
		r.CreatedAt = existing.CreatedAt
		result.Repos.Updated++
	} else {
		result.Repos.Created++
	}
	if err := h.Runtime.Store.Repo.Put(ctx, r); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("repo %s: %v", ir.FullName, err))
	}
}

// sanitizeAndDedupeLabels mirrors pool/endpoint.go::input.finalizeLabels.
// Returned slice is fresh. The caller's input is not aliased. Empty
// post-sanitize entries and duplicates collapse so the persisted
// extra_labels exactly match what the runner registers with.
func sanitizeAndDedupeLabels(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		s := pooldomain.SanitizeLabel(raw)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// materializeLT mirrors pool.endpoint.materializeLT - kept inline
// here to avoid an import cycle (pool would have to depend on backup
// or vice versa) and because the backup path doesn't need any of the
// surrounding pool-handler conveniences.
func (h *Handler) materializeLT(ctx context.Context, p *poolmodel.Pool, projectName string, projectTags map[string]string) error {
	if h.Runtime.EC2 == nil {
		if p.LaunchTemplateID == "" {
			p.LaunchTemplateID = "lt-dev-" + uuid.New().String()[:8]
			p.LaunchTemplateVersion = 1
		} else {
			p.LaunchTemplateVersion++
		}
		return nil
	}
	runnerVersion := p.RunnerVersion
	if h.Runtime.RunnerVersion != nil {
		runnerVersion = h.Runtime.RunnerVersion.Resolve(p.RunnerVersion)
	}
	bootstrapToken, _ := h.Runtime.BootstrapAPIToken.Load().(string)
	return ec2lt.CreateOrUpdate(ctx, h.Runtime.EC2, h.Runtime.IAM, p, projectName, projectTags,
		h.Runtime.Config.Server.PublicURL, runnerVersion, bootstrapToken)
}

func projectFromModel(p *projectmodel.Project) project {
	return project{
		Name:                 p.Name,
		MaxConcurrentRunners: p.MaxConcurrentRunners,
		Tags:                 p.Tags,
		Scope:                p.Scope,
		OrgName:              p.OrgName,
		RunnerGroupID:        p.RunnerGroupID,
		Disabled:             p.Disabled,
	}
}

func poolFromModel(p *poolmodel.Pool) pool {
	return pool{
		Name:                 p.Name,
		IsDefault:            p.IsDefault,
		Priority:             p.Priority,
		AMIID:                p.AMIID,
		InstanceTypes:        p.InstanceTypes,
		SubnetIDs:            p.SubnetIDs,
		SecurityGroupIDs:     p.SecurityGroupIDs,
		IAMInstanceProfile:   p.IAMInstanceProfile,
		RootVolumeGB:         p.RootVolumeGB,
		MaxRuntimeMinutes:    p.MaxRuntimeMinutes,
		MaxConcurrentRunners: p.MaxConcurrentRunners,
		Spot:                 p.Spot,
		SpawnMethod:          p.SpawnMethod,
		AllocationStrategy:   p.AllocationStrategy,
		ExtraLabels:          p.ExtraLabels,
		Tags:                 p.Tags,
		RunnerVersion:        p.RunnerVersion,
		RunnerUser:           p.RunnerUser,
		UserDataExtra:        p.UserDataExtra,
		Disabled:             p.Disabled,
	}
}

func repoFromModel(r *repomodel.Repo) repo {
	return repo{
		FullName:             r.FullName,
		MaxConcurrentRunners: r.MaxConcurrentRunners,
		Tags:                 r.Tags,
	}
}

// projectModelFor returns the project row to persist - either the
// existing row mutated in place (preserving ID + CreatedAt) or a
// fresh row with a new UUID. Scope coercion mirrors the project
// handler's Normalize() so import stays consistent with create.
func projectModelFor(existing *projectmodel.Project, bp *project) *projectmodel.Project {
	scope := bp.Scope
	if scope == "" {
		scope = projectmodel.ScopeRepo
	}
	tags := bp.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	if existing != nil {
		existing.MaxConcurrentRunners = bp.MaxConcurrentRunners
		existing.Tags = tags
		existing.Scope = scope
		existing.OrgName = bp.OrgName
		existing.RunnerGroupID = bp.RunnerGroupID
		existing.Disabled = bp.Disabled
		return existing
	}
	return &projectmodel.Project{
		ID:                   uuid.New().String(),
		Name:                 bp.Name,
		MaxConcurrentRunners: bp.MaxConcurrentRunners,
		Tags:                 tags,
		Scope:                scope,
		OrgName:              bp.OrgName,
		RunnerGroupID:        bp.RunnerGroupID,
		Disabled:             bp.Disabled,
	}
}

// poolModelFor mirrors projectModelFor for pool rows. Carries the
// existing LT pointer forward when updating so ec2lt.CreateOrUpdate
// takes the version-bump path rather than allocating a fresh template.
func poolModelFor(existing *poolmodel.Pool, projectID string, ip *pool) *poolmodel.Pool {
	if existing != nil {
		existing.Name = ip.Name
		existing.IsDefault = ip.IsDefault
		existing.Priority = ip.Priority
		existing.AMIID = ip.AMIID
		existing.InstanceTypes = ip.InstanceTypes
		existing.SubnetIDs = ip.SubnetIDs
		existing.SecurityGroupIDs = ip.SecurityGroupIDs
		existing.IAMInstanceProfile = ip.IAMInstanceProfile
		existing.RootVolumeGB = ip.RootVolumeGB
		existing.MaxRuntimeMinutes = ip.MaxRuntimeMinutes
		existing.MaxConcurrentRunners = ip.MaxConcurrentRunners
		existing.Spot = ip.Spot
		existing.SpawnMethod = ip.SpawnMethod
		existing.AllocationStrategy = ip.AllocationStrategy
		existing.ExtraLabels = ip.ExtraLabels
		existing.Tags = ip.Tags
		existing.RunnerVersion = ip.RunnerVersion
		existing.RunnerUser = ip.RunnerUser
		existing.UserDataExtra = ip.UserDataExtra
		existing.Disabled = ip.Disabled
		return existing
	}
	return &poolmodel.Pool{
		ID:                   uuid.New().String(),
		ProjectID:            projectID,
		Name:                 ip.Name,
		IsDefault:            ip.IsDefault,
		Priority:             ip.Priority,
		AMIID:                ip.AMIID,
		InstanceTypes:        ip.InstanceTypes,
		SubnetIDs:            ip.SubnetIDs,
		SecurityGroupIDs:     ip.SecurityGroupIDs,
		IAMInstanceProfile:   ip.IAMInstanceProfile,
		RootVolumeGB:         ip.RootVolumeGB,
		MaxRuntimeMinutes:    ip.MaxRuntimeMinutes,
		MaxConcurrentRunners: ip.MaxConcurrentRunners,
		Spot:                 ip.Spot,
		SpawnMethod:          ip.SpawnMethod,
		AllocationStrategy:   ip.AllocationStrategy,
		ExtraLabels:          ip.ExtraLabels,
		Tags:                 ip.Tags,
		RunnerVersion:        ip.RunnerVersion,
		RunnerUser:           ip.RunnerUser,
		UserDataExtra:        ip.UserDataExtra,
		Disabled:             ip.Disabled,
	}
}
