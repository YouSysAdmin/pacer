<script setup lang="ts">
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ToastStack from '@/components/ToastStack.vue'
import { useThemeStore } from '@/stores/theme'

// For its side effect: the store's watchEffect stamps data-theme on
// the root element before first paint.
useThemeStore()
</script>

<template>
  <router-view />
  <!-- Both mounted ONCE, here: the confirm dialog is driven through
       useConfirm from anywhere, and the toast stack renders the global
       notification store. -->
  <ToastStack />
  <ConfirmDialog />
</template>
