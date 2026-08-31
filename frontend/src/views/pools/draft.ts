// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The pool draft, shared between the modal and its three field groups.
//
// provide/inject rather than props: the groups EDIT the draft, and a
// prop is read-only by contract -- passing it down would have each
// group either mutating something it does not own or re-emitting
// twenty fields to a parent that would write them straight back. The
// draft has exactly one owner (PoolFormModal) and three renderers, so
// handing them the same reactive object says that directly.

import { inject, provide, type InjectionKey } from 'vue'
import type { PoolForm } from './poolForm'

export interface PoolDraft {
  /** The editable draft. Field groups write to it in place. */
  form: PoolForm
  /** The message to show under a field: live hint, else server error. */
  hintFor: (name: string) => string
  /** Drop a server error as the operator edits that field. */
  clearError: (name: string) => void
}

const KEY: InjectionKey<PoolDraft> = Symbol('pool-draft')

export function providePoolDraft(draft: PoolDraft) {
  provide(KEY, draft)
}

export function usePoolDraft(): PoolDraft {
  const draft = inject(KEY)
  if (!draft) {
    // A field group outside the modal renders nothing useful and would
    // fail later with a confusing "cannot read form of undefined".
    throw new Error('pool field group used outside PoolFormModal')
  }
  return draft
}
