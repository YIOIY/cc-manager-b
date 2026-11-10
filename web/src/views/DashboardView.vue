<template>
  <PageHead eyebrow="WORKSPACE" title="仪表盘" description="库存、任务与平台连接概览。">
    <el-button :loading="loading" @click="load"><RefreshCw :size="15" />刷新</el-button>
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon @close="error = ''" />
  <div class="stat-grid dashboard-stats">
    <div class="stat">
      <span>账号总数</span><b>{{ dash?.accounts.total ?? '—' }}</b
      ><small>归档 {{ dash?.accounts.archived ?? '—' }} 个</small>
    </div>
    <div class="stat">
      <span>可交付账号</span><b>{{ dash?.accounts.ready ?? '—' }}</b
      ><small>已关联平台 {{ dash?.accounts.platform_linked ?? '—' }}</small>
    </div>
    <div class="stat">
      <span>运行任务</span><b>{{ dash?.jobs.running ?? '—' }}</b
      ><small>排队 {{ dash?.jobs.queued ?? '—' }} 个</small>
    </div>
    <div class="stat">
      <span>启用代理</span><b>{{ dash?.proxies.enabled ?? '—' }}</b
      ><small>停用 {{ dash?.proxies.disabled ?? '—' }} 个</small>
    </div>
  </div>
  <div class="dashboard-grid">
    <section class="dashboard-panel dashboard-inventory">
      <div class="dashboard-panel-head">
        <div>
          <span class="panel-kicker">INVENTORY</span>
          <h2>库存概览</h2>
        </div>
        <UsersRound :size="19" />
      </div>
      <div class="inventory-list">
        <div>
          <span>平台已关联</span><strong>{{ dash?.accounts.platform_linked ?? '—' }}</strong>
        </div>
        <div>
          <span>最近 10 分钟有观测</span><strong>{{ dash?.accounts.observed_recently ?? '—' }}</strong>
        </div>
        <div>
          <span>账号完成度</span><strong>{{ readiness }}</strong>
        </div>
      </div>
      <div class="plan-list">
        <div class="subsection-title">套餐分布</div>
        <div v-for="row in planRows" :key="row.plan" class="plan-row">
          <span>{{ row.label }}</span
          ><i><em :style="{ width: row.width + '%' }"></em></i><strong>{{ row.count }}</strong>
        </div>
        <div v-if="!planRows.length" class="dashboard-muted">暂无套餐数据</div>
      </div>
    </section>
    <section class="dashboard-panel">
      <div class="dashboard-panel-head">
        <div>
          <span class="panel-kicker">TASK CENTER</span>
          <h2>任务状态</h2>
        </div>
        <ListChecks :size="19" />
      </div>
      <div class="task-summary">
        <div class="task-donut">
          <b>{{ (dash?.jobs.running || 0) + (dash?.jobs.queued || 0) }}</b
          ><span>待处理</span>
        </div>
        <div class="task-legend">
          <div>
            <i class="dot amber"></i><span>运行中</span><strong>{{ dash?.jobs.running ?? '—' }}</strong>
          </div>
          <div>
            <i class="dot blue"></i><span>排队中</span><strong>{{ dash?.jobs.queued ?? '—' }}</strong>
          </div>
          <div>
            <i class="dot green"></i><span>已成功</span><strong>{{ dash?.jobs.succeeded ?? '—' }}</strong>
          </div>
          <div>
            <i class="dot red"></i><span>失败</span><strong>{{ dash?.jobs.failed ?? '—' }}</strong>
          </div>
        </div>
      </div>
    </section>
    <section class="dashboard-panel">
      <div class="dashboard-panel-head">
        <div>
          <span class="panel-kicker">PLATFORM USAGE</span>
          <h2>今日平台消耗</h2>
        </div>
        <ChartNoAxesCombined :size="19" />
      </div>
      <div class="usage-summary">
        <div>
          <small>请求数</small><b>{{ formatCount(dash?.platform.today_requests ?? 0) }}</b>
        </div>
        <div>
          <small>平台消费</small><b class="purple-number">{{ formatMoneyFen(dash?.platform.today_cost_fen) }}</b>
        </div>
      </div>
      <div class="sync-strip">
        <span>最近同步</span
        ><strong>{{ dash?.platform.last_sync_at ? formatDateTime(dash.platform.last_sync_at) : '尚未同步' }}</strong>
        <el-tag :type="syncTone">{{ dash?.platform.last_sync_state || '未开始' }}</el-tag>
      </div>
    </section>
    <section class="dashboard-panel">
      <div class="dashboard-panel-head">
        <div>
          <span class="panel-kicker">QUICK STATUS</span>
          <h2>系统状态</h2>
        </div>
        <Activity :size="19" />
      </div>
      <div class="status-list">
        <div>
          <span>代理池</span><strong class="status-good">{{ dash?.proxies.healthy ?? '—' }} 个启用</strong>
        </div>
        <div>
          <span>平台观测</span><strong>{{ dash?.accounts.observed_recently ?? '—' }} 个账号</strong>
        </div>
        <div>
          <span>任务完成</span><strong>{{ dash?.jobs.succeeded ?? '—' }} 个成功</strong>
        </div>
      </div>
      <el-button class="dashboard-link" text type="primary" @click="router.push({ name: 'platformBills' })">
        查看平台离线账单 <ArrowUpRight :size="14" />
      </el-button>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw, UsersRound, ListChecks, ChartNoAxesCombined, Activity, ArrowUpRight } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import { api, errorMessage } from '../api'
import type { Dashboard } from '../types/api'
import { formatCount, formatDateTime, formatMoneyFen, planLabel } from '../utils/format'

