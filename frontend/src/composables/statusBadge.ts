// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// What a status means, per vocabulary.
//
// One table per scope, kept apart on purpose: the same word can carry
// different weight in different vocabularies ("running" is the healthy
// resting state of an instance but an in-flight phase of a job), so a
// single shared map would flatten distinctions the reader relies on.
//
// The values mirror internal/models/job/job.go and
// internal/models/instance/instance.go - the backend is the source of
// truth; an unknown status renders as a bare neutral pill rather than
// guessing.

export type StatusScope = 'job' | 'instance'

const JOB: Record<string, string> = {
  // The three pre-run phases share a colour: from the operator's seat
  // they are all "waiting for the runner to start".
  queued: 'badge-warning',
  claimed: 'badge-warning',
  starting: 'badge-warning',
  running: 'badge-info',
  completed: 'badge-success',
  failed: 'badge-danger',
  // User-initiated stop: nothing went wrong, nothing is active.
  cancelled: 'badge-neutral',
  // The sweeper had to kill it - that is a failure of the run even
  // though the reap itself worked.
  reaped: 'badge-danger',
}

const INSTANCE: Record<string, string> = {
  starting: 'badge-warning',
  running: 'badge-info',
  terminated: 'badge-neutral',
  reaped: 'badge-danger',
}

const SCOPES: Record<StatusScope, Record<string, string>> = {
  job: JOB,
  instance: INSTANCE,
}

export function statusBadgeClass(status: string, scope: StatusScope): string {
  const kind = SCOPES[scope][status]
  return kind ? `badge ${kind}` : 'badge'
}
