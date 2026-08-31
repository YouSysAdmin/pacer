<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Two cards: the bootstrap API token (show + rotate) and the log
// retention periods (audit + webhook deliveries).
import { computed, onMounted, ref } from 'vue'
import { settings } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { formatDate } from '@/composables/formatDate'
import PageHeader from '@/components/PageHeader.vue'
import LoadingBlock from '@/components/LoadingBlock.vue'
import Notice from '@/components/Notice.vue'
import BaseModal from '@/components/BaseModal.vue'
import FormField from '@/components/FormField.vue'

interface TokenStatus {
  set: boolean
  masked: string
  updated_at?: string
}

interface Retention {
  audit_days: number
  audit_default: number
  audit_overridden: boolean
  webhook_days: number
  webhook_default: number
  webhook_overridden: boolean
  job_log_days: number
  job_log_default: number
  job_log_overridden: boolean
}

// The three periods, described once. Each was a hand-written block of
// field + hint + "use default" button, and the third copy is where a
// wrong key or a mismatched bound hides. Bounds mirror
// domain/settings/retention.go - the server rejects out-of-range
// values, this only stops the input offering them.
interface RetentionField {
  key: 'audit_days' | 'webhook_days' | 'job_log_days'
  label: string
  hint: string
  min: number
  max: number
}

const RETENTION_FIELDS: RetentionField[] = [
  {
    key: 'audit_days',
    label: 'Audit log',
    hint: 'State-change records on the Audit page. Rows are deleted past this age.',
    min: 1,
    max: 3650,
  },
  {
    key: 'webhook_days',
    label: 'Webhook deliveries',
    hint: 'Debug trail of incoming GitHub webhooks. Rows are deleted past this age.',
    min: 1,
    max: 365,
  },
  {
    key: 'job_log_days',
    label: 'Job logs',
    hint:
      'Captured bootstrap output on failed jobs - up to 64 KiB each, and the only ' +
      'table that grows without bound. Past this age the LOG is cleared; the job ' +
      'itself stays, so cost and runtime history are unaffected.',
    min: 1,
    max: 365,
  },
]

interface RotateResult {
  pools_rematerialized: number
  pools_failed?: string[]
}

const notify = useNotificationStore()

const status = ref<TokenStatus | null>(null)
const loading = ref(true)

const rotating = ref(false)
const rotateResult = ref<RotateResult | null>(null)
const confirmOpen = ref(false)

// Retention card state. `retention` is the GET-shaped payload from
// /api/settings/retention. The inputs are decoupled from it so the
// operator can edit without round-tripping the server.
const retention = ref<Retention | null>(null)
const inputs = ref<Record<string, string>>({})
const savingRetention = ref(false)

// Field metadata joined to the current server state, so the template
// reads one list instead of three near-identical blocks.
const retentionRows = computed(() =>
  RETENTION_FIELDS.map((f) => {
    const r = retention.value
    const prefix = f.key.replace(/_days$/, '')
    return {
      ...f,
      current: r ? (r[f.key] as number) : 0,
      // The API names the sibling fields <prefix>_default /
      // <prefix>_overridden, e.g. job_log_days -> job_log_default.
      fallback: r ? ((r as unknown as Record<string, number>)[prefix + '_default'] ?? 0) : 0,
      overridden: r
        ? Boolean((r as unknown as Record<string, boolean>)[prefix + '_overridden'])
        : false,
    }
  }),
)

function seedInputs(r: Retention) {
  // Seed with the current EFFECTIVE values - showing the number
  // unconditionally beats hiding it behind an empty field; the
  // "default: N" hint + "use default" button carry the cleared state.
  inputs.value = Object.fromEntries(RETENTION_FIELDS.map((f) => [f.key, String(r[f.key])]))
}

