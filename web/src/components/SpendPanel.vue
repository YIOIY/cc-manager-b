<template>
  <section class="dashboard-panel spend-panel">
    <div class="dashboard-panel-head">
      <div>
        <h2>消费</h2>
        <p class="panel-sub">按模型账单统计；今天的数据约有 2 小时延迟。</p>
      </div>
      <el-button text type="primary" @click="emit('bills')">按模型账单</el-button>
    </div>
    <template v-if="hasData">
      <div class="subsection-title">最近 7 天</div>
      <div class="spend-days" role="img" aria-label="最近 7 天每日消费">
        <div
          v-for="(day, i) in days"
          :key="day.date"
          class="spend-day"
          :class="{ pending: !day.final }"
          :title="day.date + ' ' + money(day)"
        >
          <i><em :style="{ height: Math.max(bars[i] || 0, day.cost_usd_cents ? 4 : 0) + '%' }"></em></i>
          <small>{{ day.date.slice(5) }}</small>
          <b>{{ shortMoney(day.cost_usd_cents) }}</b>
        </div>
      </div>
      <p class="spend-note">斜纹的一天是今天。</p>
    </template>
    <div v-else class="dashboard-muted panel-empty">暂无账单数据，配置网页登录后约 2 小时出现。</div>

    <template v-if="hourly">
      <div class="subsection-title hourly-title">
        <span>近 24 小时</span>
        <small v-if="summary.total"
          >共 {{ formatUsdCents(summary.total) }}，最高的一小时在 {{ hourLabel(summary.peakHour) }}（{{
            formatUsdCents(summary.peak)
          }}）</small
        >
      </div>
      <template v-if="summary.total">
        <div class="spend-hours" role="img" aria-label="近 24 小时每小时消费">
          <i
            v-for="h in hourBars"
            :key="h.hour"
            :title="hourLabel(h.hour) + ' ' + formatUsd(h.cost_usd)"
            :class="{ peak: h.hour === summary.peakHour }"
            ><em :style="{ height: h.height + '%' }"></em
          ></i>
        </div>
        <div class="spend-hours-axis" aria-hidden="true">
          <span v-for="h in axis" :key="h">{{ hourLabel(h) }}</span>
        </div>
      </template>
      <p v-else class="dashboard-muted">这 24 小时里没有记录到新的花费。</p>
    </template>

    <div v-if="hasData" class="spend-breakdown">
      <div>
        <div class="subsection-title">按模型</div>
        <div v-for="m in models" :key="m.model_name" class="plan-row">
          <span :title="m.model_name">{{ m.model_name }}</span
          ><i><em :style="{ width: m.width + '%' }"></em></i><strong>{{ m.percent }}%</strong>
        </div>
      </div>
      <div>
        <div class="subsection-title">按账号</div>
        <div v-for="a in accounts" :key="a.account_id" class="plan-row">
          <span :title="a.account">{{ a.account || '未归属' }}</span
          ><i><em :style="{ width: a.width + '%' }"></em></i><strong>{{ formatUsd(a.cost_usd) }}</strong>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { DashboardPlatform, HourlyCost, SpendDay } from '../types/api'
import { hourlyBars, hourlySummary, spendBars } from '../model/dashboard'
import { formatUsd, formatUsdCents } from '../model/money'

const props = defineProps<{
  platform: DashboardPlatform | undefined
  /** Spend per hour for the last 24 hours; undefined when it could not be loaded. */
  hourly?: HourlyCost[]
}>()
const emit = defineEmits<{ bills: [] }>()

