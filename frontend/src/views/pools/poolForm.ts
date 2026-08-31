// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The pool form's pure half: constants, the draft shape, live hints,
// body building, and submit-time validation. Framework-free so it can
// be unit-tested without a DOM.

import {
  AMI_PATTERN,
  AMI_RE,
  POOL_NAME_RE,
  SG_PATTERN,
  SG_RE,
  SUBNET_PATTERN,
  SUBNET_RE,
  isPosixUser,
  isReservedTagKey,
  notSelfHosted,
  sanitizeLabel,
} from '@/lib/validators'

// Allocation strategies are labelled with the AWS strategy names the
// Fleet request actually sends. The backend enum value stays the
// short form (cost / lowest_price / capacity / priority).
export const ALLOC_STRATEGIES = [
  { value: 'cost', label: 'price-capacity-optimized (default)' },
  { value: 'lowest_price', label: 'lowest-price' },
  { value: 'capacity', label: 'capacity-optimized' },
  { value: 'priority', label: 'prioritized' },
]

export const ALLOC_HELP: Record<string, { summary: string; onDemand: string; spot: string }> = {
  cost: {
    summary:
      'Cheapest with a capacity signal. AWS skips shallow spot pools even when they are momentarily cheaper, so interruptions stay rare. instance_types order is ignored.',
    onDemand: 'lowest-price',
    spot: 'price-capacity-optimized',
  },
  lowest_price: {
    summary:
      'Pure cheapest, no capacity signal. Picks the instantaneously cheapest spot pool even when it is shallow and likely to be interrupted. Use only when cost trumps reliability.',
    onDemand: 'lowest-price',
    spot: 'lowest-price',
  },
  capacity: {
    summary:
      'Deepest spot pool regardless of price. On-demand has no capacity concept and falls back to lowest price.',
    onDemand: 'lowest-price',
    spot: 'capacity-optimized',
  },
  priority: {
    summary:
      'Honors the instance_types list order. For spot, capacity is still primary and the list order breaks ties.',
    onDemand: 'prioritized',
    spot: 'capacity-optimized-prioritized',
  },
}

// Caps mirror domain/pool/endpoint.go::input.
export const NAME_MAX = 128
export const POOL_RUNNER_USER_MAX = 32
export const RUNNER_VERSION_MAX = 32
export const EXTRA_LABEL_MAX = 64
export const USER_DATA_MAX = 32768
export const IAM_PROFILE_MAX = 128
export const INSTANCE_TYPE_MAX = 64
// Slice caps -- entries, not characters. Backend rule is
// `min=1,max=32` on instance_types / subnet_ids / security_group_ids.
export const SLICE_MAX = 32

// The pool row as the API returns it (the fields the form consumes).
export interface Pool {
  id: string
  project_id: string
  name: string
  is_default: boolean
  priority: number
  ami_id: string
  instance_types?: string[]
  subnet_ids?: string[]
  security_group_ids?: string[]
  iam_instance_profile: string
  root_volume_gb: number
  max_runtime_minutes: number
  max_concurrent_runners: number
  spot: boolean
  spawn_method?: string
  allocation_strategy?: string
  extra_labels?: string[]
  tags?: Record<string, string>
  runner_version?: string
  runner_user?: string
  user_data_extra?: string
  disabled: boolean
  launch_template_id?: string
  launch_template_version?: number
}

// The editable draft. Lists the operator types as comma strings stay
// strings until buildBody().
export interface PoolForm {
  project_id: string
  name: string
  is_default: boolean
  priority: number
  ami_id: string
  instance_types: string
  subnet_ids: string[]
  security_group_ids: string[]
  iam_instance_profile: string
  root_volume_gb: number
  max_runtime_minutes: number
  max_concurrent_runners: number
  spot: boolean
  spawn_method: string
  allocation_strategy: string
  extra_labels: string
  tags: Record<string, string>
  runner_version: string
  runner_user: string
  user_data_extra: string
  disabled: boolean
}

export function emptyForm(): PoolForm {
  return {
    project_id: '',
    name: '',
    is_default: false,
    priority: 100,
    ami_id: '',
    instance_types: 'm6i.large,m5.large',
    subnet_ids: [],
    security_group_ids: [],
    iam_instance_profile: '',
    root_volume_gb: 0,
    max_runtime_minutes: 60,
    max_concurrent_runners: 5,
    spot: true,
    spawn_method: 'fleet',
    allocation_strategy: 'cost',
    extra_labels: '',
    tags: {},
    runner_version: '',
    runner_user: '',
    user_data_extra: '',
    disabled: false,
  }
}

