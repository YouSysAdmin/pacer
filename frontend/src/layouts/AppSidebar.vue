<script setup lang="ts">
// The rail: brand, the nav groups from navigation.ts, and a foot with
// the session and the theme control.
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { NAVIGATION } from './navigation'
import { getIcon } from './icons'
import BrandMark from '@/components/BrandMark.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import ScopeSwitcher from './ScopeSwitcher.vue'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

function isActive(path: string): boolean {
  if (path === '/') return route.path === '/'
  return route.path === path || route.path.startsWith(path + '/')
}

async function logout() {
  await authStore.logout()
  router.push('/login')
}
</script>

<template>
  <aside class="sidebar" aria-label="Primary navigation">
    <RouterLink to="/" class="brand" aria-label="Pacer">
      <BrandMark :size="20" />
      <span class="brand-name">Pacer</span>
    </RouterLink>

    <ScopeSwitcher />

    <nav v-for="g in NAVIGATION" :key="g.label" class="nav-group" :aria-label="g.label">
      <div class="nav-group-label">{{ g.label }}</div>
      <template v-for="e in g.entries" :key="e.path">
        <a
          v-if="e.external"
          :href="e.path"
          class="nav-item"
          target="_blank"
          rel="noopener noreferrer"
        >
          <span class="nav-icon" aria-hidden="true" v-html="getIcon(e.icon)" />
          {{ e.label }}
        </a>
        <RouterLink v-else :to="e.path" class="nav-item" :class="{ active: isActive(e.path) }">
          <span class="nav-icon" aria-hidden="true" v-html="getIcon(e.icon)" />
          {{ e.label }}
        </RouterLink>
      </template>
    </nav>

    <div class="sidebar-foot">
      <ThemeToggle />
      <div v-if="authStore.user" class="sidebar-user">
        <div class="user-email" :title="authStore.user.email">{{ authStore.user.email }}</div>
        <button class="btn btn-secondary btn-sm" @click="logout">
          <span class="nav-icon" aria-hidden="true" v-html="getIcon('log-out')" />
          Logout
        </button>
      </div>
      <span v-else class="conn" title="Server is reachable">
        <span class="dot"></span>
        {{ authStore.authDisabled ? 'auth disabled' : 'connected' }}
      </span>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  gap: 20px;
  width: 216px;
  flex-shrink: 0;
  padding: 16px 12px;
  background: var(--bg-sidebar);
  border-right: 1px solid var(--border-primary);
  overflow-y: auto;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 10px;
  text-decoration: none;
}

.brand-name {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: 0.02em;
  color: var(--text-primary);
}

.nav-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-group-label {
  padding: 0 10px 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 10px;
  border-radius: var(--radius-md);
  font-size: 14px;
  color: var(--text-secondary);
  text-decoration: none;
  transition: var(--transition);
}

.nav-item:hover {
  background: var(--sidebar-hover);
  color: var(--text-primary);
}

.nav-item.active {
  background: var(--bg-active);
  color: var(--text-primary);
  font-weight: 550;
}

.nav-icon {
  display: inline-flex;
  flex-shrink: 0;
}

.sidebar-foot {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: auto;
  padding: 12px 10px 4px;
  border-top: 1px solid var(--border-primary);
}

.sidebar-user {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.user-email {
  font-size: 13px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar-user .btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  justify-content: center;
}

.conn {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-muted);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--success-500);
}
</style>
