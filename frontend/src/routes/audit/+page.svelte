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

  // The backend's time window is [since, until) -- exclusive on the
  // right. The UI treats `until` as INCLUSIVE ("show me through end
  // of this day"); the API call shifts +1 day forward right before
  // dispatching. Without this shift, picking today excluded today.
  function nextDayUTC(yyyyMMdd) {
    const d = new Date(yyyyMMdd + "T00:00:00Z");
    d.setUTCDate(d.getUTCDate() + 1);
    return isoDate(d);
  }

  let until = $state(isoDate(todayUTC()));
  let since = $state(isoDate(new Date(todayUTC().getTime() - 7 * 86400_000)));
  let actionFilter = $state("");
  let limit = $state(50);
  let offset = $state(0);

  let loading = $state(false);
  let error = $state(null);
  let data = $state(null);

  // Set of entry IDs whose detail JSON is expanded.
  let expanded = $state(new Set());
  function toggleDetail(id) {
    const next = new Set(expanded);
    if (next.has(id)) next.delete(id); else next.add(id);
    expanded = next;
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      data = await audit.list({
        since,
        until: nextDayUTC(until),
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

  function setRange(days) {
    offset = 0;
    until = isoDate(todayUTC());
    since = isoDate(new Date(todayUTC().getTime() - days * 86400_000));
    refresh();
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

  function fmtTime(t) {
    if (!t) return "";
    return new Date(t).toLocaleString();
  }

  function actorLabel(e) {
    return e.actor_email || (e.actor_user_id ? "user:" + e.actor_user_id.slice(0, 8) : "system");
  }

  function targetLabel(e) {
    if (!e.target_type) return "—";
    if (!e.target_id) return e.target_type;
    return `${e.target_type}:${e.target_id}`;
  }

  function actionClass(a) {
    if (a.endsWith(".failed") || a.endsWith(".reaped") || a.endsWith(".exhausted") || a.endsWith(".no_pool_match") || a === "auth.login_failed" || a === "auth.oidc.login_denied" || a === "auth.oidc.login_failed") return "crit";
    if (a.endsWith(".retry")) return "warn";
    if (a.endsWith(".completed") || a.endsWith(".launched") || a.endsWith(".registered") || a.endsWith(".created") || a.endsWith(".bound") || a === "auth.oidc.login_succeeded") return "ok";
    return "info";
  }

  function prettyJSON(s) {
    if (!s) return "";
    try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
  }

  $effect(() => {
    refresh();
  });

  let totalPages = $derived(data ? Math.max(1, Math.ceil(data.total / limit)) : 1);
  let currentPage = $derived(Math.floor(offset / limit) + 1);
</script>

<main>
  <div class="page-header">
    <h2>Audit log</h2>
    <div class="row-actions">
      <button class="btn" onclick={() => setRange(1)}>1 d</button>
      <button class="btn" onclick={() => setRange(7)}>7 d</button>
      <button class="btn" onclick={() => setRange(30)}>30 d</button>
      <button class="btn" onclick={() => setRange(90)}>90 d</button>
      <button class="btn" onclick={refresh} disabled={loading}>refresh</button>
    </div>
  </div>

  <div class="card">
    <h3>Window</h3>
    <div class="field-row">
      <div class="field">
        <label for="since">Since <span class="muted">(UTC)</span></label>
        <input id="since" class="input" type="date" bind:value={since}
               onchange={() => { offset = 0; refresh(); }} />
      </div>
      <div class="field">
        <label for="until">Until <span class="muted">(UTC, inclusive)</span></label>
        <input id="until" class="input" type="date" bind:value={until}
               onchange={() => { offset = 0; refresh(); }} />
      </div>
    </div>
    <div class="field-row">
      <div class="field">
        <label for="action">Action <span class="muted">(exact match)</span></label>
        <input id="action" class="input" type="text" placeholder="e.g. job.spawn_retry"
               bind:value={actionFilter}
               onchange={() => { offset = 0; refresh(); }} />
      </div>
      <div class="field">
        <label for="lim">Per page</label>
        <select id="lim" class="select" bind:value={limit}
                onchange={() => { offset = 0; refresh(); }}>
          <option value={25}>25</option>
          <option value={50}>50</option>
          <option value={100}>100</option>
          <option value={250}>250</option>
        </select>
      </div>
    </div>
  </div>

  {#if error}<div class="banner err">{error}</div>{/if}

  {#if data}
    {#if !data.entries || data.entries.length === 0}
      <div class="empty">
        <pre class="ascii">    .---. .---. .---.
    | * | | * | | * |
    '---' '---' '---'</pre>
        <h3>No audit entries in this window</h3>
        <p>Nothing matched between <strong>{since}</strong> and <strong>{until}</strong>{actionFilter ? ` for action ${actionFilter}` : ""}. Try a wider range or clear the action filter.</p>
      </div>
    {:else}
      <table class="tbl">
        <thead>
          <tr>
            <th>When</th>
            <th>Action</th>
            <th>Actor</th>
            <th>Target</th>
            <th>IP</th>
            <th>Detail</th>
          </tr>
        </thead>
        <tbody>
          {#each data.entries as e (e.id)}
            <tr>
              <td class="mono">{fmtTime(e.occurred_at)}</td>
              <td><span class="tag {actionClass(e.action)}">{e.action}</span></td>
              <td class="mono">{actorLabel(e)}</td>
              <td class="mono">{targetLabel(e)}</td>
              <td class="mono">{e.client_ip || "—"}</td>
              <td>
                {#if e.detail}
                  <button class="btn xs" onclick={() => toggleDetail(e.id)}>
                    {expanded.has(e.id) ? "hide" : "show"}
                  </button>
                {:else}
                  <span class="muted">—</span>
                {/if}
              </td>
            </tr>
            {#if e.detail && expanded.has(e.id)}
              <tr class="detail-row">
                <td colspan="6"><pre class="detail-json">{prettyJSON(e.detail)}</pre></td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>

      <div class="pager">
        <span class="muted">
          {data.total.toLocaleString()} entries -- page {currentPage} of {totalPages}
        </span>
        <div class="row-actions">
          <button class="btn" onclick={prevPage} disabled={offset === 0 || loading}>prev</button>
          <button class="btn" onclick={nextPage}
                  disabled={offset + limit >= data.total || loading}>next</button>
        </div>
      </div>
    {/if}
  {/if}
</main>

<style>
  .detail-row td {
    background: var(--bg-1);
    padding: 12px 16px;
    border-top: 1px solid var(--line-2);
  }
  .detail-json {
    margin: 0;
    padding: 10px 12px;
    background: var(--bg-0);
    border: 1px solid var(--line-2);
    border-radius: var(--r-sm);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.45;
    color: var(--fg-1);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 320px;
    overflow-y: auto;
  }
  .pager {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 16px;
  }
</style>
