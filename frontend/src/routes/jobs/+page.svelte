<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { onMount, untrack } from "svelte";
  import { jobs, systemHealth } from "$lib/api.js";
  import Modal from "$lib/Modal.svelte";

  const STATUSES = [
    "", // all
    "queued",
    "claimed",
    "starting",
    "running",
    "completed",
    "failed",
    "cancelled",
    "reaped",
  ];

  let list = $state([]);
  // Pagination envelope from GET /api/jobs. total drives the pager
  // copy. limit/offset round-trip into the next request. Reset to
  // page 1 whenever a filter or page-size changes.
  let total = $state(0);
  let limit = $state(50);
  let offset = $state(0);
  let loading = $state(false);
  let error = $state(null);
  let filter = $state("");
  // Reconcile-now state: separate from `loading` so the table can
  // keep its own refresh affordance independent of the reaper sweep.
  let reconciling = $state(false);
  let reconcileMsg = $state(null);

  // Detail modal state. `detailOpen` drives the Modal's bind:open.
  // `detail` is the bundle returned by GET /api/jobs/:id (job +
  // payload + instance + audit). `detailLoading` and `detailErr`
  // gate the modal body so it can show a spinner / error without
  // unmounting the modal frame.
  let detailOpen = $state(false);
  let detail = $state(null);
  let detailLoading = $state(false);
  let detailErr = $state(null);
  let detailID = $state(null);

  function statusClass(s) {
    if (s === "running") return "info";
    if (s === "completed") return "ok";
    if (s === "failed" || s === "reaped") return "crit";
    if (s === "cancelled") return "warn";
    if (s === "queued" || s === "claimed" || s === "starting") return "warn";
    return "";
  }

  // GitHub workflow_job step shape: status is the lifecycle phase
  // (queued / in_progress / completed) and conclusion is the actual
  // outcome (success / failure / skipped / cancelled / timed_out /
  // neutral / action_required / null when not finished).  Coloring on
  // status alone paints every finished step green, including failures
  // -- so we prefer conclusion when present and fall back to status.
  function stepResult(step) {
    const c = (step.conclusion || "").toLowerCase();
    if (c === "success")   return { text: "success",   cls: "ok"   };
    if (c === "failure" || c === "timed_out") return { text: c, cls: "crit" };
    if (c === "cancelled" || c === "action_required" || c === "neutral") {
      return { text: c, cls: "warn" };
    }
    if (c === "skipped")   return { text: "skipped",   cls: "info" };
    if (c)                 return { text: c,           cls: ""     };
    // No conclusion yet -- step is still in flight. Show the lifecycle.
    const s = (step.status || "").toLowerCase();
    if (s === "in_progress") return { text: "running", cls: "info" };
    if (s === "queued")      return { text: "queued",  cls: "warn" };
    return { text: s || "-", cls: "" };
  }

  function fmt(t) {
    if (!t) return "";
    const d = new Date(t);
    return d.toLocaleString();
  }

  function age(start, end) {
    if (!start) return "";
    const a = new Date(start).getTime();
    const b = end ? new Date(end).getTime() : Date.now();
    const s = Math.floor((b - a) / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    return `${h}h ${m % 60}m`;
  }

  // Heartbeat staleness classifier for instances. The reaper stamps
  // last_seen_at every ~60s for every alive instance. A row whose
  // last_seen_at is older than that means the reaper isn't visiting
  // it (panic, IAM revoke, region mismatch, etc).
  //
  //   < 90s   -> ok   ("the reaper just touched this")
  //   < 180s  -> warn ("one tick missed, could be transient")
  //   >= 180s -> crit ("multiple ticks missed -- something is wrong")
  //
  // Only meaningful for in-flight states (starting/running).
  // Terminated rows freeze last_seen_at at termination time, which
  // would always trip the crit class -- skip the colorization for
  // those (state itself already tells the operator it's done).
  function heartbeat(instance) {
    if (!instance || !instance.last_seen_at) {
      return { text: "never", cls: "crit" };
    }
    const inFlight =
      instance.state === "starting" || instance.state === "running";
    const ageMs = Date.now() - new Date(instance.last_seen_at).getTime();
    const ageStr = age(instance.last_seen_at, null) + " ago";
    if (!inFlight) return { text: ageStr, cls: "" };
    if (ageMs < 90_000)  return { text: ageStr, cls: "ok" };
    if (ageMs < 180_000) return { text: ageStr, cls: "warn" };
    return { text: ageStr, cls: "crit" };
  }

  function cost(usd) {
    if (usd == null) return "";
    if (usd === 0) return "$0.00";
    if (Math.abs(usd) < 0.01) return "$" + usd.toFixed(4);
    return "$" + usd.toFixed(2);
  }

  // Force an immediate reaper sweep server-side. Useful right after
  // an operator terminates an instance in the AWS console -- the
  // next scheduled tick is up to 60s away, this collapses that to
  // ~one round trip. On success, refresh the list so any newly-
  // marked-failed rows show up immediately.
  async function reconcile() {
    reconciling = true;
    reconcileMsg = null;
    error = null;
    try {
      const r = await systemHealth.reconcile();
      const checked = r?.checked ?? 0;
      if (r?.issue) {
        // Tick ran but checkEC2Health or the panic-recover path
        // wrote Health. Surface that here in addition to the banner
        // so the operator who just clicked the button sees the
        // verdict immediately.
        reconcileMsg = `reaper sweep returned an issue: ${r.issue.message}`;
      } else {
        reconcileMsg = `reaper swept ${checked} instance${checked === 1 ? "" : "s"}.`;
      }
      await refresh();
    } catch (e) {
      error = e.message;
    } finally {
      reconciling = false;
    }
  }

  // refreshGen is a monotonic counter used to discard stale fetch
  // results. With auto-refresh + manual paging both calling refresh(),
  // responses can arrive out of order -- a slower page-1 reply
  // landing after a faster page-2 reply would rewind the table.
  // Each refresh() call captures its gen at start and bails before
  // applying results if a newer call has since taken over.
  let refreshGen = 0;
  async function refresh() {
    const myGen = ++refreshGen;
    loading = true;
    error = null;
    try {
      const r = await jobs.list({
        status: filter || undefined,
        limit,
        offset,
      });
      if (myGen !== refreshGen) return; // newer refresh in flight -- abandon
      list = (r && r.entries) || [];
      total = r?.total ?? list.length;
    } catch (e) {
      if (myGen !== refreshGen) return;
      error = e.message;
    } finally {
      if (myGen === refreshGen) loading = false;
    }
  }

  function prevPage() {
    if (offset === 0) return;
    offset = Math.max(0, offset - limit);
    refresh();
  }
  function nextPage() {
    if (offset + limit >= total) return;
    offset = offset + limit;
    refresh();
  }
  // firstPage is the "show me the latest" shortcut. Used by both the
  // explicit "first" button and the "refresh" button -- with
  // auto-refresh paused past page 1, the operator needs a quick way
  // back, and refresh-while-paginating is a no-op otherwise (the
  // historical pages don't change). Setting offset before refresh()
  // is important: refresh() reads offset to build the URL.
  function firstPage() {
    offset = 0;
    refresh();
  }

  async function openDetail(id) {
    detailID = id;
    detailOpen = true;
    detailLoading = true;
    detailErr = null;
    detail = null;
    try {
      detail = await jobs.get(id);
    } catch (e) {
      detailErr = e.message;
    } finally {
      detailLoading = false;
    }
  }

  // Refresh the open modal when the underlying job state changes
  // (e.g. queued -> running -> completed during the 5s poll). Keep
  // the modal open and just re-fetch silently. No spinner so the
  // user doesn't see a flash on every poll tick.
  async function refreshDetailIfOpen() {
    if (!detailOpen || !detailID) return;
    try {
      detail = await jobs.get(detailID);
    } catch {
      // swallow -- the next list refresh will surface it
    }
  }

  // Auto-refresh is set up once on mount via setInterval -- NOT via
  // $effect -- because $effect tracks every state read inside its
  // body (including reads inside the called refresh()), which would
  // recreate the interval on every offset/filter/limit change. Each
  // recreation reset the 5s window AND racing against direct
  // refresh() calls from the pager produced out-of-order responses
  // that visibly rewound the page.
  //
  // Auto-refresh is also gated on offset === 0: when the operator
  // is browsing past page 1 they want the data to stay put while
  // they read it, not shuffle every 5s. The detail modal still
  // refreshes on its own cadence because the modal is per-job and
  // doesn't move out from under the cursor.
  onMount(() => {
    refresh();
    const t = setInterval(() => {
      if (offset === 0) refresh();
      refreshDetailIfOpen();
    }, 5000);
    return () => clearInterval(t);
  });

  // Re-fetch when filter or page size changes. Reset to page 1 so
  // a narrower filter doesn't leave the pager pointing past the
  // new (smaller) total.
  //
  // The body MUST be wrapped in untrack(). Svelte 5 tracks every
  // $state read inside an effect, including reads that happen
  // inside called functions -- so a bare refresh() call would pick
  // up `offset` (which it reads to build the request URL) as a
  // tracked dep. Clicking Next would then re-trigger this effect,
  // which would reset offset back to 0 and fetch page 1, making
  // the Next button effectively a no-op. We only want the effect
  // to fire on filter / limit transitions. Offset writes are
  // owned by the pager.
  $effect(() => {
    filter; limit;
    untrack(() => {
      offset = 0;
      refresh();
    });
  });

  let totalPages = $derived(total > 0 ? Math.max(1, Math.ceil(total / limit)) : 1);
  let currentPage = $derived(Math.floor(offset / limit) + 1);

  // ----- Modal-side derived fields. ---------------------------------
  // The webhook payload is parsed once via $derived rather than
  // peppering the markup with `detail?.payload?.workflow_job?....`
  // chains. Returns an object of safe strings/arrays/numbers. Missing
  // fields collapse to "" / [] / null.
  let derived = $derived.by(() => {
    const p = detail?.payload;
    if (!p) return {};
    const wj = p.workflow_job || {};
    const repo = p.repository || {};
    const sender = p.sender || {};
    return {
      htmlURL:       wj.html_url || "",
      headBranch:    wj.head_branch || "",
      headSHA:       wj.head_sha || "",
      workflowName:  wj.workflow_name || "",
      jobName:       wj.name || "",
      runAttempt:    wj.run_attempt || null,
      ghCreatedAt:   wj.created_at || "",
      ghStartedAt:   wj.started_at || "",
      ghCompletedAt: wj.completed_at || "",
      steps:         Array.isArray(wj.steps) ? wj.steps : [],
      repoURL:       repo.html_url || "",
      senderLogin:   sender.login || detail?.job?.sender_login || "",
      senderURL:     sender.html_url || "",
      senderType:    sender.type || "",
    };
  });

  function shortSHA(sha) { return sha ? sha.slice(0, 7) : ""; }
  function commitURL(repoURL, sha) {
    if (!repoURL || !sha) return "";
    return `${repoURL}/commit/${sha}`;
  }

  // Pretty-print the audit Detail JSON inline (it's stored as a
  // serialized string in the DB). Bad JSON falls through to the
  // raw string so the modal never breaks on a malformed entry.
  function fmtAuditDetail(s) {
    if (!s) return "";
    try {
      const o = JSON.parse(s);
      return Object.entries(o)
        .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
        .join(" ");
    } catch {
      return s;
    }
  }
</script>

<main>
  <div class="page-header">
    <h2>Jobs</h2>
    <div class="row-actions">
      <select class="select" bind:value={filter}>
        {#each STATUSES as s (s)}
          <option value={s}>{s || "all"}</option>
        {/each}
      </select>
      <select class="select" bind:value={limit}>
        <option value={25}>Per page: 25</option>
        <option value={50}>Per page: 50</option>
        <option value={100}>Per page: 100</option>
        <option value={250}>Per page: 250</option>
      </select>
      <button
        class="btn"
        onclick={firstPage}
        disabled={loading || offset === 0}
        title="Jump back to page 1 (latest activity)."
      >first</button>
      <button
        class="btn"
        onclick={firstPage}
        disabled={loading}
        title="Reload from page 1 (resets pagination)."
      >refresh</button>
      <button
        class="btn"
        onclick={reconcile}
        disabled={reconciling}
        title="Force an immediate reaper sweep instead of waiting for the next 60s tick. Useful right after terminating an instance from the AWS console."
      >{reconciling ? "reconciling..." : "reconcile now"}</button>
    </div>
  </div>

  {#if error}<div class="banner err">{error}</div>{/if}
  {#if reconcileMsg}<div class="banner ok">{reconcileMsg}</div>{/if}

  {#if list.length === 0}
    <div class="empty">
      <pre class="ascii">   -  -  -  -  -  -
   -                -
   -    no jobs     -
   -                -
   -  -  -  -  -  -</pre>
      <h3>{filter ? `No ${filter} jobs` : "No jobs yet"}</h3>
      {#if filter}
        <p>Nothing in the <strong>{filter}</strong> bucket right now. Clear the filter to see every job.</p>
        <div class="actions">
          <button class="btn" onclick={() => (filter = "")}>clear filter</button>
        </div>
      {:else}
        <p>Once a bound repo's workflow runs, it'll show up here. Make sure the repo is <a href="/repos">bound</a> and the GitHub App is configured to deliver <code>workflow_job</code> webhooks.</p>
      {/if}
    </div>
  {:else}
    <table class="tbl tbl-stack">
      <thead>
        <tr>
          <th>Status</th>
          <th>Repo</th>
          <th>GH job</th>
          <th>Sender</th>
          <th>Instance</th>
          <th>Queued</th>
          <th>Duration</th>
          <th>Est. cost</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each list as j (j.id)}
          <tr>
            <td data-label="Status">
              <span class="tag {statusClass(j.status)}">{j.status}</span>
            </td>
            <td class="mono" data-label="Repo">{j.repo_full_name}</td>
            <td class="mono" data-label="GH job">{j.gh_job_id}</td>
            <td class="mono" data-label="Sender">{j.sender_login || "\u2014"}</td>
            <td class="mono" data-label="Instance">{j.instance_id || "\u2014"}</td>
            <td class="mono" data-label="Queued">{fmt(j.queued_at)}</td>
            <td class="mono" data-label="Duration">{age(j.claimed_at || j.started_at || j.queued_at, j.completed_at)}</td>
            <td class="mono" data-label="Est. cost">{cost(j.estimated_cost_usd) || (j.completed_at ? "\u2014" : "")}</td>
            <td>
              <button class="btn xs" onclick={() => openDetail(j.id)}>details</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="pager">
      <span class="muted">
        Showing {offset + 1}-{Math.min(offset + limit, total)} of {total.toLocaleString()}
        {#if totalPages > 1}<span class="muted"> -- page {currentPage} of {totalPages}</span>{/if}
      </span>
      <div class="row-actions">
        <button class="btn xs" onclick={firstPage} disabled={offset === 0 || loading}>first</button>
        <button class="btn xs" onclick={prevPage} disabled={offset === 0 || loading}>prev</button>
        <button class="btn xs" onclick={nextPage}
                disabled={offset + limit >= total || loading}>next</button>
      </div>
    </div>
  {/if}

  <p class="muted" style="margin-top: 1rem;">
    {#if offset === 0}
      Auto-refreshes every 5 s.
    {:else}
      Auto-refresh paused while paging -- click <strong>prev</strong> back to page 1 (or <strong>refresh</strong>) to resume.
    {/if}
  </p>
</main>

<Modal bind:open={detailOpen} title={detail ? `Job ${detail.job.gh_job_id}` : "Job"}>
  {#if detailLoading}
    <p class="muted">Loading...</p>
  {:else if detailErr}
    <div class="banner err">{detailErr}</div>
  {:else if detail}
    {@const j = detail.job}
    {@const i = detail.instance}

    <div class="detail-head">
      <span class="tag {statusClass(j.status)}">{j.status}</span>
      <span class="mono">{j.repo_full_name}</span>
      {#if derived.htmlURL}
        <a class="btn xs" href={derived.htmlURL} target="_blank" rel="noopener">open in GitHub</a>
      {/if}
    </div>

    <table class="tbl detail-tbl">
      <tbody>
        <tr><th>Workflow</th><td class="mono">{derived.workflowName || "\u2014"}</td></tr>
        <tr><th>Job name</th><td class="mono">{derived.jobName || "\u2014"}</td></tr>
        {#if derived.headBranch}
          <tr><th>Branch</th><td class="mono">{derived.headBranch}</td></tr>
        {/if}
        {#if derived.headSHA}
          <tr>
            <th>Commit</th>
            <td class="mono">
              {#if commitURL(derived.repoURL, derived.headSHA)}
                <a href={commitURL(derived.repoURL, derived.headSHA)} target="_blank" rel="noopener">{shortSHA(derived.headSHA)}</a>
              {:else}
                {shortSHA(derived.headSHA)}
              {/if}
            </td>
          </tr>
        {/if}
        {#if derived.runAttempt}
          <tr><th>Attempt</th><td class="mono">{derived.runAttempt}</td></tr>
        {/if}
        <tr>
          <th>Sender</th>
          <td class="mono">
            {#if derived.senderURL}
              <a href={derived.senderURL} target="_blank" rel="noopener">{derived.senderLogin}</a>
            {:else}
              {derived.senderLogin || "\u2014"}
            {/if}
            {#if derived.senderType && derived.senderType !== "User"}
              <span class="muted"> ({derived.senderType})</span>
            {/if}
          </td>
        </tr>
        <tr><th>GH job ID</th><td class="mono">{j.gh_job_id}</td></tr>
        <tr><th>GH run ID</th><td class="mono">{j.gh_run_id}</td></tr>
        <tr><th>Pacer ID</th><td class="mono">{j.id}</td></tr>
        <tr><th>Project</th><td class="mono" title={j.project_id}>{detail.project_name || j.project_id || "\u2014"}</td></tr>
        <tr><th>Pool</th><td class="mono" title={j.pool_id}>{detail.pool_name || j.pool_id || "\u2014"}</td></tr>
        <tr><th>Attempts</th><td class="mono">{j.attempts}</td></tr>
      </tbody>
    </table>

    <h4 class="detail-section">Timeline</h4>
    <table class="tbl detail-tbl">
      <tbody>
        <tr><th>Queued</th><td class="mono">{fmt(j.queued_at)}</td></tr>
        {#if j.claimed_at}
          <tr><th>Claimed</th><td class="mono">{fmt(j.claimed_at)} <span class="muted">(+{age(j.queued_at, j.claimed_at)})</span></td></tr>
        {/if}
        {#if j.started_at}
          <tr><th>Started</th><td class="mono">{fmt(j.started_at)} <span class="muted">(+{age(j.claimed_at || j.queued_at, j.started_at)})</span></td></tr>
        {/if}
        {#if j.completed_at}
          <tr><th>Completed</th><td class="mono">{fmt(j.completed_at)} <span class="muted">(+{age(j.started_at || j.claimed_at || j.queued_at, j.completed_at)})</span></td></tr>
        {/if}
        <tr><th>Duration</th><td class="mono">{age(j.claimed_at || j.started_at || j.queued_at, j.completed_at)}</td></tr>
        {#if derived.ghCreatedAt}
          <tr><th>GH created</th><td class="mono">{fmt(derived.ghCreatedAt)}</td></tr>
        {/if}
        {#if derived.ghStartedAt}
          <tr><th>GH started</th><td class="mono">{fmt(derived.ghStartedAt)}</td></tr>
        {/if}
        {#if derived.ghCompletedAt}
          <tr><th>GH completed</th><td class="mono">{fmt(derived.ghCompletedAt)}</td></tr>
        {/if}
      </tbody>
    </table>

    {#if i}
      <h4 class="detail-section">Instance</h4>
      <table class="tbl detail-tbl">
        <tbody>
          <tr><th>ID</th><td class="mono">{i.id}</td></tr>
          <tr><th>Type</th><td class="mono">{i.instance_type || "\u2014"}</td></tr>
          <tr><th>AZ</th><td class="mono">{i.az || "\u2014"}</td></tr>
          <tr><th>Market</th><td class="mono">{i.spot ? "spot" : "on-demand"}{i.price_model ? ` (${i.price_model})` : ""}</td></tr>
          <tr><th>Price/hour</th><td class="mono">{i.price_per_hour != null ? "$" + i.price_per_hour.toFixed(4) : "\u2014"}</td></tr>
          <tr><th>State</th><td class="mono">{i.state}</td></tr>
          <tr>
            <th>Last seen</th>
            <td class="mono">
              {#if i.last_seen_at}
                {@const hb = heartbeat(i)}
                <span class="tag {hb.cls}" title={fmt(i.last_seen_at) + " UTC"}>{hb.text}</span>
              {:else}
                <span class="muted">never</span>
              {/if}
            </td>
          </tr>
          <tr><th>Launched</th><td class="mono">{fmt(i.launched_at)}</td></tr>
          {#if i.terminated_at}
            <tr><th>Terminated</th><td class="mono">{fmt(i.terminated_at)}</td></tr>
          {/if}
          <tr><th>Est. cost</th><td class="mono">{cost(j.estimated_cost_usd) || "\u2014"}</td></tr>
        </tbody>
      </table>
    {/if}

    {#if j.failure_log || j.failure_message}
      <h4 class="detail-section">Failure</h4>
      <div class="failure-meta">
        <strong>{j.failure_stage || "bootstrap"}</strong>
        {#if j.failure_message}<span class="muted"> -- {j.failure_message}</span>{/if}
      </div>
      {#if j.failure_log}
        <pre class="failure-log">{j.failure_log}</pre>
      {/if}
    {/if}

    {#if derived.steps.length > 0}
      <h4 class="detail-section">Steps</h4>
      <div class="tbl-wrap">
        <table class="tbl">
          <thead>
            <tr>
              <th style="width: 40px">#</th>
              <th>Name</th>
              <th>Result</th>
              <th>Duration</th>
            </tr>
          </thead>
          <tbody>
            {#each derived.steps as step, idx (idx)}
              {@const r = stepResult(step)}
              <tr>
                <td class="mono">{step.number ?? idx + 1}</td>
                <td>{step.name}</td>
                <td><span class="tag {r.cls}">{r.text}</span></td>
                <td class="mono">{step.started_at && step.completed_at ? age(step.started_at, step.completed_at) : "\u2014"}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <h4 class="detail-section">Audit trail</h4>
    {#if detail.audit.length === 0}
      <p class="muted">No audit entries.</p>
    {:else}
      <div class="tbl-wrap">
        <table class="tbl">
          <thead>
            <tr>
              <th>When</th>
              <th>Action</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {#each detail.audit as a (a.id)}
              <tr>
                <td class="mono">{fmt(a.occurred_at)}</td>
                <td class="mono">{a.action}</td>
                <td class="mono muted">{fmtAuditDetail(a.detail)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if detail.payload}
      <details class="raw-payload">
        <summary>Raw payload</summary>
        <pre>{JSON.stringify(detail.payload, null, 2)}</pre>
      </details>
    {/if}
  {/if}
</Modal>

<style>
  .detail-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
  }
  .detail-section {
    font-size: 11px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-3);
    margin: 18px 0 8px;
    padding: 0;
    border: 0;
  }
  .detail-tbl th {
    width: 130px;
    padding: 12px 16px;
    text-align: left;
    color: var(--fg-2);
    font-weight: 500;
    font-size: 14px;
    background: transparent;
  }
  .failure-meta { font-size: 12px; margin-bottom: 8px; color: var(--fg-1); }
  .failure-log {
    margin: 0;
    padding: 12px 14px;
    background: var(--bg-0);
    border: 1px solid var(--line-2);
    border-radius: var(--r-sm);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.45;
    color: var(--fg-1);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 360px;
    overflow-y: auto;
  }
  .raw-payload {
    margin-top: 18px;
  }
  .raw-payload summary {
    font-size: 11px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-3);
    cursor: pointer;
    user-select: none;
  }
  .raw-payload pre {
    margin: 8px 0 0;
    padding: 12px;
    background: var(--bg-0);
    border: 1px solid var(--line-2);
    border-radius: var(--r-sm);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.45;
    max-height: 320px;
    overflow: auto;
  }
</style>
