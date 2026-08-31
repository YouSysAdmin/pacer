<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Repo bindings: which GitHub repo feeds which repo-scoped project.
// Bind upserts, so the same modal covers create + edit.
import { computed, onMounted, ref } from 'vue'
import { repos, projects } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useConfirm } from '@/composables/useConfirm'
import { useFieldErrors } from '@/composables/fieldErrors'
import { isRepoFullName, isReservedTagKey } from '@/lib/validators'
import PageHeader from '@/components/PageHeader.vue'
import LoadingBlock from '@/components/LoadingBlock.vue'
import EmptyState from '@/components/EmptyState.vue'
import BaseModal from '@/components/BaseModal.vue'
import FormField from '@/components/FormField.vue'
import TagsEditor from '@/components/TagsEditor.vue'

// Caps mirror domain/repo/endpoint.go::bindInput.
const FULL_NAME_MAX = 140

interface Repo {
  full_name: string
  project_id: string
  max_concurrent_runners?: number | null
  tags?: Record<string, string>
}

interface Project {
  id: string
  name: string
  scope?: string
}

interface RepoForm {
  full_name: string
  project_id: string
  max_concurrent_runners: string | number
  tags: Record<string, string>
}

const notify = useNotificationStore()
const { confirm } = useConfirm()
const { errors: serverErrors, capture, clear: clearServerError } = useFieldErrors()

const list = ref<Repo[]>([])
const projectList = ref<Project[]>([])
const loading = ref(true)
const formError = ref<string | null>(null)
const editing = ref<string | null>(null) // full_name being edited, or null
const formOpen = ref(false)

function emptyForm(): RepoForm {
  return { full_name: '', project_id: '', max_concurrent_runners: '', tags: {} }
}

const form = ref<RepoForm>(emptyForm())

// Org-scoped projects don't accept repo bindings -- the webhook routes
// by repository.owner.login instead. Filter them out of the picker so
// operators can't pick one and hit a 400 on submit.
const bindableProjects = computed(() =>
  projectList.value.filter((p) => (p.scope || 'repo') !== 'org'),
)

async function refresh() {
  loading.value = true
  try {
    const [rs, ps] = await Promise.all([repos.list(), projects.list()])
    list.value = (rs as Repo[]) || []
    projectList.value = (ps as Project[]) || []
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    loading.value = false
  }
}

function projectName(id: string): string {
  const p = projectList.value.find((p) => p.id === id)
  return p ? p.name : id
}

function openCreate() {
  if (bindableProjects.value.length === 0) return
  editing.value = null
  form.value = emptyForm()
  form.value.project_id = bindableProjects.value[0].id
  formError.value = null
  clearServerError()
  formOpen.value = true
}

