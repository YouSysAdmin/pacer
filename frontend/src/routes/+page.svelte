<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { projects, repos, jobs, stats } from "$lib/api.js";

  // Live tiles (existing): refresh every 5 s.
  let counts = $state({
    projects: 0,
    repos: 0,
    queued: 0,
    running: 0,
    completed: 0,
    failed: 0,
  });
  let liveLoading = $state(true);
  let liveError = $state(null);

  // Month-to-date rollups: refresh every 60 s. /api/stats is cheap
  // but daily-grain doesn't change between 5 s ticks.
  let spend = $state({ total: 0, byProject: [], byRepo: [], jobsWithoutCost: 0 });
  let series = $state([]);
  let rollupLoading = $state(true);
  let rollupError = $state(null);

  // First day of the current month, in UTC, as YYYY-MM-DD. The stats
  // endpoint accepts both RFC3339 and date-only; date-only is read
  // as UTC midnight.
  function firstOfMonthUTC() {
    const d = new Date();
    const y = d.getUTCFullYear();
    const m = String(d.getUTCMonth() + 1).padStart(2, "0");
    return `${y}-${m}-01`;
  }
  const monthFrom = $derived(firstOfMonthUTC());

  // Chart axis: clamp to >=1 so an empty month doesn't divide by 0.
  const seriesMax = $derived(
    Math.max(
      1,
      ...series.map((d) => d.completed + d.failed + d.cancelled + d.reaped),
    ),
  );

  const topProjects = $derived(spend.byProject.slice(0, 5));
  const topRepos = $derived(spend.byRepo.slice(0, 5));

  // Sub-cent runs are common (a 14-second job on a $0.50/hr spot
  // instance costs $0.0019). Standard 2-decimal formatting rounds
  // those to "$0.00", which reads as "free" when it really means
  // "below display precision." When the value is non-zero but
  // under a cent, fall through to 4 decimals so something visible
  // shows up.
  function fmtUSD(n) {
    if (n == null || isNaN(n)) return "$0.00";
    const v = Number(n);
    if (v === 0) return "$0.00";
    if (Math.abs(v) < 0.01) return "$" + v.toFixed(4);
    return "$" + v.toFixed(2);
  }

  // Day-of-month label for the bar axis ("01" .. "31"); pulls the
  // last segment of the YYYY-MM-DD string the backend already
  // formatted.
  function dayLabel(s) {
    return (s || "").slice(-2);
  }

  async function refreshLive() {
    liveError = null;
    try {
      const [ps, rs, js] = await Promise.all([
        projects.list(),
        repos.list(),
        jobs.list(undefined, 200),
      ]);
      const byStatus = (s) => (js || []).filter((j) => j.status === s).length;
      counts = {
        projects: (ps || []).length,
        repos: (rs || []).length,
        queued: byStatus("queued") + byStatus("claimed") + byStatus("starting"),
        running: byStatus("running"),
        completed: byStatus("completed"),
        failed: byStatus("failed") + byStatus("cancelled") + byStatus("reaped"),
      };
    } catch (e) {
      liveError = e.message;
    } finally {
      liveLoading = false;
    }
  }

  async function refreshRollup() {
    rollupError = null;
    try {
      const [byProj, byRepo, ts] = await Promise.all([
        stats.rollup({ from: monthFrom, groupBy: "project" }),
        stats.rollup({ from: monthFrom, groupBy: "repo" }),
        stats.timeseries({ from: monthFrom }),
      ]);
      spend = {
        total: byProj?.totals?.est_cost_usd || 0,
        byProject: byProj?.buckets || [],
        byRepo: byRepo?.buckets || [],
        // Jobs whose pricing fetch failed at spawn time -- they
        // contribute zero to the total even though they really
        // cost something. Surfacing the count tells the operator
        // the headline is a floor, not a final answer.
        jobsWithoutCost: byProj?.totals?.jobs_without_cost || 0,
      };
      series = ts?.days || [];
    } catch (e) {
      rollupError = e.message;
    } finally {
      rollupLoading = false;
    }
  }

  function refreshAll() {
    refreshLive();
    refreshRollup();
  }

  $effect(() => {
    refreshLive();
    refreshRollup();
    const liveT = setInterval(refreshLive, 5000);
    const rollT = setInterval(refreshRollup, 60000);
    return () => {
      clearInterval(liveT);
      clearInterval(rollT);
    };
  });
