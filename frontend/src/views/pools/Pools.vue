<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Pool list, filtered by project, with create / fork / edit / delete
// and the runs-on clipboard helper. The form itself lives in
// PoolFormModal - this page owns the list and which pool is open.
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { pools as poolsAPI, projects as projectsAPI } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useScopeStore } from '@/stores/scope'
import { useConfirm } from '@/composables/useConfirm'
import { emptyForm, formFrom, runsOnFor, type Pool, type PoolForm } from './poolForm'
import PageHeader from '@/components/PageHeader.vue'
import LoadingBlock from '@/components/LoadingBlock.vue'
import EmptyState from '@/components/EmptyState.vue'
import PoolFormModal from './PoolFormModal.vue'

interface Project {
  id: string
  name: string
}

const route = useRoute()
const notify = useNotificationStore()
const scope = useScopeStore()
const { confirm } = useConfirm()

const list = ref<Pool[]>([])
const projectList = ref<Project[]>([])
const loading = ref(true)

// Modal state: a seeded draft plus what the save should do with it.
const formOpen = ref(false)
const formInitial = ref<PoolForm>(emptyForm())
const editing = ref<string | null>(null)
const copyingFrom = ref('')

async function refresh() {
  loading.value = true
  try {
    const [ps, prjs] = await Promise.all([poolsAPI.list(), projectsAPI.list()])
    list.value = (ps as Pool[]) || []
    projectList.value = (prjs as Project[]) || []
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

const visible = computed(() =>
  scope.currentId ? list.value.filter((p) => p.project_id === scope.currentId) : list.value,
)

function openCreate() {
  if (projectList.value.length === 0) return
  editing.value = null
  copyingFrom.value = ''
  const f = emptyForm()
  // Pre-select the scoped project, so creating a pool while filtered
  // does not silently land it somewhere else.
  f.project_id = scope.currentId || projectList.value[0].id
  formInitial.value = f
  formOpen.value = true
}

// startCopy opens the create form pre-filled from a source pool. The
// backend treats it as a brand-new pool: fresh ID, fresh LT, audit row
// tagged pool.created.
function startCopy(p: Pool) {
  editing.value = null
  copyingFrom.value = p.name
  formInitial.value = formFrom(p, 'copy')
  formOpen.value = true
}

function startEdit(p: Pool) {
  editing.value = p.id
  copyingFrom.value = ''
  formInitial.value = formFrom(p, 'edit')
  formOpen.value = true
}

async function onSaved() {
  formOpen.value = false
  await refresh()
}

async function copyRunsOn(p: Pool) {
  const s = runsOnFor(p, projectName(p.project_id))
  try {
    await navigator.clipboard.writeText(s)
    notify.success(`Copied runs-on: ${s}`)
  } catch {
    // A refused clipboard (insecure origin, missing permission) says
    // so, with the value in the message so it can be copied by hand.
    notify.error(`Clipboard write failed; copy manually: ${s}`)
  }
}

async function remove(p: Pool) {
  const ok = await confirm({
    title: 'Delete pool?',
    message:
      `Permanently delete pool "${p.name}" from ${projectName(p.project_id)}? Active jobs ` +
      'must already be drained; the EC2 launch template will be best-effort cleaned up.',
    confirmText: 'Delete',
    variant: 'danger',
  })
  if (!ok) return
  try {
    await poolsAPI.delete(p.id)
    notify.success(`Deleted ${p.name}`)
    await refresh()
  } catch (e) {
    notify.error((e as Error).message)
  }
}

onMounted(() => {
  // ?project=<id> from a /projects link sets the console-wide scope
  // rather than a filter local to this page: the operator followed a
  // link about one project, and the rail should agree with the table
  // they land on.
  const q = route.query.project
  if (typeof q === 'string' && q) scope.set(q)
  void refresh()
})
</script>

<template>
  <!-- No project picker here: the rail's scope selector owns that
       choice for every page, and a second control would let the two
       disagree about what the table is showing. -->
  <PageHeader title="Pools">
    <button class="btn btn-primary" :disabled="projectList.length === 0" @click="openCreate">
      + New pool
    </button>
    <button class="btn btn-secondary" :disabled="loading" @click="refresh">Refresh</button>
  </PageHeader>

  <LoadingBlock v-if="loading && list.length === 0" />

  <EmptyState v-else-if="list.length === 0" title="No pools yet">
    <template v-if="projectList.length === 0">
      <p>
        Pools own the EC2 launch shape (AMI, instance types, subnets, IAM profile). You need a
        <RouterLink to="/projects">project</RouterLink> before you can create one.
      </p>
      <p class="mt-2">
        <RouterLink class="btn btn-primary" to="/projects">+ New project</RouterLink>
      </p>
    </template>
    <template v-else>
      <p>
        Pools own the EC2 launch shape (AMI, instance types, subnets, IAM profile). Each project
        needs at least one to spawn runners.
      </p>
      <p class="mt-2">
        <button class="btn btn-primary" @click="openCreate">+ New pool</button>
      </p>
    </template>
  </EmptyState>

  <EmptyState v-else-if="visible.length === 0" title="No pools in this project">
    <p>
      Nothing in <strong>{{ scope.label }}</strong
      >. Clear the project filter to see every pool.
    </p>
    <p class="mt-2 flex gap-2 justify-center">
      <button class="btn btn-secondary" @click="scope.set(null)">Show all projects</button>
      <button class="btn btn-primary" @click="openCreate">+ New pool</button>
    </p>
  </EmptyState>

  <div v-else class="card">
    <div class="table-wrapper">
      <table>
        <thead>
          <tr>
            <th>Project</th>
            <th>Pool</th>
            <th>AMI</th>
            <th>Instance types</th>
            <th>Cap</th>
            <th>LT</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in visible" :key="p.id">
            <td>{{ projectName(p.project_id) }}</td>
            <td>
              <span class="cell-title">{{ p.name }}</span>
              <span v-if="p.is_default" class="badge badge-info">default</span>
              <span v-if="p.disabled" class="badge badge-warning">disabled</span>
            </td>
            <td class="code-font">{{ p.ami_id }}</td>
            <td class="code-font">{{ (p.instance_types || []).join(', ') }}</td>
            <td>{{ p.max_concurrent_runners }}</td>
            <td class="code-font">
              <template v-if="p.launch_template_id">
                {{ p.launch_template_id }}
                <span class="text-muted">v{{ p.launch_template_version }}</span>
              </template>
              <span v-else class="text-muted">-</span>
            </td>
            <td>
              <div class="table-actions">
                <button
                  class="btn btn-secondary btn-sm"
                  title="Copy runs-on labels for a workflow YAML"
                  @click="copyRunsOn(p)"
                >
                  runs-on
                </button>
                <button
                  class="btn btn-secondary btn-sm"
                  title="Open the new-pool form pre-filled from this pool"
                  @click="startCopy(p)"
                >
                  Copy
                </button>
                <button class="btn btn-secondary btn-sm" @click="startEdit(p)">Edit</button>
                <button class="btn btn-danger btn-sm" @click="remove(p)">Delete</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <PoolFormModal
    v-if="formOpen"
    :initial="formInitial"
    :editing="editing"
    :copying-from="copyingFrom"
    :projects="projectList"
    @close="formOpen = false"
    @saved="onSaved"
  />
</template>
