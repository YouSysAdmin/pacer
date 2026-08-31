<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The EC2 shape: AMI, instance types, network, volume, caps, and the
// spawn method / market / allocation-strategy pickers.
import { computed } from 'vue'
import { AMI_PATTERN, AMI_RE } from '@/lib/validators'
import { ALLOC_HELP, ALLOC_STRATEGIES, IAM_PROFILE_MAX } from './poolForm'
import { usePoolDraft } from './draft'
import FormField from '@/components/FormField.vue'
import IdListEditor from '@/components/IdListEditor.vue'

const { form, hintFor, clearError } = usePoolDraft()

// Live validity flag for the AMI input. Empty reports valid so the
// warning doesn't flash before the user types anything -- `required`
// still enforces non-empty at submit.
const amiValid = computed(() => !form.ami_id || AMI_RE.test(form.ami_id.trim()))

const amiError = computed(() =>
  !amiValid.value ? 'expected ami-xxxxxxxx (8-17 hex chars)' : hintFor('ami_id'),
)

// The two card strips are radios in everything but markup, so each
// card answers click, Space and Enter through here rather than
// repeating the assignment six times in the template.
function pickMethod(value: string) {
  form.spawn_method = value
}

function pickMarket(spot: boolean) {
  form.spot = spot
}
</script>

