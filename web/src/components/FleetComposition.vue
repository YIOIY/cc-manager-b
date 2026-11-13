<template>
  <section class="dashboard-panel fleet-composition">
    <div class="dashboard-panel-head">
      <div>
        <h2>账号构成</h2>
        <p class="panel-sub">同一批账号，从四个角度看。</p>
      </div>
    </div>
    <div v-for="group in groups" :key="group.title" class="share-group">
      <div class="share-title">
        <span>{{ group.title }}</span
        ><small>{{ group.total }} 个</small>
      </div>
      <div v-if="group.segments.length" class="share-bar" role="img" :aria-label="group.title + '分布'">
        <i
          v-for="s in group.segments"
          :key="s.key"
          :style="{ width: s.percent + '%', background: s.color }"
          :title="s.label + ' ' + s.count + ' 个（' + s.percent + '%）'"
        ></i>
      </div>
      <div v-if="group.segments.length" class="share-legend">
        <span v-for="s in group.segments" :key="s.key"
          ><i :style="{ background: s.color }"></i>{{ s.label }} <b>{{ s.count }}</b></span
        >
      </div>
      <div v-else class="dashboard-muted">{{ group.empty }}</div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { runtimeLabel } from '../account-workflow'
import { deliveryLabels, deliveryOrder, expiryLabels, expiryOrder, shareSegments } from '../model/dashboard'
import { planLabel } from '../utils/format'

const props = defineProps<{
  statusCounts: Record<string, number>
  planCounts: Record<string, number>
  deliveryCounts: Record<string, number> | undefined
  expiryCounts: Record<string, number> | undefined
}>()

// Colour says something where it can: green is healthy, amber needs a look,
// red is broken; plans have no such meaning and get a neutral ramp.
const statusColors: Record<string, string> = { using: '#18a889', cooking: '#61b8d9', pending_activation: '#9db7e0' }
const deliveryColors: Record<string, string> = {
  linked: '#18a889',
  submitting: '#61b8d9',
  pending: '#c9d3d6',
  unknown: '#e0a030',
  failed: '#c9423d',
}
const expiryColors: Record<string, string> = {
  expired: '#c9423d',
  within_7: '#e0a030',
  within_30: '#ecd08a',
  later: '#18a889',
  unknown: '#c9d3d6',
}
const failing = new Set([
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
const planRamp = ['#0f8a70', '#4bb39a', '#8fd1bf', '#c5e6dc', '#9aa8ae']

function build(
  title: string,
  counts: Record<string, number> | undefined,
  order: string[],
  label: (key: string) => string,
  color: (key: string, index: number) => string,
  empty: string,
) {
  const segments = shareSegments(counts || {}, order).map((s, i) => ({
    ...s,
    label: label(s.key),
    color: color(s.key, i),
  }))
  return { title, segments, total: segments.reduce((n, s) => n + s.count, 0), empty }
}

const groups = computed(() => [
  build(
    '平台状态',
    props.statusCounts,
    ['using'],
    runtimeLabel,
    (key) => statusColors[key] || (failing.has(key) ? '#c9423d' : '#e0a030'),
    '配置网页登录后显示',
  ),
  build(
    '上号状态',
    props.deliveryCounts,
    deliveryOrder,
    (k) => deliveryLabels[k] || k,
    (k) => deliveryColors[k] || '#c9d3d6',
    '暂无账号',
  ),
  build(
    '套餐',
    props.planCounts,
    [],
    (k) => planLabel(k),
    (_k, i) => planRamp[Math.min(i, planRamp.length - 1)]!,
    '暂无账号',
  ),
  build(
    '到期',
    props.expiryCounts,
    expiryOrder,
    (k) => expiryLabels[k] || k,
    (k) => expiryColors[k] || '#c9d3d6',
    '暂无已上号账号',
  ),
])
</script>

<style scoped>
.share-group + .share-group {
  margin-top: 16px;
}
.share-title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 7px;
  color: var(--ink-mid);
  font-size: 12.5px;
  font-weight: 700;
}
.share-title small {
  color: var(--muted-soft);
  font-weight: 400;
  font-size: 11px;
}
.share-bar {
  display: flex;
  gap: 2px;
  height: 10px;
  border-radius: 99px;
  overflow: hidden;
}
.share-bar i {
  display: block;
  min-width: 4px;
}
.share-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-top: 7px;
  color: var(--muted-strong);
  font-size: 11.5px;
}
.share-legend span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.share-legend i {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}
.share-legend b {
  color: var(--ink-soft);
  font-family: 'DM Mono', ui-monospace, monospace;
}
</style>
