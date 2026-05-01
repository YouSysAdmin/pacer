// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package pkg owns build-time identity (binary name + version string).
// Version is a var so the build can override it via:
//
//	-ldflags "-X pacer/pkg.Version=$(git describe --tags --always --dirty)"
package pkg

const AppName = "pacer"

var Version = "devel"
