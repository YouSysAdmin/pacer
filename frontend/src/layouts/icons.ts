// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The console's icon set, one map.
//
// The glyphs are FEATHER ICONS, refitted to an 18px grid.
//
//   Feather - https://feathericons.com
//   https://github.com/feathericons/feather
//   MIT License, Copyright (c) 2013-2023 Cole Bemis
//
// Refitted rather than imported: Feather is drawn on a 24 viewBox at
// stroke 2, and this set is 18 at stroke 1.5. Pasted rather than
// depended on - inline SVG for the glyphs actually used costs a few
// kilobytes where an icon font ships megabytes into the binary.
//
// Adding one means matching the grid - 18x18 viewBox, 1.5 stroke,
// currentColor, round caps - or it reads as a different weight beside
// its neighbours.

// One glyph per line, keys quoted. prettier both reflows the SVG
// strings - leaving them unreadable as shapes - and strips quotes it
// considers unnecessary.
// prettier-ignore
export const ICONS: Record<string, string> = {
    'grid': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><rect x="1.5" y="1.5" width="6" height="6" rx="1.5" stroke="currentColor" stroke-width="1.5"/><rect x="10.5" y="1.5" width="6" height="6" rx="1.5" stroke="currentColor" stroke-width="1.5"/><rect x="1.5" y="10.5" width="6" height="6" rx="1.5" stroke="currentColor" stroke-width="1.5"/><rect x="10.5" y="10.5" width="6" height="6" rx="1.5" stroke="currentColor" stroke-width="1.5"/></svg>',
    'book': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M3 3.5h4a2 2 0 012 2v9a1.5 1.5 0 00-1.5-1.5H3v-9zM15 3.5h-4a2 2 0 00-2 2v9a1.5 1.5 0 011.5-1.5H15v-9z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>',
    'key': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="6.4" cy="11.6" r="3.4" stroke="currentColor" stroke-width="1.5"/><path d="M8.8 9.2L15.5 2.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><path d="M11.7 6.3l1.5 1.5M13.6 4.4l1.5 1.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'server': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><rect x="2" y="2" width="14" height="5" rx="1.5" stroke="currentColor" stroke-width="1.5"/><rect x="2" y="11" width="14" height="5" rx="1.5" stroke="currentColor" stroke-width="1.5"/><circle cx="5" cy="4.5" r="0.75" fill="currentColor"/><circle cx="5" cy="13.5" r="0.75" fill="currentColor"/></svg>',
    'globe': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.5"/><path d="M2 9h14M9 2a11.05 11.05 0 013 7 11.05 11.05 0 01-3 7 11.05 11.05 0 01-3-7 11.05 11.05 0 013-7z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'alert-triangle': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M7.86 2.87L1.21 14.25a1.31 1.31 0 001.14 1.97h13.3a1.31 1.31 0 001.14-1.97L10.14 2.87a1.31 1.31 0 00-2.28 0z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M9 6.75v3M9 12.75h.007" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'x-circle': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.5"/><path d="M11.25 6.75l-4.5 4.5M6.75 6.75l4.5 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'info': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.5"/><path d="M9 12v-3M9 6h.007" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'x': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M13.5 4.5l-9 9M4.5 4.5l9 9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'users': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M12.75 15.75v-1.5a3 3 0 00-3-3h-6a3 3 0 00-3 3v1.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><circle cx="6.75" cy="5.25" r="3" stroke="currentColor" stroke-width="1.5"/><path d="M17.25 15.75v-1.5a3 3 0 00-2.25-2.9M12 2.33a3 3 0 010 5.84" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'list': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M6.75 4.5h9M6.75 9h9M6.75 13.5h9M2.25 4.5h.007M2.25 9h.007M2.25 13.5h.007" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'briefcase': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><rect x="2" y="6" width="14" height="10" rx="1.5" stroke="currentColor" stroke-width="1.5"/><path d="M12 6V4.5A1.5 1.5 0 0010.5 3h-3A1.5 1.5 0 006 4.5V6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'settings': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="9" cy="9" r="2.25" stroke="currentColor" stroke-width="1.5"/><path d="M14.7 11.1a1.2 1.2 0 00.24 1.32l.04.04a1.46 1.46 0 11-2.06 2.06l-.04-.04a1.2 1.2 0 00-1.32-.24 1.2 1.2 0 00-.73 1.1v.12a1.46 1.46 0 01-2.91 0v-.06a1.2 1.2 0 00-.79-1.1 1.2 1.2 0 00-1.32.24l-.04.04a1.46 1.46 0 11-2.06-2.06l.04-.04a1.2 1.2 0 00.24-1.32 1.2 1.2 0 00-1.1-.73h-.12a1.46 1.46 0 010-2.91h.06a1.2 1.2 0 001.1-.79 1.2 1.2 0 00-.24-1.32l-.04-.04a1.46 1.46 0 112.06-2.06l.04.04a1.2 1.2 0 001.32.24h.06a1.2 1.2 0 00.73-1.1v-.12a1.46 1.46 0 012.91 0v.06a1.2 1.2 0 00.73 1.1 1.2 1.2 0 001.32-.24l.04-.04a1.46 1.46 0 112.06 2.06l-.04.04a1.2 1.2 0 00-.24 1.32v.06a1.2 1.2 0 001.1.73h.12a1.46 1.46 0 010 2.91h-.06a1.2 1.2 0 00-1.1.73z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'history': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M2.25 2.25v3.75h3.75" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M2.29 9.75A6.75 6.75 0 104.5 3.98L2.25 6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M9 5.25v3.9l3.15 1.85" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'package': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M15.75 6a1.5 1.5 0 00-.75-1.3l-5.25-3a1.5 1.5 0 00-1.5 0l-5.25 3A1.5 1.5 0 002.25 6v6a1.5 1.5 0 00.75 1.3l5.25 3a1.5 1.5 0 001.5 0l5.25-3A1.5 1.5 0 0015.75 12z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M2.48 5.25L9 9l6.52-3.75M9 16.5V9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'bar-chart': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M13.5 15V7.5M9 15V3M4.5 15v-4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'log-out': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M6.75 15.75H3.75a1.5 1.5 0 01-1.5-1.5V3.75a1.5 1.5 0 011.5-1.5h3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M12 12.75L15.75 9 12 5.25M15.75 9h-9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'sun': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><circle cx="9" cy="9" r="3" stroke="currentColor" stroke-width="1.5"/><path d="M9 1.5V3M9 15v1.5M3.7 3.7l1.06 1.06M13.24 13.24l1.06 1.06M1.5 9H3M15 9h1.5M3.7 14.3l1.06-1.06M13.24 4.76l1.06-1.06" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'moon': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><path d="M15.75 9.59A6.75 6.75 0 118.41 2.25 5.25 5.25 0 0015.75 9.59z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'monitor': '<svg width="18" height="18" viewBox="0 0 18 18" fill="none"><rect x="1.5" y="2.25" width="15" height="10.5" rx="1.5" stroke="currentColor" stroke-width="1.5"/><path d="M6 15.75h6M9 12.75v3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
}

/**
 * getIcon returns the markup for a glyph, or an empty string when the
 * name is unknown.
 *
 * Empty rather than a fallback glyph: a wrong-but-present icon reads
 * as a deliberate choice, where a 17px gap is visibly missing.
 */
export function getIcon(name: string): string {
  return ICONS[name] ?? ''
}
