// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Tiny fetch wrapper.  Throws Error(msg) on non-2xx.  Backend's
// error envelope is {error: "..."}; we surface that string.
//
// On 401 the wrapper bounces the user to /login?next=<current path>
// so any protected page that loads without a valid session lands
// them at the form instead of rendering "authentication required"
// inline.  /api/auth/* calls are exempt (login + me + logout
// SHOULD be able to fail without triggering a redirect loop).

async function call(path, opts = {}) {
  const res = await fetch(path, {
    credentials: "same-origin",
    ...opts,
    headers: {
      "Content-Type": "application/json",
      ...(opts.headers || {}),
    },
  });
  if (
    res.status === 401 &&
    typeof window !== "undefined" &&
    !path.startsWith("/api/auth/") &&
    window.location.pathname !== "/login"
  ) {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.href = `/login?next=${next}`;
    // Throw so callers don't try to consume a body; the browser is
    // already navigating away.
    throw new Error("redirecting to login");
  }
  if (res.status === 204) return null;
  const text = await res.text();
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    let fields = null;
    try {
      const j = JSON.parse(text);
      if (j.error) msg = j.error;
      // Structured field errors from the validator: [{field, rule,
      // message}]. Existing flash-banner code reads err.message and
      // keeps working; per-input forms can opt in via err.fields.
      if (Array.isArray(j.fields)) fields = j.fields;
    } catch {
      // body not JSON
    }
    const err = new Error(msg);
    if (fields) err.fields = fields;
    throw err;
  }
  return text ? JSON.parse(text) : null;
}

export const projects = {
  list: () => call("/api/projects"),
  get: (id) => call(`/api/projects/${id}`),
  create: (body) =>
    call("/api/projects", { method: "POST", body: JSON.stringify(body) }),
  update: (id, body) =>
    call(`/api/projects/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  delete: (id) => call(`/api/projects/${id}`, { method: "DELETE" }),
};

export const pools = {
  list: () => call("/api/pools"),
  listByProject: (projectID) => call(`/api/projects/${projectID}/pools`),
  get: (id) => call(`/api/pools/${id}`),
  create: (projectID, body) =>
    call(`/api/projects/${projectID}/pools`, { method: "POST", body: JSON.stringify(body) }),
  update: (id, body) =>
    call(`/api/pools/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  delete: (id) => call(`/api/pools/${id}`, { method: "DELETE" }),
};

export const repos = {
  list: () => call("/api/repos"),
  // bind upserts -- POST /api/repos accepts {full_name, project_id,
  // max_concurrent_runners, tags} and ON CONFLICT updates the row,
  // so the same call covers create + edit.
  bind: (body) =>
    call("/api/repos", { method: "POST", body: JSON.stringify(body) }),
  // backend expects /api/repos/:owner/:name — slash kept literal
  unbind: (fullName) => call(`/api/repos/${fullName}`, { method: "DELETE" }),
};

export const jobs = {
  list: (status, limit = 50) => {
    const qs = new URLSearchParams();
    if (status) qs.set("status", status);
    if (limit) qs.set("limit", String(limit));
    const q = qs.toString();
    return call(`/api/jobs${q ? "?" + q : ""}`);
  },
  // Detail bundle: {job, payload, instance, audit}. Backed by
  // GET /api/jobs/:id. The modal on the jobs page renders all four.
  get: (id) => call(`/api/jobs/${id}`),
};

export const auth = {
  login: (email, password) =>
    call("/api/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),
  logout: () => call("/api/auth/logout", { method: "POST" }),
  me: () => call("/api/auth/me"),
  info: () => call("/api/auth/info"),
};

export const stats = {
  rollup: ({ from, to, groupBy } = {}) => {
    const qs = new URLSearchParams();
    if (from) qs.set("from", from);
    if (to) qs.set("to", to);
    if (groupBy) qs.set("group_by", groupBy);
    const q = qs.toString();
    return call(`/api/stats${q ? "?" + q : ""}`);
  },
  // Daily success/failed counts for the Overview chart. Same window
  // semantics as rollup() - omit params for the last-30-days default.
  timeseries: ({ from, to } = {}) => {
    const qs = new URLSearchParams();
    if (from) qs.set("from", from);
    if (to) qs.set("to", to);
    const q = qs.toString();
    return call(`/api/stats/timeseries${q ? "?" + q : ""}`);
  },
  // Top-N senders by job count over a window. Powers the "who runs
  // the most CI" panel on the stats page.
  topUsers: ({ from, to, limit } = {}) => {
    const qs = new URLSearchParams();
    if (from) qs.set("from", from);
    if (to) qs.set("to", to);
    if (limit != null) qs.set("limit", String(limit));
    const q = qs.toString();
    return call(`/api/stats/top-users${q ? "?" + q : ""}`);
  },
};

export const backup = {
  // Export returns the raw Response so the caller can stream the
  // attachment to disk via Blob -- the standard call() wrapper would
  // JSON.parse the body and lose the Content-Disposition filename.
  exportRaw: () =>
    fetch("/api/backup/export", { credentials: "same-origin" }),
  import: (snapshot) =>
    call("/api/backup/import", { method: "POST", body: JSON.stringify(snapshot) }),
};

export const audit = {
  list: ({ since, until, action, actor, targetType, limit, offset } = {}) => {
    const qs = new URLSearchParams();
    if (since) qs.set("since", since);
    if (until) qs.set("until", until);
    if (action) qs.set("action", action);
    if (actor) qs.set("actor", actor);
    if (targetType) qs.set("target_type", targetType);
    if (limit != null) qs.set("limit", String(limit));
    if (offset != null) qs.set("offset", String(offset));
    const q = qs.toString();
    return call(`/api/audit${q ? "?" + q : ""}`);
  },
};

export const health = () => call("/healthz");
