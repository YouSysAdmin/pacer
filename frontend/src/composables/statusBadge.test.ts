// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { describe, expect, it } from 'vitest'
import { statusBadgeClass } from './statusBadge'

describe('statusBadgeClass job scope', () => {
  it.each([
    ['queued', 'badge badge-warning'],
    ['claimed', 'badge badge-warning'],
    ['starting', 'badge badge-warning'],
    ['running', 'badge badge-info'],
    ['completed', 'badge badge-success'],
    ['failed', 'badge badge-danger'],
    ['cancelled', 'badge badge-neutral'],
    ['reaped', 'badge badge-danger'],
  ])('%s -> %s', (status, want) => {
    expect(statusBadgeClass(status, 'job')).toBe(want)
  })
})

describe('statusBadgeClass instance scope', () => {
  it.each([
    ['starting', 'badge badge-warning'],
    ['running', 'badge badge-info'],
    ['terminated', 'badge badge-neutral'],
    ['reaped', 'badge badge-danger'],
  ])('%s -> %s', (status, want) => {
    expect(statusBadgeClass(status, 'instance')).toBe(want)
  })
})

describe('statusBadgeClass unknown values', () => {
  it('renders a bare pill rather than guessing', () => {
    expect(statusBadgeClass('exploded', 'job')).toBe('badge')
    expect(statusBadgeClass('', 'instance')).toBe('badge')
  })
  it('a job word is not an instance word', () => {
    // "completed" exists only in the job vocabulary.
    expect(statusBadgeClass('completed', 'instance')).toBe('badge')
  })
})