const days = computed(() => props.platform?.cost_by_day || [])
const bars = computed(() => spendBars(days.value))
const week = computed(() => Number(props.platform?.week_model_cost_usd_cents || 0))
const hasData = computed(() => days.value.some((d) => d.cost_usd_cents > 0))
const models = computed(() => {
  const list = props.platform?.cost_by_model || []
  const max = list[0]?.cost_usd_cents || 1
  return list.map((m) => ({
    ...m,
    width: Math.max(4, (m.cost_usd_cents / max) * 100),
    percent: week.value ? Math.round((m.cost_usd_cents / week.value) * 100) : 0,
  }))
})
const accounts = computed(() => {
  const list = props.platform?.cost_by_account || []
  const max = list[0]?.cost_usd_cents || 1
  return list.map((a) => ({ ...a, width: Math.max(4, (a.cost_usd_cents / max) * 100) }))
})

const hourBars = computed(() => hourlyBars(props.hourly || []))
const summary = computed(() => hourlySummary(props.hourly || []))
// A label every six hours is enough to read the chart.
const axis = computed(() =>
  (props.hourly || []).filter((_, i, all) => i % 6 === 0 || i === all.length - 1).map((h) => h.hour),
)
function hourLabel(iso: string | null) {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : String(d.getHours()).padStart(2, '0') + ':00'
}
function money(day: SpendDay) {
  return formatUsd(day.cost_usd)
}
function shortMoney(cents: number) {
  const dollars = cents / 100
  if (dollars >= 1000) return '$' + (dollars / 1000).toFixed(1) + 'k'
  if (dollars >= 100) return '$' + dollars.toFixed(0)
  return formatUsdCents(cents)
}
</script>

<style scoped>
.spend-days {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 8px;
  align-items: end;
  height: 120px;
}
.spend-day {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  height: 100%;
  justify-content: flex-end;
}
.spend-day i {
  display: block;
  width: 100%;
  max-width: 28px;
  height: 72px;
  border-radius: 6px 6px 2px 2px;
  background: color-mix(in srgb, var(--el-color-primary) 12%, transparent);
  position: relative;
  overflow: hidden;
}
.spend-day i em {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--el-color-primary);
  border-radius: 6px 6px 2px 2px;
}
.spend-day.pending i {
  background: repeating-linear-gradient(
    -45deg,
    color-mix(in srgb, var(--el-color-primary) 10%, transparent),
    color-mix(in srgb, var(--el-color-primary) 10%, transparent) 4px,
    color-mix(in srgb, var(--el-color-primary) 18%, transparent) 4px,
    color-mix(in srgb, var(--el-color-primary) 18%, transparent) 8px
  );
}
.spend-day small {
  color: var(--el-text-color-secondary);
  font-size: 11px;
}
.spend-day b {
  font-size: 11px;
  font-weight: 600;
}
.spend-note {
  margin: 8px 0 0;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.hourly-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-top: 18px;
}
.hourly-title small {
  font-weight: 400;
  color: var(--el-text-color-secondary);
}
.spend-hours {
  display: flex;
  align-items: flex-end;
  gap: 3px;
  height: 56px;
  margin-top: 8px;
}
.spend-hours i {
  flex: 1;
  height: 100%;
  display: block;
  position: relative;
  border-radius: 3px 3px 1px 1px;
  background: color-mix(in srgb, var(--el-color-primary) 10%, transparent);
  overflow: hidden;
}
.spend-hours i em {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--el-color-primary);
  opacity: 0.75;
}
.spend-hours i.peak em {
  opacity: 1;
}
.spend-hours-axis {
  display: flex;
  justify-content: space-between;
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 11px;
}
.spend-breakdown {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  margin-top: 18px;
}
.plan-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(40px, 2fr) auto;
  gap: 8px;
  align-items: center;
  margin-top: 6px;
  font-size: 12px;
}
.plan-row span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.plan-row i {
  display: block;
  height: 6px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--el-color-primary) 12%, transparent);
  overflow: hidden;
}
.plan-row i em {
  display: block;
  height: 100%;
  background: var(--el-color-primary);
}
.plan-row strong {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
.subsection-title {
  font-size: 13px;
  font-weight: 600;
}
.panel-empty {
  padding: 12px 0;
}
@media (max-width: 720px) {
  .spend-breakdown {
    grid-template-columns: 1fr;
  }
}
</style>
