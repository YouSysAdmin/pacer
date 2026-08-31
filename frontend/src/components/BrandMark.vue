<script setup lang="ts">
// The Pacer mark: three velocity chevrons, inline so it scales without
// an extra request and sits wherever text sits. The brand colours are
// baked in - the mark is the ONE thing that keeps its colour across
// themes, which is what makes it read as a logo rather than an icon.
//
// `size` is the rendered HEIGHT; width follows the 3:2 viewBox.
withDefaults(defineProps<{ size?: number }>(), { size: 24 })
</script>

<template>
  <svg :width="size * 1.5" :height="size" viewBox="0 0 120 80" role="img" aria-label="Pacer">
    <path d="M 0 80 L 30 40 L 0 0 L 20 0 L 50 40 L 20 80 Z" fill="#FF6B35" />
    <path d="M 35 80 L 65 40 L 35 0 L 55 0 L 85 40 L 55 80 Z" fill="#F7931E" />
    <path d="M 70 80 L 100 40 L 70 0 L 90 0 L 120 40 L 90 80 Z" fill="#FFC107" opacity="0.85" />
  </svg>
</template>
