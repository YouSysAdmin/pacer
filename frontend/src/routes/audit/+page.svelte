<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { audit } from "$lib/api.js";

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
  let actionFilter = $state("");
  let limit = $state(50);
  let offset = $state(0);

  let loading = $state(false);
  let error = $state(null);
  let data = $state(null);

  // One row open at a time -- toggling a different row closes the
  // previous one. Set semantics from the prior version were richer
  // than they needed to be; cloud-scope's pattern of single-open is
  // tighter and reduces "where did I click" confusion.
  let openID = $state(null);
  function toggleDetail(id) { openID = (openID === id) ? null : id; }

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

  // Reset paging + reload when any filter changes.
  $effect(() => {
    range; actionFilter; limit;
    offset = 0;
    refresh();
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
      type="text"
      placeholder="filter by action (exact match, e.g. job.spawn_retry)"
      bind:value={actionFilter}
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
          {#if range === "all"}across the whole log{:else}within the last <strong>{range}</strong> day{range === "1" ? "" : "s"}{/if}{actionFilter ? ` for action ${actionFilter}` : ""}. Try a wider range or clear the action filter.
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
    flex: 1 1 220px;
    min-width: 0;
    height: 32px;
    font-size: 13px;
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

  .pager {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 16px;
    gap: 12px;
    flex-wrap: wrap;
  }
</style>
