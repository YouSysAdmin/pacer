// The server's field errors, put back where they belong.
//
// The validator answers with `{"error": "...", "fields": [{"field",
// "rule", "message"}]}` and api/client.ts attaches that array to the
// thrown ApiError. A form keyed by the same JSON names the request
// sent needs no mapping table - `field` IS that name.

import { ref } from 'vue'
import type { FieldError } from '@/api/client'

export function useFieldErrors() {
  const errors = ref<Record<string, string>>({})

  // True when the errors were placed on fields, which is the caller's
  // signal not to raise a toast as well: the message is already on
  // screen, next to the input, and saying it twice reads as two
  // problems.
  //
  // An entry with no `field` is what the backend produces for a body
  // that did not decode at all. There is no input to blame for that,
  // so it stays a toast.
  function capture(err: unknown): boolean {
    const fields = (err as { fields?: FieldError[] } | null)?.fields
    if (!Array.isArray(fields)) return false
    const next: Record<string, string> = {}
    for (const fe of fields) {
      if (!fe?.field || !fe?.message) continue
      // Multiple rules failing on one field both deserve to be read.
      next[fe.field] = next[fe.field] ? next[fe.field] + '; ' + fe.message : fe.message
    }
    errors.value = next

    return Object.keys(next).length > 0
  }

  // Clear one field as it is edited, or all of them when a request is
  // about to be made. Errors from the previous attempt outliving the
  // next one is how a form ends up refusing a value it already accepted.
  function clear(field?: string) {
    if (!field) {
      errors.value = {}

      return
    }
    if (errors.value[field]) delete errors.value[field]
  }

  return { errors, capture, clear }
}
