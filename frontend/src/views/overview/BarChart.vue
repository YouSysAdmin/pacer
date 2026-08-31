<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// The "jobs per day" bar column chart on the overview. Plain divs
// scaled by percentage - at 31 columns max there is nothing an SVG
// or a chart library would add but weight.
import { computed } from 'vue'

export interface DayPoint {
  day: string
  completed: number
  failed: number
  cancelled: number
  reaped: number
}

const props = defineProps<{ series: DayPoint[] }>()

function total(d: DayPoint): number {
  return d.completed + d.failed + d.cancelled + d.reaped
}

function failTotal(d: DayPoint): number {
  return d.failed + d.cancelled + d.reaped
}

// Axis clamp to >=1 so an empty month doesn't divide by 0.
const seriesMax = computed(() => Math.max(1, ...props.series.map(total)))

// Day-of-month label for the bar axis ("01" .. "31"). Pulls the last
// segment of the YYYY-MM-DD string the backend already formatted.
function dayLabel(s: string): string {
  return (s || '').slice(-2)
}
</script>

<template>
  <div>
    <div class="bar-chart" aria-label="Daily completed and failed jobs">
      <div
        v-for="d in series"
        :key="d.day"
        class="bar-col"
        :title="`${d.day}: ${d.completed} ok, ${failTotal(d)} failed/cancelled`"
      >
        <div class="bar" :style="{ height: (total(d) / seriesMax) * 100 + '%' }">
          <div
            v-if="failTotal(d) > 0"
            class="bar-fail"
            :style="{ height: (failTotal(d) / total(d)) * 100 + '%' }"
          ></div>
        </div>
        <div class="bar-label">{{ dayLabel(d.day) }}</div>
      </div>
    </div>
    <div class="bar-legend">
      <span><span class="legend-dot ok"></span>completed</span>
      <span><span class="legend-dot crit"></span>failed / reaped</span>
      <span class="text-muted">peak {{ seriesMax }}/day</span>
    </div>
  </div>
</template>

<style scoped>
.bar-chart {
  display: flex;
  align-items: flex-end;
  gap: 3px;
  height: 160px;
  padding-top: 8px;
}

/* Grows to share the row, but capped: a month with one day of data
   would otherwise render that single day as a block the full width of
   the card, which reads as a filled area rather than as one bar. */
.bar-col {
  flex: 1 1 0;
  max-width: 44px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  height: 100%;
}

/* The column is the success colour; the failed share is painted over
   its top so one bar carries both numbers. */
.bar {
  display: flex;
  flex-direction: column;
  min-height: 2px;
  border-radius: 2px 2px 0 0;
  background: var(--success-500);
  overflow: hidden;
}

.bar-fail {
  background: var(--danger-500);
}

.bar-label {
  margin-top: 4px;
  font-family: var(--font-mono);
  font-size: 9px;
  text-align: center;
  color: var(--text-muted);
  overflow: hidden;
}

.bar-legend {
  display: flex;
  gap: 16px;
  margin-top: 10px;
  font-size: 12px;
  color: var(--text-secondary);
}

.bar-legend > span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.legend-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}

.legend-dot.ok {
  background: var(--success-500);
}

.legend-dot.crit {
  background: var(--danger-500);
}
</style>
