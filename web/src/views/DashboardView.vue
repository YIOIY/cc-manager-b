<template>
  <PageHead
    eyebrow="WORKSPACE"
    title="仪表盘"
    description="账号还够不够用：配额、需要处理的账号、消费节奏和最近的上号结果。"
  >
    <el-button :loading="loading" @click="load"><RefreshCw :size="15" />刷新</el-button>
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon @close="error = ''" />

  <div class="stat-grid dashboard-stats">
    <div class="stat" data-stat="accounts" role="button" tabindex="0" @click="go({})" @keyup.enter="go({})">
      <span>平台账号</span><b>{{ dash ? fleetTotal : '—' }}</b>
      <small v-if="webChannel">使用中 {{ usingCount }} · 已封禁 {{ bannedCount }}</small>
      <small v-else>配置网页登录后显示平台状态</small>
    </div>
    <div
      class="stat"
      data-stat="near_limit"
      :class="{ attention: nearLimit > 0 }"
      role="button"
      tabindex="0"
      @click="go({ five_hour_min: '80' })"
      @keyup.enter="go({ five_hour_min: '80' })"
    >
      <span>接近限额</span><b>{{ dash ? nearLimit : '—' }}</b
      ><small>5 小时窗口 ≥ 80%</small>
    </div>
    <div class="stat" data-stat="today_cost">
      <span>今日消费</span>
      <b class="money">{{ formatMoneyFen(todayFen) }}</b>
      <em v-if="usd(todayFen)" class="stat-usd">{{ usd(todayFen) }}</em>
      <small>未出齐，约 2 小时延迟</small>
    </div>
    <div class="stat" data-stat="week_cost">
      <span>7 天消费</span>
      <b class="money">{{ formatMoneyFen(weekFen) }}</b>
      <em v-if="usd(weekFen)" class="stat-usd">{{ usd(weekFen) }}</em>
      <small
        >日均 {{ formatMoneyFen(dailyAverage) }}
        <template v-if="change"
          >，昨天比前一天<i :class="change.up ? 'up' : 'down'"
            >{{ change.up ? '多' : '少' }} {{ change.percent }}%</i
          ></template
        ></small
      >
    </div>
  </div>

  <section v-if="insights" class="ledger" aria-label="累计数据">
    <div>
      <span>累计花费</span>
      <b>{{ formatMoneyFen(insights.fleet.cost_fen_total) }}</b>
      <small v-if="usd(insights.fleet.cost_fen_total)">{{ usd(insights.fleet.cost_fen_total) }}</small>
      <small v-else>{{ insights.fleet.accounts }} 个账号合计</small>
    </div>
    <div>
      <span>累计 Token</span>
      <b>{{ compactCount(insights.fleet.input_tokens + insights.fleet.output_tokens) }}</b>
      <small
        >输入 {{ compactCount(insights.fleet.input_tokens) }}，输出
        {{ compactCount(insights.fleet.output_tokens) }}</small
      >
    </div>
    <div>
      <span>平均占用</span>
      <b>{{ insights.fleet.avg_five_hour_percent.toFixed(1) }}%</b>
      <small>5 小时窗口；7 天窗口 {{ insights.fleet.avg_week_percent.toFixed(1) }}%</small>
    </div>
    <div :class="{ warn: expiringSoon > 0 }">
      <span>30 天内到期</span>
      <b>{{ expiringSoon }}</b>
      <small>其中 7 天内 {{ expiry.within_7 || 0 }} 个，已到期 {{ expiry.expired || 0 }} 个</small>
    </div>
  </section>

  <div class="dashboard-grid">
    <AttentionList
      :items="attention?.items || []"
      :counts="attention?.counts || {}"
      :loading="attentionLoading"
      :failed="attentionFailed"
      @open="openAttention"
      @all="go({})"
    />
    <SpendPanel :platform="dash?.platform" :hourly="insights?.hourly" @bills="router.push({ name: 'platformBills' })" />
  </div>

  <div class="dashboard-grid wide-left">
    <QuotaBoard
      :rows="insights?.quota || []"
      :loading="insightsLoading"
      :failed="insightsFailed"
      @open="(row) => go({ q: row.account || row.alias })"
      @all="go({})"
    />
    <FleetComposition
      :status-counts="remoteCounts"
      :plan-counts="dash?.accounts.by_plan || {}"
      :delivery-counts="insights?.composition.by_delivery"
      :expiry-counts="insights?.composition.expiry"
    />
  </div>

  <div class="dashboard-grid" :class="{ single: !recentJobs }">
    <RecentJobs v-if="recentJobs" :jobs="recentJobs" @all="router.push({ name: 'jobs' })" />
    <section class="dashboard-panel system-panel">
      <div class="dashboard-panel-head">
        <div>
          <h2>系统状态</h2>
          <p class="panel-sub">数据从哪来、多久之前更新的。</p>
        </div>
      </div>
      <dl class="system-list">
        <div>
          <dt>网页同步</dt>
          <dd>
            {{ webHealthSource?.last_sync_at ? formatDateTime(webHealthSource.last_sync_at) : '尚未同步' }}
            <el-tag size="small" :type="webHealth.tone">{{ webHealth.label }}</el-tag>
          </dd>
        </div>
        <div>
          <dt>指标同步</dt>
          <dd>
            {{ dash?.platform.last_sync_at ? formatDateTime(dash.platform.last_sync_at) : '尚未同步' }}
            <el-tag size="small" :type="metricsHealth.tone">{{ metricsHealth.label }}</el-tag>
          </dd>
        </div>
        <div>
          <dt>代理</dt>
          <dd>{{ dash?.proxies.enabled ?? '—' }} 个启用，{{ dash?.proxies.disabled ?? '—' }} 个停用</dd>
        </div>
        <div>
          <dt>任务</dt>
          <dd>
            运行 {{ dash?.jobs.running ?? '—' }}，排队 {{ dash?.jobs.queued ?? '—' }}，失败
            {{ dash?.jobs.failed ?? '—' }}
          </dd>
        </div>
        <div v-if="insights">
          <dt>汇率</dt>
          <dd v-if="insights.usd_cny_rate">1 美元 = {{ formatRate(insights.usd_cny_rate) }} 人民币（平台固定汇率）</dd>
          <dd v-else>各平台连接的汇率不一致，合计只显示人民币</dd>
        </div>
      </dl>
      <p v-if="webHealth.detail" class="system-note" role="status">
        网页同步：{{ webHealth.detail }}{{ webHealth.hint ? '，' + webHealth.hint : '' }}
      </p>
      <p v-if="metricsHealth.detail" class="system-note" role="status">
        指标同步：{{ metricsHealth.detail }}{{ metricsHealth.hint ? '，' + metricsHealth.hint : '' }}
      </p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { describeMetricsHealth, describeSyncHealth, type SyncHealthView } from '../model/sync-health'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import AttentionList from '../components/AttentionList.vue'
