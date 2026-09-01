<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The audit log: free-text search, exact-action filter, range presets,
// KPI tiles, expandable per-row detail, and the manual prune control.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { audit, projects, pools } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useConfirm } from '@/composables/useConfirm'
import type { Pageable } from '@/composables/usePagination'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatCard from '@/components/StatCard.vue'
import Pagination from '@/components/Pagination.vue'

interface AuditEntry {
  id: string
  occurred_at: string
  action: string
  actor_email?: string
  actor_user_id?: string
  client_ip?: string
  request_id?: string
  target_type?: string
  target_id?: string
  detail?: string
}

interface AuditPage {
  entries?: AuditEntry[]
  total: number
}

const notify = useNotificationStore()
const { confirm } = useConfirm()

// UTC-aligned date helpers (match the stats page convention).
function todayUTC(): Date {
  const d = new Date()
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
}

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}

// The backend's window is [since, until) - exclusive on the right.
// The UI treats the picked end of the range as INCLUSIVE, so bump +1
// day before dispatching.
function nextDayUTC(yyyyMMdd: string): string {
  const d = new Date(yyyyMMdd + 'T00:00:00Z')
  d.setUTCDate(d.getUTCDate() + 1)
  return isoDate(d)
}

// Range presets: a number (days) or 'all' = no time bound.
const RANGES = [
  { value: '1', label: 'Range: 1d' },
  { value: '7', label: 'Range: 7d' },
  { value: '30', label: 'Range: 30d' },
  { value: '90', label: 'Range: 90d' },
  { value: 'all', label: 'Range: all' },
]

const range = ref('7')
// q is the free-text search across target_id, detail JSON, client_ip,
// actor_email, request_id, and action - the primary search
// affordance. actionFilter is the exact-match power-user field.
const q = ref('')
const actionFilter = ref('')
const limit = ref(50)
const offset = ref(0)

const loading = ref(false)
const data = ref<AuditPage | null>(null)

const PRUNE_OPTIONS = [
  { value: 1, label: '1 day' },
  { value: 7, label: '7 days' },
  { value: 15, label: '15 days' },
  { value: 30, label: '30 days' },
  { value: 90, label: '90 days' },
  { value: 180, label: '180 days' },
  { value: 365, label: '1 year' },
  { value: 730, label: '2 years' },
]
const pruneDays = ref(90)
const pruning = ref(false)

// One row open at a time - toggling a different row closes the
// previous one.
const openID = ref<string | null>(null)
function toggleDetail(id: string) {
  openID.value = openID.value === id ? null : id
}

// UUID-keyed targets (project / pool) are opaque on their own.
// Resolve the human name once at mount and decorate the target block.
// Best-effort: a failed fetch only means a missing decoration line.
const projectName = ref(new Map<string, string>())
const poolName = ref(new Map<string, string>())

async function loadNames() {
  try {
    const [ps, pls] = await Promise.all([projects.list(), pools.list()])
    const pm = new Map<string, string>()
    for (const p of (ps as Array<{ id: string; name: string }>) || []) pm.set(p.id, p.name)
    const lm = new Map<string, string>()
    for (const pl of (pls as Array<{ id: string; project_id: string; name: string }>) || []) {
      const proj = pm.get(pl.project_id) || pl.project_id
      lm.set(pl.id, `${proj}/${pl.name}`)
    }
    projectName.value = pm
    poolName.value = lm
  } catch {
    // Skip the decoration.
  }
}

function targetName(e: AuditEntry): string {
  if (!e.target_type || !e.target_id) return ''
  if (e.target_type === 'project') return projectName.value.get(e.target_id) || ''
  if (e.target_type === 'pool') return poolName.value.get(e.target_id) || ''
  return ''
}

function windowParams(): { since?: string; until?: string } {
  if (range.value === 'all') return {}
  const days = Number(range.value)
  return {
    since: isoDate(new Date(todayUTC().getTime() - days * 86400_000)),
    until: nextDayUTC(isoDate(todayUTC())),
  }
}

