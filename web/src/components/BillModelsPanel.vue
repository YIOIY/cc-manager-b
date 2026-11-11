<template>
  <section class="bill-models">
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <div class="bill-models-toolbar">
      <el-select v-model="connectionId" placeholder="平台连接" style="width: 200px" @change="reload">
        <el-option
          v-for="c in connections"
          :key="c.id"
          :label="c.name + (c.web_configured ? '' : '（未配置网页登录）')"
          :value="c.id"
        />
      </el-select>
      <el-date-picker
        v-model="range"
        type="daterange"
        value-format="YYYY-MM-DD"
        range-separator="至"
        start-placeholder="开始"
        end-placeholder="结束"
        :clearable="false"
        style="width: 260px"
        @change="reload"
      />
      <el-select v-model="accountId" placeholder="全部账号" clearable filterable style="width: 240px" @change="reload">
        <el-option
          v-for="a in accountOptions"
          :key="a.account_id"
          :label="a.account || a.account_id"
          :value="a.account_id"
        />
      </el-select>
      <el-button :loading="loading" @click="reload"><RefreshCw :size="14" />刷新</el-button>
      <el-button type="primary" plain :loading="syncing" :disabled="!currentConnection?.web_configured" @click="syncNow"
        >立即同步</el-button
      >
      <span v-if="syncMessage" class="bill-models-sync">{{ syncMessage }}</span>
    </div>
    <p class="bill-models-note">
      来自供应商网页后台，每 2 小时同步最近 7 天，按天、按账号、按模型拆分，只有金额。当天数据约有 2
      小时延迟，标为"未出齐"。
    </p>
    <div v-if="data" class="bill-models-summary">
      <div class="summary-card">
        <small>合计</small><b class="purple-number">¥{{ data.total_cost_yuan }}</b>
        <span>{{ data.coverage.days_present }}/{{ data.coverage.days_requested }} 天有数据</span>
      </div>
      <div class="summary-card">
        <small>按账号</small>
        <ul>
          <li v-for="a in data.by_account.slice(0, 6)" :key="a.account_id">
            <span :title="a.account">{{ a.account || a.account_id }}</span
            ><b>¥{{ a.cost_yuan }}</b>
          </li>
          <li v-if="!data.by_account.length" class="muted">暂无数据</li>
        </ul>
      </div>
      <div class="summary-card">
        <small>按模型</small>
        <ul>
          <li v-for="m in data.by_model.slice(0, 6)" :key="m.model_name">
            <span>{{ m.model_name }}</span
            ><b>¥{{ m.cost_yuan }}</b>
          </li>
          <li v-if="!data.by_model.length" class="muted">暂无数据</li>
        </ul>
      </div>
    </div>
    <el-table :data="data?.list || []" v-loading="loading" size="small" row-key="rowKey">
      <el-table-column label="日期" width="120">
        <template #default="{ row }">
          {{ row.bill_date }}<span v-if="!row.final" class="mini-badge yellow pending">未出齐</span>
        </template>
      </el-table-column>
      <el-table-column label="账号" min-width="220">
        <template #default="{ row }">
          <span :title="row.account">{{ row.account || '（未关联本地账号）' }}</span>
          <small class="muted"> · 交付 {{ row.upstream_id }}</small>
        </template>
      </el-table-column>
      <el-table-column label="模型" min-width="160" prop="model_name" />
      <el-table-column label="金额（元）" width="130" align="right">
        <template #default="{ row }">¥{{ row.cost_yuan }}</template>
      </el-table-column>
      <el-table-column label="同步时间" width="170">
        <template #default="{ row }">{{ formatDateTime(row.fetched_at) }}</template>
      </el-table-column>
    </el-table>
    <div class="bill-models-pager">
      <el-pagination
        layout="total, sizes, prev, pager, next"
        :total="data?.total || 0"
        :page-size="pageSize"
        :page-sizes="[20, 50, 100]"
        :current-page="page"
        @current-change="onPage"
        @size-change="onSize"
      />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { api, ApiError, errorMessage } from '../api'
import ErrorNotice from './ErrorNotice.vue'
import type { BillModelsResponse, ConnectionOption } from '../types/api'
import { formatDateTime } from '../utils/format'

const connections = ref<ConnectionOption[]>([])
const connectionId = ref('')
const range = ref<[string, string]>(defaultRange())
const accountId = ref('')
const data = ref<BillModelsResponse | null>(null)
const loading = ref(false)
const syncing = ref(false)
const syncMessage = ref('')
const notice = ref<unknown | null>(null)
const page = ref(1)
const pageSize = ref(20)