// formFrom seeds a draft from an existing pool. `mode: 'copy'` clears
// identity bits (name) and the default flag -- clearing default avoids
// the partial-unique-index conflict when an operator hits "save"
// without reading the form.
export function formFrom(p: Pool, mode: 'edit' | 'copy'): PoolForm {
  return {
    project_id: p.project_id,
    name: mode === 'copy' ? '' : p.name,
    is_default: mode === 'copy' ? false : p.is_default,
    priority: p.priority,
    ami_id: p.ami_id,
    instance_types: (p.instance_types || []).join(','),
    subnet_ids: [...(p.subnet_ids || [])],
    security_group_ids: [...(p.security_group_ids || [])],
    iam_instance_profile: p.iam_instance_profile,
    root_volume_gb: p.root_volume_gb,
    max_runtime_minutes: p.max_runtime_minutes,
    max_concurrent_runners: p.max_concurrent_runners,
    spot: p.spot,
    spawn_method: p.spawn_method || 'fleet',
    allocation_strategy: p.allocation_strategy || 'cost',
    extra_labels: (p.extra_labels || []).join(','),
    tags: { ...(p.tags || {}) },
    runner_version: p.runner_version || '',
    runner_user: p.runner_user || '',
    user_data_extra: p.user_data_extra || '',
    disabled: p.disabled,
  }
}

// parseListPreview splits a comma-separated input the same way
// buildBody() does, so live validation sees the same entries the
// backend will. Whitespace-only entries collapse.
export function parseListPreview(s?: string): string[] {
  return (s || '')
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)
}

// numericLt0 detects a number-input that's been driven negative. The
// backend's Normalize() silently clamps negatives, so without a UI
// hint the value the user set would be rewritten without comment.
function numericLt0(v: unknown): boolean {
  const n = Number(v)
  return Number.isFinite(n) && n < 0
}

// buildHints mirrors domain/pool/endpoint.go::input rules. The map is
// keyed by json field name so server-side err.fields overlays cleanly.
// Messages are written for the operator -- they reference the field's
// label as it appears in the form, not the json tag.
export function buildHints(form: PoolForm): Record<string, string> {
  const h: Record<string, string> = {}
  if (form.name && form.name.length > NAME_MAX) {
    h.name = `Pool name must be at most ${NAME_MAX} characters`
  }
  if (form.runner_user && !isPosixUser(form.runner_user)) {
    h.runner_user =
      'Run runner as must use only lowercase letters, digits, underscore, or dash, and not start with a digit or dash'
  }
  if (form.runner_user && form.runner_user.length > POOL_RUNNER_USER_MAX) {
    h.runner_user = `Run runner as must be at most ${POOL_RUNNER_USER_MAX} characters`
  }
  if (form.runner_version && form.runner_version.length > RUNNER_VERSION_MAX) {
    h.runner_version = `Runner version must be at most ${RUNNER_VERSION_MAX} characters`
  }
  if (form.user_data_extra && form.user_data_extra.length > USER_DATA_MAX) {
    h.user_data_extra = `Extra user-data is too large (${form.user_data_extra.length} characters; limit is ${(USER_DATA_MAX / 1024).toFixed(0)} KiB)`
  }
  if (numericLt0(form.priority)) {
    h.priority = 'Priority must be 0 or greater'
  }
  if (numericLt0(form.root_volume_gb)) {
    h.root_volume_gb = "Root volume GB must be 0 or greater (0 keeps the AMI's native size)"
  }
  if (numericLt0(form.max_runtime_minutes)) {
    h.max_runtime_minutes = 'Max runtime must be 0 minutes or greater'
  }
  if (numericLt0(form.max_concurrent_runners)) {
    h.max_concurrent_runners = 'Max concurrent runners must be 0 or greater'
  }
  // Comma-separated instance_types: at least 1, at most 32, each
  // entry 1..64 chars. Mirrors required,min=1,max=32,dive,min=1,max=64.
  const types = parseListPreview(form.instance_types)
  if (types.length === 0) {
    h.instance_types = 'Add at least one instance type'
  } else if (types.length > SLICE_MAX) {
    h.instance_types = `Too many instance types (${types.length}); the limit is ${SLICE_MAX}`
  } else {
    for (const t of types) {
      if (t.length > INSTANCE_TYPE_MAX) {
        h.instance_types = `Each instance type must be at most ${INSTANCE_TYPE_MAX} characters ("${t}" is ${t.length})`
        break
      }
    }
  }
  // Slice caps for subnets / SGs. Pattern validation per entry lives
  // on IdListEditor; here we surface the count bounds.
  if (!form.subnet_ids || form.subnet_ids.length === 0) {
    h.subnet_ids = 'Add at least one subnet ID'
  } else if (form.subnet_ids.length > SLICE_MAX) {
    h.subnet_ids = `Too many subnet IDs (${form.subnet_ids.length}); the limit is ${SLICE_MAX}`
  }
  if (!form.security_group_ids || form.security_group_ids.length === 0) {
    h.security_group_ids = 'Add at least one security group ID'
  } else if (form.security_group_ids.length > SLICE_MAX) {
    h.security_group_ids = `Too many security group IDs (${form.security_group_ids.length}); the limit is ${SLICE_MAX}`
  }
  if (form.iam_instance_profile && form.iam_instance_profile.length > IAM_PROFILE_MAX) {
    h.iam_instance_profile = `IAM instance profile name must be at most ${IAM_PROFILE_MAX} characters`
  }
  // Extra labels are a comma list. Mirror the backend
  // dive,...,gha_safe,runner_label,not_self_hosted rules.
  const labels = parseListPreview(form.extra_labels)
  for (const l of labels) {
    if (isReservedTagKey(l)) {
      h.extra_labels = 'Extra runner labels must not start with "gha:" (that prefix is reserved)'
      break
    }
    if (!notSelfHosted(l)) {
      h.extra_labels = 'Remove "self-hosted" from extra runner labels -- it\'s added automatically'
      break
    }
    if (sanitizeLabel(l) === '') {
      h.extra_labels = `"${l}" has no letters, digits, or underscores -- pick a label with at least one`
      break
    }
    if (l.length > EXTRA_LABEL_MAX) {
      h.extra_labels = `Each extra runner label must be at most ${EXTRA_LABEL_MAX} characters`
      break
    }
  }
  for (const k of Object.keys(form.tags || {})) {
    if (isReservedTagKey(k)) {
      h.tags = 'Tag keys starting with "gha:" are reserved; pick a different prefix'
      break
    }
  }
  return h
}

