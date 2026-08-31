<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The job detail bundle: job row + webhook payload + instance + audit
// trail, from GET /api/jobs/:id. Refreshes itself silently every 5 s
// while open so a queued -> running -> completed transition shows up
// without closing the dialog (no spinner on the quiet path -- a flash
// per tick makes the modal unreadable).
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { jobs } from '@/api'
import { formatDate } from '@/composables/formatDate'
import { age, cost } from './jobFormat'
import BaseModal from '@/components/BaseModal.vue'
import StatusBadge from '@/components/StatusBadge.vue'

interface Job {
  id: string
  gh_job_id: number
  gh_run_id: number
  status: string
  repo_full_name: string
  project_id?: string
  pool_id?: string
  attempts: number
  sender_login?: string
  queued_at?: string
  claimed_at?: string
  started_at?: string
  completed_at?: string
  estimated_cost_usd?: number | null
  failure_stage?: string
  failure_message?: string
  failure_log?: string
}

interface Instance {
  id: string
  instance_type?: string
  az?: string
  spot?: boolean
  price_model?: string
  price_per_hour?: number | null
  state: string
  last_seen_at?: string
  launched_at?: string
  terminated_at?: string
}

interface AuditRow {
  id: string
  occurred_at: string
  action: string
  detail?: string
}

interface Step {
  number?: number
  name: string
  status?: string
  conclusion?: string | null
  started_at?: string
  completed_at?: string
}

interface DetailBundle {
  job: Job
  payload?: Record<string, unknown> | null
  instance?: Instance | null
  audit: AuditRow[]
  project_name?: string
  pool_name?: string
}

