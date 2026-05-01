// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package pacer hosts the //go:embed of the prerendered Svelte Frontend.
package pacer

import "embed"

// Frontend is the embedded Svelte build. `make frontend` runs `bun run build`
// in frontend/
//
//go:embed all:frontend/dist
var Frontend embed.FS