async function refresh() {
  loading.value = true
  try {
    data.value = (await audit.list({
      ...windowParams(),
      q: q.value.trim() || undefined,
      action: actionFilter.value || undefined,
      limit: limit.value,
      offset: offset.value,
    })) as AuditPage
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

// Manual prune. Confirms first because audit deletes are irreversible
// - shows the cutoff date (operator-local time) so the user can
// sanity-check before the click.
async function runPrune() {
  const days = Number(pruneDays.value)
  if (!Number.isFinite(days) || days <= 0) return
  const cutoff = new Date(Date.now() - days * 86400_000)
  const ok = await confirm({
    title: 'Prune audit log',
    message:
      `Delete every audit entry older than ${days} ${days === 1 ? 'day' : 'days'} ` +
      `(before ${cutoff.toLocaleString()})? This cannot be undone. ` +
      'The prune itself will be recorded in the audit log.',
    confirmText: 'Delete',
    variant: 'danger',
  })
  if (!ok) return
  pruning.value = true
  try {
    const r = (await audit.prune(days)) as { deleted?: number; cutoff?: string } | null
    const n = r?.deleted ?? 0
    const ago = r?.cutoff ? new Date(r.cutoff).toLocaleString() : `${days} days ago`
    notify.success(
      `Deleted ${n.toLocaleString()} audit ${n === 1 ? 'entry' : 'entries'} older than ${ago}.`,
    )
    offset.value = 0
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    pruning.value = false
  }
}

function goToPage(page: number) {
  offset.value = page * limit.value
  void refresh()
}

const pageable = computed<Pageable>(() => {
  const total = data.value?.total ?? 0
  return {
    current_page: Math.floor(offset.value / limit.value),
    size: limit.value,
    total_pages: total > 0 ? Math.max(1, Math.ceil(total / limit.value)) : 1,
    total_elements: total,
    empty: total === 0,
  }
})

// Reset paging + reload when a discrete filter changes. Offset writes
// are owned by the pager, so this is an explicit watch on the filters
// rather than a watchEffect that would also track offset and rewind
// the page it just left.
watch([range, actionFilter, limit], () => {
  offset.value = 0
  void refresh()
})

// The search box is debounced (300ms) so typing doesn't fire one
// request per keystroke.
let qDebounce: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(qDebounce)
  qDebounce = setTimeout(() => {
    offset.value = 0
    void refresh()
  }, 300)
})

onUnmounted(() => clearTimeout(qDebounce))

onMounted(() => {
  void refresh()
  void loadNames()
})

// ----- Cell formatters --------------------------------------------

// Split occurred_at into HH:MM:SS + YYYY-MM-DD halves so the When
// column reads as a stacked time/date pair.
function fmtTime(t?: string): string {
  if (!t) return ''
  const d = new Date(t)
  if (isNaN(d.getTime())) return String(t)
  return d.toLocaleTimeString('en-GB', { hour12: false })
}

function fmtDay(t?: string): string {
  if (!t) return ''
  const d = new Date(t)
  if (isNaN(d.getTime())) return ''
  return d.toISOString().slice(0, 10)
}

// Class mapping kept in sync with the Go-side audit action taxonomy.
// Drives both the Outcome badge and the KPI failure count.
function actionClass(a: string): 'crit' | 'warn' | 'ok' | 'info' {
  if (
    a.endsWith('.failed') ||
    a.endsWith('.reaped') ||
    a.endsWith('.exhausted') ||
    a.endsWith('.no_pool_match') ||
    a === 'auth.login_failed' ||
    a === 'auth.oidc.login_denied' ||
    a === 'auth.oidc.login_failed'
  ) {
    return 'crit'
  }
  if (a.endsWith('.retry')) return 'warn'
  if (
    a.endsWith('.completed') ||
    a.endsWith('.launched') ||
    a.endsWith('.registered') ||
    a.endsWith('.created') ||
    a.endsWith('.bound') ||
    a === 'auth.oidc.login_succeeded' ||
    a === 'auth.login_succeeded'
  ) {
    return 'ok'
  }
  return 'info'
}

const OUTCOME_BADGE: Record<string, string> = {
  crit: 'badge badge-danger',
  warn: 'badge badge-warning',
  ok: 'badge badge-success',
  info: 'badge badge-info',
}

function outcomeLabel(a: string): string {
  const c = actionClass(a)
  return c === 'crit' ? 'fail' : c === 'warn' ? 'retry' : c === 'ok' ? 'ok' : 'info'
}

// Compact one-line summary of the detail JSON for the Event cell:
// a few key=value pairs, object values skipped, full JSON on expand.
function eventSubline(e: AuditEntry): string {
  const parts: string[] = []
  if (e.detail) {
    try {
      const o = JSON.parse(e.detail) as Record<string, unknown>
      for (const [k, v] of Object.entries(o)) {
        if (v == null || v === '') continue
        let s: string
        if (Array.isArray(v)) {
          if (v.length === 0) continue
          s = v.join(',')
        } else if (typeof v === 'object') {
          continue
        } else {
          s = String(v)
        }
        if (s.length > 40) s = s.slice(0, 37) + '...'
        parts.push(`${k}=${s}`)
        if (parts.length >= 3) break
      }
    } catch {
      // detail wasn't JSON - fall through to the empty subline
    }
  }
  return parts.join(' / ')
}

