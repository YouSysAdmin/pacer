// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package migrations embeds the SQLite goose migrations as a sibling FS
// the sqlite backend hands to goose.SetBaseFS.
// Kept in its own package so the //go:embed directive lives next to the .sql files it references.
package migrations

import "embed"

// FS is the migrations filesystem that goose walks for *.sql files.
//
//go:embed *.sql
var FS embed.FS
