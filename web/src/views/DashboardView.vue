<template>
  <PageHead eyebrow="WORKSPACE" title="仪表盘" description="还剩多少能用、花了多少、有多少要处理、什么时候要续。">
    <el-button :loading="loading" @click="load"><RefreshCw :size="15" />刷新</el-button>
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon @close="error = ''" />

  <div class="stat-grid dashboard-stats">
    <div class="stat" data-stat="accounts" role="button" tabindex="0" @click="go({})" @keyup.enter="go({})">
      <span>可用容量</span>
      <b v-if="dash && webChannel"
        >{{ usingCount }}<em> / {{ fleetTotal }} 在用</em></b
      >
      <b v-else>{{ dash ? fleetTotal : '—' }}</b>
      <small v-if="webChannel"
        >平均已用：5h {{ insights ? insights.fleet.avg_five_hour_percent.toFixed(0) + '%' : '—' }} · 7d
        {{ insights ? insights.fleet.avg_week_percent.toFixed(0) + '%' : '—' }}</small
      >
      <small v-else>配置网页登录后显示平台状态</small>
    </div>
    <div class="stat" data-stat="today_cost">
      <span>今日消费</span>
      <b class="money">{{ todayUsd || '—' }}</b>
      <p class="stat-total">
        <span>总消费</span>
        <strong>{{ totalUsd || '—' }}</strong>
      </p>
      <small
        >7 天 {{ weekUsd || '—' }} · 日均 {{ dailyAverageUsd || '—'
        }}<template v-if="change"
          >，昨天比前一天<i :class="change.up ? 'up' : 'down'"
            >{{ change.up ? '多' : '少' }} {{ change.percent }}%</i
          ></template
        ></small
      >
      <small class="stat-foot">今日数据约有 2 小时延迟</small>
    </div>
    <div class="stat" data-stat="action" :class="{ attention: actionTotal > 0 }">
      <span>需要动手</span>
      <b>{{ dash ? actionTotal : '—' }}</b>
      <small class="stat-links">
        <a role="button" tabindex="0" data-stat="banned" @click="scrollToAttention" @keyup.enter="scrollToAttention"
          >封禁 / 失败 {{ attention?.counts?.banned ?? (webChannel ? bannedCount : 0) }}</a
        >
        <a
          role="button"
          tabindex="0"
          data-stat="near_limit"
          :class="{ hot: nearLimit > 0 }"
          @click="go({ five_hour_min: '80' })"
          @keyup.enter="go({ five_hour_min: '80' })"
          >接近限额 {{ nearLimit }}</a
        >
      </small>
    </div>
    <div class="stat" data-stat="expiry" :class="{ warn: (expiry.within_7 || 0) + (expiry.expired || 0) > 0 }">
      <span>到期节奏</span>
      <b>{{ insights ? expiringSoon : '—' }}<em v-if="insights"> 个 30 天内到期</em></b>
      <small
        >7 天内 {{ expiry.within_7 || 0 }} · 已到期 {{ expiry.expired || 0
        }}<template v-if="nextExpiry"> · 最早 {{ nextExpiry.slice(5) }}</template></small
      >
    </div>
  </div>

  <div class="dashboard-grid wide-left">
    <QuotaBoard
      :rows="insights?.quota || []"
      :loading="insightsLoading"
      :failed="insightsFailed"
      @open="(row) => go({ q: row.account || row.alias })"
      @all="go({})"
    />
    <SpendPanel :platform="dash?.platform" :hourly="insights?.hourly" @bills="router.push({ name: 'platformBills' })" />
  </div>

  <div class="dashboard-grid">
    <AttentionList
      ref="attentionPanel"
      :items="attention?.items || []"
      :counts="attention?.counts || {}"
      :loading="attentionLoading"
      :failed="attentionFailed"
      @open="openAttention"
      @all="go({})"
    />
    <IdleAccounts
      :rows="insights?.idle || []"
      :loading="insightsLoading"
      :failed="insightsFailed"
      @open="(row) => go({ q: row.account || row.alias })"
    />
  </div>

  <details class="dashboard-more" :open="systemAlert || undefined">
    <summary>
      {{ recentJobs ? '账号构成、最近上号与系统状态' : '账号构成与系统状态' }}
      <el-tag v-if="systemAlert" size="small" type="danger">同步异常</el-tag>
    </summary>
    <div class="dashboard-grid three">
      <FleetComposition
        :status-counts="remoteCounts"
        :plan-counts="dash?.accounts.by_plan || {}"
        :delivery-counts="insights?.composition.by_delivery"
        :expiry-counts="insights?.composition.expiry"
      />
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
        </dl>
        <p v-if="webHealth.detail" class="system-note" role="status">
          网页同步：{{ webHealth.detail }}{{ webHealth.hint ? '，' + webHealth.hint : '' }}
        </p>
        <p v-if="metricsHealth.detail" class="system-note" role="status">
          指标同步：{{ metricsHealth.detail }}{{ metricsHealth.hint ? '，' + metricsHealth.hint : '' }}
        </p>
      </section>
    </div>
  </details>
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
import IdleAccounts from '../components/IdleAccounts.vue'
import { api, errorMessage } from '../api'
import type { AttentionItem, AttentionResponse, Dashboard, DashboardInsights } from '../types/api'
import { formatDateTime } from '../utils/format'
import { accountQuery, dailyAverageCents, dayChange } from '../model/dashboard'
import { formatUsd, formatUsdCents } from '../model/money'

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
const todayUsd = computed(() => {
  const v = dash.value?.platform.today_model_cost_usd
  return v != null ? formatUsd(v) : ''
})
const weekUsd = computed(() => {
  const v = dash.value?.platform.week_model_cost_usd
  return v != null ? formatUsd(v) : ''
})
const totalUsd = computed(() => {
  const v = dash.value?.platform.total_model_cost_usd
  return v != null ? formatUsd(v) : ''
})
const days = computed(() => dash.value?.platform.cost_by_day || [])
const dailyAverageUsd = computed(() => {
  const cents = dailyAverageCents(days.value)
  return cents ? formatUsdCents(cents) : ''
})
const change = computed(() => dayChange(days.value))
const expiry = computed(() => insights.value?.composition.expiry || {})
const expiringSoon = computed(
  () => (expiry.value.expired || 0) + (expiry.value.within_7 || 0) + (expiry.value.within_30 || 0),
)
const nextExpiry = computed(() => insights.value?.composition.next_expiry || '')
// Accounts needing a hand: failures/bans plus those near the 5-hour limit.
const actionTotal = computed(
  () => (attention.value?.counts?.banned ?? (webChannel.value ? bannedCount.value : 0)) + nearLimit.value,
)
const attentionPanel = ref<{ $el?: HTMLElement } | null>(null)
function scrollToAttention() {
  attentionPanel.value?.$el?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
}
const recentJobs = computed(() => insights.value?.recent_jobs ?? null)
const metricsHealth = computed<SyncHealthView>(() =>
  dash.value ? describeMetricsHealth(dash.value.platform) : { tone: 'info', label: '未开始' },
)
const webHealthSource = computed(() => dash.value?.platform.web_sync_health)
const webHealth = computed<SyncHealthView>(() =>
  webHealthSource.value ? describeSyncHealth(webHealthSource.value) : { tone: 'info', label: '未配置' },
)

