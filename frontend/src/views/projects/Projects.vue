<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Project list + create/edit modal. A project is a logical grouping;
// everything EC2-shaped lives on its pools.
import { computed, onMounted, ref } from 'vue'
import { projects, pools as poolsAPI } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useConfirm } from '@/composables/useConfirm'
import { useFieldErrors } from '@/composables/fieldErrors'
import { isReservedTagKey, noSlashOrSpace } from '@/lib/validators'
import PageHeader from '@/components/PageHeader.vue'
import LoadingBlock from '@/components/LoadingBlock.vue'
import EmptyState from '@/components/EmptyState.vue'
import BaseModal from '@/components/BaseModal.vue'
import FormField from '@/components/FormField.vue'
import TagsEditor from '@/components/TagsEditor.vue'

// Caps mirror the Go DTO tags on project/endpoint.go::input. Keep
// these in sync when the backend rules move. Drift shows up as a
// green client tick followed by a server 400.
const NAME_MAX = 128
const ORG_NAME_MAX = 39

interface Project {
  id: string
  name: string
  max_concurrent_runners: number
  tags?: Record<string, string>
  scope?: string
  org_name?: string
  runner_group_id?: number
  disabled: boolean
}

interface ProjectForm {
  name: string
  max_concurrent_runners: number
  tags: Record<string, string>
  scope: string
  org_name: string
  runner_group_id: number
  disabled: boolean
}

const notify = useNotificationStore()
const { confirm } = useConfirm()
const { errors: serverErrors, capture, clear: clearServerError } = useFieldErrors()

const list = ref<Project[]>([])
const poolCounts = ref<Record<string, number>>({})
const loading = ref(true)
const formError = ref<string | null>(null)
const editing = ref<string | null>(null)
const formOpen = ref(false)

function emptyForm(): ProjectForm {
  return {
    name: '',
    max_concurrent_runners: 0,
    tags: {},
    scope: 'repo',
    org_name: '',
    runner_group_id: 0,
    disabled: false,
  }
}

const form = ref<ProjectForm>(emptyForm())

