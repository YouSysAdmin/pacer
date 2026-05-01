// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package runner is the runner self-registration HTTP API.
// Two endpoints, both authenticated by the HMAC-signed callback token
// embedded in the spawning instance's user-data:
//
//	POST /api/runner/register   instance up, requesting JIT runner config
//	POST /api/runner/complete   job finished, instance about to halt
//
// Auth chain on every call:
//  1. callback.Verify (HMAC + expiry - proves the token came from this
//     server and is still valid)
//  2. constant-time compare against Job.CallbackTokenHash (binds the
//     token to a single job; replay across jobs blocked)
//  3. caller-side status check (Register requires `claimed`)
//
// Any auth failure writes a 401/404 and returns nil; the caller
// returns nil to Fiber so the response stands.
// No store.go in this package - runner endpoints touch existing job/instance/audit
// stores; there is no Runner entity of its own.
//
// File layout:
//
//	runner.go     package doc + shared constants (this file)
//	endpoint.go   Fiber Handler + HTTP methods
package runner
