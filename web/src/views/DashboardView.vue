<template>
  <PageHead eyebrow="WORKSPACE" title="仪表盘" description="账号还够不够用：平台状态、需要处理的账号与最近消费。">
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
      <span>今日消费</span><b class="money">{{ formatMoneyFen(dash?.platform.today_model_cost_fen ?? 0) }}</b
      ><small>按模型账单 · 未出齐（约 2 小时延迟）</small>
    </div>
    <div class="stat" data-stat="week_cost">
      <span>7 天消费</span><b class="money">{{ formatMoneyFen(dash?.platform.week_model_cost_fen ?? 0) }}</b
      ><small>含今日 · 平台时区</small>
    </div>
  </div>
  <div class="dashboard-grid">
    <AttentionList
      :items="attention?.items || []"
      :counts="attention?.counts || {}"
      :loading="attentionLoading"
      @open="openAttention"
      @all="go({})"
    />
    <SpendPanel :platform="dash?.platform" @bills="router.push({ name: 'platformBills' })" />
  </div>
  <section class="dashboard-panel dashboard-footer">
    <div class="footer-item">
      <span>网页同步</span>
      <strong>{{ webHealthSource?.last_sync_at ? formatDateTime(webHealthSource.last_sync_at) : '尚未同步' }}</strong>
      <el-tag size="small" :type="webHealth.tone" :title="webHealth.detail">{{ webHealth.label }}</el-tag>
    </div>
    <div class="footer-item">
      <span>指标同步</span>
      <strong>{{ dash?.platform.last_sync_at ? formatDateTime(dash.platform.last_sync_at) : '尚未同步' }}</strong>
      <el-tag size="small" :type="metricsHealth.tone" :title="metricsHealth.detail">{{ metricsHealth.label }}</el-tag>
    </div>
    <div class="footer-item">
      <span>代理</span><strong>{{ dash?.proxies.enabled ?? '—' }} 个启用</strong>
    </div>
    <div class="footer-item">
      <span>任务</span>
      <strong
        >运行 {{ dash?.jobs.running ?? '—' }} · 排队 {{ dash?.jobs.queued ?? '—' }} · 失败
        {{ dash?.jobs.failed ?? '—' }}</strong
      >
    </div>
  </section>
  <p v-if="webHealth.detail || metricsHealth.detail" class="footer-detail" role="status">
    <template v-if="webHealth.detail"
      >网页同步：{{ webHealth.detail }}{{ webHealth.hint ? '，' + webHealth.hint : '' }}</template
    >
    <template v-if="webHealth.detail && metricsHealth.detail"> · </template>
    <template v-if="metricsHealth.detail"
      >指标同步：{{ metricsHealth.detail }}{{ metricsHealth.hint ? '，' + metricsHealth.hint : '' }}</template
    >
  </p>
</template>

<script setup lang="ts">
import { describeMetricsHealth, describeSyncHealth, type SyncHealthView } from '../model/sync-health'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import AttentionList from '../components/AttentionList.vue'
import SpendPanel from '../components/SpendPanel.vue'
import { api, errorMessage } from '../api'
import type { AttentionItem, AttentionResponse, Dashboard } from '../types/api'
import { formatDateTime, formatMoneyFen } from '../utils/format'
import { accountQuery } from '../model/dashboard'

const router = useRouter()
const dash = ref<Dashboard>()
const attention = ref<AttentionResponse>()
const loading = ref(false)
const attentionLoading = ref(false)
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
const metricsHealth = computed<SyncHealthView>(() =>
  dash.value ? describeMetricsHealth(dash.value.platform) : { tone: 'info', label: '未开始' },
)
const webHealthSource = computed(() => dash.value?.platform.web_sync_health)
const webHealth = computed<SyncHealthView>(() =>
  webHealthSource.value ? describeSyncHealth(webHealthSource.value) : { tone: 'info', label: '未配置' },
)

function go(query: Record<string, string>) {
  router.push({ name: 'accounts', query })
}
function openAttention(item: AttentionItem) {
  go(accountQuery(item))
}

async function load() {
  loading.value = true
  attentionLoading.value = true
  try {
    const [d, a] = await Promise.all([api<Dashboard>('/dashboard'), api<AttentionResponse>('/dashboard/attention')])
    dash.value = d.data
    attention.value = a.data
    error.value = ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
    attentionLoading.value = false
  }
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
  padding: 20px 21px;
  box-shadow: var(--shadow);
  position: relative;
  overflow: hidden;
}
.stat[role='button'] {
  cursor: pointer;
}
.stat[role='button']:hover {
  border-color: var(--accent-strong);
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

:global(.stat b) {
  display: block;
  font-size: 30px;
  letter-spacing: -0.06em;
  margin-top: 12px;
}

:global(.stat:nth-child(1) b) {
  color: #167c69;
}

:global(.stat:nth-child(2) b) {
  color: #b06b1d;
}

:global(.stat.attention:nth-child(2) b) {
  color: var(--el-color-danger);
}

:global(.stat:nth-child(3) b) {
  color: #2863a5;
}

:global(.stat:nth-child(4) b) {
  color: #7c4bb1;
}

:global(.stat b.money) {
  font-size: 28px;
  letter-spacing: -0.03em;
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
}

.dashboard-stats .stat {
  min-height: 146px;
  padding: 22px 24px;
}

:global(.dashboard-stats .stat span) {
  font-size: 13px;
  color: #60727e;
}

:global(.dashboard-stats .stat b) {
  font-size: 38px;
  margin-top: 18px;
}

:global(.dashboard-stats .stat small) {
  display: block;
  margin-top: 10px;
  color: var(--muted-soft);
  font-size: 11px;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: 1.05fr 0.95fr;
  gap: 16px;
  margin-top: 18px;
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
  margin-bottom: 18px;
}

:global(.dashboard-panel-head h2) {
  margin: 4px 0 0;
  color: #213d4b;
  font-size: 16px;
}

:global(.panel-kicker) {
  color: var(--accent-strong);
  font:
    700 10px/1 'DM Mono',
    monospace;
  letter-spacing: 0.12em;
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
  font-size: 11px;
}

.dashboard-footer {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 28px;
  align-items: center;
  margin-top: 16px;
  padding: 14px 21px;
}

.footer-item {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted-strong);
  font-size: 11px;
}

.footer-item strong {
  color: #526d78;
  font:
    600 11px 'DM Mono',
    monospace;
}

.footer-detail {
  margin: 8px 4px 0;
  color: var(--muted-strong);
  font-size: 11px;
  line-height: 1.6;
}

@media (max-width: 900px) {
  .dashboard-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 620px) {
  .dashboard-grid {
    gap: 12px;
  }
  :global(.dashboard-panel) {
    padding: 16px;
  }
}
</style>
