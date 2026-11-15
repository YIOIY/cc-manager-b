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
          :title="day.date + ' ' + money(day.cost_fen) + (day.final ? '' : '（未出齐）')"
        >
          <i><em :style="{ height: Math.max(bars[i] || 0, day.cost_fen ? 4 : 0) + '%' }"></em></i>
          <small>{{ day.date.slice(5) }}</small>
          <b>{{ shortMoney(day.cost_fen) }}</b>
        </div>
      </div>
      <p class="spend-note">斜纹的一天是今天，数字还没出齐。</p>
    </template>
    <div v-else class="dashboard-muted panel-empty">暂无账单数据，配置网页登录后约 2 小时出现。</div>

    <template v-if="hourly">
      <div class="subsection-title hourly-title">
        <span>近 24 小时</span>
        <small v-if="summary.total"
          >共 {{ money(summary.total) }}，最高的一小时在 {{ hourLabel(summary.peakHour) }}（{{
            money(summary.peak)
          }}）</small
        >
      </div>
      <template v-if="summary.total">
        <div class="spend-hours" role="img" aria-label="近 24 小时每小时消费">
          <i
            v-for="h in hourBars"
            :key="h.hour"
            :title="hourLabel(h.hour) + ' ' + money(h.cost_fen)"
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
          ><i><em :style="{ width: a.width + '%' }"></em></i><strong>{{ money(a.cost_fen) }}</strong>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { DashboardPlatform, HourlyCost } from '../types/api'
import { hourlyBars, hourlySummary, spendBars } from '../model/dashboard'
import { fenToUsdCents, formatUsdCents } from '../model/money'

const props = defineProps<{
  platform: DashboardPlatform | undefined
  /** Spend per hour for the last 24 hours; undefined when it could not be loaded. */
  hourly?: HourlyCost[]
  usdRate?: number
}>()
const emit = defineEmits<{ bills: [] }>()

const days = computed(() => props.platform?.cost_by_day || [])
const bars = computed(() => spendBars(days.value))
const week = computed(() => Number(props.platform?.week_model_cost_fen || 0))
const hasData = computed(() => days.value.some((d) => d.cost_fen > 0))
const models = computed(() => {
  const list = props.platform?.cost_by_model || []
  const max = list[0]?.cost_fen || 1
  return list.map((m) => ({
    ...m,
    width: Math.max(4, (m.cost_fen / max) * 100),
    percent: week.value ? Math.round((m.cost_fen / week.value) * 100) : 0,
  }))
})
const accounts = computed(() => {
  const list = props.platform?.cost_by_account || []
  const max = list[0]?.cost_fen || 1
  return list.map((a) => ({ ...a, width: Math.max(4, (a.cost_fen / max) * 100) }))
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
function money(fen: number) {
  const cents = fenToUsdCents(fen, props.usdRate)
  return cents === null ? '—' : formatUsdCents(cents)
}
function shortMoney(fen: number) {
  const cents = fenToUsdCents(fen, props.usdRate)
  if (cents === null) return '—'
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
  min-width: 0;
}
.spend-day i {
  flex: 1;
  width: 100%;
  display: flex;
  align-items: flex-end;
  border-radius: 6px;
  background: #f1f6f5;
}
.spend-day em {
  display: block;
  width: 100%;
  border-radius: 6px;
  background: linear-gradient(180deg, #2bb594, #167c69);
}
.spend-day.pending em {
  background: repeating-linear-gradient(135deg, #8fd9c5, #8fd9c5 4px, #c9eee3 4px, #c9eee3 8px);
}
.spend-day small {
  font-size: 10px;
  color: var(--muted-soft);
}
.spend-day b {
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.spend-note {
  margin: 8px 0 0;
  font-size: 11px;
  color: var(--muted-soft);
}
.hourly-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-top: 18px;
}
.hourly-title small {
  color: var(--muted-soft);
  font-size: 11px;
  font-weight: 400;
  text-align: right;
}
.spend-hours {
  display: grid;
  grid-template-columns: repeat(24, 1fr);
  gap: 3px;
  align-items: end;
  height: 64px;
  padding-bottom: 1px;
  border-bottom: 1px solid var(--line-soft);
}
.spend-hours i {
  display: flex;
  align-items: flex-end;
  height: 100%;
}
.spend-hours em {
  display: block;
  width: 100%;
  min-height: 2px;
  border-radius: 3px 3px 0 0;
  background: #9fd8c8;
}
.spend-hours i.peak em {
  background: var(--accent-deep);
}
.spend-hours-axis {
  display: flex;
  justify-content: space-between;
  margin-top: 4px;
  color: var(--muted-soft);
  font:
    10px/1 'DM Mono',
    ui-monospace,
    monospace;
}
.spend-breakdown {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  margin-top: 18px;
}
.spend-breakdown .plan-row span {
  max-width: 110px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 980px) {
  .spend-breakdown {
    grid-template-columns: 1fr;
  }
}
</style>
