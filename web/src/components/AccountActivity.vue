<template>
  <section class="account-activity" aria-label="最近动作">
    <div class="activity-head"><strong>最近动作</strong></div>
    <p v-if="loading" class="activity-note">正在读取…</p>
    <p v-else-if="failed" class="activity-note">最近动作暂时读取失败。</p>
    <p v-else-if="!items.length" class="activity-note">暂无记录。</p>
    <ul v-else class="activity-list">
      <li v-for="item in items" :key="item.key" :class="'tone-' + item.tone">
        <time :datetime="item.at">{{ formatDateTime(item.at) }}</time>
        <span class="activity-label">{{ item.label }}</span>
        <small v-if="item.actor">{{ item.actor }}</small>
        <small v-if="item.detail" class="activity-detail" :title="item.detail">{{ item.detail }}</small>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '../api'
import { formatDateTime } from '../utils/format'
import { mergeActivity, type ActivityItem } from '../model/account-detail'
import type { AuditEvent, Job } from '../types/api'

const props = defineProps<{ accountId: string; canAudit: boolean; canJobs: boolean }>()
const items = ref<ActivityItem[]>([])
const loading = ref(true)
const failed = ref(false)
let sequence = 0

async function load() {
  const request = ++sequence
  loading.value = true
  failed.value = false
  const id = encodeURIComponent(props.accountId)
  try {
    // Each source needs its own permission; ask only for what the user may see.
    const [events, jobs] = await Promise.all([
      props.canAudit
        ? api<AuditEvent[]>(`/audit-events?resource_type=account&resource_id=${id}`).then((r) => r.data)
        : Promise.resolve([] as AuditEvent[]),
      props.canJobs ? api<Job[]>(`/jobs?account_id=${id}`).then((r) => r.data) : Promise.resolve([] as Job[]),
    ])
    if (request !== sequence) return
    // `contains` filters can also match longer ids; keep exact matches only.
    items.value = mergeActivity(
      events.filter((e) => e.resource_id === props.accountId),
      jobs.filter((j) => j.account_id === props.accountId),
    )
  } catch {
    if (request === sequence) failed.value = true
  } finally {
    if (request === sequence) loading.value = false
  }
}
watch(() => props.accountId, load, { immediate: true })
</script>

<style scoped>
.account-activity {
  margin-top: 16px;
}
.activity-head {
  margin-bottom: 6px;
  font-size: 13px;
}
.activity-note {
  margin: 0;
  padding: 10px 0;
  color: var(--muted);
  font-size: 12px;
}
.activity-list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.activity-list li {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 7px 0;
  border-top: 1px solid var(--line-soft);
  font-size: 12.5px;
}
.activity-list li:first-child {
  border-top: 0;
}
.activity-list time {
  flex: none;
  width: 116px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.activity-label {
  flex: none;
  color: var(--ink-soft);
}
.tone-bad .activity-label {
  color: var(--el-color-danger);
}
.tone-ok .activity-label {
  color: #3bb346;
}
.activity-list small {
  color: var(--muted);
}
.activity-detail {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
