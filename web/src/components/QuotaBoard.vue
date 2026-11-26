<template>
  <section class="dashboard-panel quota-board">
    <div class="dashboard-panel-head">
      <div>
        <h2>配额看板</h2>
        <p class="panel-sub">每个账号两个窗口的占用，从最紧张的排起；刻线是 80%。</p>
      </div>
      <el-button v-if="rows.length" text type="primary" @click="emit('all')">全部账号</el-button>
    </div>
    <div v-if="failed" class="dashboard-muted panel-empty">配额数据暂时读取失败，点右上角「刷新」重试。</div>
    <div v-else-if="!rows.length" class="dashboard-muted panel-empty">
      {{ loading ? '正在读取…' : '还没有平台用量。账号上号并完成一次网页同步后会出现在这里。' }}
    </div>
    <template v-else>
      <div class="quota-head" aria-hidden="true">
        <span>账号</span><span>5 小时窗口</span><span>7 天窗口</span><span class="right">累计花费</span>
      </div>
      <ul class="quota-rows">
        <li
          v-for="row in shown"
          :key="row.account_id"
          :data-quota="row.account_id"
          role="button"
          tabindex="0"
          :class="{ off: isFailing(row.status) }"
          @click="emit('open', row)"
          @keyup.enter="emit('open', row)"
        >
          <div class="quota-account">
            <strong :title="row.account">{{ row.account || shortPlatformAlias(row.alias) || row.alias }}</strong>
            <small v-if="accountMeta(row)" class="meta" :title="accountMeta(row)">{{ accountMeta(row) }}</small>
            <small v-if="isFailing(row.status)" class="bad">{{ runtimeLabel(row.status) }}</small>
            <small v-else :title="row.alias">{{ shortPlatformAlias(row.alias) || row.alias }}</small>
          </div>
          <div v-for="w in windows(row)" :key="w.key" class="quota-gauge" :title="w.title">
            <template v-if="w.percent !== null">
              <i :class="'tone-' + usageTone(w.percent)"
                ><em :style="{ width: Math.min(100, w.percent) + '%' }"></em
              ></i>
              <b :class="'tone-' + usageTone(w.percent)">{{ Math.round(w.percent) }}%</b>
              <small>{{ w.reset }}</small>
            </template>
            <span v-else class="quota-none">无用量</span>
          </div>
          <div class="quota-cost">{{ costUsd(row) }}</div>
        </li>
      </ul>
      <button v-if="rows.length > limit" type="button" class="quota-more" @click="expanded = !expanded">
        {{ expanded ? '收起' : `展开其余 ${rows.length - limit} 个账号` }}
      </button>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { QuotaRow } from '../types/api'
import { runtimeLabel } from '../account-workflow'
import { formatResetAt, shortPlatformAlias, usageTone } from '../model/platform-card'
import { formatUsd } from '../model/money'

const props = withDefaults(defineProps<{ rows: QuotaRow[]; loading: boolean; failed?: boolean; limit?: number }>(), {
  failed: false,
  limit: 8,
})
const emit = defineEmits<{ open: [row: QuotaRow]; all: [] }>()

function costUsd(row: QuotaRow) {
  if (row.cost_usd_cents === null && !row.cost_usd) return '—'
  return formatUsd(row.cost_usd)
}

function ownerLabel(row: QuotaRow) {
  return (row.created_by_nickname || row.created_by_username || '').trim()
}

/** Under the account name: platform id and owner, same wording as account cards. */
function accountMeta(row: QuotaRow) {
  const parts: string[] = []
  if (row.platform_account_id) parts.push('ID ' + row.platform_account_id)
  const owner = ownerLabel(row)
  if (owner) parts.push('所属 ' + owner)
  return parts.join(' · ')
}

const expanded = ref(false)
const shown = computed(() => (expanded.value ? props.rows : props.rows.slice(0, props.limit)))

const failingStatuses = new Set([
  'ban',
  'banned',
  'check_fail',
  'login_failed',
  'bound_failed',
  'cooking_failed',
  're_login_failed',
  'sync_failed',
  'fault',
  'suspended',
  'arrearage',
])
const isFailing = (status: string) => failingStatuses.has(status)

