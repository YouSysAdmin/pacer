<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Two-way bound tag map editor. Renders one row per entry with
// (key, value, remove) controls plus an "+ add tag" button.
// Parent binds an object:
//   <TagsEditor v-model="form.tags" reserved-prefix="gha:" />
// Internally we keep an array (rows) so duplicate / blank keys can
// exist while typing. serialize() rebuilds the object on every change.
import { ref, watch } from 'vue'

const props = withDefaults(defineProps<{ reservedPrefix?: string }>(), {
  reservedPrefix: 'gha:',
})

const model = defineModel<Record<string, string>>({ default: () => ({}) })

interface Row {
  k: string
  v: string
}

function rowsFrom(obj: Record<string, string> | undefined): Row[] {
  return Object.entries(obj || {}).map(([k, v]) => ({ k, v }))
}

const rows = ref<Row[]>(rowsFrom(model.value))
let lastSerialized = JSON.stringify(model.value || {})

// Re-seed if the parent replaces the value wholesale (e.g. loading an
// existing record into the form). Comparing against the last value we
// serialized keeps our own edits from triggering a reseed.
watch(model, (incoming) => {
  const s = JSON.stringify(incoming || {})
  if (s !== lastSerialized) {
    rows.value = rowsFrom(incoming)
    lastSerialized = s
  }
})

function serialize() {
  const out: Record<string, string> = {}
  for (const r of rows.value) {
    const k = (r.k || '').trim()
    if (!k) continue
    out[k] = (r.v || '').trim()
  }
  lastSerialized = JSON.stringify(out)
  model.value = out
}

function add() {
  rows.value = [...rows.value, { k: '', v: '' }]
  serialize()
}

function remove(i: number) {
  rows.value = rows.value.filter((_, idx) => idx !== i)
  serialize()
}

function isReserved(key: string): boolean {
  if (!props.reservedPrefix) return false
  return (key || '').toLowerCase().startsWith(props.reservedPrefix.toLowerCase())
}
</script>

<template>
  <div class="tags-editor">
    <div v-if="rows.length === 0" class="tags-empty text-muted">
      No tags. Click <strong>+ add tag</strong> to add one.
    </div>
    <template v-else>
      <div v-for="(row, i) in rows" :key="i" class="tag-row">
        <input
          v-model="row.k"
          class="form-input code-font"
          placeholder="key"
          aria-label="Tag key"
          @input="serialize"
        />
        <span class="tag-eq">=</span>
        <input
          v-model="row.v"
          class="form-input code-font"
          placeholder="value"
          aria-label="Tag value"
          @input="serialize"
        />
        <button
          type="button"
          class="btn btn-danger btn-sm"
          aria-label="Remove tag"
          title="Remove"
          @click="remove(i)"
        >
          Remove
        </button>
        <span v-if="isReserved(row.k)" class="tag-warn">reserved prefix</span>
      </div>
    </template>
    <div>
      <button type="button" class="btn btn-secondary btn-sm" @click="add">+ add tag</button>
    </div>
  </div>
</template>

<style scoped>
.tags-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tag-row {
  display: grid;
  grid-template-columns: 1fr auto 1fr auto;
  align-items: center;
  gap: 8px;
}

.tag-eq {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 13px;
}

.tags-empty {
  padding: 4px 0;
  font-size: 13px;
}

.tag-warn {
  grid-column: 1 / -1;
  color: var(--danger-fg);
  font-family: var(--font-mono);
  font-size: 12px;
}

@media (max-width: 700px) {
  .tag-row {
    grid-template-columns: 1fr auto 1fr;
  }

  .tag-row .btn {
    grid-column: 1 / -1;
    justify-self: end;
  }
}
</style>
