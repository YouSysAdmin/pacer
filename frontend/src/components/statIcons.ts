// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The glyphs on stat cards.
//
// NOT layouts/icons.ts, and deliberately so. That map is the
// NAVIGATION set: 18x18 at stroke 1.5, drawn for a row of menu items.
// These are 24-viewBox at stroke 2, which is what a 20px card icon
// wants, and the two do not mix - a nav glyph placed here reads as a
// thinner drawing of the same thing rather than as the same weight.
//
// Feather geometry, like the rest of the product - see layouts/icons.ts
// for the licence note that covers both.

/** Inner shapes only. The svg wrapper is StatCard's, identical for all. */
export const STAT_ICONS: Record<string, string> = {
  // Total jobs in a window.
  total: `
    <line x1="8" y1="6" x2="21" y2="6" />
    <line x1="8" y1="12" x2="21" y2="12" />
    <line x1="8" y1="18" x2="21" y2="18" />
    <line x1="3" y1="6" x2="3.01" y2="6" />
    <line x1="3" y1="12" x2="3.01" y2="12" />
    <line x1="3" y1="18" x2="3.01" y2="18" />
  `,
  // Waiting for a runner.
  queued: `
    <circle cx="12" cy="12" r="10" />
    <polyline points="12 6 12 12 16 14" />
  `,
  // In flight right now.
  running: `
    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
  `,
  completed: `
    <polyline points="20 6 9 17 4 12" />
  `,
  failed: `
    <circle cx="12" cy="12" r="10" />
    <line x1="15" y1="9" x2="9" y2="15" />
    <line x1="9" y1="9" x2="15" y2="15" />
  `,
  // The sweeper had to intervene.
  reaped: `
    <path
    d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"
    />
    <line x1="12" y1="9" x2="12" y2="13" />
    <line x1="12" y1="17" x2="12.01" y2="17" />
  `,
  // Estimated EC2 spend.
  spend: `
    <line x1="12" y1="1" x2="12" y2="23" />
    <path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
  `,
  // Who runs the most CI.
  users: `
    <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
    <path d="M16 3.13a4 4 0 0 1 0 7.75" />
  `,
  // EC2 instances.
  instances: `
    <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
    <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
    <line x1="6" y1="6" x2="6.01" y2="6" />
    <line x1="6" y1="18" x2="6.01" y2="18" />
  `,
}
