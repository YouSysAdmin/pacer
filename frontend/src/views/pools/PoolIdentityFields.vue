<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Who the pool is: project, name, flags, priority. The parent owns
// the draft; this group mutates it in place (one reactive object, one
// owner, three renderers).
import { computed } from 'vue'
import { POOL_NAME_PATTERN, POOL_NAME_RE } from '@/lib/validators'
import { NAME_MAX } from './poolForm'
import { usePoolDraft } from './draft'
import FormField from '@/components/FormField.vue'

defineProps<{
  editing: boolean
  projects: Array<{ id: string; name: string }>
}>()

const { form, hintFor, clearError } = usePoolDraft()

// Live validity for the pool name. Empty -> valid (don't flash before
// the user types). Required catches it at submit.
const nameValid = computed(() => !form.name || POOL_NAME_RE.test(form.name.trim()))

const nameError = computed(() =>
  !nameValid.value
    ? 'lowercase alphanumeric / underscore / dash, no leading or trailing dash'
    : hintFor('name'),
)
</script>

<template>
  <div class="form-row">
    <FormField label="Project">
      <select v-model="form.project_id" class="form-select" :disabled="editing" required>
        <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </FormField>
    <FormField label="Pool name" :error="nameError" required>
      <input
        v-model="form.name"
        class="form-input"
        placeholder="large, medium, arm"
        :pattern="POOL_NAME_PATTERN"
        title="lowercase alphanumeric, underscore, or dash; not starting or ending with a dash"
        required
        :maxlength="NAME_MAX"
        @input="clearError('name')"
      />
      <template #hint>
        Used as a runner label - lowercase, digits, underscore, or dash, no leading / trailing dash.
        <template v-if="editing">
          Renaming changes the runner label this pool registers under - update every workflow's
          <code>runs-on:</code> in lock-step or jobs will stop matching this pool.
        </template>
      </template>
    </FormField>
  </div>

  <div class="form-row">
    <FormField
      hint="Pool stops claiming new jobs. Existing instances keep running until they finish or hit max runtime."
      :native="false"
    >
      <label class="checkbox-label">
        <input v-model="form.disabled" type="checkbox" />
        Disabled
      </label>
    </FormField>
    <FormField hint="Catches workflows that don't name a specific pool." :native="false">
      <label class="checkbox-label">
        <input v-model="form.is_default" type="checkbox" />
        This is the default pool
      </label>
    </FormField>
  </div>

  <FormField
    label="Priority"
    hint="Lower = preferred when multiple match."
    :error="hintFor('priority')"
  >
    <input
      v-model="form.priority"
      class="form-input w-filter"
      type="number"
      min="0"
      @input="clearError('priority')"
    />
  </FormField>
</template>
