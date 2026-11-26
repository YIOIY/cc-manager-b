<template>
  <div class="platform-usage-block">
    <template v-if="usage">
      <div v-for="row in windows" :key="row.key" class="usage-window" :data-window="row.key">
        <span class="usage-window-label">{{ row.label }}</span>
        <span class="usage-track"
          ><span class="usage-fill" :class="'tone-' + row.tone" :style="{ width: row.width + '%' }"></span
        ></span>
        <b class="usage-percent" :class="'tone-' + row.tone">{{ row.percent }}</b>
        <small class="usage-reset" :title="'重置时间 ' + row.resetFull">{{ row.reset }} 重置</small>
      </div>
      <div class="usage-summary">
        <b v-if="usd" class="usd-number" data-usd>{{ usd }}</b>
        <b v-else class="muted-number">—</b>
        <span
          :title="'累计输入 ' + formatTokens(usage.input_tokens) + ' · 累计输出 ' + formatTokens(usage.output_tokens)"
          >入 {{ compactTokens(usage.input_tokens) }} · 出 {{ compactTokens(usage.output_tokens) }}</span
        >
        <small :class="{ stale }" :title="'观察于 ' + formatResetAt(usage.observed_at)">{{
          stale ? '数据可能过期' : shortTime(usage.observed_at) + ' 同步'
        }}</small>
      </div>
    </template>
    <p v-else class="platform-usage-empty">平台未返回用量</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { PlatformUsage } from '../types/api'
import { formatResetAt, formatTokens, usageStale, usageTone } from '../model/platform-card'
import { formatUsd } from '../model/money'

const props = defineProps<{
  usage: PlatformUsage | null
  observedAt?: string
}>()
const stale = computed(() => usageStale(props.usage?.observed_at || props.observedAt))

const usd = computed(() => {
  const amount = props.usage?.cost_usd
  if (amount === null || amount === undefined || amount === '') return ''
  return formatUsd(amount)
})

function percentText(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return '—'
  const n = Number(value)
  return (Number.isInteger(n) ? String(n) : n.toFixed(1)) + '%'
}

// "17:30" when the moment is today, otherwise "11-22 00:00".
function shortTime(iso: string | null | undefined): string {
  const full = formatResetAt(iso)
  if (full === '—') return full
  const today = formatResetAt(new Date().toISOString()).slice(0, 10)
  return full.startsWith(today) ? full.slice(11, 16) : full.slice(5, 16)
}

const windows = computed(() => {
  const u = props.usage
  if (!u) return []
  return [
    { key: '5h', label: '5h', value: u.five_hour_used_percent, reset: u.five_hour_reset_at },
    { key: '7d', label: '7d', value: u.week_used_percent, reset: u.week_reset_at },
  ].map((w) => ({
    key: w.key,
    label: w.label,
    percent: percentText(w.value),
    width: Math.max(0, Math.min(100, Number(w.value) || 0)),
    tone: usageTone(w.value),
    reset: shortTime(w.reset),
    resetFull: formatResetAt(w.reset),
  }))
})

function compactTokens(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—'
  const v = Number(n)
  if (v >= 1e6) return (v / 1e6).toFixed(v >= 1e7 ? 1 : 2).replace(/\.?0+$/, '') + 'M'
  if (v >= 1e3) return (v / 1e3).toFixed(v >= 1e4 ? 0 : 1).replace(/\.0$/, '') + 'K'
  return String(v)
}
</script>

<style scoped>
.platform-usage-block {
  display: grid;
  gap: 7px;
  margin-top: 10px;
}
.usage-window {
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr) 44px auto;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.usage-window-label {
  color: var(--muted, #909399);
  font-weight: 600;
}
.usage-track {
  height: 6px;
  border-radius: 999px;
  background: var(--line-soft, #eef0f3);
  overflow: hidden;
}
.usage-fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--accent, #2f7cf6);
  transition: width 0.3s ease;
}
.usage-fill.tone-warn {
  background: var(--el-color-warning, #e6a23c);
}
.usage-fill.tone-danger {
  background: var(--el-color-danger, #f56c6c);
}
.usage-percent {
  text-align: right;
  font-variant-numeric: tabular-nums;
  color: var(--ink, #303133);
}
.usage-percent.tone-warn {
  color: var(--el-color-warning, #e6a23c);
}
.usage-percent.tone-danger {
  color: var(--el-color-danger, #f56c6c);
}
.usage-reset {
  color: var(--muted, #909399);
  font-size: 11px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.usage-summary {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  margin-top: 2px;
  font-size: 12px;
  color: var(--ink-mid, #606266);
}
.usage-summary span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.usage-summary small {
  margin-left: auto;
  flex: none;
  font-size: 11px;
  color: var(--muted, #909399);
}
.usage-summary small.stale {
  color: var(--el-color-warning, #e6a23c);
}
.usd-number {
  flex: none;
  font-size: 15px;
  color: #0062d6;
  font-variant-numeric: tabular-nums;
}
.muted-number {
  flex: none;
  color: var(--muted, #909399);
  font-weight: 500;
}
.platform-usage-empty {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--muted, #909399);
}
</style>
