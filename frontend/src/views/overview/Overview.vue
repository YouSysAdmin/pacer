<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The dashboard: live count tiles (5 s), month-to-date spend + daily
// chart (60 s). Two cadences because /api/stats is daily-grain -- it
// doesn't change between 5 s ticks.
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { projects, repos, jobs, stats } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useScopeStore } from '@/stores/scope'
import PageHeader from '@/components/PageHeader.vue'
import StatCard from '@/components/StatCard.vue'
import BarChart, { type DayPoint } from './BarChart.vue'

interface JobEntry {
  status: string
}

interface Bucket {
  key: string
  name: string
  est_cost_usd: number
}

const notify = useNotificationStore()
const scope = useScopeStore()

const counts = ref({ projects: 0, repos: 0, queued: 0, running: 0, completed: 0, failed: 0 })
const liveLoading = ref(true)

const spend = ref({
  total: 0,
  byProject: [] as Bucket[],
  byRepo: [] as Bucket[],
  jobsWithoutCost: 0,
})
const series = ref<DayPoint[]>([])
const rollupError = ref<string | null>(null)

// First day of the current month, in UTC, as YYYY-MM-DD. The stats
// endpoint accepts both RFC3339 and date-only; date-only is read as
// UTC midnight.
function firstOfMonthUTC(): string {
  const d = new Date()
  const y = d.getUTCFullYear()
  const m = String(d.getUTCMonth() + 1).padStart(2, '0')
  return `${y}-${m}-01`
}

// Sub-cent runs are common (a 14-second job on a $0.50/hr spot
// instance costs $0.0019). Standard 2-decimal formatting rounds those
// to "$0.00", which reads as "free" when it really means "below
// display precision" -- fall through to 4 decimals instead.
function fmtUSD(n?: number | null): string {
  if (n == null || isNaN(Number(n))) return '$0.00'
  const v = Number(n)
  if (v === 0) return '$0.00'
  if (Math.abs(v) < 0.01) return '$' + v.toFixed(4)
  return '$' + v.toFixed(2)
}

let liveFailed = false

async function refreshLive() {
  try {
    const [ps, rs, js] = await Promise.all([
      projects.list(),
      repos.list(),
      jobs.list({ limit: 200, projectID: scope.projectParam }),
    ])
    // jobs.list returns the envelope {entries, total, ...}. The
    // overview chips count by status within the most-recent window
    // only.
    const entries = ((js as { entries?: JobEntry[] })?.entries || []) as JobEntry[]
    const byStatus = (s: string) => entries.filter((j) => j.status === s).length
    // Repos has no pagination and every row carries project_id, so
    // the scope is applied here rather than asking the server.
    const repoRows = ((rs as Array<{ project_id: string }>) || []).filter(
      (r) => !scope.currentId || r.project_id === scope.currentId,
    )
    counts.value = {
      projects: ((ps as unknown[]) || []).length,
      repos: repoRows.length,
      queued: byStatus('queued') + byStatus('claimed') + byStatus('starting'),
      running: byStatus('running'),
      completed: byStatus('completed'),
      failed: byStatus('failed') + byStatus('cancelled') + byStatus('reaped'),
    }
    liveFailed = false
  } catch (e) {
    // Toast once, not every 5 s tick, or a flaky poll floods the corner.
    if (!liveFailed) notify.error((e as Error).message)
    liveFailed = true
  } finally {
    liveLoading.value = false
  }
}

async function refreshRollup() {
  rollupError.value = null
  const monthFrom = firstOfMonthUTC()
  try {
    const [byProj, byRepo, ts] = await Promise.all([
      stats.rollup({ from: monthFrom, groupBy: 'project', projectID: scope.projectParam }),
      stats.rollup({ from: monthFrom, groupBy: 'repo', projectID: scope.projectParam }),
      stats.timeseries({ from: monthFrom, projectID: scope.projectParam }),
    ])
    const proj = byProj as {
      totals?: { est_cost_usd?: number; jobs_without_cost?: number }
      buckets?: Bucket[]
    }
    spend.value = {
      total: proj?.totals?.est_cost_usd || 0,
      byProject: proj?.buckets || [],
      byRepo: (byRepo as { buckets?: Bucket[] })?.buckets || [],
      // Jobs whose pricing fetch failed at spawn time -- they
      // contribute zero to the total even though they really cost
      // something. Surfacing the count tells the operator the
      // headline is a floor, not a final answer.
      jobsWithoutCost: proj?.totals?.jobs_without_cost || 0,
    }
    series.value = (ts as { days?: DayPoint[] })?.days || []
  } catch (e) {
    rollupError.value = (e as Error).message
  }
}

