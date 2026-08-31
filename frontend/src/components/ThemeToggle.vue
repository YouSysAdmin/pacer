<script setup lang="ts">
// The three-way colour-mode control: light, system, dark.
//
// Three segments rather than a cycling button because "follow the
// system" is a standing instruction a person picks deliberately - a
// toggle that lands on it by accident reads as a theme that changes
// itself an hour later.
import { useThemeStore, type ThemeMode } from '@/stores/theme'
import { getIcon } from '@/layouts/icons'

const theme = useThemeStore()

const SEGMENTS: Array<{ mode: ThemeMode; icon: string; title: string }> = [
  { mode: 'light', icon: getIcon('sun'), title: 'Light' },
  { mode: 'system', icon: getIcon('monitor'), title: 'Follow the system' },
  { mode: 'dark', icon: getIcon('moon'), title: 'Dark' },
]
</script>

<template>
  <div class="theme-toggle" role="radiogroup" aria-label="Color theme">
    <button
      v-for="s in SEGMENTS"
      :key="s.mode"
      type="button"
      class="theme-seg"
      :class="{ sel: theme.mode === s.mode }"
      role="radio"
      :aria-checked="theme.mode === s.mode"
      :title="s.title"
      @click="theme.setMode(s.mode)"
    >
      <span aria-hidden="true" v-html="s.icon" />
    </button>
  </div>
</template>

<style scoped>
.theme-toggle {
  display: inline-flex;
  padding: 2px;
  gap: 2px;
  border: 1px solid var(--border-primary);
  border-radius: var(--radius-md);
  background: var(--bg-secondary);
}

.theme-seg {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 4px 8px;
  border: none;
  border-radius: calc(var(--radius-md) - 2px);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: var(--transition);
}

.theme-seg:hover {
  color: var(--text-secondary);
}

.theme-seg.sel {
  background: var(--bg-primary);
  color: var(--text-primary);
  box-shadow: var(--shadow-sm);
}
</style>