export interface PoolBody {
  name: string
  is_default: boolean
  priority: number
  ami_id: string
  instance_types: string[]
  subnet_ids: string[]
  security_group_ids: string[]
  iam_instance_profile: string
  root_volume_gb: number
  max_runtime_minutes: number
  max_concurrent_runners: number
  spot: boolean
  spawn_method: string
  allocation_strategy: string
  extra_labels: string[]
  tags: Record<string, string>
  runner_version: string
  runner_user: string
  user_data_extra: string
  disabled: boolean
}

export function buildBody(form: PoolForm): PoolBody {
  return {
    name: form.name.trim(),
    is_default: !!form.is_default,
    priority: Number(form.priority) || 100,
    ami_id: form.ami_id.trim(),
    instance_types: parseListPreview(form.instance_types),
    subnet_ids: form.subnet_ids || [],
    security_group_ids: form.security_group_ids || [],
    iam_instance_profile: form.iam_instance_profile.trim(),
    root_volume_gb: Number(form.root_volume_gb) || 0,
    max_runtime_minutes: Number(form.max_runtime_minutes) || 60,
    max_concurrent_runners: Number(form.max_concurrent_runners) || 5,
    spot: !!form.spot,
    spawn_method: form.spawn_method || 'fleet',
    // allocation_strategy is Fleet-only. Force 'cost' on
    // run_instances so a stale value (left over from toggling away
    // from Fleet) never reaches the validator.
    allocation_strategy:
      (form.spawn_method || 'fleet') === 'fleet' ? form.allocation_strategy || 'cost' : 'cost',
    extra_labels: parseListPreview(form.extra_labels),
    tags: form.tags || {},
    runner_version: (form.runner_version || '').trim(),
    runner_user: (form.runner_user || '').trim(),
    user_data_extra: form.user_data_extra,
    disabled: !!form.disabled,
  }
}

// validate is the submit-time guard: per-input pattern attributes
// block obviously malformed entries, but they don't trigger on empty
// arrays. Catch "no entries" and "list with bad row" here so the form
// surfaces a clear message instead of a server 400.
export function validate(body: PoolBody): string | null {
  if (!POOL_NAME_RE.test(body.name)) {
    return 'Pool name must be lowercase alphanumeric / underscore / dash, no leading or trailing dash'
  }
  if (!AMI_RE.test(body.ami_id)) {
    return `AMI ID must match ${AMI_PATTERN} (e.g. ami-0abcdef0123456789)`
  }
  if (body.subnet_ids.length === 0) return 'At least one subnet ID is required'
  if (!body.subnet_ids.every((s) => SUBNET_RE.test(s))) {
    return `Subnet IDs must match ${SUBNET_PATTERN}`
  }
  if (body.security_group_ids.length === 0) return 'At least one security group ID is required'
  if (!body.security_group_ids.every((s) => SG_RE.test(s))) {
    return `Security group IDs must match ${SG_PATTERN}`
  }
  if (body.instance_types.length === 0) return 'At least one instance type is required'
  return null
}

// runsOnFor builds the YAML flow-style array a workflow can paste
// under `runs-on:`. The repo-specific `<owner>-<repo>` label is
// omitted intentionally -- it's stamped per-spawn and would tie the
// workflow to a single repo, defeating cross-repo reuse.
export function runsOnFor(p: Pool, projectName: string): string {
  const labels = ['self-hosted']
  const seen = new Set(['self-hosted'])
  const add = (s: string) => {
    const x = sanitizeLabel(s)
    if (x && !seen.has(x)) {
      seen.add(x)
      labels.push(x)
    }
  }
  add(projectName)
  add(p.name)
  for (const e of p.extra_labels || []) add(e)
  return '[' + labels.join(', ') + ']'
}
