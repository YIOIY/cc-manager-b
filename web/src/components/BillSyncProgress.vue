<template>
  <section v-if="run" class="bill-progress panel">
    <div class="bill-progress-head">
      <div>
        <strong>账单同步进度</strong
        ><el-tag :type="billTone(run.state)">{{ billStateLabels[run.state] || run.state }}</el-tag>
      </div>
      <span>{{ run.from }} — {{ run.to }}</span>
    </div>
    <el-progress
      :percentage="percentage"
      :status="run.state === 'failed' ? 'exception' : run.state === 'succeeded' ? 'success' : undefined"
    />
    <div class="bill-progress-counts">
      <span>已处理 {{ run.completed }} / {{ run.total_items }} 个账号日</span
      ><span>更新 {{ run.counts.updated || 0 }}</span
      ><span>未变化 {{ run.counts.unchanged || 0 }}</span
      ><span>无返回 {{ run.counts.no_data || 0 }}</span
      ><span>失败 {{ run.counts.failed || 0 }}</span>
    </div>
    <p v-if="run.state === 'queued'" class="muted">已加入队列，后台会继续执行。</p>
    <p v-else-if="run.state === 'partial' || run.state === 'failed'" class="bill-warning">
      部分数据未能更新；已有账单已保留。请查看明细后重试失败项。
    </p>
    <p v-else-if="run.counts.no_data" class="bill-warning">无返回不代表零用量，对应日期的覆盖仍不完整。</p>
    <el-alert v-if="error" type="error" :title="error" :closable="false" />
    <div class="bill-task-actions">
      <el-select v-model="itemState" clearable placeholder="全部任务项" @change="loadItems(1)"
        ><el-option
          v-for="s in ['failed', 'no_data', 'updated', 'unchanged', 'superseded', 'queued', 'running']"
          :key="s"
          :label="billStateLabels[s]"
          :value="s" /></el-select
      ><el-button @click="toggleDetails">{{ details ? '收起明细' : '查看明细' }}</el-button
      ><el-button v-if="terminal && run.counts.failed" type="warning" :loading="retrying" @click="retry"
        >重试失败项</el-button
      >
    </div>
    <template v-if="details"
      ><el-table :data="items" empty-text="没有符合条件的任务项"
        ><el-table-column prop="account" label="账号" min-width="210" /><el-table-column
          prop="supplier_account_id"
          label="供应商账号 ID"
          min-width="135" /><el-table-column prop="bill_date" label="日期" width="120" /><el-table-column
          label="状态"
          width="130"
          ><template #default="{ row }">{{ billStateLabels[row.state] || row.state }}</template></el-table-column
        ><el-table-column prop="error_message" label="原因" min-width="240" /></el-table
      ><el-pagination
        :current-page="itemPage"
        :page-size="20"
        :total="itemTotal"
        layout="total, prev, pager, next"
        @current-change="loadItems"
    /></template>
  </section>
  <el-alert v-else-if="error" type="error" :title="error" :closable="false" />
</template>
<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { errorMessage } from '../api'
import { randomId } from '../utils/id'
import { billGet, billPost, billStateLabels, billTone, type BillRun } from '../bills-api'
const props = defineProps<{ runId: string }>(),
  emit = defineEmits<{ (event: 'finished', run: BillRun): void; (event: 'retried', result: any): void }>()
const run = ref<BillRun>(),
  error = ref(''),
  details = ref(false),
  itemState = ref('failed'),
  items = ref<any[]>([]),
  itemPage = ref(1),
  itemTotal = ref(0),
  retrying = ref(false)
let retryKey = ''
let timer: ReturnType<typeof setTimeout> | undefined,
  sequence = 0,
  stopped = false,
  notified = ''
const terminal = computed(() => !!run.value && !['queued', 'running'].includes(run.value.state)),
  percentage = computed(() =>
    run.value?.total_items ? Math.min(100, Math.floor((run.value.completed / run.value.total_items) * 100)) : 0,
  )
async function poll() {
  const seq = sequence,
    id = props.runId
  if (!id) return
  try {
    const result = await billGet<BillRun>('/sync-runs/' + encodeURIComponent(id))
    if (seq !== sequence || stopped) return
    run.value = result
    error.value = ''
    if (terminal.value) {
      if (notified !== id) {
        notified = id
        emit('finished', result)
      }
      if (details.value) await loadItems(itemPage.value)
    } else timer = setTimeout(poll, 3000)
  } catch (e) {
    if (seq === sequence && !stopped) {
      error.value = errorMessage(e)
      timer = setTimeout(poll, 5000)
    }
  }
}
function toggleDetails() {
  details.value = !details.value
  if (details.value) loadItems(1)
}
async function loadItems(page = 1) {
  const id = props.runId,
    seq = sequence
  try {
    const result = await billGet<any>(
      '/sync-runs/' +
        encodeURIComponent(id) +
        '/items?page=' +
        page +
        '&page_size=20&state=' +
        encodeURIComponent(itemState.value || ''),
    )
    if (seq !== sequence || stopped) return
    items.value = result.items
    itemPage.value = page
    itemTotal.value = result.pagination.total
  } catch (e) {
    if (seq === sequence) error.value = errorMessage(e)
  }
}
async function retry() {
  retrying.value = true
  try {
    if (!retryKey) retryKey = randomId()
    const result = await billPost('/sync-runs/' + encodeURIComponent(props.runId) + '/retry-failed', {}, retryKey)
    retryKey = ''
    emit('retried', result)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    retrying.value = false
  }
}
watch(
  () => props.runId,
  () => {
    sequence++
    retryKey = ''
    clearTimeout(timer)
    run.value = undefined
    items.value = []
    notified = ''
    poll()
  },
  { immediate: true },
)
onUnmounted(() => {
  stopped = true
  sequence++
  retryKey = ''
  clearTimeout(timer)
})
</script>
<style scoped>
.bill-progress {
  padding: 20px;
}
.bill-progress-head,
.bill-progress-head > div,
.bill-progress-counts,
.bill-task-actions {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}
.bill-progress-head {
  justify-content: space-between;
  margin-bottom: 18px;
}
.bill-progress-head > span,
.bill-progress-counts {
  font-size: 12px;
  color: #627883;
}
.bill-progress-counts {
  margin-top: 12px;
}
.bill-task-actions {
  margin: 15px 0;
}
.bill-task-actions .el-select {
  width: 160px;
}
.bill-warning {
  font-size: 12px;
  color: #a96815;
}
.el-pagination {
  margin-top: 14px;
  justify-content: flex-end;
}
</style>
