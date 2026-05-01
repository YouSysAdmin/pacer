// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package webhook is the GitHub webhook ingest.
// Single HTTP endpoint at POST /api/webhook:
//
//  1. Verify X-Hub-Signature-256 HMAC over the raw body (constant-time).
//  2. Persist the delivery to webhook_deliveries for replay/debug.
//  3. Dispatch by X-GitHub-Event header.
//     - ping          -> ack
//     - workflow_job  -> action switch (queued / in_progress / completed)
//     - other         -> logged and dropped (200 so GitHub stops retrying)
//
// On workflow_job:queued the handler does a repo-to-project lookup,
// audit-logs, and inserts jobs.queued.
// Concurrency caps are enforced at claim time, not enqueue time,
// so over-cap jobs sit in the queue until the orchestrator opens a slot.
//
// No store.go in this package - webhook handler writes via the job +
// audit stores plus a small inline INSERT to webhook_deliveries.
// (If webhook_deliveries grows real query needs, promote it to its
// own domain.)
//
// File layout:
//
//	webhook.go    package doc + shared constants (this file)
//	endpoint.go   Fiber Handler + HTTP methods
package webhook