async function refresh() {
  loading.value = true
  try {
    const [ps, allPools] = await Promise.all([projects.list(), poolsAPI.list()])
    list.value = (ps as Project[]) || []
    const counts: Record<string, number> = {}
    for (const p of (allPools as Array<{ project_id: string }>) || []) {
      counts[p.project_id] = (counts[p.project_id] || 0) + 1
    }
    poolCounts.value = counts
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.value = emptyForm()
  formError.value = null
  clearServerError()
  formOpen.value = true
}

function startEdit(p: Project) {
  editing.value = p.id
  form.value = {
    name: p.name,
    max_concurrent_runners: p.max_concurrent_runners,
    tags: { ...(p.tags || {}) },
    scope: p.scope || 'repo',
    org_name: p.org_name || '',
    runner_group_id: p.runner_group_id || 0,
    disabled: p.disabled,
  }
  formError.value = null
  clearServerError()
  formOpen.value = true
}

function cancelEdit() {
  editing.value = null
  form.value = emptyForm()
  formError.value = null
  clearServerError()
  formOpen.value = false
}

// numericLt0 detects a number-input that's been driven negative. The
// backend silently clamps negatives via Normalize(), so without an
// inline hint the value the user typed would disappear without comment.
function numericLt0(v: unknown): boolean {
  const n = Number(v)
  return Number.isFinite(n) && n < 0
}

// Live derived hints. These mirror the backend rules at
// project/endpoint.go::input -- name length, org_name shape, and the
// "required_if scope=org" conditional. Keyed by the json field name so
// server-side err.fields overlays cleanly in hintFor().
const liveHints = computed(() => {
  const f = form.value
  const h: Record<string, string> = {}
  if (f.name && f.name.length > NAME_MAX) {
    h.name = `Name must be at most ${NAME_MAX} characters`
  }
  if (numericLt0(f.max_concurrent_runners)) {
    h.max_concurrent_runners = 'Max concurrent runners must be 0 or greater'
  }
  if (numericLt0(f.runner_group_id)) {
    h.runner_group_id = 'Runner group id must be 0 or greater'
  }
  if (f.scope === 'org') {
    if (!f.org_name) {
      h.org_name = 'Org login is required when scope is set to org'
    } else if (!noSlashOrSpace(f.org_name)) {
      h.org_name = 'Org login must not contain slashes or spaces'
    } else if (f.org_name.length > ORG_NAME_MAX) {
      h.org_name = `Org login must be at most ${ORG_NAME_MAX} characters`
    }
  }
  for (const k of Object.keys(f.tags || {})) {
    if (isReservedTagKey(k)) {
      h.tags = 'Tag keys starting with "gha:" are reserved; pick a different prefix'
      break
    }
  }
  return h
})

// The live hint wins while typing; the server's message covers rules
// the client doesn't know (uniqueness, anything cross-row).
function hintFor(name: string): string {
  return liveHints.value[name] || serverErrors.value[name] || ''
}

function buildBody() {
  const f = form.value
  const scope = f.scope === 'org' ? 'org' : 'repo'
  return {
    name: f.name.trim(),
    max_concurrent_runners: Number(f.max_concurrent_runners) || 0,
    tags: f.tags || {},
    scope,
    org_name: scope === 'org' ? (f.org_name || '').trim() : '',
    runner_group_id: scope === 'org' ? Number(f.runner_group_id) || 0 : 0,
    disabled: !!f.disabled,
  }
}

async function submit() {
  formError.value = null
  clearServerError()
  // Surface the live hints synchronously on submit -- faster feedback
  // than waiting for the server round-trip (which still validates).
  if (Object.keys(liveHints.value).length > 0) {
    formError.value = 'Please fix the highlighted fields'
    return
  }
  try {
    const body = buildBody()
    if (editing.value) {
      await projects.update(editing.value, body)
      notify.success(`Updated ${body.name}`)
    } else {
      await projects.create(body)
      notify.success(`Created ${body.name}`)
    }
    cancelEdit()
    await refresh()
  } catch (e) {
    if (!capture(e)) formError.value = (e as Error).message
    else formError.value = 'Please fix the highlighted fields'
  }
}

async function remove(p: Project) {
  const ok = await confirm({
    title: 'Delete project?',
    message:
      `Permanently delete project "${p.name}"? Pools and repo bindings inside it must ` +
      'already be removed; the backend will refuse otherwise.',
    confirmText: 'Delete',
    variant: 'danger',
  })
  if (!ok) return
  try {
    await projects.delete(p.id)
    notify.success(`Deleted ${p.name}`)
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  }
}

function tagsSummary(p: Project): string {
  return Object.entries(p.tags || {})
    .map(([k, v]) => `${k}=${v}`)
    .join(', ')
}

onMounted(refresh)
</script>

<template>
  <PageHeader title="Projects">
    <button class="btn btn-primary" @click="openCreate">+ New project</button>
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">Refresh</button>
  </PageHeader>

  <LoadingBlock v-if="loading && list.length === 0" />

  <EmptyState v-else-if="list.length === 0" title="No projects yet">
    <p>
      Projects group runners by team or workload. Create one to start binding repos and provisioning
      pools.
    </p>
    <p class="mt-2">
      <button class="btn btn-primary" @click="openCreate">+ New project</button>
    </p>
  </EmptyState>

  <div v-else class="card">
    <div class="table-wrapper">
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Scope</th>
            <th>Pools</th>
            <th>Project cap</th>
            <th>Tags</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in list" :key="p.id">
            <td>
              <span class="cell-title">{{ p.name }}</span>
              <span v-if="p.disabled" class="badge badge-warning">disabled</span>
            </td>
            <td>
              <template v-if="p.scope === 'org'">
                <span class="badge badge-info">org</span>
                <span class="code-font text-xs">
                  {{ p.org_name }}{{ p.runner_group_id ? `#${p.runner_group_id}` : '' }}
                </span>
              </template>
              <span v-else class="badge badge-neutral">repo</span>
            </td>
            <td>
              <RouterLink class="cell-link" :to="`/pools?project=${p.id}`">
                {{ poolCounts[p.id] ?? 0 }}
              </RouterLink>
            </td>
            <td>{{ p.max_concurrent_runners > 0 ? p.max_concurrent_runners : '-' }}</td>
            <td class="code-font text-xs">
              <span v-if="tagsSummary(p)">{{ tagsSummary(p) }}</span>
              <span v-else class="text-muted">-</span>
            </td>
            <td>
              <div class="table-actions">
                <button class="btn btn-secondary btn-sm" @click="startEdit(p)">Edit</button>
                <button class="btn btn-danger btn-sm" @click="remove(p)">Delete</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <BaseModal
    v-if="formOpen"
    :title="editing ? 'Edit project' : 'New project'"
    size="modal-w640"
    form
    @close="cancelEdit"
    @submit="submit"
  >
    <p class="auth-note">
      Project is a logical grouping. EC2 launch settings (AMI, instance types, subnets, etc.) live
      on the project's <RouterLink to="/pools">pools</RouterLink>.
    </p>
    <div v-if="formError" class="alert alert-danger">{{ formError }}</div>

    <div class="form-row">
      <FormField label="Name" :error="hintFor('name')" required>
        <input
          v-model="form.name"
          class="form-input"
          :disabled="!!editing"
          placeholder="my-app"
          required
          :maxlength="NAME_MAX"
          @input="clearServerError('name')"
        />
      </FormField>
      <FormField
        label="Max concurrent runners"
        hint="0 = no project-wide ceiling. Per-pool caps still apply."
        :error="hintFor('max_concurrent_runners')"
      >
        <input
          v-model="form.max_concurrent_runners"
          class="form-input"
          type="number"
          min="0"
          @input="clearServerError('max_concurrent_runners')"
        />
      </FormField>
    </div>

    <div class="form-row">
      <FormField
        label="Scope"
        :hint="
          form.scope === 'org'
            ? 'Route by repository.owner.login. Shared across the org / runner group.'
            : 'Bind individual repos. Runners narrow to <owner>-<repo>.'
        "
        :error="hintFor('scope')"
      >
        <select v-model="form.scope" class="form-select" @change="clearServerError('scope')">
          <option value="repo">repo</option>
          <option value="org">org</option>
        </select>
      </FormField>
      <FormField
        v-if="form.scope === 'org'"
        label="Org login"
        hint="GitHub org name. Case-insensitive match against repository.owner.login."
        :error="hintFor('org_name')"
        required
      >
        <input
          v-model="form.org_name"
          class="form-input code-font"
          placeholder="acme-inc"
          required
          :maxlength="ORG_NAME_MAX"
          @input="clearServerError('org_name')"
        />
      </FormField>
    </div>

    <FormField
      v-if="form.scope === 'org'"
      label="Runner group id"
      :error="hintFor('runner_group_id')"
    >
      <input
        v-model="form.runner_group_id"
        class="form-input"
        type="number"
        min="0"
        @input="clearServerError('runner_group_id')"
      />
      <template #hint>
        0 = GitHub's "Default" group, id 1. Look up org-specific groups via
        <code>GET /orgs/&lt;org&gt;/actions/runner-groups</code>.
      </template>
    </FormField>

    <FormField label="Tags" :error="hintFor('tags')">
      <TagsEditor v-model="form.tags" />
      <template #hint>
        Cascade to every pool's launch template + every spawned instance and EBS volume. Pool tags
        override on key conflict, repo tags override pool tags. <code>gha:</code> prefix reserved.
      </template>
    </FormField>

    <FormField :native="false">
      <label class="checkbox-label">
        <input v-model="form.disabled" type="checkbox" />
        Disabled
      </label>
    </FormField>

    <template #footer>
      <button class="btn btn-secondary" type="button" @click="cancelEdit">Cancel</button>
      <button class="btn btn-primary" type="submit">{{ editing ? 'Save' : 'Create' }}</button>
    </template>
  </BaseModal>
</template>