// Structured detail block, sectioned by # action / # actor / # target
// / # detail / # timestamp. Rendered in a pre so the columns line up.
function buildEvtDetail(e: AuditEntry): string {
  const lines: string[] = []
  lines.push('# action')
  lines.push(`${e.action}  outcome=${outcomeLabel(e.action)}`)
  lines.push('')
  lines.push('# actor')
  if (e.actor_email) lines.push('email      = ' + e.actor_email)
  if (e.actor_user_id) lines.push('user_id    = ' + e.actor_user_id)
  if (!e.actor_email && !e.actor_user_id) lines.push('--           system')
  if (e.client_ip) lines.push('ip         = ' + e.client_ip)
  if (e.request_id) lines.push('request_id = ' + e.request_id)
  if (e.target_type || e.target_id) {
    lines.push('')
    lines.push('# target')
    if (e.target_type) lines.push('type       = ' + e.target_type)
    if (e.target_id) lines.push('id         = ' + e.target_id)
    const tn = targetName(e)
    if (tn) lines.push('name       = ' + tn)
  }
  if (e.detail) {
    let parsed: Record<string, unknown> | null = null
    try {
      parsed = JSON.parse(e.detail) as Record<string, unknown>
    } catch {
      // detail wasn't JSON - fall through to the raw rendering
    }
    if (parsed && typeof parsed === 'object' && Object.keys(parsed).length > 0) {
      lines.push('')
      lines.push('# detail')
      for (const [k, v] of Object.entries(parsed)) {
        const padded = (k + '            ').slice(0, 12)
        let s: string
        if (v == null) s = 'null'
        else if (Array.isArray(v)) s = '[' + v.join(', ') + ']'
        else if (typeof v === 'object') s = JSON.stringify(v)
        else s = String(v)
        lines.push(padded + '= ' + s)
      }
    } else if (!parsed) {
      lines.push('')
      lines.push('# detail (raw)')
      lines.push(e.detail)
    }
  }
  lines.push('')
  lines.push('# timestamp')
  lines.push(e.occurred_at || '')
  return lines.join('\n')
}

// ----- KPI tiles --------------------------------------------------
// `events` reflects the full window (data.total). The other three are
// sample stats over the visible page - a documented trade-off so we
// don't fire a second 1000-row fetch just to feed the chips.
const kpis = computed(() => {
  const ents = data.value?.entries
  if (!ents) return { events: 0, actors: 0, failures: 0, actionTypes: 0 }
  const actors = new Set(
    ents.map((e) => e.actor_email || (e.actor_user_id ? 'u:' + e.actor_user_id : 'system')),
  )
  const failures = ents.filter((e) => actionClass(e.action) === 'crit').length
  const actionTypes = new Set(ents.map((e) => e.action)).size
  return { events: data.value?.total ?? ents.length, actors: actors.size, failures, actionTypes }
})
</script>