<template>
  <FormField label="AMI ID" hint="ami- + 8-17 hex chars." :error="amiError" required>
    <input
      v-model="form.ami_id"
      class="form-input code-font"
      :pattern="AMI_PATTERN"
      placeholder="ami-0abcdef0123456789"
      title="AMI ID must match ami- followed by 8-17 hex characters"
      required
      @input="clearError('ami_id')"
    />
  </FormField>

  <FormField
    label="Instance types"
    hint="Comma-separated, priority order for spot fallback."
    :error="hintFor('instance_types')"
    required
  >
    <input
      v-model="form.instance_types"
      class="form-input code-font"
      required
      @input="clearError('instance_types')"
    />
  </FormField>

  <div class="form-row">
    <FormField
      label="Subnet IDs"
      hint="subnet- + 8-17 hex chars."
      :error="hintFor('subnet_ids')"
      :native="false"
    >
      <IdListEditor
        v-model="form.subnet_ids"
        prefix="subnet-"
        placeholder="subnet-0abcdef0123456789"
        add-label="+ add subnet"
      />
    </FormField>
    <FormField
      label="Security group IDs"
      hint="sg- + 8-17 hex chars."
      :error="hintFor('security_group_ids')"
      :native="false"
    >
      <IdListEditor
        v-model="form.security_group_ids"
        prefix="sg-"
        placeholder="sg-0abcdef0123456789"
        add-label="+ add security group"
      />
    </FormField>
  </div>

  <FormField
    label="IAM instance profile name"
    hint="Optional, leave blank if the runner host doesn't need AWS API access."
    :error="hintFor('iam_instance_profile')"
  >
    <input
      v-model="form.iam_instance_profile"
      class="form-input"
      placeholder="(none)"
      :maxlength="IAM_PROFILE_MAX"
      @input="clearError('iam_instance_profile')"
    />
  </FormField>

  <div class="form-row">
    <FormField
      label="Root volume GB"
      hint="0 = inherit the AMI's native size; any positive value must be >= AMI size."
      :error="hintFor('root_volume_gb')"
    >
      <input
        v-model="form.root_volume_gb"
        class="form-input"
        type="number"
        min="0"
        placeholder="0"
        @input="clearError('root_volume_gb')"
      />
    </FormField>
    <FormField
      label="Max runtime (min)"
      hint="Maximum time an instance can run before it is forced to terminate."
      :error="hintFor('max_runtime_minutes')"
    >
      <input
        v-model="form.max_runtime_minutes"
        class="form-input"
        type="number"
        min="0"
        @input="clearError('max_runtime_minutes')"
      />
    </FormField>
  </div>

  <FormField label="Max concurrent runners" :error="hintFor('max_concurrent_runners')">
    <input
      v-model="form.max_concurrent_runners"
      class="form-input w-filter"
      type="number"
      min="0"
      @input="clearError('max_concurrent_runners')"
    />
  </FormField>

  <FormField label="Spawn method" :native="false">
    <div class="method-toggle">
      <div
        class="method-card"
        :class="{ sel: form.spawn_method === 'fleet' }"
        role="radio"
        tabindex="0"
        :aria-checked="form.spawn_method === 'fleet'"
        @click="pickMethod('fleet')"
        @keydown.space.prevent="pickMethod('fleet')"
        @keydown.enter.prevent="pickMethod('fleet')"
      >
        <div class="n">Fleet</div>
        <div class="d">
          CreateFleet, multi-type + multi-AZ. AWS picks an available (instance_type x subnet) combo
          using your allocation strategy.
        </div>
        <div class="rec">RECOMMENDED</div>
      </div>
      <div
        class="method-card"
        :class="{ sel: form.spawn_method === 'run_instances' }"
        role="radio"
        tabindex="0"
        :aria-checked="form.spawn_method === 'run_instances'"
        @click="pickMethod('run_instances')"
        @keydown.space.prevent="pickMethod('run_instances')"
        @keydown.enter.prevent="pickMethod('run_instances')"
      >
        <div class="n">RunInstances</div>
        <div class="d">
          Serial loop, single instance type per call, first subnet only. Legacy path kept for parity
          with older deployments.
        </div>
      </div>
    </div>
  </FormField>

  <FormField label="Market" :native="false">
    <div class="method-toggle">
      <div
        class="method-card"
        :class="{ sel: form.spot }"
        role="radio"
        tabindex="0"
        :aria-checked="form.spot"
        @click="pickMarket(true)"
        @keydown.space.prevent="pickMarket(true)"
        @keydown.enter.prevent="pickMarket(true)"
      >
        <div class="n">Spot</div>
        <div class="d">
          Cheaper, interruptible. AWS guarantees price will not exceed on-demand. Right for
          ephemeral CI runners.
        </div>
        <div class="rec">RECOMMENDED</div>
      </div>
      <div
        class="method-card"
        :class="{ sel: !form.spot }"
        role="radio"
        tabindex="0"
        :aria-checked="!form.spot"
        @click="pickMarket(false)"
        @keydown.space.prevent="pickMarket(false)"
        @keydown.enter.prevent="pickMarket(false)"
      >
        <div class="n">On-demand</div>
        <div class="d">
          Stable, full price. Pick when interruption mid-job would break the workflow.
        </div>
      </div>
    </div>
  </FormField>

  <FormField v-if="form.spawn_method === 'fleet'" label="Allocation strategy" :native="false">
    <select v-model="form.allocation_strategy" class="form-select">
      <option v-for="s in ALLOC_STRATEGIES" :key="s.value" :value="s.value">{{ s.label }}</option>
    </select>
    <template #hint>
      {{ ALLOC_HELP[form.allocation_strategy]?.summary }}
      On-demand: <code>{{ ALLOC_HELP[form.allocation_strategy]?.onDemand }}</code
      >. Spot: <code>{{ ALLOC_HELP[form.allocation_strategy]?.spot }}</code
      >.
    </template>
  </FormField>
</template>

<style scoped>
/* The two-card radio strip for spawn method / market. */
.method-toggle {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.method-card {
  position: relative;
  padding: 12px 14px;
  border: 1px solid var(--border-primary);
  border-radius: var(--radius-md);
  background: var(--bg-secondary);
  cursor: pointer;
  transition: var(--transition);
}

.method-card:hover {
  border-color: var(--border-strong);
}

.method-card.sel {
  border-color: var(--border-focus);
  background: var(--bg-active);
}

.method-card .n {
  font-weight: 600;
  font-size: 14px;
  color: var(--text-primary);
  margin-bottom: 4px;
}

.method-card .d {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-secondary);
}

.method-card .rec {
  margin-top: 8px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: var(--success-fg);
}

@media (max-width: 700px) {
  .method-toggle {
    grid-template-columns: 1fr;
  }
}
</style>
