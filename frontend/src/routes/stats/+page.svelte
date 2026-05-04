<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { stats } from "$lib/api.js";

  // Default window: last 30 days, UTC midnight to UTC midnight.
  function todayUTC() {
    const d = new Date();
    return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
  }
  function isoDate(d) { return d.toISOString().slice(0, 10); }

  // The picked `to` date is treated as INCLUSIVE in the UI (the user
  // picks "include through this day"). The backend's window is
  // [from, to) -- exclusive on the right -- so the API call shifts
  // the picked date forward by one UTC day. Without this shift,
  // picking today excluded all of today's data.
  function nextDayUTC(yyyyMMdd) {
    const d = new Date(yyyyMMdd + "T00:00:00Z");
    d.setUTCDate(d.getUTCDate() + 1);
    return isoDate(d);
  }

  let to = $state(isoDate(todayUTC()));
  let from = $state(isoDate(new Date(todayUTC().getTime() - 30 * 86400_000)));
  let groupBy = $state("project");

  let loading = $state(false);
  let error = $state(null);
  let data = $state(null);
  let topUsers = $state(null);

  async function refresh() {
    loading = true;
    error = null;
    try {
      const toExclusive = nextDayUTC(to);
      // Both calls share the same window. Fired in parallel so the
      // top-users panel doesn't add a serial round-trip to the page.
      const [rollup, users] = await Promise.all([
        stats.rollup({ from, to: toExclusive, groupBy }),
        stats.topUsers({ from, to: toExclusive, limit: 10 }),
      ]);
      data = rollup;
      topUsers = users;
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function fmtUSD(n) {
    if (n == null) return "$0.00";
    if (n < 0.01 && n > 0) return "<$0.01";
    return "$" + n.toFixed(2);
  }
  function fmtMin(n) {
    if (!n) return "0";
    if (n < 60) return Math.round(n) + "m";
    const h = Math.floor(n / 60);
    const m = Math.round(n % 60);
    return `${h}h ${m}m`;
  }

  function setRange(days) {
    to = isoDate(todayUTC());
    from = isoDate(new Date(todayUTC().getTime() - days * 86400_000));
    refresh();
  }

  $effect(() => {
    refresh();
  });
</script>

<main>
  <div class="page-header">
    <h2>Stats</h2>
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
        <label for="from">From <span class="muted">(UTC)</span></label>
        <input id="from" class="input" type="date" bind:value={from} onchange={refresh} />
      </div>
      <div class="field">
        <label for="to">To <span class="muted">(UTC, inclusive)</span></label>
        <input id="to" class="input" type="date" bind:value={to} onchange={refresh} />
      </div>
    </div>
    <div class="field-row">
      <div class="field">
        <label for="gb">Group by</label>
        <select id="gb" class="select" bind:value={groupBy} onchange={refresh}>
          <option value="project">Project</option>
          <option value="pool">Pool</option>
          <option value="repo">Repo</option>
        </select>
      </div>
      <div class="field">
        <label>&nbsp;</label>
        <p class="muted" style="margin: 0; font-size: 12px; line-height: 36px;">
          Estimates only. Launch-time price * elapsed time -- ignores spot drift, EBS, and data transfer.
        </p>
      </div>
    </div>
  </div>

  {#if error}<div class="banner err">{error}</div>{/if}

  {#if data}
    <div class="stats">
      <div class="stat">
        <div class="label">Jobs</div>
        <div class="value">{data.totals.jobs}</div>
      </div>
      <div class="stat">
        <div class="label">Runner time</div>
        <div class="value">{fmtMin(data.totals.runner_minutes)}</div>
      </div>
      <div class="stat">
        <div class="label">Est. cost</div>
        <div class="value">{fmtUSD(data.totals.est_cost_usd)}</div>
      </div>
      <div class="stat">
        <div class="label">Jobs w/o cost</div>
        <div class="value">{data.totals.jobs_without_cost}</div>
      </div>
    </div>

    {#if !data.buckets || data.buckets.length === 0}
      <div class="empty">
        <pre class="ascii">    .---. .---. .---.
    | $ | | $ | | $ |
    '---' '---' '---'</pre>
        <h3>No completed jobs in this window</h3>
        <p>Nothing has run between <strong>{from}</strong> and <strong>{to}</strong>. Pick a wider range or wait for some workflows to finish.</p>
      </div>
    {:else}
      {@const groupLabel = groupBy === "repo" ? "Repository" : groupBy === "pool" ? "Pool" : "Project"}
      <table class="tbl tbl-stack">
        <thead>
          <tr>
            <th>{groupLabel}</th>
            <th>Jobs</th>
            <th>Runner time</th>
            <th>Est. cost</th>
            <th>Jobs w/o cost</th>
          </tr>
        </thead>
        <tbody>
          {#each data.buckets as b (b.key)}
            <tr>
              <td data-label={groupLabel}><strong>{b.name}</strong></td>
              <td class="mono" data-label="Jobs">{b.jobs}</td>
              <td class="mono" data-label="Runner time">{fmtMin(b.runner_minutes)}</td>
              <td class="mono" data-label="Est. cost">{fmtUSD(b.est_cost_usd)}</td>
              <td class="mono" data-label="Jobs w/o cost">{b.jobs_without_cost > 0 ? b.jobs_without_cost : "—"}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}

    {#if topUsers && topUsers.users && topUsers.users.length > 0}
      <h3 style="margin-top: 24px;">Top {topUsers.users.length} users</h3>
      <p class="muted" style="margin: 0 0 8px; font-size: 12px;">
        GitHub senders ranked by terminal-state job count in the same window.
      </p>
      <table class="tbl tbl-stack">
        <thead>
          <tr>
            <th style="width: 40px">#</th>
            <th>Login</th>
            <th>Jobs</th>
            <th>Runner time</th>
            <th>Est. cost</th>
          </tr>
        </thead>
        <tbody>
          {#each topUsers.users as u, idx (u.login)}
            <tr>
              <td class="mono" data-label="#">{idx + 1}</td>
              <td data-label="Login"><strong>{u.login}</strong></td>
              <td class="mono" data-label="Jobs">{u.jobs}</td>
              <td class="mono" data-label="Runner time">{fmtMin(u.runner_minutes)}</td>
              <td class="mono" data-label="Est. cost">{fmtUSD(u.est_cost_usd)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {/if}
</main>
