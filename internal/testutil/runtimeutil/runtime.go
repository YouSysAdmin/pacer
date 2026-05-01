// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package runtimeutil builds a minimal *env.Runtime for handler tests.
//
// Lives in a sibling package to internal/testutil so domain stores that
// depend on testutil.OpenTestDB don't pull in the full domain graph
// (which would cycle: testutil -> domain X -> testutil).
// Handler tests (which already need the full graph) import this package as an
// external test package.
package runtimeutil

import (
	"database/sql"
	"testing"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/database"
	"github.com/yousysadmin/pacer/internal/domain/audit"
	"github.com/yousysadmin/pacer/internal/domain/instance"
	"github.com/yousysadmin/pacer/internal/domain/job"
	"github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/domain/project"
	"github.com/yousysadmin/pacer/internal/domain/repo"
	"github.com/yousysadmin/pacer/internal/domain/stats"
	"github.com/yousysadmin/pacer/internal/domain/store"
	"github.com/yousysadmin/pacer/internal/domain/user"
	"github.com/yousysadmin/pacer/internal/testutil"
)

type dbWrapper struct{ db *sql.DB }

func (w *dbWrapper) Close() error   { return w.db.Close() }
func (w *dbWrapper) Path() string   { return ":memory:" }
func (w *dbWrapper) Engine() string { return "sqlite" }
func (w *dbWrapper) DB() *sql.DB    { return w.db }

// NewRuntime builds a minimal *env.Runtime backed by a freshly-migrated
// SQLite, with all per-domain stores wired up. Caller passes in a config
// (typically with WebhookSecret set for webhook tests). EC2, IAM, GHApp,
// Pricing, OIDC are nil -- tests for paths that need them must
// construct fakes themselves.
func NewRuntime(t *testing.T, cfg *env.Config) *env.Runtime {
	t.Helper()
	db := testutil.OpenTestDB(t)
	return &env.Runtime{
		Config: cfg,
		DB:     database.Database(&dbWrapper{db: db}),
		Store: &store.Store{
			User:     user.NewStore(db),
			Project:  project.NewStore(db),
			Pool:     pool.NewStore(db),
			Repo:     repo.NewStore(db),
			Job:      job.NewStore(db),
			Instance: instance.NewStore(db),
			Audit:    audit.NewStore(db),
			Stats:    stats.NewStore(db),
		},
	}
}
