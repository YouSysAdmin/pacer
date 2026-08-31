// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Tiny fetch wrapper.  Throws ApiError(msg) on non-2xx.  Backend's
// error envelope is {error: "..."}. We surface that string.
//
// On 401 the wrapper bounces the user to /login?next=<current path>
// so any protected page that loads without a valid session lands
// them at the form instead of rendering "authentication required"
// inline.  /api/auth/* calls are exempt (login + me + logout
// SHOULD be able to fail without triggering a redirect loop).

export interface FieldError {
  field: string
  rule?: string
  message: string
}

// Still `instanceof Error`, and `err.fields` stays a plain property,
// so call sites written against the old api.js keep working: flash
// banners read err.message, per-input forms opt in via err.fields.
export class ApiError extends Error {
  status: number
  fields?: FieldError[]

  constructor(message: string, status: number, fields?: FieldError[]) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    if (fields) this.fields = fields
  }
}

export async function call<T = unknown>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(opts.headers || {}),
    },
  })
  if (
    res.status === 401 &&
    typeof window !== 'undefined' &&
    !path.startsWith('/api/auth/') &&
    window.location.pathname !== '/login'
  ) {
    const next = encodeURIComponent(window.location.pathname + window.location.search)
    window.location.href = `/login?next=${next}`
    // Throw so callers don't try to consume a body. The browser is
    // already navigating away.
    throw new Error('redirecting to login')
  }
  if (res.status === 204) return null as T
  const text = await res.text()
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    let fields: FieldError[] | undefined
    try {
      const j = JSON.parse(text)
      if (j.error) msg = j.error
      // Structured field errors from the validator: [{field, rule,
      // message}].
      if (Array.isArray(j.fields)) fields = j.fields
    } catch {
      // body not JSON
    }
    throw new ApiError(msg, res.status, fields)
  }
  return text ? (JSON.parse(text) as T) : (null as T)
}
