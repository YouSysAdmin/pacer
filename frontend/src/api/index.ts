// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Per-resource API namespaces over the fetch wrapper in client.ts.
// Response payloads are typed as the views need them -- the backend
// stays the source of truth; don't invent a full schema here.

import { call } from './client'

export { ApiError } from './client'
export type { FieldError } from './client'

export const projects = {
  list: () => call('/api/projects'),
  get: (id: string) => call(`/api/projects/${id}`),
  create: (body: unknown) => call('/api/projects', { method: 'POST', body: JSON.stringify(body) }),
  update: (id: string, body: unknown) =>
    call(`/api/projects/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  delete: (id: string) => call(`/api/projects/${id}`, { method: 'DELETE' }),
}

export const pools = {
  list: () => call('/api/pools'),
  listByProject: (projectID: string) => call(`/api/projects/${projectID}/pools`),
  get: (id: string) => call(`/api/pools/${id}`),
  create: (projectID: string, body: unknown) =>
    call(`/api/projects/${projectID}/pools`, { method: 'POST', body: JSON.stringify(body) }),
  update: (id: string, body: unknown) =>
    call(`/api/pools/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  delete: (id: string) => call(`/api/pools/${id}`, { method: 'DELETE' }),
}

export const repos = {
  list: () => call('/api/repos'),
  // bind upserts -- POST /api/repos accepts {full_name, project_id,
  // max_concurrent_runners, tags} and ON CONFLICT updates the row,
  // so the same call covers create + edit.
  bind: (body: unknown) => call('/api/repos', { method: 'POST', body: JSON.stringify(body) }),
  // backend expects /api/repos/:owner/:name - slash kept literal
  unbind: (fullName: string) => call(`/api/repos/${fullName}`, { method: 'DELETE' }),
}

export interface JobsListParams {
  status?: string
  limit?: number
  offset?: number
}

export const jobs = {
  // Returns the envelope {entries, total, limit, offset}. The Jobs
  // page paginates against `total`. Older callers that just want a
  // bare array can read `.entries` from the result.
  list: ({ status, limit = 50, offset = 0 }: JobsListParams = {}) => {
    const qs = new URLSearchParams()
    if (status) qs.set('status', status)
    if (limit) qs.set('limit', String(limit))
    if (offset) qs.set('offset', String(offset))
    const q = qs.toString()
    return call(`/api/jobs${q ? '?' + q : ''}`)
  },
  // Detail bundle: {job, payload, instance, audit}. Backed by
  // GET /api/jobs/:id. The modal on the jobs page renders all four.
  get: (id: string) => call(`/api/jobs/${id}`),
}

export const auth = {
  login: (email: string, password: string) =>
    call('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  logout: () => call('/api/auth/logout', { method: 'POST' }),
  me: () => call('/api/auth/me'),
  info: () => call('/api/auth/info'),
}

export interface StatsWindowParams {
  from?: string
  to?: string
}

export const stats = {
  rollup: ({ from, to, groupBy }: StatsWindowParams & { groupBy?: string } = {}) => {
    const qs = new URLSearchParams()
    if (from) qs.set('from', from)
    if (to) qs.set('to', to)
    if (groupBy) qs.set('group_by', groupBy)
    const q = qs.toString()
    return call(`/api/stats${q ? '?' + q : ''}`)
  },
  // Daily success/failed counts for the Overview chart. Same window
  // semantics as rollup() - omit params for the last-30-days default.
  timeseries: ({ from, to }: StatsWindowParams = {}) => {
    const qs = new URLSearchParams()
    if (from) qs.set('from', from)
    if (to) qs.set('to', to)
    const q = qs.toString()
    return call(`/api/stats/timeseries${q ? '?' + q : ''}`)
  },
  // Top-N senders by job count over a window. Powers the "who runs
  // the most CI" panel on the stats page.
  topUsers: ({ from, to, limit }: StatsWindowParams & { limit?: number } = {}) => {
    const qs = new URLSearchParams()
    if (from) qs.set('from', from)
    if (to) qs.set('to', to)
    if (limit != null) qs.set('limit', String(limit))
    const q = qs.toString()
    return call(`/api/stats/top-users${q ? '?' + q : ''}`)
  },
}

export const backup = {
  // Export returns the raw Response so the caller can stream the
  // attachment to disk via Blob -- the standard call() wrapper would
  // JSON.parse the body and lose the Content-Disposition filename.
  exportRaw: () => fetch('/api/backup/export', { credentials: 'same-origin' }),
  import: (snapshot: unknown) =>
    call('/api/backup/import', { method: 'POST', body: JSON.stringify(snapshot) }),
}

export interface AuditListParams {
  since?: string
  until?: string
  action?: string
  actor?: string
  targetType?: string
  targetID?: string
  q?: string
  limit?: number
  offset?: number
}

export const audit = {
  // q is a free-text search hitting target_id, detail (JSON blob),
  // client_ip, actor_email, request_id, and action all at once --
  // the most common way operators look up an event when they have
  // a clue (instance id, IP, job id) but not the exact action name.
  list: ({
    since,
    until,
    action,
    actor,
    targetType,
    targetID,
    q: query,
    limit,
    offset,
  }: AuditListParams = {}) => {
    const qs = new URLSearchParams()
    if (since) qs.set('since', since)
    if (until) qs.set('until', until)
    if (action) qs.set('action', action)
    if (actor) qs.set('actor', actor)
    if (targetType) qs.set('target_type', targetType)
    if (targetID) qs.set('target_id', targetID)
    if (query) qs.set('q', query)
    if (limit != null) qs.set('limit', String(limit))
    if (offset != null) qs.set('offset', String(offset))
    const s = qs.toString()
    return call(`/api/audit${s ? '?' + s : ''}`)
  },
  // Manual cleanup: delete every audit row older than N days.
  // Returns {deleted, cutoff, older_than_days}. The prune itself
  // lands an audit.pruned row so the log retains a trace of who
  // cleaned what.
  prune: (olderThanDays: number) =>
    call('/api/audit/prune', {
      method: 'POST',
      body: JSON.stringify({ older_than_days: olderThanDays }),
    }),
}

export const settings = {
  getBootstrapToken: () => call('/api/settings/bootstrap-token'),
  rotateBootstrapToken: () => call('/api/settings/bootstrap-token/rotate', { method: 'POST' }),
  // Retention periods (in days) for audit_log + webhook_deliveries.
  // GET returns effective + default values. PUT accepts either or
  // both. Send 0 to clear an override (revert to YAML default).
  getRetention: () => call('/api/settings/retention'),
  putRetention: (body: unknown) =>
    call('/api/settings/retention', {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
}

export const health = () => call('/healthz')

// System health: in-process status of background workers (reaper,
// preflight). Polled by the dashboard layout to drive the banner.
// reconcile() forces an immediate reaper sweep so an operator who's
// just fixed an IAM perm doesn't wait for the next 60s tick.
export const systemHealth = {
  list: () => call('/api/health'),
  reconcile: () => call('/api/reconcile', { method: 'POST' }),
}
