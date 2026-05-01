// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package sqlite

import (
	"github.com/yousysadmin/pacer/internal/domain/audit"
	"github.com/yousysadmin/pacer/internal/domain/instance"
	"github.com/yousysadmin/pacer/internal/domain/job"
	"github.com/yousysadmin/pacer/internal/domain/pool"
	"github.com/yousysadmin/pacer/internal/domain/project"
	"github.com/yousysadmin/pacer/internal/domain/repo"
	"github.com/yousysadmin/pacer/internal/domain/stats"
	"github.com/yousysadmin/pacer/internal/domain/store"
	"github.com/yousysadmin/pacer/internal/domain/user"
	"github.com/yousysadmin/pacer/internal/domain/webhook"
)

// BindStore wires the per-domain SQLite stores into the aggregate
// Store handlers depend on.
// Each domain owns its own store.go: the default backend's stores live next to the domain.
// Future Postgres/MySQL backends will ship sibling constructors in
// `internal/database/postgres/`, `internal/database/mysql/` etc., and
// the per-engine BindStore here will pick the right ones.
func BindStore(s *SQLite) *store.Store {
	return &store.Store{
		User:     user.NewStore(s.db),
		Project:  project.NewStore(s.db),
		Pool:     pool.NewStore(s.db),
		Repo:     repo.NewStore(s.db),
		Job:      job.NewStore(s.db),
		Instance: instance.NewStore(s.db),
		Audit:    audit.NewStore(s.db),
		Stats:    stats.NewStore(s.db),
		Webhook:  webhook.NewStore(s.db),
	}
}
