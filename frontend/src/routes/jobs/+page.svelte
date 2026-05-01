<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { jobs } from "$lib/api.js";
  import Modal from "$lib/Modal.svelte";

  const STATUSES = [
    "", // all
    "queued",
    "claimed",
    "starting",
    "running",
    "completed",
    "failed",
    "reaped",
  ];

  let list = $state([]);
  let loading = $state(false);
  let error = $state(null);
  let filter = $state("");

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
    if (s === "queued" || s === "claimed" || s === "starting") return "warn";
    return "";
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

  function cost(usd) {
    if (usd == null) return "";
    if (usd === 0) return "$0.00";
    if (Math.abs(usd) < 0.01) return "$" + usd.toFixed(4);
    return "$" + usd.toFixed(2);
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      list = (await jobs.list(filter || undefined, 100)) || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
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
  // the modal open and just re-fetch silently; no spinner so the
  // user doesn't see a flash on every poll tick.
  async function refreshDetailIfOpen() {
    if (!detailOpen || !detailID) return;
    try {
      detail = await jobs.get(detailID);
    } catch {
      // swallow -- the next list refresh will surface it
    }
  }

  $effect(() => {
    refresh();
    const t = setInterval(async () => {
      await refresh();
      await refreshDetailIfOpen();
    }, 5000);
    return () => clearInterval(t);
  });

  // re-fetch when filter changes
  $effect(() => {
    filter;
    refresh();
  });

  // ----- Modal-side derived fields. ---------------------------------
  // The webhook payload is parsed once via $derived rather than
  // peppering the markup with `detail?.payload?.workflow_job?....`
  // chains. Returns an object of safe strings/arrays/numbers; missing
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
      <button class="btn" onclick={refresh} disabled={loading}>refresh</button>
    </div>
  </div>

  {#if error}<div class="banner err">{error}</div>{/if}

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
    <table class="tbl">
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
            <td>
              <span class="tag {statusClass(j.status)}">{j.status}</span>
            </td>
            <td class="mono">{j.repo_full_name}</td>
            <td class="mono">{j.gh_job_id}</td>
            <td class="mono">{j.sender_login || "—"}</td>
            <td class="mono">{j.instance_id || "—"}</td>
            <td class="mono">{fmt(j.queued_at)}</td>
            <td class="mono">{age(j.claimed_at || j.started_at || j.queued_at, j.completed_at)}</td>
            <td class="mono">{cost(j.estimated_cost_usd) || (j.completed_at ? "—" : "")}</td>
            <td>
              <button class="btn xs" onclick={() => openDetail(j.id)}>details</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <p class="muted" style="margin-top: 1rem;">Auto-refreshes every 5 s.</p>
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
        <tr><th>Workflow</th><td class="mono">{derived.workflowName || "—"}</td></tr>
        <tr><th>Job name</th><td class="mono">{derived.jobName || "—"}</td></tr>
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
              {derived.senderLogin || "—"}
            {/if}
            {#if derived.senderType && derived.senderType !== "User"}
              <span class="muted"> ({derived.senderType})</span>
            {/if}
          </td>
        </tr>
        <tr><th>GH job ID</th><td class="mono">{j.gh_job_id}</td></tr>
        <tr><th>GH run ID</th><td class="mono">{j.gh_run_id}</td></tr>
        <tr><th>Pacer ID</th><td class="mono">{j.id}</td></tr>
        <tr><th>Project</th><td class="mono">{j.project_id}</td></tr>
        <tr><th>Pool</th><td class="mono">{j.pool_id || "—"}</td></tr>
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
          <tr><th>Type</th><td class="mono">{i.instance_type || "—"}</td></tr>
          <tr><th>AZ</th><td class="mono">{i.az || "—"}</td></tr>
          <tr><th>Market</th><td class="mono">{i.spot ? "spot" : "on-demand"}{i.price_model ? ` (${i.price_model})` : ""}</td></tr>
          <tr><th>Price/hour</th><td class="mono">{i.price_per_hour != null ? "$" + i.price_per_hour.toFixed(4) : "—"}</td></tr>
          <tr><th>State</th><td class="mono">{i.state}</td></tr>
          <tr><th>Launched</th><td class="mono">{fmt(i.launched_at)}</td></tr>
          {#if i.terminated_at}
            <tr><th>Terminated</th><td class="mono">{fmt(i.terminated_at)}</td></tr>
          {/if}
          <tr><th>Est. cost</th><td class="mono">{cost(j.estimated_cost_usd) || "—"}</td></tr>
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
      <table class="tbl">
        <thead>
          <tr>
            <th style="width: 40px">#</th>
            <th>Name</th>
            <th>Status</th>
            <th>Conclusion</th>
            <th>Duration</th>
          </tr>
        </thead>
        <tbody>
          {#each derived.steps as step, idx (idx)}
            <tr>
              <td class="mono">{step.number ?? idx + 1}</td>
              <td>{step.name}</td>
              <td><span class="tag {statusClass(step.status)}">{step.status}</span></td>
              <td class="mono">{step.conclusion || "—"}</td>
              <td class="mono">{step.started_at && step.completed_at ? age(step.started_at, step.completed_at) : "—"}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}

    <h4 class="detail-section">Audit trail</h4>
    {#if detail.audit.length === 0}
      <p class="muted">No audit entries.</p>
    {:else}
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
