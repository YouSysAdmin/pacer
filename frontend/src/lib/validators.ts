// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Shared pre-submit validators that mirror the backend's
// internal/core/validation/custom.go rules. The point is to give the
// operator immediate feedback as they type, instead of waiting for a
// 400 from the server. Keep these in sync with the Go validators when
// either side changes -- the backend is still the source of truth, but
// drifting client-side regexes show up as misleading green ticks.
//
// Each helper returns a boolean (true = valid) so call sites can use
// the result both in `aria-invalid` bindings and conditional message
// blocks. The two patterns exported with `*_PATTERN` suffix are raw
// strings (no leading `^`, no trailing `$`) suitable for the HTML5
// `pattern` attribute. The matching `*_RE` constants are anchored
// RegExp objects for JS-level checks -- having both lets the browser
// handle the form-submit-time barrier AND the live oninput feedback
// without duplicating the source pattern.

// --- AWS resource IDs --------------------------------------------------

export const AMI_PATTERN = 'ami-[a-f0-9]{8,17}'
export const SUBNET_PATTERN = 'subnet-[a-f0-9]{8,17}'
export const SG_PATTERN = 'sg-[a-f0-9]{8,17}'

export const AMI_RE = new RegExp('^' + AMI_PATTERN + '$')
export const SUBNET_RE = new RegExp('^' + SUBNET_PATTERN + '$')
export const SG_RE = new RegExp('^' + SG_PATTERN + '$')

// --- Pool name (runner_label_strict) ----------------------------------

// `\-` is harmless in default JS regex parsing AND required for the
// HTML5 pattern attribute's /v mode where unescaped `-` inside a
// character class is an error. One source string, two consumers.
export const POOL_NAME_PATTERN = '[a-z0-9_]+(?:[a-z0-9_\\-]*[a-z0-9_])?'
export const POOL_NAME_RE = new RegExp('^' + POOL_NAME_PATTERN + '$')

// --- POSIX user (posix_user) ------------------------------------------

// Validator allows max=32 in the Go DTO. The pattern caps at 32 to
// match. Empty value passes -- the field is optional and `required`
// handles its own presence check separately.
export const POSIX_USER_PATTERN = '[a-z_][a-z0-9_\\-]{0,31}'
export const POSIX_USER_RE = new RegExp('^' + POSIX_USER_PATTERN + '$')

// --- Tag keys ---------------------------------------------------------

// `gha:` is reserved for tool-managed tags. The backend gha_safe
// validator rejects user-supplied keys with this prefix
// (case-insensitive). Mirrors validation.registerCustom.
export function isReservedTagKey(k?: string | null): boolean {
  return (k || '').toLowerCase().startsWith('gha:')
}

// --- Strings ----------------------------------------------------------

export function isPosixUser(s?: string | null): boolean {
  if (!s) return true
  return POSIX_USER_RE.test(s)
}

export function isPoolName(s?: string | null): boolean {
  if (!s) return false
  return POOL_NAME_RE.test(s)
}

// no_slash_or_space mirrors validation.registerCustom -- used on
// project.org_name. Rejects '/', ' ', and '\t'.
export function noSlashOrSpace(s?: string | null): boolean {
  return !/[/ \t]/.test(s || '')
}

// repo_full_name mirrors validation.registerCustom. The Go validator
// splits on "/" with SplitN 2, so "owner/name/sub" still satisfies
// (parts=["owner","name/sub"], both non-empty). Match that exactly.
export function isRepoFullName(s?: string | null): boolean {
  if (!s) return false
  const i = s.indexOf('/')
  return i > 0 && i < s.length - 1
}

// sanitizeLabel mirrors pool.SanitizeLabel (Go). Lowercases, replaces
// runs of non-[a-z0-9_] with '-', trims trailing dashes. Used both for
// the runs-on workflow preview and as the basis for runner_label /
// runner_label_strict / not_self_hosted.
export function sanitizeLabel(s?: string | null): string {
  if (!s) return ''
  let out = ''
  let lastDash = false
  for (const ch of String(s).toLowerCase()) {
    if (/[a-z0-9_]/.test(ch)) {
      out += ch
      lastDash = false
    } else if (out.length > 0 && !lastDash) {
      out += '-'
      lastDash = true
    }
  }
  return out.replace(/-+$/, '')
}

// runner_label: sanitize-non-empty. Used on pool ExtraLabels entries
// so a label of "---" or " " can't slip through.
export function isRunnerLabel(s?: string | null): boolean {
  return sanitizeLabel(s) !== ''
}

// runner_label_strict: input must equal its canonical form. Used on
// pool.Name.
export function isRunnerLabelStrict(s?: string | null): boolean {
  if (!s) return false
  return s === sanitizeLabel(s)
}

// not_self_hosted: sanitized form must not equal "self-hosted". Used
// on pool ExtraLabels.
export function notSelfHosted(s?: string | null): boolean {
  return sanitizeLabel(s) !== 'self-hosted'
}

// --- err.fields helpers -----------------------------------------------

// fieldErrorsFrom turns an error thrown by api/client.ts (with the
// optional .fields array from the validator's structured envelope)
// into a flat {fieldName: message} map. Returns {} when there are no
// field-level details (legacy `{error: string}` responses) so callers
// can spread it unconditionally onto a reactive map.
//
// Multiple errors on the same field are joined with "; " so a field
// with both a max and a charset failure shows both reasons.
export function fieldErrorsFrom(err: unknown): Record<string, string> {
  const fields = (err as { fields?: unknown } | null)?.fields
  if (!Array.isArray(fields)) return {}
  const out: Record<string, string> = {}
  for (const f of fields as Array<{ field?: string; message: string }>) {
    if (!f || !f.field) continue
    out[f.field] = out[f.field] ? out[f.field] + '; ' + f.message : f.message
  }
  return out
}
