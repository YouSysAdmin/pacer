// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Which project the console is looking at.
//
// A FILTER, not a tenancy boundary - which is the difference from the
// mailyard console this borrows its shape from. There, a project scopes
// every request and "all projects" is not a state the app can be in;
// here a project is a logical grouping, the API is global, and an
// operator watching the queue legitimately wants to see every project
// at once. So null is a first-class value and the default.
//
// Consequences of that choice, both deliberate:
//   - Changing the scope does NOT navigate. The page being viewed
//     exists in every scope, so it just refilters where the operator
//     already is, rather than bouncing them to the dashboard.
//   - Pages the scope cannot reach (Backup, Settings, Audit) are not
//     hidden or disabled; they simply ignore it, and the switcher says
//     so rather than pretending to apply.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { projects as projectsAPI } from '@/api'

export interface ScopeProject {
  id: string
  name: string
  scope?: string
  disabled?: boolean
}

const STORAGE_KEY = 'pacer_scope_project'

export const useScopeStore = defineStore('scope', () => {
  const projects = ref<ScopeProject[]>([])
  const loaded = ref(false)

  // Read straight from storage so the first render is already scoped:
  // resolving it after the project list arrives would show one frame of
  // every project's rows before narrowing.
  const currentId = ref<string | null>(readStored())

  const current = computed(() => projects.value.find((p) => p.id === currentId.value) ?? null)

  // The label the switcher shows. Falls back to the raw id while the
  // list is still loading, so the control never flashes "All projects"
  // over a scope that is actually set.
  const label = computed(() => {
    if (!currentId.value) return 'All projects'
    return current.value?.name ?? currentId.value
  })

  // What list endpoints should send. Empty string means "no filter",
  // which is what every backend handler treats as every project.
  const projectParam = computed(() => currentId.value ?? undefined)

  function readStored(): string | null {
    try {
      return localStorage.getItem(STORAGE_KEY)
    } catch {
      // Private windows and blocked site data both throw here.
      return null
    }
  }

  function persist(id: string | null) {
    try {
      if (id) localStorage.setItem(STORAGE_KEY, id)
      else localStorage.removeItem(STORAGE_KEY)
    } catch {
      // A browser refusing storage still gets a working scope, it
      // just does not survive a reload.
    }
  }

  function set(id: string | null) {
    currentId.value = id
    persist(id)
  }

  async function load() {
    try {
      const ps = ((await projectsAPI.list()) as ScopeProject[]) || []
      projects.value = ps
      // A stored id whose project has since been deleted would scope
      // every page to nothing, with a switcher showing a raw UUID and
      // no obvious way back. Fall back to showing everything.
      if (currentId.value && !ps.some((p) => p.id === currentId.value)) set(null)
    } catch {
      // The scope is a convenience; a failed list leaves the console
      // unscoped rather than blocking it. Pages surface their own
      // errors.
    } finally {
      loaded.value = true
    }
  }

  /** Resolve a project id to its name, for rows that carry only the id. */
  function nameOf(id: string): string {
    return projects.value.find((p) => p.id === id)?.name ?? id
  }

  return { projects, loaded, currentId, current, label, projectParam, set, load, nameOf }
})