</script>

<main>
  <div class="page-header">
    <h2>Overview</h2>
    <button class="btn" onclick={refreshAll} disabled={liveLoading}>
      {liveLoading ? "refreshing…" : "refresh"}
    </button>
  </div>

  {#if liveError}
    <div class="banner err">{liveError}</div>
  {/if}

  <div class="overview-grid">
    <section class="overview-main">
      <div class="stats">
        <div class="stat"><div class="label">Projects</div><div class="value">{counts.projects}</div></div>
        <div class="stat"><div class="label">Bound repos</div><div class="value">{counts.repos}</div></div>
        <div class="stat"><div class="label">Queued / starting</div><div class="value">{counts.queued}</div></div>
        <div class="stat"><div class="label">Running</div><div class="value">{counts.running}</div></div>
        <div class="stat"><div class="label">Completed</div><div class="value">{counts.completed}</div></div>
        <div class="stat"><div class="label">Failed / reaped</div><div class="value">{counts.failed}</div></div>
      </div>

      <div class="card">
        <h3>Jobs this month</h3>
        {#if rollupError}
          <div class="banner err">{rollupError}</div>
        {:else if series.length === 0}
          <p class="muted">No completed jobs this month yet.</p>
        {:else}
          <div class="bar-chart" aria-label="Daily completed and failed jobs">
            {#each series as d (d.day)}
              {@const total = d.completed + d.failed + d.cancelled + d.reaped}
              {@const failTotal = d.failed + d.cancelled + d.reaped}
              <div class="bar-col" title="{d.day}: {d.completed} ok, {failTotal} failed/cancelled">
                <div class="bar" style="height: {(total / seriesMax) * 100}%">
                  {#if failTotal > 0}
                    <div class="bar-fail" style="height: {(failTotal / total) * 100}%"></div>
                  {/if}
                </div>
                <div class="bar-label">{dayLabel(d.day)}</div>
              </div>
            {/each}
          </div>
          <div class="bar-legend">
            <span><span class="dot ok"></span>completed</span>
            <span><span class="dot crit"></span>failed / reaped</span>
            <span class="muted">peak {seriesMax}/day</span>
          </div>
        {/if}
      </div>
    </section>

    <aside class="overview-side">
      <div class="card">
        <h3>Spend (this month)</h3>
        <div class="spend-total">
          <div class="label">Total est.</div>
          <div class="value">{fmtUSD(spend.total)}</div>
          {#if spend.jobsWithoutCost > 0}
            <div class="muted spend-note">
              {spend.jobsWithoutCost} job{spend.jobsWithoutCost === 1 ? "" : "s"} without cost data (pricing fetch failed at spawn)
            </div>
          {/if}
        </div>

        <div class="spend-section-label">Top projects</div>
        {#if topProjects.length === 0}
          <p class="muted">No data.</p>
        {:else}
          <ul class="rank-list">
            {#each topProjects as p (p.key)}
              <li>
                <span class="name" title={p.name}>{p.name}</span>
                <span class="amount">{fmtUSD(p.est_cost_usd)}</span>
              </li>
            {/each}
          </ul>
        {/if}

        <div class="spend-section-label">Top repos</div>
        {#if topRepos.length === 0}
          <p class="muted">No data.</p>
        {:else}
          <ul class="rank-list">
            {#each topRepos as r (r.key)}
              <li>
                <span class="name" title={r.name}>{r.name}</span>
                <span class="amount">{fmtUSD(r.est_cost_usd)}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </aside>
  </div>

  <p class="muted">
    Live tiles refresh every 5 s; spend + chart every 60 s.
  </p>
</main>