const router = useRouter()
const dash = ref<Dashboard>()
const loading = ref(false)
const error = ref('')

const readiness = computed(() =>
  dash.value?.accounts.total ? Math.round((dash.value.accounts.ready / dash.value.accounts.total) * 100) + '%' : '—',
)
const planRows = computed(() => {
  const total = dash.value?.accounts.total || 1
  return Object.entries(dash.value?.accounts.by_plan || {}).map(([plan, count]) => ({
    plan,
    label: planLabel(plan),
    count,
    width: Math.max(8, (Number(count) / total) * 100),
  }))
})
const syncTone = computed(() =>
  dash.value?.platform.last_sync_state === 'succeeded'
    ? 'success'
    : dash.value?.platform.last_sync_state
      ? 'warning'
      : 'info',
)

async function load() {
  loading.value = true
  try {
    dash.value = (await api<Dashboard>('/dashboard')).data
    error.value = ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
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
  color: #2863a5;
}

:global(.stat:nth-child(3) b) {
  color: #b06b1d;
}

:global(.stat:nth-child(4) b) {
  color: #7c4bb1;
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
  grid-template-columns: 1.15fr 0.85fr;
  gap: 16px;
  margin-top: 18px;
}

.dashboard-panel {
  min-width: 0;
  padding: 20px 21px;
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(20, 35, 40, 0.045);
}

.dashboard-panel-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 18px;
}

:global(.dashboard-panel-head svg) {
  color: var(--accent-strong);
}

:global(.dashboard-panel-head h2) {
  margin: 4px 0 0;
  color: #213d4b;
  font-size: 16px;
}

.panel-kicker {
  color: var(--accent-strong);
  font:
    700 10px/1 'DM Mono',
    monospace;
  letter-spacing: 0.12em;
}

.inventory-list {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}

:global(.inventory-list > div) {
  padding: 13px 12px;
  border: 1px solid #e7eeee;
  border-radius: 10px;
  background: #fbfdfd;
}

:global(.inventory-list span) {
  display: block;
  color: #83939a;
  font-size: 11px;
}

:global(.usage-summary small) {
  display: block;
  color: #83939a;
  font-size: 11px;
}

:global(.inventory-list strong) {
  display: block;
  margin-top: 8px;
  color: var(--accent-deep);
  font:
    700 25px/1 'DM Mono',
    monospace;
}

.plan-list {
  margin-top: 22px;
}

.subsection-title {
  margin-bottom: 10px;
  color: #506671;
  font-size: 12px;
  font-weight: 800;
}

.plan-row {
  display: grid;
  grid-template-columns: minmax(110px, 1fr) 2fr 28px;
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
}

.dashboard-muted {
  color: var(--muted-soft);
  font-size: 11px;
}

.task-summary {
  display: flex;
  align-items: center;
  gap: 28px;
  min-height: 142px;
}

.task-donut {
  display: grid;
  place-items: center;
  width: 122px;
  height: 122px;
  flex: none;
  border-radius: 50%;
  background:
    radial-gradient(circle at center, var(--surface) 56%, transparent 57%),
    conic-gradient(#f0ad3b 0 24%, #4e9cda 24% 46%, #4caf87 46% 88%, #e26d6d 88% 100%);
  box-shadow: inset 0 0 0 1px #e6eeee;
}

:global(.task-donut b) {
  margin-top: 16px;
  color: #214251;
  font:
    700 25px 'DM Mono',
    monospace;
}

:global(.task-donut span) {
  margin-top: -17px;
  color: #8a999f;
  font-size: 10px;
}

.task-legend {
  display: grid;
  gap: 13px;
  flex: 1;
}

:global(.task-legend > div) {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--muted-strong);
  font-size: 11px;
}

:global(.status-list > div) {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--muted-strong);
  font-size: 11px;
}

.sync-strip {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--muted-strong);
  font-size: 11px;
}

:global(.task-legend strong) {
  margin-left: auto;
  color: #304c58;
  font:
    700 12px 'DM Mono',
    monospace;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #bbb;
}

.dot.amber {
  background: #efaa38;
}

.dot.blue {
  background: #4e9cda;
}

.dot.green {
  background: #4caf87;
}

.dot.red {
  background: #dd6c6c;
}

.usage-summary {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

:global(.usage-summary > div) {
  padding: 16px 14px;
  border-radius: 10px;
  background: #f6faf9;
  border: 1px solid #e4efed;
}

:global(.usage-summary b) {
  display: block;
  margin-top: 10px;
  color: var(--accent-deep);
  font:
    700 24px 'DM Mono',
    monospace;
}

.sync-strip {
  justify-content: space-between;
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--line-soft);
}

:global(.sync-strip strong) {
  margin-left: auto;
  color: #526d78;
  font:
    11px 'DM Mono',
    monospace;
}

.status-list {
  display: grid;
  gap: 15px;
}

:global(.status-list > div) {
  justify-content: space-between;
  padding-bottom: 13px;
  border-bottom: 1px solid var(--line-soft);
}

:global(.status-list strong) {
  color: #526d78;
  font:
    600 11px 'DM Mono',
    monospace;
}

.status-list .status-good {
  color: var(--accent-deep);
}

.dashboard-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: 18px;
  padding-left: 0;
}

@media (max-width: 900px) {
  .dashboard-grid {
    grid-template-columns: 1fr;
  }
  .inventory-list {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (max-width: 620px) {
  .dashboard-grid {
    gap: 12px;
  }
  .dashboard-panel {
    padding: 16px;
  }
  .inventory-list {
    grid-template-columns: 1fr;
  }
  .task-summary {
    gap: 15px;
  }
  .task-donut {
    width: 100px;
    height: 100px;
  }
}
</style>