function windows(row: QuotaRow) {
  const reset = (iso: string) => (iso ? '重置 ' + formatResetAt(iso).slice(5, 16) : '')
  return [
    {
      key: 'five',
      percent: row.five_hour_percent,
      reset: reset(row.five_hour_reset_at),
      title: '5 小时窗口' + (row.five_hour_reset_at ? '，重置于 ' + formatResetAt(row.five_hour_reset_at) : ''),
    },
    {
      key: 'week',
      percent: row.week_percent,
      reset: reset(row.week_reset_at),
      title: '7 天窗口' + (row.week_reset_at ? '，重置于 ' + formatResetAt(row.week_reset_at) : ''),
    },
  ]
}
</script>

<style scoped>
.quota-head,
.quota-rows li {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr) minmax(0, 1fr) 92px;
  align-items: center;
  gap: 18px;
}
.quota-head {
  padding: 0 8px 8px;
  border-bottom: 1px solid var(--line-soft);
  color: var(--muted-soft);
  font-size: 11px;
}
.quota-head .right {
  text-align: right;
}
.quota-rows {
  list-style: none;
  margin: 0;
  padding: 0;
}
.quota-rows li {
  padding: 9px 8px;
  border-bottom: 1px solid var(--line-soft);
  border-radius: 8px;
  cursor: pointer;
}
.quota-rows li:last-child {
  border-bottom: 0;
}
.quota-rows li:hover,
.quota-rows li:focus-visible {
  background: #eaf5ff;
  outline: none;
}
.quota-rows li.off {
  opacity: 0.72;
}
.quota-account {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.quota-account strong,
.quota-account small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.quota-account strong {
  color: var(--ink);
  font: 500 13px/1.5 inherit;
}
.quota-account small {
  color: var(--muted-soft);
  font-size: 11px;
  line-height: 1.4;
}
.quota-account small.meta {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.quota-account small.bad {
  color: var(--danger);
  font-weight: 600;
}
.quota-gauge {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 38px;
  grid-template-rows: auto auto;
  align-items: center;
  column-gap: 8px;
  min-width: 0;
}
/* The track carries a tick at 80%, where an account counts as near its limit. */
.quota-gauge i {
  position: relative;
  height: 6px;
  border-radius: 3px;
  background: var(--line-soft);
  overflow: hidden;
}
.quota-gauge i::after {
  content: '';
  position: absolute;
  left: 80%;
  top: 0;
  bottom: 0;
  width: 1px;
  background: rgba(28, 31, 35, 0.2);
}
.quota-gauge em {
  display: block;
  height: 100%;
  border-radius: 3px;
  background: var(--accent);
}
.quota-gauge i.tone-warn em {
  background: #e0a030;
}
.quota-gauge i.tone-danger em {
  background: var(--danger);
}
.quota-gauge b {
  text-align: right;
  color: var(--ink-mid);
  font: 600 12px/1 inherit;
  font-variant-numeric: tabular-nums;
}
.quota-gauge b.tone-warn {
  color: #a8721a;
}
.quota-gauge b.tone-danger {
  color: var(--danger);
}
.quota-gauge small {
  grid-column: 1 / -1;
  margin-top: 3px;
  color: var(--muted-soft);
  font-size: 10.5px;
  min-height: 13px;
}
.quota-none {
  grid-column: 1 / -1;
  color: var(--muted-soft);
  font-size: 11px;
}
.quota-cost {
  text-align: right;
  color: var(--ink-mid);
  font: 500 12px/1 inherit;
  font-variant-numeric: tabular-nums;
}
.quota-more {
  display: block;
  width: 100%;
  margin-top: 8px;
  padding: 7px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: transparent;
  color: var(--accent);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
.quota-more:hover {
  background: #eaf5ff;
}
@media (max-width: 760px) {
  .quota-head {
    display: none;
  }
  .quota-rows li {
    grid-template-columns: 1fr 1fr;
    row-gap: 8px;
  }
  .quota-account {
    grid-column: 1 / -1;
  }
  .quota-cost {
    grid-column: 1 / -1;
    text-align: left;
  }
}
</style>
