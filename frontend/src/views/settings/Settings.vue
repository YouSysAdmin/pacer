<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Two cards: the bootstrap API token (show + rotate) and the log
// retention periods (audit + webhook deliveries).
import { onMounted, ref } from 'vue'
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
}

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
const auditInput = ref('')
const webhookInput = ref('')
const savingRetention = ref(false)

async function refresh() {
  loading.value = true
  try {
    const [s, r] = await Promise.all([settings.getBootstrapToken(), settings.getRetention()])
    status.value = s as TokenStatus
    retention.value = r as Retention
    // Seed the editable inputs with the current EFFECTIVE values --
    // showing the number unconditionally beats hiding it behind an
    // empty field; the "default: N" hint + "use default" button carry
    // the cleared state.
    auditInput.value = String((r as Retention).audit_days)
    webhookInput.value = String((r as Retention).webhook_days)
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

// Build a PUT body that only includes the fields the operator
// actually changed -- avoids overwriting one field while editing
// the other.
function buildRetentionBody(): Record<string, number> | null {
  const body: Record<string, number> = {}
  const cur = retention.value
  if (!cur) return null
  const a = parseInt(auditInput.value, 10)
  const w = parseInt(webhookInput.value, 10)
  if (a !== cur.audit_days) {
    if (Number.isNaN(a)) {
      notify.error('Audit days: not a number')
      return null
    }
    body.audit_days = a
  }
  if (w !== cur.webhook_days) {
    if (Number.isNaN(w)) {
      notify.error('Webhook days: not a number')
      return null
    }
    body.webhook_days = w
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
    auditInput.value = String(r.audit_days)
    webhookInput.value = String(r.webhook_days)
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
async function resetField(field: 'audit_days' | 'webhook_days') {
  await applyRetention({ [field]: 0 }, `Reverted ${field.replace('_', ' ')} to the YAML default.`)
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
            Failed: <span class="code-font">{{ rotateResult.pools_failed.join(', ') }}</span> --
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
          How long the audit log and webhook delivery records are kept before the daily pruner
          deletes them. The server starts with the YAML defaults below. Values entered here override
          those at runtime and persist in the settings table. Changes take effect at the next daily
          prune sweep -- use the manual prune button on the
          <RouterLink to="/audit">audit page</RouterLink> if you need to clean up immediately.
        </p>

        <template v-if="retention">
          <div class="retention-row">
            <FormField label="Audit log (days)">
              <input
                v-model="auditInput"
                class="form-input w-filter"
                type="number"
                min="1"
                max="3650"
                :disabled="savingRetention"
              />
              <template #hint>
                default: {{ retention.audit_default }} days
                <span v-if="retention.audit_overridden" class="badge badge-info">overridden</span>
              </template>
            </FormField>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="savingRetention || !retention.audit_overridden"
              title="Revert to the YAML default"
              @click="resetField('audit_days')"
            >
              Use default
            </button>
          </div>

          <div class="retention-row">
            <FormField label="Webhook deliveries (days)">
              <input
                v-model="webhookInput"
                class="form-input w-filter"
                type="number"
                min="1"
                max="365"
                :disabled="savingRetention"
              />
              <template #hint>
                default: {{ retention.webhook_default }} days
                <span v-if="retention.webhook_overridden" class="badge badge-info">overridden</span>
              </template>
            </FormField>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="savingRetention || !retention.webhook_overridden"
              title="Revert to the YAML default"
              @click="resetField('webhook_days')"
            >
              Use default
            </button>
          </div>

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
/* One row per setting: the field (with its default/overridden hint)
   beside its use-default action, aligned to the input line. */
.retention-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.retention-row .btn {
  margin-top: 26px;
}
</style>