function refreshAll() {
  void refreshLive()
  void refreshRollup()
}

let liveTimer: ReturnType<typeof setInterval> | undefined
let rollupTimer: ReturnType<typeof setInterval> | undefined

// Both cadences re-read the scope on their next tick anyway; this
// refreshes immediately so the page does not sit on the old project's
// numbers for up to a minute.
watch(() => scope.currentId, refreshAll)

onMounted(() => {
  refreshAll()
  liveTimer = setInterval(refreshLive, 5000)
  rollupTimer = setInterval(refreshRollup, 60000)
})

onUnmounted(() => {
  clearInterval(liveTimer)
  clearInterval(rollupTimer)
})
</script>

<template>
  <PageHeader title="Overview">
    <button class="btn btn-secondary" :disabled="liveLoading" @click="refreshAll">
      {{ liveLoading ? 'Refreshing...' : 'Refresh' }}
    </button>
  </PageHeader>

  <div class="overview-grid">
    <section class="overview-main">
      <div class="stats-grid">
        <!-- Projects stays a global count even under a scope: it is
             how many there are to switch between, not a property of
             the one selected. The sub-line says so. -->
        <StatCard
          label="Projects"
          icon="total"
          :value="String(counts.projects)"
          :sub="scope.currentId ? 'across the installation' : ''"
        />
        <StatCard label="Bound repos" icon="instances" :value="String(counts.repos)" />
        <StatCard label="Queued / starting" icon="queued" :value="String(counts.queued)" />
        <StatCard label="Running" icon="running" :value="String(counts.running)" />
        <StatCard label="Completed" icon="completed" :value="String(counts.completed)" />
        <StatCard label="Failed / reaped" icon="failed" :value="String(counts.failed)" />
      </div>

      <div class="card">
        <div class="card-header"><h2>Jobs this month</h2></div>
        <div class="card-body">
          <div v-if="rollupError" class="alert alert-danger">{{ rollupError }}</div>
          <p v-else-if="series.length === 0" class="text-muted">
            No completed jobs this month yet.
          </p>
          <BarChart v-else :series="series" />
        </div>
      </div>
    </section>

    <aside class="overview-side">
      <div class="card">
        <div class="card-header"><h2>Spend (this month)</h2></div>
        <div class="card-body">
          <div class="spend-total">
            <div class="spend-label">Total est.</div>
            <div class="spend-value">{{ fmtUSD(spend.total) }}</div>
            <div v-if="spend.jobsWithoutCost > 0" class="text-muted text-sm">
              {{ spend.jobsWithoutCost }} {{ spend.jobsWithoutCost === 1 ? 'job' : 'jobs' }} without
              cost data (pricing fetch failed at spawn)
            </div>
          </div>

          <div class="spend-section-label">Top projects</div>
          <p v-if="spend.byProject.length === 0" class="text-muted text-sm">No data.</p>
          <ul v-else class="rank-list">
            <li v-for="p in spend.byProject.slice(0, 5)" :key="p.key">
              <span class="rank-name" :title="p.name">{{ p.name }}</span>
              <span class="rank-amount">{{ fmtUSD(p.est_cost_usd) }}</span>
            </li>
          </ul>

          <div class="spend-section-label">Top repos</div>
          <p v-if="spend.byRepo.length === 0" class="text-muted text-sm">No data.</p>
          <ul v-else class="rank-list">
            <li v-for="r in spend.byRepo.slice(0, 5)" :key="r.key">
              <span class="rank-name" :title="r.name">{{ r.name }}</span>
              <span class="rank-amount">{{ fmtUSD(r.est_cost_usd) }}</span>
            </li>
          </ul>
        </div>
      </div>
    </aside>
  </div>

  <p class="text-muted text-sm mt-3">Live tiles refresh every 5 s. Spend + chart every 60 s.</p>
</template>

<style scoped>
.overview-grid {
  display: grid;
  grid-template-columns: 1fr 300px;
  gap: 16px;
  align-items: start;
}

.overview-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

@media (max-width: 1100px) {
  .overview-grid {
    grid-template-columns: 1fr;
  }
}

.spend-total {
  margin-bottom: 16px;
}

.spend-label {
  font-size: 12px;
  color: var(--text-muted);
}

.spend-value {
  font-family: var(--font-mono);
  font-size: 26px;
  font-weight: 600;
  color: var(--text-primary);
}

.spend-section-label {
  margin: 14px 0 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.rank-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.rank-list li {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}

.rank-name {
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rank-amount {
  font-family: var(--font-mono);
  color: var(--text-primary);
  flex-shrink: 0;
}
</style>