const props = defineProps<{ jobId: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const detail = ref<DetailBundle | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

async function load(silent: boolean) {
  if (!silent) {
    loading.value = true
    error.value = null
  }
  try {
    detail.value = (await jobs.get(props.jobId)) as DetailBundle
  } catch (e) {
    if (!silent) error.value = (e as Error).message
    // Silent-path errors are swallowed -- the next tick retries.
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  void load(false)
  timer = setInterval(() => void load(true), 5000)
})

onUnmounted(() => clearInterval(timer))

const fmt = (t?: string | null) => (t ? formatDate(t) : '')

// The webhook payload parsed once. Missing fields collapse to "" / []
// so the markup stays free of long optional chains.
const derived = computed(() => {
  const p = detail.value?.payload as
    | {
        workflow_job?: Record<string, unknown>
        repository?: Record<string, unknown>
        sender?: Record<string, unknown>
      }
    | null
    | undefined
  if (!p) return null
  const wj = (p.workflow_job || {}) as Record<string, unknown>
  const repo = (p.repository || {}) as Record<string, unknown>
  const sender = (p.sender || {}) as Record<string, unknown>
  return {
    htmlURL: (wj.html_url as string) || '',
    headBranch: (wj.head_branch as string) || '',
    headSHA: (wj.head_sha as string) || '',
    workflowName: (wj.workflow_name as string) || '',
    jobName: (wj.name as string) || '',
    runAttempt: (wj.run_attempt as number) || null,
    ghCreatedAt: (wj.created_at as string) || '',
    ghStartedAt: (wj.started_at as string) || '',
    ghCompletedAt: (wj.completed_at as string) || '',
    steps: (Array.isArray(wj.steps) ? wj.steps : []) as Step[],
    repoURL: (repo.html_url as string) || '',
    senderLogin: (sender.login as string) || detail.value?.job?.sender_login || '',
    senderURL: (sender.html_url as string) || '',
    senderType: (sender.type as string) || '',
  }
})

// The market, and how its price was quoted. The two usually agree, so
// naming the model as well would read as "spot (spot)"; it is only
// worth saying when the pricing lookup fell back to something else.
function marketLabel(i: Instance): string {
  const market = i.spot ? 'spot' : 'on-demand'
  if (!i.price_model || i.price_model === market) return market
  return `${market} (priced ${i.price_model})`
}

function shortSHA(sha: string): string {
  return sha ? sha.slice(0, 7) : ''
}

function commitURL(repoURL: string, sha: string): string {
  if (!repoURL || !sha) return ''
  return `${repoURL}/commit/${sha}`
}

// GitHub workflow_job step shape: status is the lifecycle phase and
// conclusion is the actual outcome. Coloring on status alone paints
// every finished step green, including failures -- prefer conclusion
// when present and fall back to status.
function stepResult(step: Step): { text: string; cls: string } {
  const c = (step.conclusion || '').toLowerCase()
  if (c === 'success') return { text: 'success', cls: 'badge badge-success' }
  if (c === 'failure' || c === 'timed_out') return { text: c, cls: 'badge badge-danger' }
  if (c === 'cancelled' || c === 'action_required' || c === 'neutral') {
    return { text: c, cls: 'badge badge-warning' }
  }
  if (c === 'skipped') return { text: 'skipped', cls: 'badge badge-info' }
  if (c) return { text: c, cls: 'badge' }
  // No conclusion yet -- step is still in flight. Show the lifecycle.
  const s = (step.status || '').toLowerCase()
  if (s === 'in_progress') return { text: 'running', cls: 'badge badge-info' }
  if (s === 'queued') return { text: 'queued', cls: 'badge badge-warning' }
  return { text: s || '-', cls: 'badge' }
}

// Heartbeat staleness classifier. The reaper stamps last_seen_at
// every ~60s for every alive instance:
//   < 90s   -> ok   ("the reaper just touched this")
//   < 180s  -> warn ("one tick missed, could be transient")
//   >= 180s -> crit ("multiple ticks missed -- something is wrong")
// Only meaningful for in-flight states; terminated rows freeze
// last_seen_at at termination time.
function heartbeat(instance: Instance): { text: string; cls: string } {
  if (!instance.last_seen_at) return { text: 'never', cls: 'badge badge-danger' }
  const inFlight = instance.state === 'starting' || instance.state === 'running'
  const ageMs = Date.now() - new Date(instance.last_seen_at).getTime()
  const ageStr = age(instance.last_seen_at, null) + ' ago'
  if (!inFlight) return { text: ageStr, cls: 'badge' }
  if (ageMs < 90_000) return { text: ageStr, cls: 'badge badge-success' }
  if (ageMs < 180_000) return { text: ageStr, cls: 'badge badge-warning' }
  return { text: ageStr, cls: 'badge badge-danger' }
}

// Pretty-print the audit Detail JSON inline (it's stored as a
// serialized string in the DB). Bad JSON falls through to the raw
// string so the modal never breaks on a malformed entry.
function fmtAuditDetail(s?: string): string {
  if (!s) return ''
  try {
    const o = JSON.parse(s) as Record<string, unknown>
    return Object.entries(o)
      .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
      .join(' ')
  } catch {
    return s
  }
}
</script>

<template>
  <BaseModal
    :title="detail ? `Job ${detail.job.gh_job_id}` : 'Job'"
    size="modal-w800"
    @close="emit('close')"
  >
    <p v-if="loading && !detail" class="text-muted">Loading...</p>
    <div v-else-if="error" class="alert alert-danger">{{ error }}</div>

    <template v-else-if="detail">
      <div class="detail-head">
        <StatusBadge :status="detail.job.status" scope="job" />
        <span class="code-font">{{ detail.job.repo_full_name }}</span>
        <a
          v-if="derived?.htmlURL"
          class="btn btn-secondary btn-sm"
          :href="derived.htmlURL"
          target="_blank"
          rel="noopener"
        >
          Open in GitHub
        </a>
      </div>

      <dl class="facts">
        <dt>Workflow</dt>
        <dd class="code-font">{{ derived?.workflowName || '-' }}</dd>
        <dt>Job name</dt>
        <dd class="code-font">{{ derived?.jobName || '-' }}</dd>
        <template v-if="derived?.headBranch">
          <dt>Branch</dt>
          <dd class="code-font">{{ derived.headBranch }}</dd>
        </template>
        <template v-if="derived?.headSHA">
          <dt>Commit</dt>
          <dd class="code-font">
            <a
              v-if="commitURL(derived.repoURL, derived.headSHA)"
              :href="commitURL(derived.repoURL, derived.headSHA)"
              target="_blank"
              rel="noopener"
            >
              {{ shortSHA(derived.headSHA) }}
            </a>
            <template v-else>{{ shortSHA(derived.headSHA) }}</template>
          </dd>
        </template>
        <template v-if="derived?.runAttempt">
          <dt>Attempt</dt>
          <dd class="code-font">{{ derived.runAttempt }}</dd>
        </template>
        <dt>Sender</dt>
        <dd class="code-font">
          <a v-if="derived?.senderURL" :href="derived.senderURL" target="_blank" rel="noopener">
            {{ derived.senderLogin }}
          </a>
          <template v-else>{{ derived?.senderLogin || '-' }}</template>
          <span v-if="derived?.senderType && derived.senderType !== 'User'" class="text-muted">
            ({{ derived.senderType }})
          </span>
        </dd>
        <dt>GH job ID</dt>
        <dd class="code-font">{{ detail.job.gh_job_id }}</dd>
        <dt>GH run ID</dt>
        <dd class="code-font">{{ detail.job.gh_run_id }}</dd>
        <dt>Pacer ID</dt>
        <dd class="code-font">{{ detail.job.id }}</dd>
        <dt>Project</dt>
        <dd class="code-font" :title="detail.job.project_id">
          {{ detail.project_name || detail.job.project_id || '-' }}
        </dd>
        <dt>Pool</dt>
        <dd class="code-font" :title="detail.job.pool_id">
          {{ detail.pool_name || detail.job.pool_id || '-' }}
        </dd>
        <dt>Attempts</dt>
        <dd class="code-font">{{ detail.job.attempts }}</dd>
      </dl>

      <h4 class="detail-section">Timeline</h4>
      <dl class="facts">
        <dt>Queued</dt>
        <dd class="code-font">{{ fmt(detail.job.queued_at) }}</dd>
        <template v-if="detail.job.claimed_at">
          <dt>Claimed</dt>
          <dd class="code-font">
            {{ fmt(detail.job.claimed_at) }}
            <span class="text-muted"
              >(+{{ age(detail.job.queued_at, detail.job.claimed_at) }})</span
            >
          </dd>
        </template>
        <template v-if="detail.job.started_at">
          <dt>Started</dt>
          <dd class="code-font">
            {{ fmt(detail.job.started_at) }}
            <span class="text-muted">
              (+{{ age(detail.job.claimed_at || detail.job.queued_at, detail.job.started_at) }})
            </span>
          </dd>
        </template>
        <template v-if="detail.job.completed_at">
          <dt>Completed</dt>
          <dd class="code-font">
            {{ fmt(detail.job.completed_at) }}
            <span class="text-muted">
              (+{{
                age(
                  detail.job.started_at || detail.job.claimed_at || detail.job.queued_at,
                  detail.job.completed_at,
                )
              }})
            </span>
          </dd>
        </template>
        <dt>Duration</dt>
        <dd class="code-font">
          {{
            age(
              detail.job.claimed_at || detail.job.started_at || detail.job.queued_at,
              detail.job.completed_at,
            )
          }}
        </dd>
        <template v-if="derived?.ghCreatedAt">
          <dt>GH created</dt>
          <dd class="code-font">{{ fmt(derived.ghCreatedAt) }}</dd>
        </template>
        <template v-if="derived?.ghStartedAt">
          <dt>GH started</dt>
          <dd class="code-font">{{ fmt(derived.ghStartedAt) }}</dd>
        </template>
        <template v-if="derived?.ghCompletedAt">
          <dt>GH completed</dt>
          <dd class="code-font">{{ fmt(derived.ghCompletedAt) }}</dd>
        </template>
      </dl>

      <template v-if="detail.instance">
        <h4 class="detail-section">Instance</h4>
        <dl class="facts">
          <dt>ID</dt>
          <dd class="code-font">{{ detail.instance.id }}</dd>
          <dt>Type</dt>
          <dd class="code-font">{{ detail.instance.instance_type || '-' }}</dd>
          <dt>AZ</dt>
          <dd class="code-font">{{ detail.instance.az || '-' }}</dd>
          <dt>Market</dt>
          <dd class="code-font">{{ marketLabel(detail.instance) }}</dd>
          <dt>Price/hour</dt>
          <dd class="code-font">
            {{
              detail.instance.price_per_hour != null
                ? '$' + detail.instance.price_per_hour.toFixed(4)
                : '-'
            }}
          </dd>
          <dt>State</dt>
          <dd><StatusBadge :status="detail.instance.state" scope="instance" /></dd>
          <dt>Last seen</dt>
          <dd class="code-font">
            <span
              v-if="detail.instance.last_seen_at"
              :class="heartbeat(detail.instance).cls"
              :title="fmt(detail.instance.last_seen_at) + ' UTC'"
            >
              {{ heartbeat(detail.instance).text }}
            </span>
            <span v-else class="text-muted">never</span>
          </dd>
          <dt>Launched</dt>
          <dd class="code-font">{{ fmt(detail.instance.launched_at) }}</dd>
          <template v-if="detail.instance.terminated_at">
            <dt>Terminated</dt>
            <dd class="code-font">{{ fmt(detail.instance.terminated_at) }}</dd>
          </template>
          <dt>Est. cost</dt>
          <dd class="code-font">{{ cost(detail.job.estimated_cost_usd) || '-' }}</dd>
        </dl>
      </template>

      <template v-if="detail.job.failure_log || detail.job.failure_message">
        <h4 class="detail-section">Failure</h4>
        <div class="failure-meta">
          <strong>{{ detail.job.failure_stage || 'bootstrap' }}</strong>
          <span v-if="detail.job.failure_message" class="text-muted">
            -- {{ detail.job.failure_message }}
          </span>
        </div>
        <pre v-if="detail.job.failure_log" class="failure-log">{{ detail.job.failure_log }}</pre>
      </template>

      <template v-if="derived && derived.steps.length > 0">
        <h4 class="detail-section">Steps</h4>
        <div class="table-wrapper">
          <table>
            <thead>
              <tr>
                <th style="width: 40px">#</th>
                <th>Name</th>
                <th>Result</th>
                <th>Duration</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(step, idx) in derived.steps" :key="idx">
                <td class="code-font">{{ step.number ?? idx + 1 }}</td>
                <td>{{ step.name }}</td>
                <td>
                  <span :class="stepResult(step).cls">{{ stepResult(step).text }}</span>
                </td>
                <td class="code-font">
                  {{
                    step.started_at && step.completed_at
                      ? age(step.started_at, step.completed_at)
                      : '-'
                  }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <h4 class="detail-section">Audit trail</h4>
      <p v-if="detail.audit.length === 0" class="text-muted">No audit entries.</p>
      <div v-else class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>When</th>
              <th>Action</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in detail.audit" :key="a.id">
              <td class="code-font">{{ fmt(a.occurred_at) }}</td>
              <td class="code-font">{{ a.action }}</td>
              <td class="code-font text-muted">{{ fmtAuditDetail(a.detail) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <details v-if="detail.payload" class="raw-payload">
        <summary>Raw payload</summary>
        <pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre>
      </details>
    </template>
  </BaseModal>
</template>

<style scoped>
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
  color: var(--text-muted);
  margin: 18px 0 8px;
}

.failure-meta {
  font-size: 13px;
  margin-bottom: 8px;
  color: var(--text-secondary);
}

.failure-log,
.raw-payload pre {
  margin: 0;
  padding: 12px 14px;
  background: var(--bg-code);
  border: 1px solid var(--border-primary);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.45;
  color: var(--text-code);
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
  color: var(--text-muted);
  cursor: pointer;
  user-select: none;
}

.raw-payload pre {
  margin-top: 8px;
  max-height: 320px;
  overflow: auto;
}
</style>
