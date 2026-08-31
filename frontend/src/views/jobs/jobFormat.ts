// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Formatting shared by the jobs table and the detail modal.

// age renders the distance between two moments as "3m 12s" / "2h 5m".
// A missing end means "still going" and measures against now.
export function age(start?: string | null, end?: string | null): string {
  if (!start) return ''
  const a = new Date(start).getTime()
  const b = end ? new Date(end).getTime() : Date.now()
  const s = Math.floor((b - a) / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

// Sub-cent runs show 4 decimals so they don't read as free.
export function cost(usd?: number | null): string {
  if (usd == null) return ''
  if (usd === 0) return '$0.00'
  if (Math.abs(usd) < 0.01) return '$' + usd.toFixed(4)
  return '$' + usd.toFixed(2)
}
