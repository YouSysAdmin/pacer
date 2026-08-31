// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The menu as data. AppSidebar renders this; nothing else reads it.
// Icon names key into layouts/icons.ts.

export interface NavEntry {
  label: string
  path: string
  icon: string
  /** Opens in a new tab instead of the router. */
  external?: boolean
}

export interface NavGroup {
  label: string
  entries: NavEntry[]
}

// Two groups: observation and configuration, plus external help.
export const NAVIGATION: NavGroup[] = [
  {
    label: 'Control',
    entries: [
      { label: 'Overview', path: '/', icon: 'grid' },
      { label: 'Jobs', path: '/jobs', icon: 'list' },
      { label: 'Stats', path: '/stats', icon: 'bar-chart' },
      { label: 'Audit', path: '/audit', icon: 'history' },
    ],
  },
  {
    label: 'Config',
    entries: [
      { label: 'Projects', path: '/projects', icon: 'briefcase' },
      { label: 'Repos', path: '/repos', icon: 'book' },
      { label: 'Pools', path: '/pools', icon: 'server' },
      { label: 'Backup', path: '/backup', icon: 'package' },
      { label: 'Settings', path: '/settings', icon: 'settings' },
    ],
  },
  {
    label: 'Help',
    entries: [
      { label: 'Docs', path: 'https://pacer.yousysadmin.com/', icon: 'globe', external: true },
    ],
  },
]
