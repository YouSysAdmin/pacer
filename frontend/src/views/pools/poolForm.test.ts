// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { describe, expect, it } from 'vitest'
import {
  buildBody,
  buildHints,
  emptyForm,
  formFrom,
  parseListPreview,
  runsOnFor,
  validate,
  type Pool,
} from './poolForm'

function validForm() {
  const f = emptyForm()
  f.project_id = 'p1'
  f.name = 'large'
  f.ami_id = 'ami-0abcdef0123456789'
  f.subnet_ids = ['subnet-0abcdef012345678']
  f.security_group_ids = ['sg-0abcdef012345678']
  return f
}

function samplePool(): Pool {
  return {
    id: 'pool-1',
    project_id: 'p1',
    name: 'large',
    is_default: true,
    priority: 10,
    ami_id: 'ami-0abcdef0123456789',
    instance_types: ['m6i.large', 'm5.large'],
    subnet_ids: ['subnet-0abcdef012345678'],
    security_group_ids: ['sg-0abcdef012345678'],
    iam_instance_profile: 'runner-profile',
    root_volume_gb: 40,
    max_runtime_minutes: 90,
    max_concurrent_runners: 8,
    spot: true,
    spawn_method: 'fleet',
    allocation_strategy: 'capacity',
    extra_labels: ['gpu', 'arm64'],
    tags: { team: 'ci' },
    runner_version: '2.319.1',
    runner_user: 'ubuntu',
    user_data_extra: 'echo hi',
    disabled: false,
  }
}

describe('parseListPreview', () => {
  it('splits, trims, and drops empties', () => {
    expect(parseListPreview(' a, b ,,c ')).toEqual(['a', 'b', 'c'])
    expect(parseListPreview('')).toEqual([])
    expect(parseListPreview(undefined)).toEqual([])
  })
})

describe('buildBody', () => {
  it('parses comma lists and trims strings', () => {
    const f = validForm()
    f.instance_types = ' m6i.large , m5.large ,'
    f.extra_labels = 'gpu, arm64'
    f.ami_id = ' ami-0abcdef0123456789 '
    const b = buildBody(f)
    expect(b.instance_types).toEqual(['m6i.large', 'm5.large'])
    expect(b.extra_labels).toEqual(['gpu', 'arm64'])
    expect(b.ami_id).toBe('ami-0abcdef0123456789')
  })

  it('forces allocation_strategy to cost for run_instances', () => {
    const f = validForm()
    f.spawn_method = 'run_instances'
    f.allocation_strategy = 'priority'
    expect(buildBody(f).allocation_strategy).toBe('cost')
  })

  it('keeps the picked strategy for fleet', () => {
    const f = validForm()
    f.allocation_strategy = 'capacity'
    expect(buildBody(f).allocation_strategy).toBe('capacity')
  })

  it('defaults zeroed numerics', () => {
    const f = validForm()
    f.priority = 0
    f.max_runtime_minutes = 0
    f.max_concurrent_runners = 0
    const b = buildBody(f)
    expect(b.priority).toBe(100)
    expect(b.max_runtime_minutes).toBe(60)
    expect(b.max_concurrent_runners).toBe(5)
  })
})

describe('buildHints', () => {
  it('clean form has no hints', () => {
    expect(buildHints(validForm())).toEqual({})
  })

  it('flags missing instance types, subnets, and SGs', () => {
    const f = validForm()
    f.instance_types = ''
    f.subnet_ids = []
    f.security_group_ids = []
    const h = buildHints(f)
    expect(h.instance_types).toMatch(/at least one/i)
    expect(h.subnet_ids).toMatch(/at least one/i)
    expect(h.security_group_ids).toMatch(/at least one/i)
  })

  it('flags reserved gha: prefix on labels and tags', () => {
    const f = validForm()
    f.extra_labels = 'gha:evil'
    f.tags = { 'gha:project': 'x' }
    const h = buildHints(f)
    expect(h.extra_labels).toMatch(/reserved/)
    expect(h.tags).toMatch(/reserved/)
  })

  it('flags self-hosted and unsanitizable labels', () => {
    const f = validForm()
    f.extra_labels = 'Self-Hosted'
    expect(buildHints(f).extra_labels).toMatch(/self-hosted/)
    f.extra_labels = '---'
    expect(buildHints(f).extra_labels).toMatch(/no letters/)
  })

  it('flags negative numerics', () => {
    const f = validForm()
    f.priority = -1
    f.root_volume_gb = -5
    const h = buildHints(f)
    expect(h.priority).toBeTruthy()
    expect(h.root_volume_gb).toBeTruthy()
  })

  it('flags a bad runner user', () => {
    const f = validForm()
    f.runner_user = '1root'
    expect(buildHints(f).runner_user).toMatch(/lowercase/)
  })
})

describe('validate', () => {
  it('accepts a well-formed body', () => {
    expect(validate(buildBody(validForm()))).toBeNull()
  })

  it('rejects a malformed name, AMI, subnet, and SG', () => {
    const f = validForm()
    f.name = 'Bad Name'
    expect(validate(buildBody(f))).toMatch(/pool name/i)

    const f2 = validForm()
    f2.ami_id = 'ami-XYZ'
    expect(validate(buildBody(f2))).toMatch(/AMI/)

    const f3 = validForm()
    f3.subnet_ids = ['subnet-nothex']
    expect(validate(buildBody(f3))).toMatch(/subnet/i)

    const f4 = validForm()
    f4.security_group_ids = ['sg-nothex']
    expect(validate(buildBody(f4))).toMatch(/security group/i)
  })
})

describe('formFrom', () => {
  it('edit keeps identity, copy clears name and default', () => {
    const p = samplePool()
    const edit = formFrom(p, 'edit')
    expect(edit.name).toBe('large')
    expect(edit.is_default).toBe(true)
    expect(edit.instance_types).toBe('m6i.large,m5.large')

    const copy = formFrom(p, 'copy')
    expect(copy.name).toBe('')
    expect(copy.is_default).toBe(false)
    expect(copy.tags).toEqual({ team: 'ci' })
    // The copy owns its collections -- mutating them must not touch
    // the source pool.
    copy.subnet_ids.push('subnet-1234567890abcdef')
    expect(p.subnet_ids).toEqual(['subnet-0abcdef012345678'])
  })
})

describe('runsOnFor', () => {
  it('builds the sanitized, deduped label list', () => {
    const p = samplePool()
    expect(runsOnFor(p, 'My App')).toBe('[self-hosted, my-app, large, gpu, arm64]')
  })

  it('drops labels that sanitize to duplicates or empties', () => {
    const p = samplePool()
    p.extra_labels = ['Large', '---', 'gpu']
    expect(runsOnFor(p, 'proj')).toBe('[self-hosted, proj, large, gpu]')
  })
})
