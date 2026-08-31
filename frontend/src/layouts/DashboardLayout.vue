<script setup lang="ts">
// The signed-in shell: rail on the left, health banner and the routed
// page on the right. /login sits OUTSIDE this layout in the route
// tree, so nothing here has to sniff the path.
import { onMounted, onUnmounted, ref } from 'vue'
import { systemHealth } from '@/api'
import { useAuthStore } from '@/stores/auth'
import AppSidebar from './AppSidebar.vue'

interface HealthIssue {
  component: string
  message: string
}

const authStore = useAuthStore()

// Background-worker issues from GET /api/health. Polled every 30s so
// a failure (missing IAM perm, panicked reaper) becomes visible
// without operator action. Cleared automatically when the next clean
// reaper tick clears Health server-side.
const healthIssues = ref<HealthIssue[]>([])
let healthTimer: ReturnType<typeof setInterval> | undefined

async function pollHealth() {
  try {
    const r = (await systemHealth.list()) as { issues?: HealthIssue[] } | null
    healthIssues.value = r?.issues || []
  } catch {
    // Don't surface fetch errors as banners -- a flaky poll shouldn't
    // look like a server-side problem. The api client already handles
    // the 401 redirect.
  }
}

onMounted(() => {
  void authStore.loadMe()
  void pollHealth()
  healthTimer = setInterval(pollHealth, 30000)
})

onUnmounted(() => {
  clearInterval(healthTimer)
})
</script>

<template>
  <div class="shell">
    <AppSidebar />
    <main class="shell-main">
      <div v-if="healthIssues.length > 0" class="notice notice-danger health-banner" role="status">
        <div v-for="i in healthIssues" :key="i.component">
          <strong>{{ i.component }}:</strong> {{ i.message }}
        </div>
      </div>
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100vh;
}

.shell-main {
  flex: 1;
  min-width: 0;
  padding: var(--gutter);
  background: var(--bg-secondary);
}

.health-banner {
  margin-bottom: 16px;
}
</style>
