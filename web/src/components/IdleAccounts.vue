<template>
  <section class="dashboard-panel idle-panel">
    <div class="dashboard-panel-head">
      <div>
        <h2>闲置账号</h2>
        <p class="panel-sub">在用、但 5 小时和 7 天用量都不到 10%；套餐剩得越多越靠前。</p>
      </div>
    </div>
    <ul v-if="rows.length" class="idle-list">
      <li
        v-for="row in rows"
        :key="row.account_id"
        :data-idle="row.account_id"
        role="button"
        tabindex="0"
        @click="emit('open', row)"
        @keyup.enter="emit('open', row)"
      >
        <div class="idle-account">
          <strong :title="row.account">{{ row.account || shortPlatformAlias(row.alias) }}</strong>
          <small v-if="shortPlatformAlias(row.alias)">{{ shortPlatformAlias(row.alias) }}</small>
        </div>
        <span class="idle-week" title="7 天窗口已用">7d {{ percent(row.week_percent) }}</span>
        <span class="idle-left" :class="{ soon: row.days_left !== null && row.days_left <= 7 }" :title="row.plan_end">{{
          row.days_left === null ? '到期未知' : `剩 ${row.days_left} 天`
        }}</span>
      </li>
    </ul>
    <div v-else class="dashboard-muted idle-empty">
      {{
        failed ? '数据暂时读取失败，点右上角「刷新」重试。' : loading ? '正在读取…' : '没有闲置账号，在用账号都有使用。'
      }}
    </div>
  </section>
</template>

<script setup lang="ts">
import type { IdleRow } from '../types/api'
import { shortPlatformAlias } from '../model/platform-card'

defineProps<{ rows: IdleRow[]; loading: boolean; failed?: boolean }>()
const emit = defineEmits<{ open: [row: IdleRow] }>()

function percent(value: number | null): string {
  if (value === null || value === undefined) return '—'
  return (Number.isInteger(value) ? value : value.toFixed(1)) + '%'
}
</script>

<style scoped>
.idle-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.idle-list li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 64px;
  align-items: center;
  gap: 12px;
  padding: 9px 6px;
  border-top: 1px solid var(--line-tint, #eef2f2);
  border-radius: 8px;
  cursor: pointer;
  font-size: 12.5px;
}
.idle-list li:first-child {
  border-top: 0;
}
.idle-list li:hover,
.idle-list li:focus-visible {
  background: var(--tint-50);
  outline: none;
}
.idle-account {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.idle-account strong,
.idle-account small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.idle-account strong {
  color: var(--ink);
  font-weight: 500;
}
.idle-account small {
  color: var(--muted);
  font-size: 11px;
}
.idle-week {
  color: var(--muted-strong);
  font-variant-numeric: tabular-nums;
}
.idle-left {
  text-align: right;
  color: var(--ink-mid);
  font-variant-numeric: tabular-nums;
}
.idle-left.soon {
  color: var(--pending);
}
.idle-empty {
  padding: 14px 0 6px;
}
</style>
