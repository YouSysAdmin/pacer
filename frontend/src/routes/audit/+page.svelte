<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { audit, projects, pools } from "$lib/api.js";
  import { confirmDialog } from "$lib/confirm.svelte.js";
  import { onMount, untrack } from "svelte";

  // UTC-aligned date helpers (match the stats page convention).
  function todayUTC() {
    const d = new Date();
    return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
  }
  function isoDate(d) { return d.toISOString().slice(0, 10); }

  // The backend's window is [since, until) -- exclusive on the right.
  // The UI treats the user-picked end of the range as INCLUSIVE, so we
  // bump +1 day before dispatching.
  function nextDayUTC(yyyyMMdd) {
    const d = new Date(yyyyMMdd + "T00:00:00Z");
    d.setUTCDate(d.getUTCDate() + 1);
    return isoDate(d);
  }

  // Range presets: a number (days) or 'all' = no time bound.
  const RANGES = [
    { value: "1",   label: "Range: 1d"  },
    { value: "7",   label: "Range: 7d"  },
    { value: "30",  label: "Range: 30d" },
    { value: "90",  label: "Range: 90d" },
    { value: "all", label: "Range: all" },
  ];

  let range = $state("7");
  // q is the free-text search across target_id, detail JSON,
  // client_ip, actor_email, request_id, and action. This is the
  // primary search affordance -- paste an instance id, an IP, a
  // job id, or part of an action name and find it. actionFilter
  // is kept around for power users who want an exact action match
  // (and is what `select` widgets would feed into).
  let q = $state("");
  let actionFilter = $state("");
  let limit = $state(50);
  let offset = $state(0);

  let loading = $state(false);
  let error = $state(null);
  let data = $state(null);

  // Manual prune state. pruneDays drives the dropdown; pruneMsg is
  // the toast-style banner shown after success. Failures land in
  // `error` (same surface as list failures).
  const PRUNE_OPTIONS = [
    { value: 1,   label: "1 day"    },
    { value: 7,   label: "7 days"   },
    { value: 15,  label: "15 days"  },
    { value: 30,  label: "30 days"  },
    { value: 90,  label: "90 days"  },
    { value: 180, label: "180 days" },
    { value: 365, label: "1 year"   },
    { value: 730, label: "2 years"  },
  ];
  let pruneDays = $state(90);
  let pruning = $state(false);
  let pruneMsg = $state(null);

  // One row open at a time -- toggling a different row closes the
  // previous one. Set semantics from the prior version were richer
  // than they needed to be; cloud-scope's pattern of single-open is
  // tighter and reduces "where did I click" confusion.
  let openID = $state(null);
  function toggleDetail(id) { openID = (openID === id) ? null : id; }

  // UUID-keyed targets (project / pool) are opaque on their own.
  // Resolve the human name once at mount and decorate the target block.
  // Pools render as <project>/<pool> to disambiguate identically named
  // pools across projects (same convention the stats page uses).
  // One-time fetch -- projects/pools rotate slowly enough that a stale
  // entry only means missing one decoration line, never a wrong one,
  // since we fall back to showing nothing extra.
  let projectName = $state(new Map());
  let poolName = $state(new Map());

  onMount(async () => {
    try {
      const [ps, pls] = await Promise.all([projects.list(), pools.list()]);
      const pm = new Map();
      for (const p of ps || []) pm.set(p.id, p.name);
      const lm = new Map();
      for (const pl of pls || []) {
        const proj = pm.get(pl.project_id) || pl.project_id;
        lm.set(pl.id, `${proj}/${pl.name}`);
      }
      projectName = pm;
      poolName = lm;
    } catch {
      // Best-effort -- if either fetch fails (auth, network),
      // we just skip the name decoration.
    }
  });

  function targetName(e) {
    if (!e.target_type || !e.target_id) return "";
    if (e.target_type === "project") return projectName.get(e.target_id) || "";
    if (e.target_type === "pool")    return poolName.get(e.target_id)    || "";
    return "";
  }

  function windowParams() {
    if (range === "all") return {};
    const days = Number(range);
    return {
      since: isoDate(new Date(todayUTC().getTime() - days * 86400_000)),
      until: nextDayUTC(isoDate(todayUTC())),
    };
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      data = await audit.list({
        ...windowParams(),
        q: q.trim() || undefined,
        action: actionFilter || undefined,
        limit,
        offset,
      });
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  // Manual prune. Confirms first because audit deletes are
  // irreversible -- shows the cutoff date (in operator-local time)
  // and the period so the user can sanity-check before the click.
  async function runPrune() {
    const days = Number(pruneDays);
    if (!Number.isFinite(days) || days <= 0) return;
    const cutoff = new Date(Date.now() - days * 86400_000);
    const ok = await confirmDialog({
      title: "Prune audit log",
      message:
        `Delete every audit entry older than ${days} day${days === 1 ? "" : "s"} ` +
        `(before ${cutoff.toLocaleString()})?\n\n` +
        `This cannot be undone. The prune itself will be recorded ` +
        `in the audit log.`,
      confirmLabel: "Delete",
      cancelLabel: "Cancel",
      confirmDanger: true,
    });
    if (!ok) return;
    pruning = true;
    pruneMsg = null;
    error = null;
    try {
      const r = await audit.prune(days);
      const n = r?.deleted ?? 0;
      const ago = r?.cutoff ? new Date(r.cutoff).toLocaleString() : `${days} days ago`;
      pruneMsg = `Deleted ${n.toLocaleString()} audit entr${n === 1 ? "y" : "ies"} older than ${ago}.`;
      offset = 0;
      await refresh();
    } catch (e) {
      error = e.message;
    } finally {
      pruning = false;
    }
  }

  function prevPage() {
    if (offset === 0) return;
    offset = Math.max(0, offset - limit);
    refresh();
  }
  function nextPage() {
    if (!data || offset + limit >= data.total) return;
    offset = offset + limit;
    refresh();
  }

  // Reset paging + reload when any filter changes. q is debounced
  // (300ms) so typing into the search box doesn't fire one request
  // per keystroke.
  //
  // untrack() is load-bearing: without it Svelte 5 picks up
  // `offset` as a tracked dep via refresh()'s internal read, and
  // clicking Next would re-run this effect and rewind offset back
  // to 0 -- the pager would silently do nothing. See the same
  // pattern (and the same fix) on the jobs page.
  $effect(() => {
    range; actionFilter; limit;
    untrack(() => {
      offset = 0;
      refresh();
    });
  });
  let qDebounce = null;
  $effect(() => {
    q;
    if (qDebounce) clearTimeout(qDebounce);
    qDebounce = setTimeout(() => {
      offset = 0;
      refresh();
    }, 300);
    return () => { if (qDebounce) clearTimeout(qDebounce); };
  });

  // ----- Cell formatters --------------------------------------------

  // Split occurred_at into HH:MM:SS + YYYY-MM-DD halves so the When
  // column reads as a stacked time/date pair.
  function fmtTime(t) {
    if (!t) return "";
    const d = new Date(t);
    if (isNaN(d)) return String(t);
    return d.toLocaleTimeString("en-GB", { hour12: false });
  }
  function fmtDate(t) {
    if (!t) return "";
    const d = new Date(t);
    if (isNaN(d)) return "";
    return d.toISOString().slice(0, 10);
  }

  // Just the category (job / pool / project / user / ...). The full
  // target_id is shown inside the expanded detail block, so leaving it
  // out of the table keeps the column narrow on long-id types like
  // UUIDs.
  function targetLabel(e) {
    return e.target_type || "-";
  }

  // Existing class mapping kept in sync with the Go-side audit action
  // taxonomy. Drives both the Outcome tag and the KPI failure count.
  function actionClass(a) {
    if (a.endsWith(".failed") || a.endsWith(".reaped") || a.endsWith(".exhausted") ||
        a.endsWith(".no_pool_match") ||
        a === "auth.login_failed" || a === "auth.oidc.login_denied" || a === "auth.oidc.login_failed") return "crit";
    if (a.endsWith(".retry")) return "warn";
    if (a.endsWith(".completed") || a.endsWith(".launched") || a.endsWith(".registered") ||
        a.endsWith(".created") || a.endsWith(".bound") ||
        a === "auth.oidc.login_succeeded" || a === "auth.login_succeeded") return "ok";
    return "info";
  }
  function outcomeLabel(a) {
    const c = actionClass(a);
    return c === "crit" ? "fail"
         : c === "warn" ? "retry"
         : c === "ok"   ? "ok"
         :                "info";
  }

  // Compact one-line summary of the detail JSON for the Event cell.
  // Mirrors cloud-scope's pattern: target ref first, then a few
  // key=value pairs from the detail blob, all separated by " / ".
  // Object/array values are rendered minimally so the line stays
  // short; full JSON is shown on expand.
  function eventSubline(e) {
    const parts = [];
    if (e.detail) {
      try {
        const o = JSON.parse(e.detail);
        for (const [k, v] of Object.entries(o)) {
          if (v == null || v === "") continue;
          let s;
          if (Array.isArray(v)) {
            if (v.length === 0) continue;
            s = v.join(",");
          } else if (typeof v === "object") {
            continue;
          } else {
            s = String(v);
          }
          if (s.length > 40) s = s.slice(0, 37) + "...";
          parts.push(`${k}=${s}`);
          if (parts.length >= 3) break;
        }
      } catch {
        // detail wasn't JSON -- fall through to the empty subline
      }
    }
    return parts.join(" / ");
  }

  // Structured detail block, sectioned by # action / # actor /
  // # target / # detail / # timestamp. The pre rendering in CSS
  // preserves spacing so the columns line up.
  function buildEvtDetail(e) {
    const lines = [];
    lines.push("# action");
    lines.push(`${e.action}  outcome=${outcomeLabel(e.action)}`);
    lines.push("");
    lines.push("# actor");
    if (e.actor_email)    lines.push("email      = " + e.actor_email);
    if (e.actor_user_id)  lines.push("user_id    = " + e.actor_user_id);
    if (!e.actor_email && !e.actor_user_id) lines.push("--           system");
    if (e.client_ip)      lines.push("ip         = " + e.client_ip);
    if (e.request_id)     lines.push("request_id = " + e.request_id);
    if (e.target_type || e.target_id) {
      lines.push("");
      lines.push("# target");
      if (e.target_type) lines.push("type       = " + e.target_type);
      if (e.target_id)   lines.push("id         = " + e.target_id);
      const tn = targetName(e);
      if (tn)            lines.push("name       = " + tn);
    }
    if (e.detail) {
      let parsed = null;
      try { parsed = JSON.parse(e.detail); } catch {
        // detail wasn't JSON -- fall through to the raw rendering
      }
      if (parsed && typeof parsed === "object" && Object.keys(parsed).length > 0) {
        lines.push("");
        lines.push("# detail");
        for (const [k, v] of Object.entries(parsed)) {
          const padded = (k + "            ").slice(0, 12);
          let s;
          if (v == null) s = "null";
          else if (Array.isArray(v)) s = "[" + v.join(", ") + "]";
          else if (typeof v === "object") s = JSON.stringify(v);
          else s = String(v);
          lines.push(padded + "= " + s);
        }
      } else if (!parsed) {
        lines.push("");
        lines.push("# detail (raw)");
        lines.push(e.detail);
      }
    }
    lines.push("");
    lines.push("# timestamp");
    lines.push(e.occurred_at || "");
    return lines.join("\n");
  }

  // ----- KPI tiles --------------------------------------------------
  // `events` reflects the full window (data.total). The other three
  // are sample stats over the visible page; on a window with more
  // entries than the page size, "actors" / "failures" / "actionTypes"
  // describe what's currently rendered, not the whole window. That's
  // a documented trade-off so we don't fire a second 1000-row fetch
  // just to feed the chips.
  let kpis = $derived.by(() => {
    if (!data || !data.entries) return { events: 0, actors: 0, failures: 0, actionTypes: 0 };
    const ents = data.entries;
    const actors = new Set(
      ents.map((e) => e.actor_email || (e.actor_user_id ? "u:" + e.actor_user_id : "system")),
    );
    const failures = ents.filter((e) => actionClass(e.action) === "crit").length;
    const actionTypes = new Set(ents.map((e) => e.action)).size;
    return { events: data.total ?? ents.length, actors: actors.size, failures, actionTypes };
  });

  let totalPages = $derived(data ? Math.max(1, Math.ceil(data.total / limit)) : 1);
  let currentPage = $derived(Math.floor(offset / limit) + 1);
</script>

<main>
  <div class="page-header">
    <div>
      <h2>Audit log</h2>
      <p class="muted page-desc">
        Immutable record of state-changing actions: project / pool / repo edits,
        runner lifecycle, and login attempts. Newest first.
      </p>
    </div>
    <div class="row-actions">
      <button class="btn" onclick={refresh} disabled={loading}>refresh</button>
    </div>
  </div>

  <!-- Manual prune. Sits between the page header and the KPI tiles
       so the destructive action stays away from the search/filter
       toolbar where misclicks would be more likely. -->
  <div class="prune-bar">
    <span class="prune-label">Prune entries older than</span>
    <select class="select" bind:value={pruneDays} disabled={pruning}>
      {#each PRUNE_OPTIONS as o (o.value)}
        <option value={o.value}>{o.label}</option>
      {/each}
    </select>
    <button
      class="btn danger"
      onclick={runPrune}
      disabled={pruning}
      title="Permanently delete audit entries older than the selected period."
    >{pruning ? "pruning..." : "prune"}</button>
    {#if pruneMsg}
      <span class="prune-msg">{pruneMsg}</span>
    {/if}
  </div>

  <div class="stats kpi-row">
    <div class="stat">
      <div class="label">Events (window)</div>
      <div class="value">{kpis.events.toLocaleString()}</div>
    </div>
    <div class="stat">
      <div class="label">Unique actors (page)</div>
      <div class="value">{kpis.actors}</div>
    </div>
    <div class="stat">
      <div class="label">Failures (page)</div>
      <div class="value" style={kpis.failures > 0 ? "color: var(--crit)" : ""}>
        {kpis.failures}
      </div>
    </div>
    <div class="stat">
      <div class="label">Action types (page)</div>
      <div class="value">{kpis.actionTypes}</div>
    </div>
  </div>

  <div class="tbl-toolbar">
    <input
      class="input"
      type="search"
      placeholder="search instance id / job id / ip / email / request id / action / detail..."
      bind:value={q}
    />
    <input
      class="input action-input"
      type="text"
      placeholder="action (exact, e.g. job.spawn_retry)"
      bind:value={actionFilter}
      title="Exact action match. For partial matches, use the search box -- it covers action too."
    />
    <select class="select" bind:value={range}>
      {#each RANGES as r (r.value)}
        <option value={r.value}>{r.label}</option>
      {/each}
    </select>
    <select class="select" bind:value={limit}>
      <option value={25}>Per page: 25</option>
      <option value={50}>Per page: 50</option>
      <option value={100}>Per page: 100</option>
      <option value={250}>Per page: 250</option>
    </select>
    <span class="toolbar-count muted">
      {data ? `${data.total.toLocaleString()} events` : "-"}
      {#if loading}<span class="loading-tag"> loading...</span>{/if}
    </span>
  </div>

  {#if error}<div class="banner err">{error}</div>{/if}

  {#if data}
    {#if !data.entries || data.entries.length === 0}
      <div class="empty">
        <pre class="ascii">    .---. .---. .---.
    | * | | * | | * |
    '---' '---' '---'</pre>
        <h3>No audit entries in this window</h3>
        <p>
          Nothing matched
          {#if q.trim()}for <strong>"{q.trim()}"</strong>{/if}
          {#if range === "all"}across the whole log{:else}within the last <strong>{range}</strong> day{range === "1" ? "" : "s"}{/if}{actionFilter ? ` for action ${actionFilter}` : ""}. Try a wider range or clear the search.
        </p>
      </div>
    {:else}
      <table class="tbl tbl-stack audit-tbl">
        <thead>
          <tr>
            <th style="width: 130px">When</th>
            <th>Event</th>
            <th>Actor</th>
            <th style="width: 90px">Target</th>
            <th>IP</th>
            <th style="width: 90px">Outcome</th>
            <th style="width: 56px"></th>
          </tr>
        </thead>
        <tbody>
          {#each data.entries as e (e.id)}
            <tr class="audit-row" class:open={openID === e.id}>
              <td class="mono" data-label="When">
                <span class="cell-stack">
                  <span>{fmtTime(e.occurred_at)}</span>
                  <span class="sub">{fmtDate(e.occurred_at)}</span>
                </span>
              </td>
              <td data-label="Event">
                <span class="cell-stack">
                  <strong class="action-name">{e.action}</strong>
                  {#if eventSubline(e)}
                    <span class="sub">{eventSubline(e)}</span>
                  {/if}
                </span>
              </td>
              <td class="mono" data-label="Actor">
                {#if e.actor_email}
                  {e.actor_email}
                {:else if e.actor_user_id}
                  user:{e.actor_user_id.slice(0, 8)}
                {:else}
                  <span class="muted">system</span>
                {/if}
              </td>
              <td class="mono" data-label="Target">{targetLabel(e)}</td>
              <td class="mono" data-label="IP">{e.client_ip || "-"}</td>
              <td data-label="Outcome">
                <span class="tag {actionClass(e.action)}">{outcomeLabel(e.action)}</span>
              </td>
              <td class="row-toggle">
                <button
                  class="btn xs ghost"
                  aria-label={openID === e.id ? "collapse" : "expand"}
                  onclick={() => toggleDetail(e.id)}
                >
                  {openID === e.id ? "-" : "+"}
                </button>
              </td>
            </tr>
            {#if openID === e.id}
              <tr class="detail-row">
                <td colspan="7"><pre class="evt-detail">{buildEvtDetail(e)}</pre></td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>

      <div class="pager">
        <span class="muted">
          Showing {offset + 1}-{Math.min(offset + limit, data.total)} of {data.total.toLocaleString()}
          {#if totalPages > 1}<span class="muted"> -- page {currentPage} of {totalPages}</span>{/if}
        </span>
        <div class="row-actions">
          <button class="btn xs" onclick={prevPage} disabled={offset === 0 || loading}>prev</button>
          <button class="btn xs" onclick={nextPage}
                  disabled={offset + limit >= data.total || loading}>next</button>
        </div>
      </div>
    {/if}
  {/if}
</main>

<style>
  .page-desc {
    margin: 4px 0 0;
    max-width: 760px;
    font-size: 13px;
    line-height: 1.5;
  }

  .kpi-row { margin-bottom: 14px; }

  /* Inline toolbar -- a card-styled flex strip carrying the search,
     range, and page-size controls. Matches cloud-scope's compact
     audit control surface; on phones the existing flex-wrap rule
     stacks each control to full width. */
  .tbl-toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
    background: var(--bg-2);
    border: 1px solid var(--line-2);
    border-radius: var(--r-md);
    margin-bottom: 14px;
    flex-wrap: wrap;
  }
  .tbl-toolbar > .input {
    flex: 2 1 280px;
    min-width: 0;
    height: 32px;
    font-size: 13px;
  }
  /* The action-exact input is a power-user affordance; give it less
     room than the main search box so it doesn't compete visually. */
  .tbl-toolbar > .action-input {
    flex: 1 1 180px;
  }
  .tbl-toolbar > .select {
    width: auto;
    height: 32px;
    font-size: 13px;
  }
  .toolbar-count {
    font-family: var(--font-mono);
    font-size: 11px;
    margin-left: auto;
    white-space: nowrap;
  }
  .loading-tag { color: var(--brand-300); margin-left: 6px; }

  /* Two-line cells: a vertical mini-stack inside a single TD so the
     label/value treatment in the mobile .tbl-stack mode keeps the
     primary text and its subtitle together. */
  .audit-tbl .cell-stack {
    display: inline-flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .audit-tbl .sub {
    color: var(--fg-3);
    font-size: 11px;
    font-family: var(--font-mono);
    line-height: 1.3;
    word-break: break-all;
  }
  .audit-tbl .action-name {
    color: var(--fg-0);
    font-weight: 500;
    font-family: var(--font-mono);
    font-size: 13px;
    letter-spacing: 0;
  }
  .audit-tbl td { vertical-align: top; }
  .audit-tbl tr.audit-row.open { background: var(--bg-3); }

  .row-toggle { text-align: right; }
  .row-toggle .btn {
    width: 28px;
    padding: 0;
    font-family: var(--font-mono);
    font-size: 14px;
  }

  /* Inline expanded detail. The pre uses pre-wrap so long values
     (commit URLs, stack-trace fragments) wrap rather than push the
     row sideways. */
  .detail-row td {
    background: var(--bg-1);
    padding: 0;
    border-top: 1px solid var(--line-2);
  }
  .evt-detail {
    margin: 0;
    padding: 14px 18px;
    background: var(--bg-0);
    border: 0;
    font-family: var(--font-mono);
    font-size: 11.5px;
    line-height: 1.55;
    color: var(--fg-1);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 380px;
    overflow-y: auto;
  }

  /* .pager lives in app.css now (shared across paginated pages). */

  /* Prune control bar. Inline strip; not styled as a card so the
     destructive action doesn't feel like a setting. */
  .prune-bar {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
    font-size: 13px;
    color: var(--fg-2);
    flex-wrap: wrap;
  }
  .prune-bar .select { height: 32px; width: auto; }
  .prune-label { font-family: var(--font-mono); font-size: 12px; color: var(--fg-3); }
  .prune-msg {
    margin-left: auto;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--ok);
  }
</style>
