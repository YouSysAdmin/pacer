<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Which project the console is filtered to.
//
// Its own component because it is the one thing in the rail that is
// not navigation: it does not link anywhere, it changes what the
// pages already open are SHOWING.
//
// The LOOK is the mailyard console's project picker -- a tile carrying
// the initial, the name, a caret, and a menu of the same tiles. The
// BEHAVIOUR is not: there, a project is a tenancy boundary, so picking
// one navigates home and "all projects" is not a state the app can be
// in. Here it is a filter, so null is a real choice and picking one
// refilters the page the operator is already on rather than throwing
// away the sort and page they had set.
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useScopeStore } from '@/stores/scope'
import { getIcon } from './icons'

const scope = useScopeStore()
const route = useRoute()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

// Routes the scope cannot narrow. Backup and Settings are global by
// nature; audit_log carries no project_id column, so filtering it
// would need a migration rather than a query param. Saying so beats
// a control that looks live and changes nothing.
const GLOBAL_ROUTES = ['/backup', '/settings', '/audit']

const appliesHere = computed(() => !GLOBAL_ROUTES.includes(route.path))

// The tile's letter. "All projects" gets a glyph instead, since an
// initial there would be a letter standing for no project.
const initial = computed(() => scope.current?.name?.charAt(0)?.toUpperCase() ?? '')

function choose(id: string | null) {
  scope.set(id)
  open.value = false
}

function onDocClick(e: MouseEvent) {
  if (!root.value?.contains(e.target as Node)) open.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div ref="root" class="picker">
    <button
      type="button"
      class="picker-open"
      :aria-expanded="open"
      :title="
        appliesHere
          ? 'Filter every list to one project'
          : 'This page is global -- the project filter does not apply here'
      "
      @click="open = !open"
    >
      <span class="picker-who">
        <span class="tile" :class="{ 'tile-all': !scope.currentId }" aria-hidden="true">
          <span v-if="scope.currentId">{{ initial }}</span>
          <span v-else class="tile-glyph" v-html="getIcon('grid')"></span>
        </span>
        <span class="picker-name">{{ scope.label }}</span>
      </span>
      <span class="picker-caret" aria-hidden="true" v-html="getIcon('chevron-down')"></span>
    </button>

    <div v-if="open" class="picker-menu" role="listbox">
      <button
        type="button"
        class="picker-row"
        :class="{ on: scope.currentId === null }"
        role="option"
        :aria-selected="scope.currentId === null"
        @click="choose(null)"
      >
        <span class="tile tile-sm tile-all" aria-hidden="true">
          <span class="tile-glyph" v-html="getIcon('grid')"></span>
        </span>
        <span class="picker-row-name">All projects</span>
      </button>

      <div v-if="scope.projects.length > 0" class="picker-rule"></div>

      <button
        v-for="p in scope.projects"
        :key="p.id"
        type="button"
        class="picker-row"
        :class="{ on: scope.currentId === p.id }"
        role="option"
        :aria-selected="scope.currentId === p.id"
        @click="choose(p.id)"
      >
        <span class="tile tile-sm" aria-hidden="true">{{ p.name.charAt(0).toUpperCase() }}</span>
        <span class="picker-row-name">{{ p.name }}</span>
        <span v-if="p.disabled" class="badge badge-warning">off</span>
      </button>

      <p v-if="scope.loaded && scope.projects.length === 0" class="picker-empty">
        No projects yet.
      </p>
    </div>

    <!-- Said out loud, not just implied by a dimmer label. A scope of
         "alpha" beside an audit log listing every project reads as a
         broken filter unless the page admits it does not apply. -->
    <p v-if="scope.currentId && !appliesHere" class="picker-note">Not applied on this page</p>
  </div>
</template>

<style scoped>
.picker {
  position: relative;
  padding: 0 8px;
}

.picker-open {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-primary);
  border-radius: var(--radius);
  background: transparent;
  color: var(--text-primary);
  font-family: inherit;
  cursor: pointer;
  transition:
    background var(--transition),
    border-color var(--transition);
}

.picker-open:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

.picker-who {
  display: flex;
  overflow: hidden;
  align-items: center;
  gap: 8px;
}

.picker-name {
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.picker-caret,
.tile-glyph {
  display: flex;
  flex-shrink: 0;
}

.picker-caret {
  width: 15px;
  height: 15px;
  color: var(--text-muted);
}

/* The project's first letter, standing in for a logo nobody uploads.
   Sized through a variable so the smaller one in the menu is a single
   override rather than a second copy of the rule. */
.tile {
  --tile: 24px;
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: var(--tile);
  height: var(--tile);
  border-radius: 6px;
  background: var(--primary-600);
  color: var(--text-on-primary);
  font-size: 12px;
  font-weight: 600;
}

.tile-sm {
  --tile: 20px;
  border-radius: 5px;
  font-size: 10px;
}

/* "All projects" is not a project, so it does not get a project's
   solid tile -- an outline with the grid glyph instead. */
.tile-all {
  background: transparent;
  border: 1px solid var(--border-strong);
  color: var(--text-secondary);
}

.tile-all .tile-glyph {
  width: 13px;
  height: 13px;
}

.tile-sm .tile-glyph {
  width: 11px;
  height: 11px;
}

.picker-menu {
  position: absolute;
  top: calc(100% + 2px);
  right: 8px;
  left: 8px;
  z-index: 200;
  max-height: 320px;
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--border-primary);
  border-radius: var(--radius);
  background: var(--bg-popover);
  box-shadow: var(--shadow-lg);
}

/* Every line in the menu is one of these, whether it picks a project
   or clears the filter. */
.picker-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 7px 8px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  text-align: left;
  cursor: pointer;
  transition:
    background var(--transition),
    color var(--transition);
}

.picker-row:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.picker-row.on {
  background: var(--bg-active);
  color: var(--text-primary);
}

.picker-row-name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.picker-row .badge {
  margin-left: auto;
}

.picker-rule {
  height: 1px;
  margin: 4px 6px;
  background: var(--border-primary);
}

.picker-empty {
  padding: 8px 10px;
  font-size: 12px;
  color: var(--text-muted);
}

.picker-note {
  margin: 6px 2px 0;
  font-size: 11px;
  line-height: 1.35;
  color: var(--warning-fg);
}
</style>
