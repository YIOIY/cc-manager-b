<template>
  <PageHead :eyebrow="route.meta.eyebrow || 'WORKSPACE'" :title="route.meta.title" :description="config.description">
    <el-button v-if="listKey === 'accounts' && reconcileConnection" @click="reconcileOpen = true"
      ><ListChecks :size="16" />平台对账</el-button
    >
    <el-button v-if="listKey === 'accounts' && canImportAccounts" @click="openImport"
      ><FileJson :size="16" />JSON 导入</el-button
    >
    <el-button v-if="createLabel && canCreate" type="primary" @click="openCreate"
      ><Plus :size="16" />{{ createLabel }}</el-button
    >
  </PageHead>
  <el-alert v-if="page.error.value" :title="page.error.value" type="error" show-icon @close="page.error.value = ''" />
  <ProxyNetworkPanel v-if="listKey === 'proxies'" ref="proxyNetworkPanel" @changed="page.load" />
  <div class="list-workspace panel" :class="{ 'platform-monitor': listKey === 'accounts' }">
    <div class="list-heading">
      <div>
        <span class="heading-icon small"><component :is="headingIcon" :size="18" /></span>
        <h2>{{ config.name }}</h2>
        <span class="count-badge">{{ page.total.value }}</span>
      </div>
      <el-button text :loading="page.loading.value" @click="page.load"><RefreshCw :size="15" />刷新</el-button>
    </div>
    <ListFilters
      v-model:draft-query="page.draftQuery.value"
      v-model:draft-conditions="page.draftConditions.value"
      v-model:filters-open="page.filtersOpen.value"
      :config="config"
      :query="page.query.value"
      :conditions="page.conditions.value"
      :active-fields="page.activeFields.value"
      :active-filter-count="page.activeFilterCount.value"
      :has-conditions="page.hasConditions.value"
      :abnormal-only="page.abnormalOnly.value"
      @apply="applySearch"
      @reset="page.resetFilters"
      @remove-query="page.removeQuery"
      @remove-condition="page.removeCondition"
      @toggle-abnormal="page.toggleAbnormal"
      @clear-abnormal="page.clearAbnormal"
    />
    <el-alert
      v-if="actionMessage"
      class="action-feedback"
      :title="actionMessage"
      :type="actionType"
      show-icon
      @close="actionMessage = ''"
    />
    <AccountCards
      v-if="listKey === 'accounts'"
      :accounts="accountsPaged"
      :loading="page.loading.value"
      @select="selectAccount"
      @edit-platform="editPlatform"
      @reauthorize="reauthorizeAccount"
      @assign="openAssignAccount"
    />
    <div v-else class="table-scroll">
      <el-table v-loading="page.loading.value" :data="page.pagedItems.value" row-key="id">
        <el-table-column :label="identityLabel" :min-width="220">
          <template #default="{ row }">
            <div class="primary-cell">
              <span v-if="listKey === 'proxies'" class="record-icon"><Network :size="17" /></span>
              <div>
                <strong>{{ identity(row) }}</strong>
                <small v-if="row.id && row.id !== identity(row)" :title="String(row.id)">{{ row.id }}</small>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          v-for="column in config.columns"
          :key="column.field"
          :label="column.label"
          :min-width="column.minWidth"
          :width="column.width"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <el-tag
              v-if="column.format === 'status'"
              :type="statusType(column.field, row[column.field])"
              effect="light"
              >{{ statusText(column.field, row[column.field]) }}</el-tag
            >
            <el-tag
              v-else-if="column.format === 'boolean'"
              :type="row[column.field] ? 'success' : 'info'"
              effect="plain"
              >{{ row[column.field] ? '已配置' : '未配置' }}</el-tag
            >
            <span
              v-else-if="column.field === 'proxy_id'"
              :class="row.proxy_host || row.proxy_id ? 'bound-proxy' : 'muted'"
              :title="row.proxy_host ? String(row.proxy_host) : row.proxy_id ? '代理记录不可用' : ''"
              >{{ row.proxy_host || (row.proxy_id ? '代理不可用' : '未绑定') }}</span
            >
            <span v-else>{{ cellText(row, column) }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="listKey === 'proxies'" label="所属用户" min-width="120">
          <template #default="{ row }">
            <span v-if="ownerLabel(row)">{{ ownerLabel(row) }}</span>
            <span v-else class="muted">{{ isAdmin ? '未分配' : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="listKey === 'proxies'" label="最近本机检测" min-width="220">
          <template #default="{ row }">
            <div v-if="row.latest_check" class="primary-cell">
              <div>
                <el-tag :type="row.latest_check.ok ? 'success' : 'warning'" effect="plain">{{
                  row.latest_check.ok ? '本机可达' : '本机检测失败'
                }}</el-tag>
                <small
                  >{{ routeLabel(row.latest_check.route_mode) }} ·
                  {{ row.latest_check.exit_ip || '出口 IP 未获取' }}</small
                >
                <small>{{ formatDateTime(row.latest_check.checked_at) }}</small>
              </div>
            </div>
            <span v-else class="muted">尚未检测</span>
          </template>
        </el-table-column>
        <el-table-column v-if="listKey === 'jobs'" label="结果" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.code" :title="String(row.code)">{{
              outcomeText(row as { code?: string; message?: string })
            }}</span
            ><span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column
          v-if="
            (listKey === 'proxies' &&
              (can('proxies.test') ||
                can('proxies.view') ||
                can('proxies.update') ||
                can('proxies.delete') ||
                isAdmin)) ||
            (listKey === 'connections' && (can('connections.update') || can('connections.delete')))
          "
          label="操作"
          :width="isAdmin ? 220 : 170"
          class-name="actions-column"
          fixed="right"
        >
          <template #default="{ row }">
            <template v-if="listKey === 'proxies'">
              <div class="row-actions">
                <el-button
                  text
                  type="primary"
                  v-if="can('proxies.test')"
                  :disabled="!row.enabled || !!testingProxy"
                  :loading="testingProxy === row.id"
                  @click="testProxy(row)"
                  >检测</el-button
                >
                <el-button v-if="isAdmin" text @click="openAssignProxy(row)">分配</el-button>
                <el-button v-if="can('proxies.update') && owns(row.created_by_user_id)" text @click="openEditProxy(row)"
                  >编辑</el-button
                >
                <el-dropdown trigger="click" placement="bottom-end" @command="(cmd: string) => proxyCommand(cmd, row)">
                  <el-button text class="row-more" title="更多操作"><MoreHorizontal :size="16" /></el-button>
                  <template #dropdown>
                    <el-dropdown-menu>
                      <el-dropdown-item command="details">检测记录</el-dropdown-item>
                      <el-dropdown-item v-if="can('proxies.update') && owns(row.created_by_user_id)" command="toggle">{{
                        row.enabled ? '停用' : '启用'
                      }}</el-dropdown-item>
                      <el-dropdown-item
                        v-if="can('proxies.delete') && owns(row.created_by_user_id)"
                        command="delete"
                        divided
                        class="row-danger"
                        >删除</el-dropdown-item
                      >
                    </el-dropdown-menu>
                  </template>
                </el-dropdown>
              </div>
            </template>
            <template v-else>
              <el-button v-if="can('connections.update')" text type="primary" @click="openEditConnection(row)"
                >修改</el-button
              >
              <el-button v-if="can('connections.delete')" text type="danger" @click="removeConnection(row)"
                >删除</el-button
              >
            </template>
          </template>
        </el-table-column>
        <template #empty>
          <div class="list-empty">
            <div class="empty-icon">
              <SearchX v-if="page.hasConditions.value" :size="27" /><component :is="headingIcon" v-else :size="27" />
            </div>
            <strong>{{
              page.hasConditions.value ? '没有匹配的结果' : page.loading.value ? '正在加载…' : '暂无' + config.name
            }}</strong>
            <p>{{ page.hasConditions.value ? '尝试减少筛选条件，或更换关键词。' : emptyHint }}</p>
            <el-button v-if="page.hasConditions.value" @click="page.resetFilters">清除筛选</el-button>
            <el-button v-else-if="createLabel && canCreate" type="primary" plain @click="openCreate"
              ><Plus :size="15" />{{ createLabel }}</el-button
            >
          </div>
        </template>
      </el-table>
    </div>
    <div class="list-footer">
      <span>
        {{ page.hasConditions.value ? '匹配' : '共' }} <strong>{{ page.total.value }}</strong> 条
        <span v-if="page.hasConditions.value && !page.serverPaged.value">
          / 已加载 {{ page.items.value.length }} 条</span
        >
      </span>
      <el-pagination
        v-model:current-page="page.currentPage.value"
        v-model:page-size="page.pageSize.value"
        :page-sizes="[10, 20, 50]"
        :total="page.total.value"
        layout="sizes, prev, pager, next"
        size="small"
      />
    </div>
  </div>
  <template v-if="listKey === 'accounts'">
    <AccountDeliveryDialog
      v-model="deliveryOpen"
      :account="selectedAccount"
      :connections="connections"
      @changed="onDeliveryChanged"
      @assigned="onAssigned"
      @configure="router.push({ name: 'connections' })"
      @complete="completeAccount"
      @reauthorize="reauthorizeAccount"
      @deleted="onAccountDeleted"
    />
    <ImportDialog v-model="importOpen" @feedback="feedback" @imported="onImported" />
    <PlatformProfileDialog
      v-model="platformProfileOpen"
      :account="platformProfileAccount"
      @saved="onPlatformProfileSaved"
      @conflict="page.load"
    />
    <AccountFormDialog
      v-model="accountFormOpen"
      :account="editingAccount"
      :connection-id="editingConnectionId"
      :force-reauth="forceReauth"
      :proxies="proxies"
      :connections="connections"
      @saved="onAccountSaved"
      @reauthorized="onAccountReauthorized"
      @proxy-created="refreshOnboardingProxies"
    />
  </template>
  <ProxyFormDialog v-if="listKey === 'proxies'" v-model="proxyFormOpen" :proxy="editingProxy" @saved="onProxySaved" />
  <AssignUserDialog
    v-if="assignOpen"
    v-model="assignOpen"
    :endpoint="assignEndpoint"
    :resource-id="assignResourceId"
    :resource-label="assignResourceLabel"
    :current-user-id="assignCurrentUserId"
    @assigned="onAssigned"
  />
  <RemoteAccountsDrawer v-if="reconcileConnection" v-model="reconcileOpen" :connection-id="reconcileConnection.id" />
  <ConnectionFormDialog
    v-if="listKey === 'connections'"
    v-model="connectionFormOpen"
    :connection="editingConnection"
    @saved="onConnectionSaved"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { FileJson, Plus, RefreshCw, Network, UsersRound, ListChecks, SearchX, MoreHorizontal } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import ListFilters from '../components/ListFilters.vue'
import AccountCards from '../components/AccountCards.vue'
import AccountDeliveryDialog from '../components/AccountDeliveryDialog.vue'
import AccountFormDialog from '../components/AccountFormDialog.vue'
import ImportDialog from '../components/ImportDialog.vue'
import PlatformProfileDialog from '../components/PlatformProfileDialog.vue'
import ProxyFormDialog from '../components/ProxyFormDialog.vue'
import AssignUserDialog from '../components/AssignUserDialog.vue'
import ConnectionFormDialog from '../components/ConnectionFormDialog.vue'
import RemoteAccountsDrawer from '../components/RemoteAccountsDrawer.vue'
import ProxyNetworkPanel from '../components/ProxyNetworkPanel.vue'
import { api, errorMessage } from '../api'
import { listConfigs, states, type Column } from '../list-model'
import { deliveryState, outcomeText } from '../account-workflow'
import { useListPage } from '../composables/useListPage'
import { useSession } from '../composables/useSession'
import { formatDateTime, formatMoneyFen, formatPercent, hasValue, planLabel } from '../utils/format'
import type {
  Account,
  ConnectionOption,
  ImportResult,
  ListRow,
  OnboardResult,
  PlatformConnection,
  PlatformProfileResult,
  Proxy,
  ProxyOption,
} from '../types/api'

const route = useRoute()
const router = useRouter()
const { can, canOnboard, onboardAllowed, owns, user, check } = useSession()
const isAdmin = computed(() => !!user.value?.is_admin)
const listKey = computed(() => route.meta.list || 'accounts')
const canCreate = computed(
  () =>
    (listKey.value === 'accounts' && canOnboard()) ||
    (listKey.value === 'proxies' && can('proxies.create')) ||
    (listKey.value === 'connections' && can('connections.create')),
)
const canImportAccounts = computed(() => can('accounts.import') && onboardAllowed())
const onboardBlockedMessage = '当前账号已被禁止上号，请联系管理员'
const config = computed(() => listConfigs[listKey.value] || listConfigs.accounts!)

const proxies = ref<ProxyOption[]>([])
const connections = ref<ConnectionOption[]>([])
const reconcileOpen = ref(false)
const reconcileConnection = computed(() => connections.value.find((c) => c.web_configured && c.enabled) || null)
async function loadLookups() {
  if (listKey.value !== 'accounts') return
  // Lookups only need accounts.view, so this page never depends on the proxies / connections modules.
  const [p, c] = await Promise.all([
    api<ProxyOption[]>('/lookups/proxies'),
    api<ConnectionOption[]>('/lookups/platform-connections'),
  ])
  proxies.value = p.data
  connections.value = c.data
}
async function refreshOnboardingProxies() {
  try {
    await loadLookups()
  } catch (e) {
    feedback(errorMessage(e, '代理已保存，但列表刷新失败，请稍后刷新'), 'warning')
  }
}
let currentItems: () => ListRow[] = () => []
const page = useListPage<ListRow>(config, {
  extra: loadLookups,
  pollWhile: () =>
    listKey.value === 'jobs' ||
    (currentItems() as unknown as Account[]).some((row) => deliveryState(row) === 'submitting'),
})
currentItems = () => page.items.value
const accountsAll = computed(() => page.items.value as unknown as Account[])
const accountsPaged = computed(() => page.pagedItems.value as unknown as Account[])

const actionMessage = ref('')
const actionType = ref<'success' | 'info' | 'warning' | 'error'>('info')
function feedback(message: string, type: 'success' | 'info' | 'warning' | 'error' = 'info') {
  actionMessage.value = message
  actionType.value = type
}
function applySearch() {
  const problem = page.applySearch()
  page.error.value = problem
}

const createLabel = computed(
  () => ({ accounts: '新建账号', proxies: '新增代理', connections: '新建连接' })[listKey.value] || '',
)
const emptyHint = computed(
  () =>
    ({ accounts: '新建账号或通过 JSON 导入，开始管理库存。', proxies: '添加第一条代理，统一管理和检测出口。' })[
      listKey.value
    ] || '有数据后将在这里显示。',
)
const headingIcon = computed(() =>
  listKey.value === 'proxies' ? Network : listKey.value === 'accounts' ? UsersRound : ListChecks,
)
const identityLabel = computed(
  () =>
    ({
      accounts: '账号 / 资源 ID',
      proxies: '代理 / 资源 ID',
      jobs: '任务 ID',
      connections: '连接名称 / ID',
      audit: '事件 ID',
    })[listKey.value] || 'ID',
)

function identity(row: ListRow): string {
  const r = row as Record<string, unknown>
  switch (listKey.value) {
    case 'accounts':
      return String(r.account ?? '')
    case 'proxies':
      return String(r.name || r.host || '')
    case 'connections':
      return String(r.name || r.base_url || '')
    default:
      return String(r.id ?? '—')
  }
}
function statusText(field: string, value: unknown) {
  if (field === 'archived') return value ? '已归档' : '正常库存'
  if (field === 'enabled') return value ? '启用' : '停用'
  return states[String(value)] || String(value || '—')
}
function statusType(field: string, value: unknown): 'success' | 'info' | 'warning' | 'danger' {
  if (field === 'archived') return value ? 'info' : 'success'
  if (field === 'enabled') return value ? 'success' : 'info'
  return value === 'succeeded'
    ? 'success'
    : value === 'failed'
      ? 'danger'
      : value === 'running' || value === 'unknown'
        ? 'warning'
        : 'info'
}
function cellText(row: ListRow, column: Column) {
  const value = (row as Record<string, unknown>)[column.field]
  if (!hasValue(value)) return listKey.value === 'audit' ? '—' : '未同步'
  if (column.format === 'plan') return planLabel(value)
  if (column.format === 'date') return formatDateTime(value)
  if (column.format === 'money') return formatMoneyFen(value)
  if (column.field === 'success_rate_percent') return formatPercent(value)
  return value === 'account.submit' ? '账号上传' : String(value)
}
const routeLabel = (mode: string) => (mode === 'upstream' ? '前置代理' : mode === 'direct' ? '直连 / TUN' : '历史检测')

// ---- accounts ----
const selectedAccount = ref<Account | null>(null)
const deliveryOpen = ref(false)
const importOpen = ref(false)
const accountFormOpen = ref(false)
const editingAccount = ref<Account | null>(null)
const editingConnectionId = ref('')
const forceReauth = ref(false)
const platformProfileOpen = ref(false)
const platformProfileAccount = ref<Account | null>(null)
function editPlatform(row: Account) {
  platformProfileAccount.value = accountsAll.value.find((item) => item.id === row.id) || row
  platformProfileOpen.value = true
  actionMessage.value = ''
}
async function ensureOnboardAllowed(): Promise<boolean> {
  try {
    await check()
  } catch {
    /* keep cached session; API still enforces */
  }
  if (onboardAllowed()) return true
  feedback(onboardBlockedMessage, 'error')
  return false
}
function reauthorizeAccount(row: Account) {
  // Paste-credential reauth stays available; Claude OAuth is blocked inside the panel / API.
  deliveryOpen.value = false
  editingAccount.value = accountsAll.value.find((item) => item.id === row.id) || row
  editingConnectionId.value =
    row.platform_links?.find((l) => l.ingestion_state === 'accepted')?.connection_id ||
    row.submission_job?.connection_id ||
    ''
  forceReauth.value = true
  accountFormOpen.value = true
  actionMessage.value = ''
}
function ownerLabel(row: { created_by_nickname?: string; created_by_username?: string }) {
  return (row.created_by_nickname || row.created_by_username || '').trim()
}

const assignOpen = ref(false)
const assignEndpoint = ref('/accounts')
const assignResourceId = ref('')
const assignResourceLabel = ref('')
const assignCurrentUserId = ref('')
function openAssignAccount(row: Account) {
  assignEndpoint.value = '/accounts'
  assignResourceId.value = row.id
  assignResourceLabel.value = row.account || row.id
  assignCurrentUserId.value = row.created_by_user_id || ''
  assignOpen.value = true
}
function openAssignProxy(row: ListRow) {
  const p = row as unknown as Proxy
  assignEndpoint.value = '/proxies'
  assignResourceId.value = p.id
  assignResourceLabel.value = p.name || p.host || p.id
  assignCurrentUserId.value = p.created_by_user_id || ''
  assignOpen.value = true
}
async function onAssigned(result: { userId: string; message?: string }) {
  feedback(result.message || (result.userId ? '已分配给用户' : '已取消分配（仅管理员可见）'), 'success')
  await page.load()
}
function onPlatformProfileSaved(result: PlatformProfileResult) {
  actionMessage.value = result.verified
    ? '平台资料已修改：' + result.changed.join('、')
    : '平台已接受修改，但未能立即核对' + (result.hint ? '：' + result.hint : '')
  void page.load()
}
function selectAccount(row: Account) {
  selectedAccount.value = accountsAll.value.find((item) => item.id === row.id) || row
  deliveryOpen.value = true
  actionMessage.value = ''
}
async function onDeliveryChanged() {
  const id = selectedAccount.value?.id
  await page.load()
  if (id) {
    selectedAccount.value = accountsAll.value.find((row) => row.id === id) || selectedAccount.value
  }
}
async function completeAccount(row: Account, connectionId?: string) {
  if (!(await ensureOnboardAllowed())) return
  deliveryOpen.value = false
  editingAccount.value = row
  editingConnectionId.value = connectionId || ''
  forceReauth.value = false
  accountFormOpen.value = true
}
async function onAccountSaved(result: OnboardResult) {
  forceReauth.value = false
  feedback('账号已提交，等待平台处理。可在账号卡片查看上号结果。', 'info')
  await page.load()
  selectedAccount.value = accountsAll.value.find((row) => row.id === result.account_id) || null
  deliveryOpen.value = !!selectedAccount.value
}
async function onAccountReauthorized() {
  forceReauth.value = false
  feedback('凭证已更新并推送到平台，状态将在下次同步后刷新。', 'success')
  await page.load()
}
async function onAccountDeleted() {
  selectedAccount.value = null
  feedback('账号已删除', 'success')
  await page.load()
}
async function onImported(result: ImportResult) {
  feedback(`导入完成：创建 ${result.created} 条，跳过 ${result.skipped} 条`, 'success')
  await page.load()
}

// ---- proxies ----
const proxyNetworkPanel = ref<InstanceType<typeof ProxyNetworkPanel>>()
const proxyFormOpen = ref(false)
const editingProxy = ref<Proxy | null>(null)
const testingProxy = ref('')
function openEditProxy(row: ListRow) {
  editingProxy.value = row as unknown as Proxy
  proxyFormOpen.value = true
}
async function proxyCommand(cmd: string, row: ListRow) {
  const p = row as unknown as Proxy
  if (cmd === 'details') {
    proxyNetworkPanel.value?.details(row)
  } else if (cmd === 'toggle') {
    try {
      await api('/proxies/' + p.id, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !p.enabled, expected_version: p.version }),
      })
      feedback(p.enabled ? '代理已停用' : '代理已启用', 'success')
      await page.load()
    } catch (e) {
      feedback(errorMessage(e, '操作失败'), 'error')
    }
  } else if (cmd === 'delete') {
    await removeProxy(row)
  }
}
async function removeProxy(row: ListRow) {
  const p = row as unknown as Proxy
  try {
    await ElMessageBox.confirm(
      `确定删除代理“${p.name || p.host}”？检测记录会一并删除；仍被账号绑定的代理无法删除。`,
      '删除代理',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await api('/proxies/' + p.id, { method: 'DELETE' })
    feedback('代理已删除', 'success')
    await page.load()
  } catch (e) {
    feedback(errorMessage(e, '删除失败'), 'error')
  }
}
async function testProxy(row: ListRow) {
  if (testingProxy.value) return
  testingProxy.value = String(row.id)
  try {
    await proxyNetworkPanel.value?.test(row)
  } finally {
    testingProxy.value = ''
  }
}
async function onProxySaved(updated: boolean) {
  feedback(updated ? '代理已更新' : '代理已添加', 'success')
  await page.load()
}

// ---- connections ----
const connectionFormOpen = ref(false)
const editingConnection = ref<PlatformConnection | null>(null)
function openEditConnection(row: ListRow) {
  editingConnection.value = row as unknown as PlatformConnection
  connectionFormOpen.value = true
}
async function onConnectionSaved(updated: boolean) {
  feedback(updated ? '平台连接已更新' : '平台连接已创建', 'success')
  await page.load()
}
async function removeConnection(row: ListRow) {
  const c = row as unknown as PlatformConnection
  try {
    await ElMessageBox.confirm(`确定删除平台连接“${c.name || c.base_url}”？删除后不可恢复。`, '删除平台连接', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api('/platform-connections/' + c.id, { method: 'DELETE' })
    feedback('平台连接已删除', 'success')
    await page.load()
  } catch (e) {
    feedback(errorMessage(e, '删除失败'), 'error')
  }
}

async function openImport() {
  if (!(await ensureOnboardAllowed())) return
  importOpen.value = true
}
async function openCreate() {
  if (listKey.value === 'accounts') {
    if (!(await ensureOnboardAllowed())) return
    editingAccount.value = null
    editingConnectionId.value = ''
    forceReauth.value = false
    accountFormOpen.value = true
    return
  }
  if (listKey.value === 'proxies') {
    editingProxy.value = null
    proxyFormOpen.value = true
  } else if (listKey.value === 'connections') {
    editingConnection.value = null
    connectionFormOpen.value = true
  }
}

onMounted(async () => {
  await page.load()
  if (listKey.value === 'jobs' || listKey.value === 'accounts') page.polling.start()
})
</script>

<style scoped>
:global(.list-heading h2) {
  margin: 0;
  font-size: 15px;
}

.list-workspace {
  overflow: hidden;
}

.list-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 16px 3px;
}

:global(.list-heading > div) {
  display: flex;
  gap: 9px;
  align-items: center;
}

:global(.list-heading > .el-button) {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}

.count-badge {
  display: inline-grid;
  place-items: center;
  min-width: 24px;
  height: 20px;
  padding: 0 7px;
  background: #f0f5f5;
  color: #70808a;
  border-radius: 99px;
  font-size: 10px;
  font-weight: 700;
}

.action-feedback {
  margin: 10px 15px;
}

.table-scroll {
  overflow-x: auto;
}

:global(.list-workspace .el-table) {
  border: 0;
  border-radius: 0;
  box-shadow: none;
}

:global(.list-workspace .el-table th.el-table__cell) {
  background: #f7f9fa;
  padding: 11px 0;
}

:global(.list-workspace .el-table td.el-table__cell) {
  padding: 12px 0;
}

.primary-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

:global(.primary-cell > div) {
  min-width: 0;
}

:global(.primary-cell strong) {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(.primary-cell small) {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(.primary-cell strong) {
  font-size: 12px;
  color: var(--ink-soft);
}

:global(.primary-cell small) {
  margin-top: 4px;
  color: #94a1a8;
  font:
    10px 'DM Mono',
    monospace;
}

.record-icon {
  display: grid;
  place-items: center;
  width: 31px;
  height: 31px;
  border-radius: 7px;
  background: #eef8f5;
  color: #198d76;
  flex: none;
}

.bound-proxy {
  color: #187e69;
  font-weight: 700;
}

.list-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 260px;
  padding: 25px 15px;
  color: #667883;
}

.empty-icon {
  display: grid;
  place-items: center;
  width: 55px;
  height: 55px;
  border-radius: 8px;
  background: #f1f6f6;
  color: #9aabb0;
  margin-bottom: 14px;
}

:global(.list-empty strong) {
  font-size: 13px;
}

:global(.list-empty p) {
  margin: 6px 0 14px;
  color: #9aa7ad;
  font-size: 11px;
  text-align: center;
}

:global(.list-empty .el-button) {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}

.list-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 15px;
  padding: 11px 15px;
  border-top: 1px solid var(--line-soft);
  color: #8a979d;
  font-size: 11px;
}

:global(.list-footer strong) {
  color: #435862;
}

.platform-monitor .list-heading {
  padding: 18px 20px 8px;
}

:global(.platform-monitor .list-heading h2) {
  color: #d8e6f7;
}

.platform-monitor .count-badge {
  background: #12243b;
  color: #6ea7dd;
}

:global(.platform-monitor .list-heading > .el-button) {
  color: #62d8ba;
}

.platform-monitor .list-footer {
  border-color: #1d3049;
  color: #7188a1;
}

.platform-monitor .list-heading {
  padding: 16px 16px 3px;
}

:global(.platform-monitor .list-heading h2) {
  color: var(--ink-soft);
}

.platform-monitor .count-badge {
  background: #f0f5f5;
  color: #70808a;
}

:global(.platform-monitor .list-heading > .el-button) {
  color: #139279;
}

.platform-monitor .list-footer {
  border-color: var(--line-soft);
  color: #8a979d;
}

@media (max-width: 620px) {
  .list-footer {
    align-items: flex-start;
    flex-direction: column;
  }
  :global(.list-footer .el-pagination) {
    width: 100%;
    justify-content: center;
  }
}
</style>