import SpendPanel from '../components/SpendPanel.vue'
import QuotaBoard from '../components/QuotaBoard.vue'
import FleetComposition from '../components/FleetComposition.vue'
import RecentJobs from '../components/RecentJobs.vue'
import { api, errorMessage } from '../api'
import type { AttentionItem, AttentionResponse, Dashboard, DashboardInsights } from '../types/api'
import { formatDateTime, formatMoneyFen } from '../utils/format'
import { accountQuery, dailyAverageFen, dayChange } from '../model/dashboard'
import { fenToUsdCents, formatRate, formatUsdCents } from '../model/money'

const router = useRouter()
const dash = ref<Dashboard>()
const attention = ref<AttentionResponse>()
const insights = ref<DashboardInsights>()
const loading = ref(false)
const attentionLoading = ref(false)
const attentionFailed = ref(false)
const insightsLoading = ref(false)
const insightsFailed = ref(false)
const error = ref('')

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
const remoteCounts = computed(() => dash.value?.platform.remote_counts_by_status || {})
const remoteTotal = computed(() => Object.values(remoteCounts.value).reduce((a, b) => a + Number(b), 0))
const webChannel = computed(() => remoteTotal.value > 0)
const fleetTotal = computed(() => (webChannel.value ? remoteTotal.value : dash.value?.accounts.total || 0))
const usingCount = computed(() => Number(remoteCounts.value.using || 0))
const bannedCount = computed(() =>
  Object.entries(remoteCounts.value).reduce((n, [status, c]) => (failingStatuses.has(status) ? n + Number(c) : n), 0),
)
const nearLimit = computed(() => dash.value?.platform.near_limit_accounts ?? 0)
const todayFen = computed(() => dash.value?.platform.today_model_cost_fen ?? 0)
const weekFen = computed(() => dash.value?.platform.week_model_cost_fen ?? 0)
const days = computed(() => dash.value?.platform.cost_by_day || [])
const dailyAverage = computed(() => dailyAverageFen(days.value))
const change = computed(() => dayChange(days.value))
const expiry = computed(() => insights.value?.composition.expiry || {})
const expiringSoon = computed(
  () => (expiry.value.expired || 0) + (expiry.value.within_7 || 0) + (expiry.value.within_30 || 0),
)
const recentJobs = computed(() => insights.value?.recent_jobs ?? null)
const metricsHealth = computed<SyncHealthView>(() =>
  dash.value ? describeMetricsHealth(dash.value.platform) : { tone: 'info', label: '未开始' },
)
const webHealthSource = computed(() => dash.value?.platform.web_sync_health)
const webHealth = computed<SyncHealthView>(() =>
  webHealthSource.value ? describeSyncHealth(webHealthSource.value) : { tone: 'info', label: '未配置' },
)

