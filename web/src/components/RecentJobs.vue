<template>
  <section class="dashboard-panel recent-jobs">
    <div class="dashboard-panel-head">
      <div>
        <h2>最近上号</h2>
        <p class="panel-sub">最近几次提交到平台的结果。</p>
      </div>
      <el-button text type="primary" @click="emit('all')">任务中心</el-button>
    </div>
    <ul v-if="jobs.length" class="job-rows">
      <li v-for="job in rows" :key="job.id" :class="'tone-' + job.outcome.tone">
        <i class="job-dot"></i>
        <div class="job-main">
          <strong :title="job.account">{{ job.account || '（账号已删除）' }}</strong>
          <span :title="job.outcome.label">{{ job.outcome.label }}</span>
        </div>
        <div class="job-meta">
          <span>{{ job.connection }}</span>
          <time :datetime="job.updated_at">{{ formatDateTime(job.updated_at) }}</time>
        </div>
      </li>
    </ul>
    <div v-else class="dashboard-muted panel-empty">还没有上号记录。在「账号管理」里新建账号并提交后会出现在这里。</div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { RecentJob } from '../types/api'
import { jobOutcome } from '../model/dashboard'
import { formatDateTime } from '../utils/format'

const props = defineProps<{ jobs: RecentJob[] }>()
const emit = defineEmits<{ all: [] }>()
const rows = computed(() => props.jobs.map((job) => ({ ...job, outcome: jobOutcome(job) })))
</script>

<style scoped>
.job-rows {
  list-style: none;
  margin: 0;
  padding: 0;
}
.job-rows li {
  display: grid;
  grid-template-columns: 9px minmax(0, 1fr) auto;
  align-items: center;
  gap: 11px;
  padding: 9px 4px;
  border-bottom: 1px solid var(--line-soft);
}
.job-rows li:last-child {
  border-bottom: 0;
}
.job-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--muted-soft);
}
.tone-success .job-dot {
  background: var(--accent);
}
.tone-danger .job-dot {
  background: var(--danger);
}
.tone-warning .job-dot {
  background: #e0a030;
}
.tone-info .job-dot {
  background: #61b8d9;
}
.job-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.job-main strong,
.job-main span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.job-main strong {
  color: var(--ink);
  font: 500 13px/1.5 inherit;
}
.job-main span {
  color: var(--muted-strong);
  font-size: 12px;
}
.tone-danger .job-main span {
  color: var(--danger);
}
.job-meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  color: var(--muted-soft);
  font-size: 11px;
  white-space: nowrap;
}
</style>
