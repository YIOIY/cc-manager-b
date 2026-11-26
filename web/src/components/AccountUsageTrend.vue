<template>
  <section class="usage-trend" aria-label="用量趋势">
    <div class="trend-head">
      <strong>用量趋势</strong>
      <span class="trend-range" role="group" aria-label="时间范围">
        <button
          v-for="d in [7, 30]"
          :key="d"
          type="button"
          :class="{ on: days === d }"
          :data-days="d"
          @click="days = d"
        >
          {{ d }} 天
        </button>
      </span>
    </div>
    <p v-if="state === 'loading'" class="trend-note">正在读取…</p>
    <p v-else-if="state === 'failed'" class="trend-note">用量历史暂时读取失败。</p>
    <p v-else-if="points.length < 2" class="trend-note">还没有足够的用量记录，同步几次后会出现趋势。</p>
    <template v-else>
      <svg
        class="trend-lines"
        :viewBox="`0 0 ${W} ${H}`"
        preserveAspectRatio="none"
        role="img"
        aria-label="5 小时与 7 天占用"
      >
        <line v-for="g in [25, 50, 75]" :key="g" :x1="0" :x2="W" :y1="y(g)" :y2="y(g)" class="grid" />
        <polyline :points="line('five_hour_used_percent')" class="l5" />
        <polyline :points="line('week_used_percent')" class="l7" />
      </svg>
      <div class="trend-legend">
        <span class="k5">5 小时 {{ last.five_hour_used_percent }}%</span>
        <span class="k7">7 天 {{ last.week_used_percent }}%</span>
        <small>{{ rangeText }}</small>
      </div>
      <div v-if="spend.length" class="trend-spend" role="img" aria-label="每日花费">
        <div v-for="d in spend" :key="d.date" class="spend-bar" :title="d.date + ' ' + money(d.cents)">
          <i><em :style="{ height: Math.max(d.cents ? 4 : 0, (d.cents / spendMax) * 100) + '%' }"></em></i>
          <small>{{ d.date.slice(5) }}</small>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { dailySpend, downsample, type UsageSnapshot } from '../model/account-detail'
import { formatUsdCents } from '../model/money'

const props = defineProps<{ accountId: string }>()
const W = 600
const H = 110
const days = ref(7)
const state = ref<'loading' | 'ready' | 'failed'>('loading')
const list = ref<UsageSnapshot[]>([])
let sequence = 0

async function load() {
  const request = ++sequence
  state.value = 'loading'
  try {
    const res = await api<{ list: UsageSnapshot[] }>(
      `/accounts/${props.accountId}/platform-usage-history?days=${days.value}`,
    )
    if (request !== sequence) return
    list.value = res.data.list || []
    state.value = 'ready'
  } catch {
    if (request === sequence) state.value = 'failed'
  }
}
watch(() => [props.accountId, days.value], load, { immediate: true })

const points = computed(() => downsample(list.value))
const last = computed(() => points.value[points.value.length - 1]!)
const t0 = computed(() => Date.parse(points.value[0]?.observed_at || ''))
const t1 = computed(() => Date.parse(last.value?.observed_at || ''))
function y(percent: number) {
  return H - (Math.max(0, Math.min(100, percent)) / 100) * (H - 4) - 2
}
function line(key: 'five_hour_used_percent' | 'week_used_percent') {
  const span = Math.max(1, t1.value - t0.value)
  return points.value
    .map((p) => `${(((Date.parse(p.observed_at) - t0.value) / span) * W).toFixed(1)},${y(p[key]).toFixed(1)}`)
    .join(' ')
}
const spend = computed(() => dailySpend(list.value))
const spendMax = computed(() => Math.max(1, ...spend.value.map((d) => d.cents)))
function money(cents: number) {
  return formatUsdCents(cents)
}
const rangeText = computed(() => {
  const a = points.value[0]?.observed_at.slice(5, 16).replace('T', ' ')
  const b = last.value?.observed_at.slice(5, 16).replace('T', ' ')
  return a && b ? `${a} → ${b}` : ''
})
</script>

<style scoped>
.usage-trend {
  margin-top: 16px;
}
.trend-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  font-size: 13px;
}
.trend-range {
  display: inline-flex;
  padding: 2px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface-soft, #f5f6f8);
}
.trend-range button {
  padding: 2px 10px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--ink-mid, #606266);
  font-size: 12px;
  cursor: pointer;
}
.trend-range button.on {
  background: var(--surface, #fff);
  color: var(--accent-dark);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(28, 31, 35, 0.1);
}
.trend-note {
  margin: 0;
  padding: 14px 0;
  color: var(--muted);
  font-size: 12px;
}
.trend-lines {
  display: block;
  width: 100%;
  height: 110px;
  border: 1px solid var(--line-soft);
  border-radius: 6px;
  background: var(--surface-soft, #fafbfc);
}
.grid {
  stroke: var(--line-soft, #eef0f3);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.l5,
.l7 {
  fill: none;
  stroke-width: 1.6;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}
.l5 {
  stroke: var(--accent, #2f7cf6);
}
.l7 {
  stroke: #8f6bd6;
}
.trend-legend {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 6px;
  font-size: 12px;
  color: var(--ink-mid, #606266);
}
.trend-legend span::before {
  content: '';
  display: inline-block;
  width: 10px;
  height: 3px;
  margin-right: 5px;
  border-radius: 2px;
  vertical-align: middle;
}
.k5::before {
  background: var(--accent, #2f7cf6);
}
.k7::before {
  background: #8f6bd6;
}
.trend-legend small {
  margin-left: auto;
  color: var(--muted);
}
.trend-spend {
  display: flex;
  gap: 4px;
  align-items: flex-end;
  height: 54px;
  margin-top: 10px;
}
.spend-bar {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  min-width: 0;
  height: 100%;
}
.spend-bar i {
  display: flex;
  align-items: flex-end;
  width: 100%;
  flex: 1;
}
.spend-bar em {
  display: block;
  width: 100%;
  border-radius: 3px 3px 0 0;
  background: var(--accent, #2f7cf6);
  opacity: 0.75;
}
.spend-bar small {
  margin-top: 2px;
  color: var(--muted);
  font-size: 10px;
}
</style>