// Dollars are shown only when the platform rate is known (it arrives with
// the insights); otherwise amounts stay in RMB.
function usd(fen: number): string {
  const cents = fenToUsdCents(fen, insights.value?.usd_cny_rate)
  return cents === null ? '' : formatUsdCents(cents)
}
function compactCount(n: number): string {
  if (n >= 1e8) return (n / 1e8).toFixed(2) + ' 亿'
  if (n >= 1e4) return (n / 1e4).toFixed(1) + ' 万'
  return n.toLocaleString('zh-CN')
}
function go(query: Record<string, string>) {
  router.push({ name: 'accounts', query })
}
function openAttention(item: AttentionItem) {
  go(accountQuery(item))
}

// The three requests load and fail on their own: a panel whose data is
// missing says so, and the rest of the page stays usable.
async function loadSummary() {
  try {
    dash.value = (await api<Dashboard>('/dashboard')).data
    error.value = ''
  } catch (e) {
    error.value = errorMessage(e)
  }
}
async function loadAttention() {
  attentionLoading.value = true
  try {
    attention.value = (await api<AttentionResponse>('/dashboard/attention')).data
    attentionFailed.value = false
  } catch {
    attentionFailed.value = true
  } finally {
    attentionLoading.value = false
  }
}
async function loadInsights() {
  insightsLoading.value = true
  try {
    insights.value = (await api<DashboardInsights>('/dashboard/insights')).data
    insightsFailed.value = false
  } catch {
    insights.value = undefined
    insightsFailed.value = true
  } finally {
    insightsLoading.value = false
  }
}
async function load() {
  loading.value = true
  await Promise.all([loadSummary(), loadAttention(), loadInsights()])
  loading.value = false
}
onMounted(load)
</script>

<style scoped>
.stat-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
  margin-top: 26px;
}
.stat {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: var(--shadow);
  position: relative;
  overflow: hidden;
  min-height: 146px;
  padding: 22px 24px;
}
.stat[role='button'] {
  cursor: pointer;
}
.stat[role='button']:hover,
.stat[role='button']:focus-visible {
  border-color: var(--accent-strong);
  outline: none;
}
.stat:after {
  content: '';
  position: absolute;
  right: -22px;
  top: -30px;
  width: 92px;
  height: 92px;
  border: 1px solid #d9f1ea;
  border-radius: 50%;
  box-shadow: 0 0 0 12px #f5fbf9;
}
.stat span {
  font-size: 13px;
  color: #60727e;
}
.stat b {
  display: block;
  margin-top: 16px;
  font-size: 38px;
  letter-spacing: -0.06em;
}
.stat b.money {
  font-size: 28px;
  letter-spacing: -0.03em;
}
.stat:nth-child(1) b {
  color: #167c69;
}
.stat:nth-child(2) b {
  color: #b06b1d;
}
.stat.attention:nth-child(2) b {
  color: var(--el-color-danger);
}
.stat:nth-child(3) b {
  color: #2863a5;
}
.stat:nth-child(4) b {
  color: #7c4bb1;
}
.stat-usd {
  display: block;
  margin-top: 2px;
  color: var(--ink-mid);
  font:
    600 13px/1.4 'DM Mono',
    ui-monospace,
    monospace;
  font-style: normal;
}
.stat small {
  display: block;
  margin-top: 8px;
  color: var(--muted-soft);
  font-size: 11px;
  line-height: 1.5;
}
.stat small i {
  font-style: normal;
  font-weight: 600;
}
.stat small i.up {
  color: #b06b1d;
}
.stat small i.down {
  color: var(--accent-deep);
}