const currentConnection = computed(() => connections.value.find((c) => c.id === connectionId.value))
const accountOptions = computed(() => data.value?.by_account || [])

function dayString(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
function defaultRange(): [string, string] {
  const end = new Date()
  const start = new Date(end.getTime() - 6 * 24 * 60 * 60 * 1000)
  return [dayString(start), dayString(end)]
}
async function load() {
  if (!connectionId.value) return
  loading.value = true
  notice.value = null
  try {
    const params = new URLSearchParams({
      connection_id: connectionId.value,
      from: range.value[0],
      to: range.value[1],
      page: String(page.value),
      page_size: String(pageSize.value),
    })
    if (accountId.value) params.set('account_ids', accountId.value)
    const res = (await api<BillModelsResponse>('/platform-usage/bills/models?' + params)).data
    data.value = {
      ...res,
      list: res.list.map((r) => ({ ...r, rowKey: r.upstream_id + '/' + r.bill_date + '/' + r.model_name })) as never,
    }
  } catch (e) {
    notice.value = e
  } finally {
    loading.value = false
  }
}
function reload() {
  page.value = 1
  void load()
}
function onPage(p: number) {
  page.value = p
  void load()
}
function onSize(s: number) {
  pageSize.value = s
  reload()
}
async function syncNow() {
  if (!connectionId.value || syncing.value) return
  syncing.value = true
  syncMessage.value = ''
  notice.value = null
  try {
    await api('/platform-usage/bills/models/sync', {
      method: 'POST',
      body: JSON.stringify({ connection_id: connectionId.value }),
    })
    syncMessage.value = '已开始同步，约 1 分钟后刷新查看'
  } catch (e) {
    if (e instanceof ApiError && e.code === 'SYNC_IN_BACKOFF') {
      const next = e.extra.details?.next_attempt_at
      syncMessage.value = '同步正在退避等待' + (typeof next === 'string' ? '，下次 ' + formatDateTime(next) : '')
    } else {
      syncMessage.value = errorMessage(e, '同步未能开始')
    }
  } finally {
    syncing.value = false
  }
}
onMounted(async () => {
  try {
    const list = (await api<ConnectionOption[]>('/lookups/platform-connections')).data
    connections.value = list
    connectionId.value = (list.find((c) => c.enabled && c.web_configured) || list.find((c) => c.enabled))?.id || ''
    await load()
  } catch (e) {
    notice.value = e
  }
})
</script>

<style scoped>
.mini-badge {
  display: inline-block;
  padding: 3px 7px;
  border-radius: 5px;
  font: 700 10px/1 monospace;
  background: var(--surface-muted, #f0f2f5);
  color: var(--text-secondary, #606266);
}
.mini-badge.teal {
  background: #e0f5ef;
  color: #0f8a6a;
}
.mini-badge.yellow {
  background: #fdf6d8;
  color: #8a6d00;
}
.mini-badge.attention {
  background: var(--el-color-danger-light-9);
  color: var(--el-color-danger);
}
.mini-badge + .mini-badge {
  margin-left: 4px;
}
.bill-models-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
.bill-models-sync {
  font-size: 13px;
  color: var(--text-secondary, #606266);
}
.bill-models-note {
  margin: 10px 0 14px;
  font-size: 12px;
  color: var(--text-tertiary, #909399);
}
.bill-models-summary {
  display: grid;
  grid-template-columns: 1fr 1.4fr 1.4fr;
  gap: 12px;
  margin-bottom: 14px;
}
.summary-card {
  padding: 12px 14px;
  border: 1px solid var(--line, #e8eaf0);
  border-radius: 12px;
  background: var(--surface, #fff);
  min-width: 0;
}
.summary-card small {
  display: block;
  font-size: 12px;
  color: var(--text-secondary, #606266);
  margin-bottom: 6px;
}
.summary-card > b {
  font-size: 20px;
  display: block;
}
.summary-card > span {
  font-size: 12px;
  color: var(--text-tertiary, #909399);
}
.summary-card ul {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.summary-card li {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  font-size: 13px;
}
.summary-card li span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.muted {
  color: var(--text-tertiary, #909399);
}
.mini-badge.pending {
  margin-left: 6px;
}
.bill-models-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
@media (max-width: 900px) {
  .bill-models-summary {
    grid-template-columns: 1fr;
  }
}
</style>
