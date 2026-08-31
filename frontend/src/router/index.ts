// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Every top-level path registered here must have a matching entry in
// spaRoutePrefixes (internal/server/routes.go) or the Go server 404s
// the deep link in production. routes_test.go parses this file to
// enforce that -- keep route `path` literals on their own lines.

import { createRouter, createWebHistory } from 'vue-router'
import DashboardLayout from '@/layouts/DashboardLayout.vue'

const router = createRouter({
  history: createWebHistory('/'),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/login/Login.vue'),
      meta: { title: 'Sign in' },
    },
    {
      path: '/',
      component: DashboardLayout,
      children: [
        {
          path: '',
          name: 'overview',
          component: () => import('@/views/overview/Overview.vue'),
          meta: { title: 'Overview' },
        },
        {
          path: '/jobs',
          name: 'jobs',
          component: () => import('@/views/jobs/Jobs.vue'),
          meta: { title: 'Jobs' },
        },
        {
          path: '/stats',
          name: 'stats',
          component: () => import('@/views/stats/Stats.vue'),
          meta: { title: 'Stats' },
        },
        {
          path: '/audit',
          name: 'audit',
          component: () => import('@/views/audit/Audit.vue'),
          meta: { title: 'Audit' },
        },
        {
          path: '/projects',
          name: 'projects',
          component: () => import('@/views/projects/Projects.vue'),
          meta: { title: 'Projects' },
        },
        {
          path: '/repos',
          name: 'repos',
          component: () => import('@/views/repos/Repos.vue'),
          meta: { title: 'Repos' },
        },
        {
          path: '/pools',
          name: 'pools',
          component: () => import('@/views/pools/Pools.vue'),
          meta: { title: 'Pools' },
        },
        {
          path: '/backup',
          name: 'backup',
          component: () => import('@/views/backup/Backup.vue'),
          meta: { title: 'Backup' },
        },
        {
          path: '/settings',
          name: 'settings',
          component: () => import('@/views/settings/Settings.vue'),
          meta: { title: 'Settings' },
        },
      ],
    },
  ],
})

router.afterEach((to, _from, failure) => {
  if (failure) return
  const title = to.meta.title as string | undefined
  document.title = title ? `${title} - Pacer` : 'Pacer'
})

// A deploy replaces the hashed chunk files; a tab that kept the old
// shell open then fails to lazy-import a view. Reload once to pick up
// the new build instead of showing a dead navigation. The sessionStorage
// flag prevents a reload loop when the chunk is genuinely gone.
router.onError((error, to) => {
  if (!/module script failed|dynamically imported module/i.test(String(error?.message))) return
  const key = 'pacer_chunk_reload'
  try {
    if (sessionStorage.getItem(key)) return
    sessionStorage.setItem(key, '1')
  } catch {
    // storage unavailable; a reload loop is worse than a dead link
    return
  }
  window.location.href = to.fullPath
})

export default router
