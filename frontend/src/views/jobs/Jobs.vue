<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Paginated job table with a status filter, a 5 s auto-refresh (page 1
// only -- older pages stay put while the operator reads them), and a
// reconcile-now shortcut for the reaper.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { jobs, systemHealth } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useScopeStore } from '@/stores/scope'
import { formatDate } from '@/composables/formatDate'
import type { Pageable } from '@/composables/usePagination'
import { age, cost } from './jobFormat'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import Pagination from '@/components/Pagination.vue'
import JobDetailModal from './JobDetailModal.vue'

const STATUSES = [
  '', // all
  'queued',
  'claimed',
  'starting',
  'running',
  'completed',
  'failed',
  'cancelled',
  'reaped',
]

interface JobRow {
  id: string
  status: string
  repo_full_name: string
  gh_job_id: number
  sender_login?: string
  instance_id?: string
  queued_at?: string
  claimed_at?: string
  started_at?: string
  completed_at?: string
  estimated_cost_usd?: number | null
}

const notify = useNotificationStore()
const scope = useScopeStore()

const list = ref<JobRow[]>([])
// Pagination envelope from GET /api/jobs. total drives the pager copy;
// limit/offset round-trip into the next request.
const total = ref(0)
const limit = ref(50)
const offset = ref(0)
const loading = ref(false)
const filter = ref('')

// Reconcile-now state: separate from `loading` so the table keeps its
// own refresh affordance independent of the reaper sweep.
const reconciling = ref(false)

const detailID = ref<string | null>(null)

const fmt = (t?: string | null) => (t ? formatDate(t) : '')

// refreshGen is a monotonic counter used to discard stale fetch
// results. With auto-refresh + manual paging both calling refresh(),
// responses can arrive out of order -- a slower page-1 reply landing
// after a faster page-2 reply would rewind the table.
let refreshGen = 0
let pollFailed = false

async function refresh() {
  const myGen = ++refreshGen
  loading.value = true
  try {
    const r = (await jobs.list({
      status: filter.value || undefined,
      limit: limit.value,
      offset: offset.value,
      projectID: scope.projectParam,
    })) as { entries?: JobRow[]; total?: number } | null
    if (myGen !== refreshGen) return // newer refresh in flight -- abandon
    list.value = r?.entries || []
    total.value = r?.total ?? list.value.length
    pollFailed = false
  } catch (e) {
    if (myGen !== refreshGen) return
    // Toast once, not on every poll tick.
    if (!pollFailed) notify.error((e as Error).message)
    pollFailed = true
  } finally {
    if (myGen === refreshGen) loading.value = false
  }
}

// firstPage is the "show me the latest" shortcut -- with auto-refresh
// paused past page 1, the operator needs a quick way back.
function firstPage() {
  offset.value = 0
  void refresh()
}

function goToPage(page: number) {
  offset.value = page * limit.value
  void refresh()
}

const pageable = computed<Pageable>(() => ({
  current_page: Math.floor(offset.value / limit.value),
  size: limit.value,
  total_pages: total.value > 0 ? Math.max(1, Math.ceil(total.value / limit.value)) : 1,
  total_elements: total.value,
  empty: total.value === 0,
}))

// Force an immediate reaper sweep server-side. Useful right after an
// operator terminates an instance in the AWS console -- the next
// scheduled tick is up to 60 s away, this collapses that to ~one
// round trip.
async function reconcile() {
  reconciling.value = true
  try {
    const r = (await systemHealth.reconcile()) as {
      checked?: number
      issue?: { message: string }
    } | null
    if (r?.issue) {
      notify.error(`Reaper sweep returned an issue: ${r.issue.message}`)
    } else {
      const checked = r?.checked ?? 0
      notify.success(`Reaper swept ${checked} ${checked === 1 ? 'instance' : 'instances'}.`)
    }
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    reconciling.value = false
  }
}

// Auto-refresh: one interval created on mount -- NOT a watchEffect
// that reads refresh()'s internals, which would recreate the interval
// on every offset change and race the pager (the Svelte page needed
// untrack() for exactly this). Gated on offset === 0: past page 1 the
// operator wants the data to stay put while they read it.
let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (offset.value === 0) void refresh()
  }, 5000)
})

