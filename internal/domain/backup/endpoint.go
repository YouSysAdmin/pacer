// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/yousysadmin/pacer/internal/core/ec2lt"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/response"
	"github.com/yousysadmin/pacer/internal/models/audit"
	poolmodel "github.com/yousysadmin/pacer/internal/models/pool"
	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	repomodel "github.com/yousysadmin/pacer/internal/models/repo"
)

type Handler struct {
	Runtime *env.Runtime
}

// formatVersion is the schema version stamped into every export.
// Bump when the snapshot shape changes incompatibly; Import refuses
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

type project struct {
	Name                 string            `json:"name"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners"`
	Tags                 map[string]string `json:"tags,omitempty"`
	Scope                string            `json:"scope,omitempty"`
	OrgName              string            `json:"org_name,omitempty"`
	RunnerGroupID        int               `json:"runner_group_id,omitempty"`
	Disabled             bool              `json:"disabled,omitempty"`
	Pools                []pool            `json:"pools,omitempty"`
	Repos                []repo            `json:"repos,omitempty"`
}

type pool struct {
	Name                 string            `json:"name"`
	IsDefault            bool              `json:"is_default,omitempty"`
	Priority             int               `json:"priority"`
	AMIID                string            `json:"ami_id"`
	InstanceTypes        []string          `json:"instance_types"`
	SubnetIDs            []string          `json:"subnet_ids"`
	SecurityGroupIDs     []string          `json:"security_group_ids"`
	IAMInstanceProfile   string            `json:"iam_instance_profile,omitempty"`
	RootVolumeGB         int               `json:"root_volume_gb"`
	MaxRuntimeMinutes    int               `json:"max_runtime_minutes"`
	MaxConcurrentRunners int               `json:"max_concurrent_runners"`
	Spot                 bool              `json:"spot,omitempty"`
	SpawnMethod          string            `json:"spawn_method,omitempty"`
	AllocationStrategy   string            `json:"allocation_strategy,omitempty"`
	ExtraLabels          []string          `json:"extra_labels,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
	RunnerVersion        string            `json:"runner_version,omitempty"`
	RunnerUser           string            `json:"runner_user,omitempty"`
	UserDataExtra        string            `json:"user_data_extra,omitempty"`
	Disabled             bool              `json:"disabled,omitempty"`
}

type repo struct {
	FullName             string            `json:"full_name"`
	MaxConcurrentRunners *int              `json:"max_concurrent_runners,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
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
	h.audit(c, audit.ActionConfigExported, "", audit.Detail(map[string]any{
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

	h.audit(c, audit.ActionConfigImported, "", audit.Detail(map[string]any{
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
	if bp.Name == "" {
		result.Errors = append(result.Errors, "project: name required")
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
	if ip.Name == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: pool name required", pmodel.Name))
		return
	}
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
	if err := h.Runtime.Store.Pool.Put(ctx, plmodel); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s pool %s: %v", pmodel.Name, ip.Name, err))
		return
	}
}

func (h *Handler) applyRepo(ctx context.Context, pmodel *projectmodel.Project, ir *repo, result *importResult) {
	if ir.FullName == "" {
		result.Errors = append(result.Errors, fmt.Sprintf("project %s: repo full_name required", pmodel.Name))
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

// materializeLT mirrors pool.endpoint.materializeLT - kept inline
// here to avoid an import cycle (pool would have to depend on backup
// or vice versa) and because the backup path doesn't need any of the
// surrounding pool-handler conveniences.
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
	return ec2lt.CreateOrUpdate(ctx, h.Runtime.EC2, h.Runtime.IAM, p, projectName, projectTags)
}

func (h *Handler) audit(c *fiber.Ctx, action, targetID, detail string) {
	_ = h.Runtime.Store.Audit.Put(c.UserContext(), &audit.Entry{
		ID:         uuid.NewString(),
		Action:     action,
		TargetType: "config",
		TargetID:   targetID,
		Detail:     detail,
		ClientIP:   c.IP(),
		OccurredAt: time.Now().UTC(),
	})
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
		ID:                   uuid.NewString(),
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
		ID:                   uuid.NewString(),
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
