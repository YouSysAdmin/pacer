<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// List editor for prefixed AWS IDs (subnet-..., sg-..., etc.). Parent
// binds a string array plus a prefix. One input per entry with a
// remove button and a "+ add" at the bottom. Validation is soft -
// the regex feeds the browser's HTML5 `pattern` check (skipped on
// empty inputs) and a visual warning. Empty rows are filtered when we
// serialize back to the parent, so the user can transiently leave a
// blank row open without breaking the form.
import { computed, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    prefix?: string
    placeholder?: string
    addLabel?: string
  }>(),
  { prefix: '', placeholder: '', addLabel: '+ add' },
)

const model = defineModel<string[]>({ default: () => [] })

interface Row {
  v: string
}

function rowsFrom(arr: string[] | undefined): Row[] {
  return (arr || []).map((v) => ({ v }))
}

const rows = ref<Row[]>(rowsFrom(model.value))
let lastSerialized = JSON.stringify(model.value || [])

// Re-seed when the parent replaces value wholesale (cancel/edit).
watch(model, (incoming) => {
  const s = JSON.stringify(incoming || [])
  if (s !== lastSerialized) {
    rows.value = rowsFrom(incoming)
    lastSerialized = s
  }
})

function serialize() {
  const out = rows.value.map((r) => (r.v || '').trim()).filter(Boolean)
  lastSerialized = JSON.stringify(out)
  model.value = out
}

function add() {
  rows.value = [...rows.value, { v: '' }]
  serialize()
}

function remove(i: number) {
  rows.value = rows.value.filter((_, idx) => idx !== i)
  serialize()
}

// 8-17 lowercase hex chars after the prefix matches both legacy
// (8-char) and modern (17-char) AWS resource IDs. Empty prefix
// disables validation entirely.
const pattern = computed(() => (props.prefix ? props.prefix + '[a-f0-9]{8,17}' : undefined))

function isValid(v: string): boolean {
  if (!v || !pattern.value) return true
  return new RegExp('^' + pattern.value + '$').test(v.trim())
}
</script>

<template>
  <div class="id-list">
    <div v-if="rows.length === 0" class="id-empty text-muted">
      No entries. Click <strong>{{ addLabel }}</strong> to add one.
    </div>
    <template v-else>
      <div v-for="(row, i) in rows" :key="i" class="id-row">
        <input
          v-model="row.v"
          class="form-input code-font"
          :placeholder="placeholder"
          :pattern="pattern"
          :aria-invalid="!isValid(row.v)"
          :aria-label="placeholder || 'ID'"
          :title="prefix ? `expected ${prefix}<8-17 hex>` : ''"
          @input="serialize"
        />
        <button
          type="button"
          class="btn btn-danger btn-sm"
          aria-label="Remove entry"
          title="Remove"
          @click="remove(i)"
        >
          Remove
        </button>
        <span v-if="!isValid(row.v)" class="id-warn">expected {{ prefix }}&lt;8-17 hex&gt;</span>
      </div>
    </template>
    <div>
      <button type="button" class="btn btn-secondary btn-sm" @click="add">{{ addLabel }}</button>
    </div>
  </div>
</template>

<style scoped>
.id-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.id-row {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: center;
  gap: 8px;
}

.id-empty {
  padding: 4px 0;
  font-size: 13px;
}

.id-warn {
  grid-column: 1 / -1;
  color: var(--danger-fg);
  font-family: var(--font-mono);
  font-size: 12px;
}
</style>