async function refresh() {
  loading.value = true
  try {
    const [s, r] = await Promise.all([settings.getBootstrapToken(), settings.getRetention()])
    status.value = s as TokenStatus
    retention.value = r as Retention
    seedInputs(r as Retention)
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

// Build a PUT body that only includes the fields the operator
// actually changed - avoids overwriting one field while editing
// the other.
function buildRetentionBody(): Record<string, number> | null {
  const cur = retention.value
  if (!cur) return null
  const body: Record<string, number> = {}
  for (const f of RETENTION_FIELDS) {
    const typed = parseInt(inputs.value[f.key] ?? '', 10)
    if (typed === cur[f.key]) continue
    if (Number.isNaN(typed)) {
      notify.error(`${f.label}: not a number`)
      return null
    }
    body[f.key] = typed
  }
  if (Object.keys(body).length === 0) {
    notify.info('Nothing changed')
    return null
  }
  return body
}

async function applyRetention(body: Record<string, number>, doneMsg: string) {
  savingRetention.value = true
  try {
    const r = (await settings.putRetention(body)) as Retention
    retention.value = r
    seedInputs(r)
    notify.success(doneMsg)
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    savingRetention.value = false
  }
}

async function saveRetention() {
  const body = buildRetentionBody()
  if (!body) return
  await applyRetention(body, 'Saved. Next prune sweep (within 24 h) will use the new periods.')
}

// Per-field "use default": sends 0 (the explicit clear-override
// sentinel) for that field only.
async function resetField(f: RetentionField) {
  await applyRetention({ [f.key]: 0 }, `Reverted ${f.label} to the YAML default.`)
}

async function doRotate() {
  rotating.value = true
  rotateResult.value = null
  try {
    rotateResult.value = (await settings.rotateBootstrapToken()) as RotateResult
    // Refresh status (updated_at, masked) after a successful rotate.
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    rotating.value = false
    confirmOpen.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <PageHeader title="Settings">
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">
      {{ loading ? 'Loading...' : 'Refresh' }}
    </button>
  </PageHeader>

  <LoadingBlock v-if="loading && !status" />

  <template v-else>
    <div class="card">
      <div class="card-header"><h2>Bootstrap API token</h2></div>
      <div class="card-body">
        <p class="auth-note">
          The shared secret baked into every pool's launch-template user-data. The in-instance
          bootstrap script presents it as <code>Authorization: Bearer &lt;token&gt;</code> when
          calling <code>POST /api/runner/bootstrap</code> to fetch its per-job callback token.
          Rotate the token if you suspect leakage or as part of a routine rotation policy.
        </p>

        <dl v-if="status?.set" class="facts">
          <dt>Token</dt>
          <dd class="code-font">{{ status.masked }}</dd>
          <dt>Last rotated</dt>
          <dd>{{ formatDate(status.updated_at, 'never') }}</dd>
        </dl>
        <Notice v-else kind="danger">
          No bootstrap API token in the settings table. Restart pacer to auto-generate one.
        </Notice>

        <Notice v-if="rotateResult" kind="success" class="mt-3">
          Token rotated. {{ rotateResult.pools_rematerialized }}
          {{ rotateResult.pools_rematerialized === 1 ? 'pool' : 'pools' }} re-materialized.
          <template v-if="rotateResult.pools_failed && rotateResult.pools_failed.length > 0">
            Failed: <span class="code-font">{{ rotateResult.pools_failed.join(', ') }}</span> -
            re-save manually.
          </template>
        </Notice>

        <div class="mt-4">
          <button
            class="btn btn-danger"
            :disabled="rotating || !status?.set"
            @click="confirmOpen = true"
          >
            {{ rotating ? 'Rotating...' : 'Rotate' }}
          </button>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-header"><h2>Log retention</h2></div>
      <div class="card-body">
        <p class="auth-note">
          How long each kind of record is kept before the daily pruner sweeps it. The server starts
          with the YAML defaults below; values entered here override those at runtime and persist in
          the settings table. Changes take effect at the next daily sweep - use the manual prune
          button on the <RouterLink to="/audit">audit page</RouterLink> if you need to clean the
          audit log immediately.
        </p>

        <template v-if="retention">
          <FormField v-for="f in retentionRows" :key="f.key" :label="`${f.label} (days)`">
            <!-- The reset button sits on the input's own line rather
                 than at the far edge of the card: it acts on this
                 field, and a hint two lines long would otherwise
                 leave it floating opposite nothing. -->
            <div class="retention-row">
              <input
                v-model="inputs[f.key]"
                class="form-input w-filter"
                type="number"
                :min="f.min"
                :max="f.max"
                :disabled="savingRetention"
              />
              <button
                class="btn btn-secondary btn-sm"
                :disabled="savingRetention || !f.overridden"
                title="Revert to the YAML default"
                @click="resetField(f)"
              >
                Use default
              </button>
            </div>
            <template #hint>
              {{ f.hint }}
              <br />
              default: {{ f.fallback }} days
              <span v-if="f.overridden" class="badge badge-info">overridden</span>
            </template>
          </FormField>

          <div class="mt-2">
            <button class="btn btn-primary" :disabled="savingRetention" @click="saveRetention">
              {{ savingRetention ? 'Saving...' : 'Save' }}
            </button>
          </div>
        </template>
      </div>
    </div>
  </template>

  <BaseModal
    v-if="confirmOpen"
    title="Rotate bootstrap API token?"
    size="modal-w560"
    @close="confirmOpen = false"
  >
    <p>
      A new token will be generated and stored in the settings table. Every pool's launch template
      will be re-materialized with the new token (one new LT version per pool).
    </p>
    <p class="text-muted">
      <strong>In-flight spawns</strong> launched against an older LT version will fail to bootstrap
      (401 from the bootstrap endpoint) and will be marked failed by the orchestrator. Drain pending
      jobs before rotating if you can't tolerate that.
    </p>
    <template #footer>
      <button class="btn btn-secondary" :disabled="rotating" @click="confirmOpen = false">
        Cancel
      </button>
      <button class="btn btn-danger" :disabled="rotating" @click="doRotate">
        {{ rotating ? 'Rotating...' : 'Rotate now' }}
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
/* The input and its reset action share a line. */
.retention-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
</style>