// Open the folded section by default when a sync channel is failing.
const systemAlert = computed(() => webHealth.value.tone === 'danger' || metricsHealth.value.tone === 'danger')

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
  gap: 12px;
  margin-top: 0;
}
.stat {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: 0 1px 2px rgba(28, 31, 35, 0.04);
  position: relative;
  overflow: hidden;
  min-height: 112px;
  padding: 18px 20px 16px;
}
.stat:before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--accent);
  opacity: 0.85;
}
.stat[role='button'] {
  cursor: pointer;
}
.stat[role='button']:hover,
.stat[role='button']:focus-visible {
  border-color: #b3d4ff;
  background: #f7fbff;
  box-shadow: 0 4px 12px rgba(0, 119, 250, 0.08);
  outline: none;
}
.stat:after {
  display: none;
}
.stat span {
  font-size: 12px;
  color: var(--muted);
  font-weight: 500;
}
.stat b {
  display: block;
  margin-top: 12px;
  font-size: 30px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}
.stat b.money {
  font-size: 26px;
}
.stat-total {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin: 10px 0 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: #f3f8ff;
  border: 1px solid #d6eaff;
  line-height: 1.2;
}
.stat-total span {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--accent-deep, #004fb3);
}
.stat-total strong {
  font-size: 18px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
  color: var(--accent);
}
.stat:nth-child(1) {
  background: linear-gradient(180deg, #f3f8ff 0%, #fff 70%);
  border-color: #d6eaff;
}
.stat:nth-child(1) b {
  color: var(--accent);
}
.stat:nth-child(2) b,
.stat:nth-child(3) b,
.stat:nth-child(4) b {
  color: var(--ink);
}
.stat-usd {
  display: block;
  margin-top: 2px;
  color: var(--ink-mid);
  font: 500 13px/1.4 inherit;
  font-style: normal;
}
.stat small {
  display: block;
  margin-top: 8px;
  color: var(--muted-soft);
  font-size: 12px;
  line-height: 1.5;
}
.stat small i {
  font-style: normal;
  font-weight: 600;
}
.stat small i.up {
  color: var(--pending);
}
.stat small i.down {
  color: var(--accent);
}

.stat b em {
  margin-left: 4px;
  color: var(--muted);
  font-size: 13px;
  font-style: normal;
  font-weight: 500;
  letter-spacing: 0;
}
.stat small.stat-foot {
  margin-top: 2px;
}
.stat-links {
  display: flex !important;
  flex-wrap: wrap;
  gap: 4px 14px;
}
.stat-links a {
  color: var(--ink-mid);
  cursor: pointer;
  text-decoration: none;
  border-bottom: 1px dashed var(--line-strong);
}
.stat-links a:hover,
.stat-links a:focus-visible {
  color: var(--accent);
  border-color: var(--accent);
  outline: none;
}
.stat-links a.hot {
  color: var(--el-color-danger);
}
.stat.attention:nth-child(3) {
  border-color: #ffd4ce;
  background: linear-gradient(180deg, #fff7f6 0%, #fff 72%);
}
.stat.attention:nth-child(3):before {
  background: var(--el-color-danger);
}
.stat.attention:nth-child(3) b {
  color: var(--el-color-danger);
}
.stat.warn:nth-child(4):before {
  background: var(--pending);
}
.stat.warn:nth-child(4) b {
  color: var(--pending);
}

.dashboard-more {
  margin-top: 16px;
}
.dashboard-more > summary {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 4px;
  color: var(--muted-strong);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  user-select: none;
}
.dashboard-more > summary:hover {
  color: var(--ink);
}
.dashboard-more > .dashboard-grid {
  margin-top: 4px;
}
.dashboard-grid.three {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.dashboard-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 16px;
  align-items: start;
}
.dashboard-grid.wide-left {
  grid-template-columns: 1.4fr 0.8fr;
}
.dashboard-grid.single {
  grid-template-columns: 1fr;
}

:global(.dashboard-panel) {
  min-width: 0;
  padding: 16px 18px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: 0 1px 2px rgba(28, 31, 35, 0.03);
}
:global(.dashboard-panel-head) {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--line-soft);
}
:global(.dashboard-panel-head h2) {
  margin: 0;
  color: var(--ink);
  font-size: 15px;
  font-weight: 600;
}
:global(.panel-sub) {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 12px;
}
:global(.panel-empty) {
  padding: 14px 0 6px;
}
:global(.subsection-title) {
  margin-bottom: 10px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 600;
}
:global(.plan-row) {
  display: grid;
  grid-template-columns: minmax(110px, 1fr) 2fr auto;
  align-items: center;
  gap: 10px;
  margin: 8px 0;
  color: var(--muted);
  font-size: 12px;
}
:global(.plan-row i) {
  height: 6px;
  overflow: hidden;
  background: var(--line-soft);
  border-radius: 3px;
}
:global(.plan-row em) {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: 3px;
}
:global(.plan-row strong) {
  text-align: right;
  color: var(--ink);
  font-family: inherit;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
:global(.dashboard-muted) {
  color: var(--muted);
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
  .dashboard-grid.wide-left,
  .dashboard-grid.three {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 980px) {
  .stat-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
@media (max-width: 620px) {
  .stat-grid {
    grid-template-columns: 1fr;
  }
  :global(.dashboard-panel) {
    padding: 14px;
  }
}
</style>
