<template>
  <section class="dashboard-panel spend-panel">
    <div class="dashboard-panel-head">
      <div>
        <span class="panel-kicker">SPEND · 7 DAYS</span>
        <h2>消费（最近 7 天）</h2>
      </div>
      <el-button text type="primary" @click="emit('bills')">按模型账单 <ArrowUpRight :size="14" /></el-button>
    </div>
    <template v-if="hasData">
      <div class="spend-days" role="img" aria-label="最近 7 天每日消费">
        <div
          v-for="(day, i) in days"
          :key="day.date"
          class="spend-day"
          :class="{ pending: !day.final }"
          :title="day.date + ' ' + formatMoneyFen(day.cost_fen)"
        >
          <i><em :style="{ height: Math.max(bars[i] || 0, day.cost_fen ? 4 : 0) + '%' }"></em></i>
          <small>{{ day.date.slice(5) }}</small>
          <b>{{ shortMoney(day.cost_fen) }}</b>
        </div>
      </div>
      <p class="spend-note">今天的数据约 2 小时延迟，标为"未出齐"。</p>
      <div class="spend-breakdown">
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
            <span :title="a.account">{{ a.account || a.account_id }}</span
            ><i><em :style="{ width: a.width + '%' }"></em></i><strong>{{ formatMoneyFen(a.cost_fen) }}</strong>
          </div>
        </div>
      </div>
    </template>
    <div v-else class="dashboard-muted spend-empty">暂无账单数据，配置网页登录后约 2 小时出现。</div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { ArrowUpRight } from 'lucide-vue-next'
import type { DashboardPlatform } from '../types/api'
import { spendBars } from '../model/dashboard'
import { formatMoneyFen } from '../utils/format'

const props = defineProps<{ platform: DashboardPlatform | undefined }>()
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
function shortMoney(fen: number) {
  const yuan = fen / 100
  if (yuan >= 10000) return (yuan / 10000).toFixed(1) + '万'
  if (yuan >= 1000) return (yuan / 1000).toFixed(1) + 'k'
  return yuan.toFixed(0)
}
</script>

<style scoped>
.spend-days {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 8px;
  align-items: end;
  height: 132px;
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
  color: var(--text-tertiary, #909399);
}
.spend-day b {
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.spend-note {
  margin: 8px 0 12px;
  font-size: 11px;
  color: var(--text-tertiary, #909399);
}
.spend-breakdown {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.spend-breakdown .plan-row span {
  max-width: 110px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.spend-empty {
  padding: 18px 0 6px;
}
@media (max-width: 980px) {
  .spend-breakdown {
    grid-template-columns: 1fr;
  }
}
</style>
