<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Cost/usage rollups over a picked window, grouped by project, pool,
// or repo, plus the top-users panel.
import { computed, onMounted, ref, watch } from 'vue'
import { stats } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useScopeStore } from '@/stores/scope'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import FormField from '@/components/FormField.vue'
import StatCard from '@/components/StatCard.vue'

interface Bucket {
  key: string
  name: string
  jobs: number
  runner_minutes: number
  est_cost_usd: number
  jobs_without_cost: number
}

interface Rollup {
  totals: {
    jobs: number
    runner_minutes: number
    est_cost_usd: number
    jobs_without_cost: number
  }
  buckets?: Bucket[]
}

interface TopUser {
  login: string
  jobs: number
  runner_minutes: number
  est_cost_usd: number
}

const notify = useNotificationStore()
const scope = useScopeStore()

// Default window: last 30 days, UTC midnight to UTC midnight.
function todayUTC(): Date {
  const d = new Date()
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
}

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}

// The picked `to` date is treated as INCLUSIVE in the UI (the user
// picks "include through this day"). The backend's window is
// [from, to) -- exclusive on the right -- so the API call shifts
// the picked date forward by one UTC day. Without this shift,
// picking today excluded all of today's data.
function nextDayUTC(yyyyMMdd: string): string {
  const d = new Date(yyyyMMdd + 'T00:00:00Z')
  d.setUTCDate(d.getUTCDate() + 1)
  return isoDate(d)
}

const to = ref(isoDate(todayUTC()))
const from = ref(isoDate(new Date(todayUTC().getTime() - 30 * 86400_000)))
const groupBy = ref('project')

const loading = ref(false)
const data = ref<Rollup | null>(null)
const topUsers = ref<{ users?: TopUser[] } | null>(null)

const groupLabel = computed(() =>
  groupBy.value === 'repo' ? 'Repository' : groupBy.value === 'pool' ? 'Pool' : 'Project',
)

async function refresh() {
  loading.value = true
  try {
    const toExclusive = nextDayUTC(to.value)
    // Both calls share the same window. Fired in parallel so the
    // top-users panel doesn't add a serial round-trip to the page.
    const [rollup, users] = await Promise.all([
      stats.rollup({
        from: from.value,
        to: toExclusive,
        groupBy: groupBy.value,
        projectID: scope.projectParam,
      }),
      stats.topUsers({
        from: from.value,
        to: toExclusive,
        limit: 10,
        projectID: scope.projectParam,
      }),
    ])
    data.value = rollup as Rollup
    topUsers.value = users as { users?: TopUser[] }
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

function fmtUSD(n?: number | null): string {
  if (n == null) return '$0.00'
  if (n < 0.01 && n > 0) return '<$0.01'
  return '$' + n.toFixed(2)
}

function fmtMin(n?: number | null): string {
  if (!n) return '0'
  if (n < 60) return Math.round(n) + 'm'
  const h = Math.floor(n / 60)
  const m = Math.round(n % 60)
  return `${h}h ${m}m`
}

function setRange(days: number) {
  to.value = isoDate(todayUTC())
  from.value = isoDate(new Date(todayUTC().getTime() - days * 86400_000))
  refresh()
}

// The scope narrows the totals too, so the whole page reloads rather
// than filtering the buckets it already has.
watch(() => scope.currentId, refresh)

onMounted(refresh)
</script>

<template>
  <PageHeader title="Stats">
    <button class="btn btn-secondary btn-sm" @click="setRange(1)">1 d</button>
    <button class="btn btn-secondary btn-sm" @click="setRange(7)">7 d</button>
    <button class="btn btn-secondary btn-sm" @click="setRange(30)">30 d</button>
    <button class="btn btn-secondary btn-sm" @click="setRange(90)">90 d</button>
    <button class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">Refresh</button>
  </PageHeader>

  <div class="card">
    <div class="card-body">
      <div class="form-row">
        <FormField label="From" hint="UTC">
          <input v-model="from" class="form-input" type="date" @change="refresh" />
        </FormField>
        <FormField label="To" hint="UTC, inclusive">
          <input v-model="to" class="form-input" type="date" @change="refresh" />
        </FormField>
        <FormField
          label="Group by"
          hint="Estimates only. Launch-time price * elapsed time -- ignores spot drift, EBS, and data transfer."
        >
          <select v-model="groupBy" class="form-select" @change="refresh">
            <option value="project">Project</option>
            <option value="pool">Pool</option>
            <option value="repo">Repo</option>
          </select>
        </FormField>
      </div>
    </div>
  </div>

  <template v-if="data">
    <div class="stats-grid">
      <StatCard label="Jobs" icon="total" :value="String(data.totals.jobs)" />
      <StatCard label="Runner time" icon="queued" :value="fmtMin(data.totals.runner_minutes)" />
      <StatCard label="Est. cost" icon="spend" :value="fmtUSD(data.totals.est_cost_usd)" />
      <StatCard
        label="Jobs w/o cost"
        icon="failed"
        :value="String(data.totals.jobs_without_cost)"
        sub="pricing fetch failed at spawn"
      />
    </div>

    <EmptyState
      v-if="!data.buckets || data.buckets.length === 0"
      title="No completed jobs in this window"
    >
      <p>
        Nothing has run between <strong>{{ from }}</strong> and <strong>{{ to }}</strong
        >. Pick a wider range or wait for some workflows to finish.
      </p>
    </EmptyState>

    <div v-else class="card">
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>{{ groupLabel }}</th>
              <th>Jobs</th>
              <th>Runner time</th>
              <th>Est. cost</th>
              <th>Jobs w/o cost</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="b in data.buckets" :key="b.key">
              <td>
                <span class="cell-title">{{ b.name }}</span>
              </td>
              <td class="code-font">{{ b.jobs }}</td>
              <td class="code-font">{{ fmtMin(b.runner_minutes) }}</td>
              <td class="code-font">{{ fmtUSD(b.est_cost_usd) }}</td>
              <td class="code-font">{{ b.jobs_without_cost > 0 ? b.jobs_without_cost : '-' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="topUsers?.users && topUsers.users.length > 0" class="card">
      <div class="card-header">
        <h2>Top {{ topUsers.users.length }} users</h2>
        <span class="text-sm text-muted">
          GitHub senders ranked by terminal-state job count in the same window.
        </span>
      </div>
      <div class="table-wrapper">
        <table>
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
            <tr v-for="(u, idx) in topUsers.users" :key="u.login">
              <td class="code-font">{{ idx + 1 }}</td>
              <td>
                <span class="cell-title">{{ u.login }}</span>
              </td>
              <td class="code-font">{{ u.jobs }}</td>
              <td class="code-font">{{ fmtMin(u.runner_minutes) }}</td>
              <td class="code-font">{{ fmtUSD(u.est_cost_usd) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>
</template>
