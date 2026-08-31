<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The create / edit / fork dialog. It owns ONE draft object, passes it
// down to the three field groups, and is the only place that talks to
// the API - so "what the form holds" and "what gets sent" cannot
// drift between the groups.
import { computed, reactive, ref, watch } from 'vue'
import { pools as poolsAPI } from '@/api'
import { useNotificationStore } from '@/stores/notification'
import { useFieldErrors } from '@/composables/fieldErrors'
import { buildBody, buildHints, validate, type PoolForm } from './poolForm'
import { providePoolDraft } from './draft'
import BaseModal from '@/components/BaseModal.vue'
import PoolIdentityFields from './PoolIdentityFields.vue'
import PoolPlacementFields from './PoolPlacementFields.vue'
import PoolRunnerFields from './PoolRunnerFields.vue'

const props = defineProps<{
  // The seeded draft. The modal copies it, so a cancelled edit leaves
  // the caller's object untouched.
  initial: PoolForm
  // Pool id when editing; null for create and for a fork.
  editing: string | null
  // Source pool name when forking; '' otherwise. Title copy only.
  copyingFrom: string
  projects: Array<{ id: string; name: string }>
}>()

const emit = defineEmits<{ (e: 'close'): void; (e: 'saved'): void }>()

const notify = useNotificationStore()
const { errors: serverErrors, capture, clear: clearServerError } = useFieldErrors()

// reactive() rather than ref(): the field groups mutate `form.x`
// directly, and a plain object keeps that a normal property write in
// three child components instead of .value bookkeeping.
const form = reactive<PoolForm>({ ...props.initial })
const formError = ref<string | null>(null)
const saving = ref(false)

// Re-seed when the caller opens the dialog on a different pool
// without unmounting it.
watch(
  () => props.initial,
  (next) => Object.assign(form, next),
)

const liveHints = computed(() => buildHints(form))

// The live hint wins while typing; the server's message covers rules
// the client doesn't know (uniqueness, AMI existence, anything the
// EC2 API refuses).
function hintFor(name: string): string {
  return liveHints.value[name] || serverErrors.value[name] || ''
}

// The field groups read the draft from here rather than through
// props - see draft.ts for why.
providePoolDraft({ form, hintFor, clearError: clearServerError })

const title = computed(() => {
  if (props.editing) return 'Edit pool'
  return props.copyingFrom ? `New pool (copied from ${props.copyingFrom})` : 'New pool'
})

async function submit() {
  formError.value = null
  clearServerError()
  // Live hints cover the per-field rules. Block submit if any are
  // flagged so the operator sees the inline message rather than a
  // server 400.
  if (Object.keys(liveHints.value).length > 0) {
    formError.value = 'Please fix the highlighted fields'
    return
  }
  const body = buildBody(form)
  const v = validate(body)
  if (v) {
    formError.value = v
    return
  }
  saving.value = true
  try {
    if (props.editing) {
      await poolsAPI.update(props.editing, body)
      notify.success(`Updated ${body.name}`)
    } else {
      await poolsAPI.create(form.project_id, body)
      notify.success(`Created ${body.name}`)
    }
    emit('saved')
  } catch (e) {
    if (!capture(e)) formError.value = (e as Error).message
    else formError.value = 'Please fix the highlighted fields'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseModal :title="title" size="modal-w800" form @close="emit('close')" @submit="submit">
    <div v-if="formError" class="alert alert-danger">{{ formError }}</div>

    <PoolIdentityFields :editing="!!editing" :projects="projects" />
    <PoolPlacementFields />
    <PoolRunnerFields />

    <template #footer>
      <button class="btn btn-secondary" type="button" @click="emit('close')">Cancel</button>
      <button class="btn btn-primary" type="submit" :disabled="saving">
        {{ saving ? 'Saving...' : editing ? 'Save' : 'Create' }}
      </button>
    </template>
  </BaseModal>
</template>
