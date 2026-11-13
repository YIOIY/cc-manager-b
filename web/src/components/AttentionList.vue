<template>
  <section class="dashboard-panel attention-panel">
    <div class="dashboard-panel-head">
      <div>
        <h2>需要处理</h2>
        <p class="panel-sub">接近限额、封禁、快到期或和平台对不上的账号。</p>
      </div>
      <el-button v-if="items.length" text type="primary" @click="emit('all')"
        >查看全部 <ArrowUpRight :size="14"
      /></el-button>
    </div>
    <div v-if="summary.length" class="attention-summary">
      <span v-for="s in summary" :key="s.kind"
        >{{ s.label }} <b>{{ s.count }}</b></span
      >
    </div>
    <ul v-if="items.length" class="attention-list">
      <li
        v-for="item in items"
        :key="item.kind + ':' + item.upstream_id + ':' + item.account_id"
        :data-attention="item.upstream_id || item.account_id"
        :class="'severity-' + item.severity"
        role="button"
        tabindex="0"
        @click="emit('open', item)"
        @keyup.enter="emit('open', item)"
      >
        <i class="attention-dot"></i>
        <div class="attention-account">
          <strong :title="item.account">{{ item.account || item.alias || item.upstream_id }}</strong>
          <small v-if="item.alias && item.alias !== item.account" :title="item.alias">{{ item.alias }}</small>
        </div>
        <div class="attention-what">
          <span class="attention-label">{{ attentionLabel(item) }}</span>
          <small v-if="item.detail" :title="item.detail">{{ item.detail }}</small>
        </div>
        <ArrowUpRight :size="14" class="attention-arrow" />
      </li>
    </ul>
    <div v-else class="dashboard-muted attention-empty">
      {{ failed ? '清单暂时读取失败，点右上角「刷新」重试。' : loading ? '正在读取…' : '暂无需要处理的账号' }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { ArrowUpRight } from 'lucide-vue-next'
import type { AttentionItem, AttentionKind } from '../types/api'
import { attentionLabel } from '../model/dashboard'

const props = defineProps<{
  items: AttentionItem[]
  counts: Partial<Record<AttentionKind, number>>
  failed?: boolean
  loading: boolean
}>()
const emit = defineEmits<{ open: [item: AttentionItem]; all: [] }>()

const kindLabels: Record<AttentionKind, string> = {
  near_limit: '接近限额',
  banned: '封禁 / 失败',
  expiring: '即将到期',
  missing: '平台已移除',
  profile_diff: '资料不一致',
}
const summary = computed(() =>
  (Object.keys(kindLabels) as AttentionKind[])
    .map((kind) => ({ kind, label: kindLabels[kind], count: props.counts[kind] || 0 }))
    .filter((s) => s.count > 0),
)
</script>

<style scoped>
.attention-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  margin-bottom: 12px;
  font-size: 12px;
  color: var(--text-secondary, #606266);
}
.attention-summary b {
  margin-left: 2px;
  color: var(--text-primary, #303133);
}
.attention-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.attention-list li {
  display: grid;
  grid-template-columns: 10px minmax(0, 1.1fr) minmax(0, 1fr) 16px;
  align-items: center;
  gap: 12px;
  padding: 10px 6px;
  border-top: 1px solid var(--line-tint, #eef2f2);
  cursor: pointer;
  border-radius: 8px;
}
.attention-list li:first-child {
  border-top: 0;
}
.attention-list li:hover {
  background: #f6fbfa;
}
.attention-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #93a3ab;
}
.severity-danger .attention-dot {
  background: var(--el-color-danger);
}
.severity-warn .attention-dot {
  background: var(--el-color-warning);
}
.attention-account,
.attention-what {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.attention-account strong,
.attention-account small,
.attention-what small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attention-account strong {
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.attention-account small,
.attention-what small {
  font-size: 11px;
  color: var(--text-tertiary, #909399);
}
.attention-label {
  font-size: 13px;
}
.severity-danger .attention-label {
  color: var(--el-color-danger);
  font-weight: 600;
}
.severity-warn .attention-label {
  color: #b06b1d;
}
.attention-arrow {
  color: #93a3ab;
}
.attention-empty {
  padding: 18px 0 6px;
}
</style>