onUnmounted(() => clearInterval(timer))

// Re-fetch when a filter, the page size, or the project scope
// changes. Reset to page 1 each time: a narrower filter would
// otherwise leave the pager pointing past the new (smaller) total.
// Explicit watch on the inputs only -- offset writes are owned by
// the pager, and watching refresh()'s own reads would make paging
// re-trigger this and rewind itself.
watch([filter, limit, () => scope.currentId], () => {
  offset.value = 0
  void refresh()
})
</script>

<template>
  <PageHeader title="Jobs">
    <select v-model="filter" class="form-select w-filter">
      <option v-for="s in STATUSES" :key="s" :value="s">{{ s || 'all' }}</option>
    </select>
    <select v-model.number="limit" class="form-select w-filter">
      <option :value="25">Per page: 25</option>
      <option :value="50">Per page: 50</option>
      <option :value="100">Per page: 100</option>
      <option :value="250">Per page: 250</option>
    </select>
    <button
      class="btn btn-secondary"
      :disabled="loading"
      title="Reload from page 1 (resets pagination)."
      @click="firstPage"
    >
      Refresh
    </button>
    <button
      class="btn btn-secondary"
      :disabled="reconciling"
      title="Force an immediate reaper sweep instead of waiting for the next 60s tick. Useful right after terminating an instance from the AWS console."
      @click="reconcile"
    >
      {{ reconciling ? 'Reconciling...' : 'Reconcile now' }}
    </button>
  </PageHeader>

  <EmptyState v-if="list.length === 0" :title="filter ? `No ${filter} jobs` : 'No jobs yet'">
    <template v-if="filter">
      <p>
        Nothing in the <strong>{{ filter }}</strong> bucket right now. Clear the filter to see every
        job.
      </p>
      <p class="mt-2">
        <button class="btn btn-secondary" @click="filter = ''">Clear filter</button>
      </p>
    </template>
    <p v-else>
      Once a bound repo's workflow runs, it'll show up here. Make sure the repo is
      <RouterLink to="/repos">bound</RouterLink> and the GitHub App is configured to deliver
      <code>workflow_job</code> webhooks.
    </p>
  </EmptyState>

  <template v-else>
    <div class="card">
      <div class="table-wrapper">
        <table>
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
            <tr v-for="j in list" :key="j.id">
              <td><StatusBadge :status="j.status" scope="job" /></td>
              <td class="code-font">{{ j.repo_full_name }}</td>
              <td class="code-font">{{ j.gh_job_id }}</td>
              <td class="code-font">{{ j.sender_login || '-' }}</td>
              <td class="code-font">{{ j.instance_id || '-' }}</td>
              <td class="code-font">{{ fmt(j.queued_at) }}</td>
              <td class="code-font">
                {{ age(j.claimed_at || j.started_at || j.queued_at, j.completed_at) }}
              </td>
              <td class="code-font">
                {{ cost(j.estimated_cost_usd) || (j.completed_at ? '-' : '') }}
              </td>
              <td>
                <div class="table-actions">
                  <button class="btn btn-secondary btn-sm" @click="detailID = j.id">Details</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination :pageable="pageable" @page="goToPage" />
    </div>

    <p class="text-muted text-sm mt-3">
      <template v-if="offset === 0">Auto-refreshes every 5 s.</template>
      <template v-else>
        Auto-refresh paused while paging -- go back to page 1 (or hit <strong>Refresh</strong>) to
        resume.
      </template>
    </p>
  </template>

  <JobDetailModal v-if="detailID" :job-id="detailID" @close="detailID = null" />
</template>
