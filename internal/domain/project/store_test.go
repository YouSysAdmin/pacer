// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package project

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	projectmodel "github.com/yousysadmin/pacer/internal/models/project"
	"github.com/yousysadmin/pacer/internal/testutil"
)

func TestProject_PutGet_RoundTrip(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()

	in := &projectmodel.Project{
		ID:                   "p-1",
		Name:                 "demo",
		MaxConcurrentRunners: 7,
		Tags:                 map[string]string{"team": "core", "cost_center": "alpha"},
		Scope:                projectmodel.ScopeRepo,
	}
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "p-1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v %v", got, err)
	}
	if got.Name != "demo" || got.MaxConcurrentRunners != 7 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Tags["team"] != "core" || got.Tags["cost_center"] != "alpha" {
		t.Fatalf("tags lost: %v", got.Tags)
	}
	if got.Scope != projectmodel.ScopeRepo {
		t.Fatalf("scope: want %q, got %q", projectmodel.ScopeRepo, got.Scope)
	}
}

func TestProject_GetByName_NotFound(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	got, err := s.GetByName(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetByName err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing project, got %v", got)
	}
}

func TestProject_GetByOrgName_CaseInsensitive(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()

	if err := s.Put(ctx, &projectmodel.Project{
		ID: "p-1", Name: "octocat-runners",
		Scope: projectmodel.ScopeOrg, OrgName: "Octocat", RunnerGroupID: 7,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	for _, q := range []string{"octocat", "OCTOCAT", "Octocat", "OctOcAt"} {
		got, err := s.GetByOrgName(ctx, q)
		if err != nil {
			t.Fatalf("GetByOrgName(%q): %v", q, err)
		}
		if got == nil {
			t.Fatalf("GetByOrgName(%q): nil; expected p-1", q)
		}
		if got.ID != "p-1" || got.RunnerGroupID != 7 {
			t.Fatalf("GetByOrgName(%q): %+v", q, got)
		}
	}
}

func TestProject_GetByOrgName_RepoScopeIgnored(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()

	if err := s.Put(ctx, &projectmodel.Project{
		ID: "repo-proj", Name: "repo-proj",
		Scope: projectmodel.ScopeRepo, OrgName: "Octocat",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.GetByOrgName(ctx, "octocat")
	if err != nil {
		t.Fatalf("GetByOrgName: %v", err)
	}
	if got != nil {
		t.Fatalf("repo-scoped project returned by GetByOrgName: %+v", got)
	}
}

func TestProject_OrgScopeUniqueIndex(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()

	if err := s.Put(ctx, &projectmodel.Project{
		ID: "first", Name: "first",
		Scope: projectmodel.ScopeOrg, OrgName: "octocat",
	}); err != nil {
		t.Fatalf("first Put: %v", err)
	}

	err := s.Put(ctx, &projectmodel.Project{
		ID: "second", Name: "second",
		Scope: projectmodel.ScopeOrg, OrgName: "octocat",
	})
	if err == nil {
		t.Fatalf("expected unique-constraint violation for duplicate org_name, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected UNIQUE error, got: %v", err)
	}
}

func TestProject_OrgUniqueIgnoresRepoScope(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := context.Background()

	if err := s.Put(ctx, &projectmodel.Project{
		ID: "org-1", Name: "org-1",
		Scope: projectmodel.ScopeOrg, OrgName: "octocat",
	}); err != nil {
		t.Fatalf("org Put: %v", err)
	}
	if err := s.Put(ctx, &projectmodel.Project{
		ID: "repo-1", Name: "repo-1",
		Scope: projectmodel.ScopeRepo, OrgName: "octocat",
	}); err != nil {
		t.Fatalf("repo Put with same org_name should succeed: %v", err)
	}
}

func TestProject_ConcurrentRunnerCount(t *testing.T) {
	db := testutil.OpenTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Put(ctx, &projectmodel.Project{ID: "p", Name: "p"}); err != nil {
		t.Fatalf("Put project: %v", err)
	}
	insertPool(t, db, "po", "p")

	insertJob(t, db, "j1", "p", "po", "claimed")
	insertJob(t, db, "j2", "p", "po", "starting")
	insertJob(t, db, "j3", "p", "po", "running")
	insertJob(t, db, "j4", "p", "po", "queued")
	insertJob(t, db, "j5", "p", "po", "completed")

	n, err := s.ConcurrentRunnerCount(ctx, "p")
	if err != nil {
		t.Fatalf("ConcurrentRunnerCount: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 active, got %d", n)
	}
}

func insertPool(t *testing.T, db *sql.DB, id, projectID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO pools (
			id, project_id, name, is_default, priority,
			ami_id, instance_types, subnet_ids, security_group_ids,
			iam_instance_profile, root_volume_gb, max_runtime_minutes,
			max_concurrent_runners, spot, spawn_method, allocation_strategy,
			extra_labels, tags, runner_version, runner_user, disabled
		)
		VALUES (?, ?, 'default', 1, 100,
			'ami-test', '["t3.large"]', '["subnet-1"]', '["sg-1"]',
			'', 30, 60, 5, 1, 'fleet', 'cost',
			'[]', '{}', '', '', 0)`, id, projectID)
	if err != nil {
		t.Fatalf("insertPool: %v", err)
	}
}

var jobCounter int64

func insertJob(t *testing.T, db *sql.DB, id, projectID, poolID, status string) {
	t.Helper()
	jobCounter++
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs (
			id, gh_job_id, gh_run_id, installation_id,
			repo_full_name, project_id, pool_id, status, payload
		) VALUES (?, ?, 1, 1, 'octocat/x', ?, ?, ?, '{}')`,
		id, jobCounter, projectID, poolID, status)
	if err != nil {
		t.Fatalf("insertJob: %v", err)
	}
}
