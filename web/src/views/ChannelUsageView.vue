<template>
  <PageHead eyebrow="CHANNEL USAGE" title="渠道用量" />
  <ErrorNotice :error="notice" compact closable @close="notice = null" />
  <div class="cu-toolbar">
    <el-select class="cu-conn" v-model="connectionId" placeholder="平台连接" @change="onConnectionChange">
      <el-option
        v-for="c in connections"
        :key="c.id"
        :label="c.name + (c.web_configured ? '' : '（未配置网页登录）')"
        :value="c.id"
      />
    </el-select>
    <el-date-picker
      class="cu-dates"
      v-model="range"
      type="daterange"
      value-format="YYYY-MM-DD"
      range-separator="至"
      start-placeholder="开始"
      end-placeholder="结束"
      :clearable="false"
    />
    <el-select
      class="cu-accounts"
      v-model="upstreamIds"
      multiple
      filterable
      clearable
      placeholder="账号 ID（上游交付）"
    >
      <el-option v-for="a in filteredAccounts" :key="a.upstream_id" :label="optionLabel(a)" :value="a.upstream_id" />
    </el-select>
    <el-select
      v-if="isAdmin"
      class="cu-owners"
      v-model="ownerUserIds"
      multiple
      filterable
      clearable
      placeholder="所属人"
      @change="onOwnerChange"
    >
      <el-option v-for="o in owners" :key="o.id" :label="ownerLabel(o)" :value="o.id" />
    </el-select>
    <el-button class="cu-query" type="primary" :loading="loading" :disabled="!connectionId" @click="reload"
      ><Search :size="14" />查询</el-button
    >
  </div>
  <p class="cu-note">数据来自供应商网页后台同步，约每 2 小时更新一次。</p>
  <el-table
    class="cu-table"
    :data="data?.list || []"
    v-loading="loading"
    size="small"
    empty-text="暂无数据"
    table-layout="fixed"
  >
    <el-table-column label="日期" min-width="120" prop="bill_date" />
    <el-table-column label="账号 ID" min-width="120">
      <template #default="{ row }">
        <span class="mono">{{ row.platform_account_id }}</span>
      </template>
    </el-table-column>
    <el-table-column label="账号" min-width="120" prop="account" show-overflow-tooltip>
      <template #default="{ row }">
        {{ row.account || '（未关联本地账号）' }}
      </template>
    </el-table-column>
    <el-table-column label="所属人" min-width="120" show-overflow-tooltip>
      <template #default="{ row }">
        {{ row.owner_nickname || row.owner_username || '—' }}
      </template>
    </el-table-column>
    <el-table-column label="消耗额度（美元）" min-width="120" align="right">
      <template #default="{ row }">
        <b>${{ row.cost_usd }}</b>
      </template>
    </el-table-column>
  </el-table>
  <div class="cu-pager">
    <el-pagination
      background
      layout="total, sizes, prev, pager, next"
      :total="data?.total || 0"
      :page-size="pageSize"
      :current-page="page"
      :page-sizes="[20, 50, 100]"
      @current-change="onPage"
      @size-change="onSize"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Search } from 'lucide-vue-next'
import { api } from '../api'
import ErrorNotice from '../components/ErrorNotice.vue'
import PageHead from '../components/PageHead.vue'
import { useSession } from '../composables/useSession'
import type {
  ChannelUsageAccount,
  ChannelUsageAccountsResponse,
  ChannelUsageOwner,
  ChannelUsageResponse,
  ConnectionOption,
} from '../types/api'

const { user } = useSession()
const isAdmin = computed(() => !!user.value?.is_admin)

const connections = ref<ConnectionOption[]>([])
const connectionId = ref('')
const range = ref<[string, string]>(defaultRange())
const upstreamIds = ref<string[]>([])
const ownerUserIds = ref<string[]>([])
const accountOptions = ref<ChannelUsageAccount[]>([])
const owners = ref<ChannelUsageOwner[]>([])
const data = ref<ChannelUsageResponse | null>(null)
const loading = ref(false)
const notice = ref<unknown | null>(null)
const page = ref(1)
const pageSize = ref(50)

/** Account picker follows the owner filter; empty owners shows everyone. */
const filteredAccounts = computed(() => {
  if (!ownerUserIds.value.length) return accountOptions.value
  const allowed = new Set(ownerUserIds.value)
  return accountOptions.value.filter((a) => a.owner_user_id && allowed.has(a.owner_user_id))
})

function dayString(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
function defaultRange(): [string, string] {
  const end = new Date()
  const start = new Date(end.getTime() - 6 * 24 * 60 * 60 * 1000)
  return [dayString(start), dayString(end)]
}
function optionLabel(a: ChannelUsageAccount) {
  const id = a.platform_account_id || a.upstream_id
  const account = a.account || '未关联'
  const owner = a.owner_nickname || a.owner_username || '—'
  return `ID ${id} · ${account} · ${owner}`
}
function ownerLabel(o: ChannelUsageOwner) {
  return o.nickname || o.username || o.id
}
/** Drop selected accounts that no longer match the owner filter. */
function pruneUpstreamSelection() {
  const allowed = new Set(filteredAccounts.value.map((a) => a.upstream_id))
  upstreamIds.value = upstreamIds.value.filter((id) => allowed.has(id))
}
function onOwnerChange() {
  pruneUpstreamSelection()
}
async function loadAccounts() {
  if (!connectionId.value) {
    accountOptions.value = []
    owners.value = []
    return
  }
  const res = (
    await api<ChannelUsageAccountsResponse>(
      '/platform-usage/channel-usage/accounts?connection_id=' + encodeURIComponent(connectionId.value),
    )
  ).data
  accountOptions.value = res.list || []
  owners.value = res.owners || []
  pruneUpstreamSelection()
}
async function load() {
  if (!connectionId.value || !range.value?.[0] || !range.value?.[1]) return
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
    if (upstreamIds.value.length) params.set('upstream_ids', upstreamIds.value.join(','))
    if (isAdmin.value && ownerUserIds.value.length) params.set('owner_user_ids', ownerUserIds.value.join(','))
    data.value = (await api<ChannelUsageResponse>('/platform-usage/channel-usage?' + params)).data
  } catch (e) {
    notice.value = e
    data.value = null
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
async function onConnectionChange() {
  upstreamIds.value = []
  ownerUserIds.value = []
  data.value = null
  try {
    await loadAccounts()
  } catch (e) {
    notice.value = e
  }
}
onMounted(async () => {
  try {
    const list = (await api<ConnectionOption[]>('/lookups/platform-connections')).data
    connections.value = list
    connectionId.value = (list.find((c) => c.enabled && c.web_configured) || list.find((c) => c.enabled))?.id || ''
    await loadAccounts()
  } catch (e) {
    notice.value = e
  }
})
</script>

<style scoped>
.cu-toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: nowrap;
  margin-bottom: 10px;
}
.cu-conn {
  flex: 0 0 120px;
  width: 120px;
}
.cu-dates {
  flex: 0 0 180px;
  width: 180px;
}
.cu-accounts {
  flex: 1 1 auto;
  min-width: 0;
}
.cu-owners {
  flex: 0 0 200px;
  width: 200px;
  max-width: 200px;
}
.cu-query {
  flex: 0 0 auto;
}
.cu-toolbar :deep(.el-select),
.cu-toolbar :deep(.el-date-editor) {
  width: 100%;
}
.cu-owners :deep(.el-select__selection) {
  flex-wrap: wrap;
}
.cu-note {
  margin: 0 0 14px;
  font-size: 12px;
  color: var(--muted-strong);
}
.cu-table {
  width: 100%;
}
.cu-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