/* One band of running totals: four figures separated by rules, not cards. */
.ledger {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  margin-top: 16px;
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
}
.ledger > div {
  padding: 15px 22px;
  border-left: 1px solid var(--line-soft);
  min-width: 0;
}
.ledger > div:first-child {
  border-left: 0;
}
.ledger span {
  display: block;
  color: var(--muted-strong);
  font-size: 12px;
}
.ledger b {
  display: block;
  margin-top: 6px;
  color: var(--ink-soft);
  font:
    700 20px/1.2 'DM Mono',
    ui-monospace,
    monospace;
}
.ledger .warn b {
  color: #a8721a;
}
.ledger small {
  display: block;
  margin-top: 4px;
  color: var(--muted-soft);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: 1.05fr 0.95fr;
  gap: 16px;
  margin-top: 16px;
  align-items: start;
}
.dashboard-grid.wide-left {
  grid-template-columns: 1.45fr 0.75fr;
}
.dashboard-grid.single {
  grid-template-columns: 1fr;
}

:global(.dashboard-panel) {
  min-width: 0;
  padding: 20px 21px;
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(20, 35, 40, 0.045);
}
:global(.dashboard-panel-head) {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}
:global(.dashboard-panel-head h2) {
  margin: 0;
  color: #213d4b;
  font-size: 16px;
}
:global(.panel-sub) {
  margin: 4px 0 0;
  color: var(--muted-soft);
  font-size: 12px;
}
:global(.panel-empty) {
  padding: 14px 0 6px;
}
:global(.subsection-title) {
  margin-bottom: 10px;
  color: #506671;
  font-size: 12px;
  font-weight: 800;
}
:global(.plan-row) {
  display: grid;
  grid-template-columns: minmax(110px, 1fr) 2fr auto;
  align-items: center;
  gap: 10px;
  margin: 10px 0;
  color: #637780;
  font-size: 11px;
}
:global(.plan-row i) {
  height: 7px;
  overflow: hidden;
  background: #edf3f2;
  border-radius: 99px;
}
:global(.plan-row em) {
  display: block;
  height: 100%;
  background: linear-gradient(90deg, #63cdb2, var(--accent-strong));
  border-radius: 99px;
}
:global(.plan-row strong) {
  text-align: right;
  color: var(--accent-deep);
  font-family: 'DM Mono', monospace;
  white-space: nowrap;
}
:global(.dashboard-muted) {
  color: var(--muted-soft);
  font-size: 12px;
}

.system-list {
  margin: 0;
}
.system-list > div {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 12px;
  padding: 9px 0;
  border-bottom: 1px solid var(--line-soft);
  font-size: 12.5px;
}
.system-list > div:last-child {
  border-bottom: 0;
}
.system-list dt {
  color: var(--muted-strong);
}
.system-list dd {
  margin: 0;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  color: var(--ink-soft);
}
.system-note {
  margin: 10px 0 0;
  color: var(--muted-strong);
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 1100px) {
  .dashboard-grid,
  .dashboard-grid.wide-left {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 980px) {
  .stat-grid,
  .ledger {
    grid-template-columns: repeat(2, 1fr);
  }
  .ledger > div:nth-child(3) {
    border-left: 0;
  }
  .ledger > div:nth-child(n + 3) {
    border-top: 1px solid var(--line-soft);
  }
}
@media (max-width: 620px) {
  .stat-grid,
  .ledger {
    grid-template-columns: 1fr;
  }
  .ledger > div {
    border-left: 0;
    border-top: 1px solid var(--line-soft);
  }
  .ledger > div:first-child {
    border-top: 0;
  }
  :global(.dashboard-panel) {
    padding: 16px;
  }
}
</style>