function startEdit(r: Repo) {
  editing.value = r.full_name
  form.value = {
    full_name: r.full_name,
    project_id: r.project_id,
    max_concurrent_runners: r.max_concurrent_runners ?? '',
    tags: { ...(r.tags || {}) },
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

// numericLt0 detects a number-input that's been driven negative.
// The backend has no explicit rule for max_concurrent_runners on
// repos (the field is *int with omitempty) but the operator intent
// is unambiguous.
function numericLt0(v: unknown): boolean {
  if (v === '' || v == null) return false
  const n = Number(v)
  return Number.isFinite(n) && n < 0
}

// Live hints mirror domain/repo/endpoint.go::bindInput rules.
const liveHints = computed(() => {
  const f = form.value
  const h: Record<string, string> = {}
  if (f.full_name) {
    if (!isRepoFullName(f.full_name)) {
      h.full_name = 'Repository must be in "owner/name" form (e.g. octocat/hello-world)'
    } else if (f.full_name.length > FULL_NAME_MAX) {
      h.full_name = `Repository must be at most ${FULL_NAME_MAX} characters`
    }
  }
  if (numericLt0(f.max_concurrent_runners)) {
    h.max_concurrent_runners =
      'Max concurrent runners must be 0 or greater (leave blank to inherit the project cap)'
  }
  for (const k of Object.keys(f.tags || {})) {
    if (isReservedTagKey(k)) {
      h.tags = 'Tag keys starting with "gha:" are reserved; pick a different prefix'
      break
    }
  }
  return h
})

function hintFor(name: string): string {
  return liveHints.value[name] || serverErrors.value[name] || ''
}

async function bind() {
  formError.value = null
  clearServerError()
  if (Object.keys(liveHints.value).length > 0) {
    formError.value = 'Please fix the highlighted fields'
    return
  }
  const f = form.value
  const body: Record<string, unknown> = {
    full_name: f.full_name.trim(),
    project_id: f.project_id,
    tags: f.tags || {},
  }
  const cap = Number(f.max_concurrent_runners)
  if (cap > 0) body.max_concurrent_runners = cap
  try {
    await repos.bind(body)
    notify.success(editing.value ? `Updated ${body.full_name}` : `Bound ${body.full_name}`)
    cancelEdit()
    await refresh()
  } catch (e) {
    if (!capture(e)) formError.value = (e as Error).message
    else formError.value = 'Please fix the highlighted fields'
  }
}

async function unbind(r: Repo) {
  const ok = await confirm({
    title: 'Unbind repo?',
    message:
      `Remove the binding between ${r.full_name} and its project? Workflows from this repo ` +
      'will stop matching pacer pools until rebound.',
    confirmText: 'Unbind',
    variant: 'danger',
  })
  if (!ok) return
  try {
    await repos.unbind(r.full_name)
    notify.success(`Unbound ${r.full_name}`)
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  }
}

function tagsSummary(r: Repo): string {
  return Object.entries(r.tags || {})
    .map(([k, v]) => `${k}=${v}`)
    .join(', ')
}

onMounted(refresh)
</script>

<template>
  <PageHeader title="Repos">
    <button class="btn btn-primary" :disabled="bindableProjects.length === 0" @click="openCreate">
      + Bind repo
    </button>
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">Refresh</button>
  </PageHeader>

  <LoadingBlock v-if="loading && list.length === 0" />

  <EmptyState v-else-if="list.length === 0" title="No bindings yet">
    <template v-if="bindableProjects.length === 0">
      <p>
        A repo binds to a <strong>repo-scoped</strong> project so its workflow jobs can claim
        runners. Create one (or switch an existing project's scope from <em>org</em> to
        <em>repo</em>) under <RouterLink to="/projects">Projects</RouterLink>. Org-scoped projects
        route by <code>repository.owner.login</code> and don't need bindings.
      </p>
      <p class="mt-2">
        <RouterLink class="btn btn-primary" to="/projects">+ New project</RouterLink>
      </p>
    </template>
    <template v-else>
      <p>
        Bind a GitHub repo (<code>owner/name</code>) to one of your repo-scoped projects so its
        workflow jobs can claim runners.
      </p>
      <p class="mt-2">
        <button class="btn btn-primary" @click="openCreate">+ Bind repo</button>
      </p>
    </template>
  </EmptyState>

  <div v-else class="card">
    <div class="table-wrapper">
      <table>
        <thead>
          <tr>
            <th>Repository</th>
            <th>Project</th>
            <th>Cap override</th>
            <th>Tags</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in list" :key="r.full_name">
            <td class="code-font">{{ r.full_name }}</td>
            <td>{{ projectName(r.project_id) }}</td>
            <td>{{ r.max_concurrent_runners ?? '-' }}</td>
            <td class="code-font text-xs">
              <span v-if="tagsSummary(r)">{{ tagsSummary(r) }}</span>
              <span v-else class="text-muted">-</span>
            </td>
            <td>
              <div class="table-actions">
                <button class="btn btn-secondary btn-sm" @click="startEdit(r)">Edit</button>
                <button class="btn btn-danger btn-sm" @click="unbind(r)">Unbind</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <BaseModal
    v-if="formOpen"
    :title="editing ? 'Edit binding' : 'Bind repo to project'"
    size="modal-w640"
    form
    @close="cancelEdit"
    @submit="bind"
  >
    <div v-if="formError" class="alert alert-danger">{{ formError }}</div>

    <div class="form-row">
      <FormField label="Repository" hint="owner/name" :error="hintFor('full_name')" required>
        <input
          v-model="form.full_name"
          class="form-input code-font"
          :disabled="!!editing"
          placeholder="octocat/hello-world"
          required
          :maxlength="FULL_NAME_MAX"
          @input="clearServerError('full_name')"
        />
      </FormField>
      <FormField label="Project" :error="hintFor('project_id')">
        <select
          v-model="form.project_id"
          class="form-select"
          required
          @change="clearServerError('project_id')"
        >
          <option v-for="p in bindableProjects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </FormField>
    </div>

    <FormField
      label="Max concurrent runners"
      hint="Blank = inherit project cap."
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

    <FormField label="Tags" :error="hintFor('tags')">
      <TagsEditor v-model="form.tags" />
      <template #hint>
        Override pool + project tags on key conflict. Stamped on the spawned instance + EBS volumes
        only -- not on the pool's launch template, which is shared. <code>gha:</code> prefix
        reserved.
      </template>
    </FormField>

    <template #footer>
      <button class="btn btn-secondary" type="button" @click="cancelEdit">Cancel</button>
      <button class="btn btn-primary" type="submit">{{ editing ? 'Save' : 'Bind' }}</button>
    </template>
  </BaseModal>
</template>
