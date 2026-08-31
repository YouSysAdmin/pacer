<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Export the config surface as JSON; import it back with per-section
// upsert counts.
import { ref } from 'vue'
import { backup } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import PageHeader from '@/components/PageHeader.vue'
import FormField from '@/components/FormField.vue'
import Notice from '@/components/Notice.vue'

interface SectionCounts {
  created: number
  updated: number
}

interface ImportResult {
  projects: SectionCounts
  pools: SectionCounts
  repos: SectionCounts
  errors?: string[]
}

const notify = useNotificationStore()

// Export pulls the JSON down through exportRaw() rather than the
// call() wrapper so the Content-Disposition filename the backend
// stamps survives. URL.createObjectURL + a synthetic <a> click is the
// standard browser-side download trick.
const exporting = ref(false)

async function doExport() {
  exporting.value = true
  try {
    const res = await backup.exportRaw()
    if (!res.ok) {
      const text = await res.text()
      let msg = `HTTP ${res.status}`
      try {
        const j = JSON.parse(text)
        if (j.error) msg = j.error
      } catch {
        // Non-JSON error body, keep the HTTP status as the message.
      }
      throw new Error(msg)
    }
    const blob = await res.blob()
    const cd = res.headers.get('content-disposition') || ''
    const m = cd.match(/filename="([^"]+)"/)
    const filename = m ? m[1] : `pacer-backup-${new Date().toISOString().slice(0, 10)}.json`
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    exporting.value = false
  }
}

// Import accepts either a file upload or pasted JSON. File takes
// precedence when both are populated. Submission shows the per-
// section counts the backend returns plus any per-row errors.
const importFile = ref<File | null>(null)
const importText = ref('')
const importing = ref(false)
const importResult = ref<ImportResult | null>(null)

function pickFile(ev: Event) {
  importFile.value = (ev.target as HTMLInputElement).files?.[0] || null
}

async function doImport() {
  importing.value = true
  importResult.value = null
  try {
    let raw: string
    if (importFile.value) {
      raw = await importFile.value.text()
    } else if (importText.value.trim()) {
      raw = importText.value
    } else {
      throw new Error('Provide a file or paste JSON below')
    }
    let snap: unknown
    try {
      snap = JSON.parse(raw)
    } catch (e) {
      throw new Error('Not valid JSON: ' + (e as Error).message)
    }
    importResult.value = (await backup.import(snap)) as ImportResult
  } catch (e) {
    notify.error((e as Error).message)
  } finally {
    importing.value = false
  }
}

function clearImport() {
  importFile.value = null
  importText.value = ''
  importResult.value = null
  const input = document.getElementById('backup-file') as HTMLInputElement | null
  if (input) input.value = ''
}
</script>

<template>
  <PageHeader title="Backup" />

  <div class="card">
    <div class="card-header"><h2>Export</h2></div>
    <div class="card-body">
      <p class="auth-note">
        Downloads every project, pool, and repo binding as a single JSON document. Operational data
        (jobs, instances, audit log, users, secrets) is intentionally excluded.
      </p>
      <button class="btn btn-primary" :disabled="exporting" @click="doExport">
        {{ exporting ? 'Preparing...' : 'Download backup' }}
      </button>
    </div>
  </div>

  <div class="card">
    <div class="card-header"><h2>Import</h2></div>
    <div class="card-body">
      <p class="auth-note">
        Upserts by stable name: projects by name, pools by <code>(project, pool)</code>, repos by
        <code>full_name</code>. Existing rows are updated in place. New rows are created. Pool
        imports re-materialize the EC2 launch template.
      </p>

      <FormField label="Backup file" for="backup-file">
        <input
          id="backup-file"
          class="form-input"
          type="file"
          accept="application/json,.json"
          @change="pickFile"
        />
      </FormField>
      <FormField label="...or paste JSON" for="backup-text">
        <textarea
          id="backup-text"
          v-model="importText"
          class="form-textarea code-font"
          rows="8"
          placeholder="paste exported JSON here"
        ></textarea>
      </FormField>

      <div class="flex gap-2">
        <button class="btn btn-primary" :disabled="importing" @click="doImport">
          {{ importing ? 'Importing...' : 'Import' }}
        </button>
        <button class="btn btn-secondary" :disabled="importing" @click="clearImport">Clear</button>
      </div>

      <Notice v-if="importResult" kind="success" class="mt-3">
        Import complete: projects {{ importResult.projects.created }} created /
        {{ importResult.projects.updated }} updated, pools {{ importResult.pools.created }} created
        / {{ importResult.pools.updated }} updated, repos {{ importResult.repos.created }} created /
        {{ importResult.repos.updated }} updated.
      </Notice>
      <Notice
        v-if="importResult?.errors && importResult.errors.length > 0"
        kind="warning"
        class="mt-2"
        :title="`${importResult.errors.length} row ${importResult.errors.length === 1 ? 'error' : 'errors'}`"
      >
        <ul class="err-list">
          <li v-for="(e, i) in importResult.errors" :key="i">{{ e }}</li>
        </ul>
      </Notice>
    </div>
  </div>
</template>

<style scoped>
.err-list {
  margin: 6px 0 0;
  padding-left: 18px;
}

.err-list li {
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.5;
}
</style>
