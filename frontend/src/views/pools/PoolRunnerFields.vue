<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The runner half: labels, tags, version pin, OS user, user-data tail.
import { POSIX_USER_PATTERN } from '@/lib/validators'
import { POOL_RUNNER_USER_MAX, RUNNER_VERSION_MAX } from './poolForm'
import { usePoolDraft } from './draft'
import FormField from '@/components/FormField.vue'
import TagsEditor from '@/components/TagsEditor.vue'

const { form, hintFor, clearError } = usePoolDraft()
</script>

<template>
  <FormField label="Extra runner labels" :error="hintFor('extra_labels')">
    <input
      v-model="form.extra_labels"
      class="form-input code-font"
      placeholder="gpu,arm64"
      @input="clearError('extra_labels')"
    />
    <template #hint>
      Comma-separated, appended to the auto-derived
      <code>self-hosted, &lt;project&gt;, &lt;pool&gt;, &lt;owner&gt;-&lt;repo&gt;</code>. Use for
      capability tags like <code>gpu</code>, <code>arm64</code>, <code>large</code>. Sanitized to
      GitHub's charset. The <code>gha:</code> prefix is reserved.
    </template>
  </FormField>

  <FormField label="Tags" :error="hintFor('tags')" :native="false">
    <TagsEditor v-model="form.tags" />
    <template #hint>
      Applied to the launch template, every spawned instance, and its EBS volumes. Pool tags
      override project tags, repo tags override pool tags. <code>gha:</code> prefix reserved.
    </template>
  </FormField>

  <div class="form-row">
    <FormField
      label="Runner version"
      hint="Actions/runner release. Leave blank for server-resolved latest, e.g. 2.319.1."
      :error="hintFor('runner_version')"
    >
      <input
        v-model="form.runner_version"
        class="form-input code-font"
        placeholder="(latest)"
        :maxlength="RUNNER_VERSION_MAX"
        @input="clearError('runner_version')"
      />
    </FormField>
    <FormField label="Run runner as" :error="hintFor('runner_user')">
      <input
        v-model="form.runner_user"
        class="form-input code-font"
        placeholder="(root)"
        :pattern="POSIX_USER_PATTERN"
        :maxlength="POOL_RUNNER_USER_MAX"
        @input="clearError('runner_user')"
      />
      <template #hint>
        OS user on the spawned instance. Leave blank to run as root with
        <code>RUNNER_ALLOW_RUNASROOT=1</code>; set to <code>admin</code> / <code>ec2-user</code> /
        <code>ubuntu</code> when the AMI installs CI tooling per-user.
      </template>
    </FormField>
  </div>

  <FormField
    label="Extra user-data"
    hint="Appended after the runner shutdown."
    :error="hintFor('user_data_extra')"
  >
    <textarea
      v-model="form.user_data_extra"
      class="form-textarea code-font"
      rows="5"
      @input="clearError('user_data_extra')"
    ></textarea>
  </FormField>
</template>