<template>
  <PageHeader>
    <template #title>
      <h1>Audit log</h1>
      <p class="header-sub">
        Immutable record of state-changing actions: project / pool / repo edits, runner lifecycle,
        and login attempts. Newest first.
      </p>
    </template>
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">Refresh</button>
  </PageHeader>

  <!-- Manual prune. Sits between the page header and the KPI tiles so
       the destructive action stays away from the search toolbar where
       misclicks would be more likely. -->
  <div class="prune-bar">
    <span class="prune-label">Prune entries older than</span>
    <select v-model.number="pruneDays" class="form-select" :disabled="pruning">
      <option v-for="o in PRUNE_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>
    <button
      class="btn btn-danger btn-sm"
      :disabled="pruning"
      title="Permanently delete audit entries older than the selected period."
      @click="runPrune"
    >
      {{ pruning ? 'Pruning...' : 'Prune' }}
    </button>
  </div>

  <div class="stats-grid kpi-row">
    <StatCard label="Events (window)" icon="total" :value="kpis.events.toLocaleString()" />
    <StatCard label="Unique actors (page)" icon="users" :value="String(kpis.actors)" />
    <StatCard label="Failures (page)" icon="failed" :value="String(kpis.failures)" />
    <StatCard label="Action types (page)" icon="running" :value="String(kpis.actionTypes)" />
  </div>

  <div class="filter-bar">
    <input
      v-model="q"
      class="form-input w-search"
      type="search"
      placeholder="search instance id / job id / ip / email / request id / action / detail..."
    />
    <input
      v-model="actionFilter"
      class="form-input w-filter"
      type="text"
      placeholder="action (exact)"
      title="Exact action match. For partial matches, use the search box - it covers action too."
    />
    <select v-model="range" class="form-select w-filter">
      <option v-for="r in RANGES" :key="r.value" :value="r.value">{{ r.label }}</option>
    </select>
    <select v-model.number="limit" class="form-select w-filter">
      <option :value="25">Per page: 25</option>
      <option :value="50">Per page: 50</option>
      <option :value="100">Per page: 100</option>
      <option :value="250">Per page: 250</option>
    </select>
    <span class="toolbar-count text-muted">
      {{ data ? `${data.total.toLocaleString()} events` : '-' }}
      <span v-if="loading"> loading...</span>
    </span>
  </div>

  <template v-if="data">
    <EmptyState
      v-if="!data.entries || data.entries.length === 0"
      title="No audit entries in this window"
    >
      <p>
        Nothing matched
        <template v-if="q.trim()">
          for <strong>"{{ q.trim() }}"</strong>
        </template>
        <template v-if="range === 'all'">across the whole log</template>
        <template v-else>
          within the last <strong>{{ range }}</strong> {{ range === '1' ? 'day' : 'days' }}
        </template>
        {{ actionFilter ? ` for action ${actionFilter}` : '' }}. Try a wider range or clear the
        search.
      </p>
    </EmptyState>

    <div v-else class="card">
      <div class="table-wrapper">
        <table class="audit-tbl">
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
            <template v-for="e in data.entries" :key="e.id">
              <tr class="audit-row" :class="{ open: openID === e.id }">
                <td class="code-font">
                  <span class="cell-stack">
                    <span>{{ fmtTime(e.occurred_at) }}</span>
                    <span class="sub">{{ fmtDay(e.occurred_at) }}</span>
                  </span>
                </td>
                <td>
                  <span class="cell-stack">
                    <strong class="action-name">{{ e.action }}</strong>
                    <span v-if="eventSubline(e)" class="sub">{{ eventSubline(e) }}</span>
                  </span>
                </td>
                <td class="code-font">
                  <template v-if="e.actor_email">{{ e.actor_email }}</template>
                  <template v-else-if="e.actor_user_id">
                    user:{{ e.actor_user_id.slice(0, 8) }}
                  </template>
                  <span v-else class="text-muted">system</span>
                </td>
                <td class="code-font">{{ e.target_type || '-' }}</td>
                <td class="code-font">{{ e.client_ip || '-' }}</td>
                <td>
                  <span :class="OUTCOME_BADGE[actionClass(e.action)]">
                    {{ outcomeLabel(e.action) }}
                  </span>
                </td>
                <td class="row-toggle">
                  <button
                    class="btn btn-secondary btn-sm"
                    :aria-label="openID === e.id ? 'collapse' : 'expand'"
                    @click="toggleDetail(e.id)"
                  >
                    {{ openID === e.id ? '-' : '+' }}
                  </button>
                </td>
              </tr>
              <tr v-if="openID === e.id" class="detail-row">
                <td colspan="7">
                  <pre class="evt-detail">{{ buildEvtDetail(e) }}</pre>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <Pagination :pageable="pageable" @page="goToPage" />
    </div>
  </template>
</template>

<style scoped>
.kpi-row {
  margin-bottom: 14px;
}

.prune-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}

.prune-bar .form-select {
  width: auto;
}

.prune-label {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-muted);
}

.toolbar-count {
  font-family: var(--font-mono);
  font-size: 12px;
  margin-left: auto;
  white-space: nowrap;
}

/* Two-line cells: a vertical mini-stack inside a single TD keeping
   the primary text and its subtitle together. */
.audit-tbl .cell-stack {
  display: inline-flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: 8px 0;
}

.audit-tbl .sub {
  color: var(--text-muted);
  font-size: 11px;
  font-family: var(--font-mono);
  line-height: 1.3;
  word-break: break-all;
}

.audit-tbl .action-name {
  color: var(--text-primary);
  font-weight: 500;
  font-family: var(--font-mono);
  font-size: 13px;
}

.audit-tbl td {
  vertical-align: top;
  height: auto;
  padding-top: 8px;
  padding-bottom: 8px;
}

.audit-tbl tr.audit-row.open td {
  background: var(--bg-tertiary);
}

.row-toggle {
  text-align: right;
}

.row-toggle .btn {
  width: 28px;
  padding: 0;
  font-family: var(--font-mono);
  font-size: 14px;
}

/* Inline expanded detail. pre-wrap so long values (commit URLs,
   stack-trace fragments) wrap rather than push the row sideways. */
.detail-row td {
  padding: 0;
}

.evt-detail {
  margin: 0;
  padding: 14px 18px;
  background: var(--bg-code);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-code);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 380px;
  overflow-y: auto;
}
</style>
