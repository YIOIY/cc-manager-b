<template>
  <div class="platform-card-grid">
    <article v-for="card in cards" :key="card.id" class="usage-card">
      <div class="usage-card-head">
        <div class="usage-identity">
          <span class="health-dot" :class="card.statusTone"></span>
          <strong :title="card.platform_alias || card.account">{{
            card.platform_alias || card.account || '未命名账号'
          }}</strong>
        </div>
        <span class="delivery-status" :class="card.delivery.tone">{{ card.delivery.label }}</span>
      </div>
      <div class="account-card-identity">
        <span :title="card.account">{{ card.account }}</span>
        <span v-if="card.platform_account_id" class="account-platform-id">平台 ID {{ card.platform_account_id }}</span>
      </div>
      <div class="account-card-badges">
        <span class="mini-badge yellow">{{ planLabel(card.plan) || '未配置套餐' }}</span>
        <span v-if="card.deliveryState === 'linked'" class="mini-badge teal">{{ card.statusLabel }}</span>
        <span v-if="card.archived" class="mini-badge">已归档</span>
      </div>
      <template v-if="card.deliveryState === 'linked'">
        <div class="usage-card-rule"></div>
        <div class="usage-stats">
          <div>
            <small>今日请求</small><b>{{ formatCount(card.platform_requests) }}</b>
          </div>
          <div>
            <small>输入 Token</small><b class="blue-number">{{ formatCount(card.platform_prompt_tokens) }}</b>
          </div>
          <div>
            <small>输出 Token</small><b class="date-number">{{ formatCount(card.platform_completion_tokens) }}</b>
          </div>
          <div>
            <small>最近账单</small><b class="purple-number">{{ formatMoneyFen(card.platform_cost_fen, '未同步') }}</b>
          </div>
        </div>
        <div class="usage-bars">
          <div class="bar-row">
            <span>请求</span
            ><i><em class="bar-blue" :style="{ width: width(card.platform_requests, maxRequests) + '%' }"></em></i
            ><strong>{{ formatCount(card.platform_requests) }}</strong>
          </div>
          <div class="bar-row">
            <span>输入</span
            ><i><em class="bar-pink" :style="{ width: width(card.platform_prompt_tokens, maxTokens) + '%' }"></em></i
            ><strong>{{ formatCount(card.platform_prompt_tokens) }}</strong>
          </div>
          <div class="bar-row">
            <span>输出</span
            ><i
              ><em class="bar-cyan" :style="{ width: width(card.platform_completion_tokens, maxTokens) + '%' }"></em></i
            ><strong>{{ formatCount(card.platform_completion_tokens) }}</strong>
          </div>
        </div>
      </template>
      <div v-else class="account-delivery-placeholder" :class="card.delivery.tone">
        <component
          :is="card.deliveryState === 'submitting' ? RefreshCw : card.deliveryState === 'failed' ? AlertCircle : Info"
          :size="22"
          :class="{ 'is-spinning': card.deliveryState === 'submitting' }"
        />
        <strong>{{ card.delivery.label }}</strong>
        <p>{{ card.delivery.description }}</p>
        <div v-if="card.deliveryState === 'pending' || card.deliveryState === 'failed'" class="account-material-status">
          <span>{{ card.password_configured ? '密码已配置' : '密码待补充' }}</span
          ><span>{{ card.credentials_configured ? '凭证已配置' : '凭证待补充' }}</span>
        </div>
      </div>
      <div class="usage-card-foot">
        <span :title="card.deliveryState === 'linked' ? card.connectionNames : card.delivery.description">{{
          card.footer
        }}</span>
        <el-button
          class="card-action"
          :type="card.deliveryState === 'linked' ? '' : 'primary'"
          :disabled="card.archived && card.deliveryState !== 'linked'"
          size="small"
          @click="emit('select', card)"
        >
          {{ can('accounts.onboard') ? card.delivery.action : '查看详情' }} <ArrowUpRight :size="13" />
        </el-button>
      </div>
    </article>
    <div v-if="!cards.length" class="platform-empty">
      <ChartNoAxesCombined :size="34" /><strong>{{ loading ? '正在加载账号…' : '暂无账号' }}</strong
      ><span>新建账号后将在这里展示平台观测数据。</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RefreshCw, AlertCircle, Info, ArrowUpRight, ChartNoAxesCombined } from 'lucide-vue-next'
import type { Account } from '../types/api'
import { deliveryState, deliveryStates, runtimeLabel, type DeliveryState } from '../account-workflow'
import { formatCount, formatMoneyFen, hasValue, planLabel } from '../utils/format'
import { useSession } from '../composables/useSession'

const props = defineProps<{ accounts: Account[]; allAccounts: Account[]; loading: boolean }>()
const emit = defineEmits<{ select: [account: Account] }>()
const { can } = useSession()

type Card = Account & {
  deliveryState: DeliveryState
  delivery: (typeof deliveryStates)[DeliveryState]
  statusLabel: string
  statusTone: string
  connectionNames: string
  footer: string
}

const cards = computed<Card[]>(() =>
  props.accounts.map((row) => {
    const state = deliveryState(row)
    const names = (row.platform_links || [])
      .map((l) => l.connection_name)
      .filter(Boolean)
      .join('、')
    return {
      ...row,
      deliveryState: state,
      delivery: deliveryStates[state],
      statusLabel: runtimeLabel(row.platform_status),
      statusTone: state === 'linked' ? 'online' : state === 'failed' || state === 'unknown' ? 'attention' : 'unknown',
      connectionNames: names,
      footer:
        state === 'linked'
          ? '已关联 · ' + (names || '平台')
          : state === 'submitting'
            ? '上号结果自动更新'
            : '尚未完成平台上号',
    }
  }),
)
function maxOf(values: number[]) {
  let max = 1
  for (const v of values) if (v > max) max = v
  return max
}
const maxRequests = computed(() => maxOf(props.allAccounts.map((row) => Number(row.platform_requests) || 0)))
const maxTokens = computed(() =>
  maxOf(
    props.allAccounts.flatMap((row) => [
      Number(row.platform_prompt_tokens) || 0,
      Number(row.platform_completion_tokens) || 0,
    ]),
  ),
)
function width(value: unknown, max: number) {
  return hasValue(value) ? Math.max(2, Math.min(100, (Number(value) / max) * 100)) : 0
}
</script>

<style scoped>
:global(.usage-card-foot > div) {
  display: flex;
  align-items: center;
  gap: 8px;
}

:global(.usage-card-foot .el-button) {
  padding: 0 2px;
  font-size: 10px;
}

:global(.usage-card-foot .el-button svg) {
  vertical-align: -2px;
}

.platform-card-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
  padding: 16px 18px 20px;
}

.usage-card {
  position: relative;
  overflow: hidden;
  min-width: 0;
  padding: 13px 14px 11px;
  background: #08111f;
  border: 1px solid #aa9516;
  border-radius: 14px;
  box-shadow:
    inset 0 0 0 1px rgba(255, 219, 52, 0.08),
    0 9px 22px rgba(0, 0, 0, 0.22);
  transition:
    transform 0.18s ease,
    border-color 0.18s ease;
}

.usage-card:hover {
  transform: translateY(-2px);
  border-color: #e0c426;
}

.usage-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.usage-identity {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

:global(.usage-identity strong) {
  overflow: hidden;
  color: #d7e6f7;
  font: 700 12px/1.2 monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.health-dot {
  width: 9px;
  height: 9px;
  flex: none;
  border-radius: 50%;
  background: #f3dd37;
  box-shadow: 0 0 9px rgba(243, 221, 55, 0.65);
}

.health-dot.online {
  background: #54ddbd;
  box-shadow: 0 0 9px rgba(84, 221, 189, 0.65);
}

.health-dot.attention {
  background: #ffbc4f;
  box-shadow: 0 0 9px rgba(255, 188, 79, 0.55);
}

.health-dot.unknown {
  background: #7f93aa;
  box-shadow: none;
}

.mini-badge {
  padding: 4px 7px;
  border-radius: 5px;
  font: 700 10px/1 monospace;
}

.mini-badge.green {
  background: #063a35;
  color: #50ddbe;
}

.mini-badge.teal {
  background: #063730;
  color: #36d8b3;
}

.mini-badge.yellow {
  background: #39360e;
  color: #e7d231;
}

.usage-card-rule {
  height: 1px;
  margin: 14px 0 12px;
  background: #18283e;
}

.usage-stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 7px;
}

:global(.usage-stats small) {
  display: block;
  margin-bottom: 6px;
  color: #7187a0;
  font: 10px/1 monospace;
}

:global(.usage-stats b) {
  display: block;
  overflow: hidden;
  color: #dce7f5;
  font: 700 16px/1 monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-stats .blue-number {
  color: #46aefa;
}

.usage-stats .date-number {
  color: #a8d4ff;
}

.usage-bars {
  display: grid;
  gap: 8px;
  margin-top: 15px;
}

.bar-row {
  display: grid;
  grid-template-columns: 30px 1fr 36px;
  align-items: center;
  gap: 7px;
  color: #6d839a;
  font: 10px/1 monospace;
}

:global(.bar-row i) {
  height: 4px;
  overflow: hidden;
  background: #1a2a40;
  border-radius: 99px;
}

:global(.bar-row em) {
  display: block;
  height: 100%;
  border-radius: 99px;
}

.bar-blue {
  background: #4caeff;
}

.bar-pink {
  background: #f25776;
}

.bar-cyan {
  background: #68c7f4;
}

:global(.bar-row strong) {
  text-align: right;
  color: #56adf1;
  font: 700 10px/1 monospace;
}

.usage-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 14px;
  color: #74899f;
  font: 10px/1 monospace;
}

:global(.usage-card-foot strong) {
  padding: 4px 7px;
  border-radius: 6px;
  background: #102846;
  color: #5eaef0;
}

.platform-empty {
  grid-column: 1/-1;
  display: flex;
  min-height: 300px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: #7890aa;
}

:global(.platform-empty strong) {
  color: #c8d8e8;
  font-size: 14px;
}

:global(.platform-empty span) {
  font-size: 11px;
}

@media (max-width: 1240px) {
  .platform-card-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 920px) {
  .platform-card-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 620px) {
  .platform-card-grid {
    grid-template-columns: 1fr;
    padding: 12px;
  }
  .usage-card {
    border-radius: 11px;
  }
  :global(.usage-stats b) {
    font-size: 14px;
  }
}

.platform-monitor .platform-card-grid {
  padding: 16px 15px 20px;
  gap: 14px;
  background: var(--surface-soft);
  border-top: 1px solid var(--line-soft);
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 350px), 1fr));
  align-items: stretch;
}

.platform-monitor .usage-card {
  padding: 18px;
  background: var(--surface);
  border: 1px solid #dfe8e8;
  border-radius: 12px;
  box-shadow: 0 5px 16px rgba(20, 35, 40, 0.055);
  display: flex;
  flex-direction: column;
}

.platform-monitor .usage-card:hover {
  transform: translateY(-2px);
  border-color: #9bd7ca;
  box-shadow: 0 10px 22px rgba(20, 35, 40, 0.09);
}

:global(.platform-monitor .usage-identity strong) {
  color: var(--ink-soft);
  font:
    700 12px/1.2 'DM Mono',
    monospace;
  font-family: inherit;
  font-size: 13px;
  font-weight: 700;
  line-height: 1.5;
}

.platform-monitor .health-dot {
  background: var(--line-strong);
  box-shadow: none;
}

.platform-monitor .health-dot.online {
  background: #1aa383;
  box-shadow: 0 0 0 3px #e4f6f1;
}

.platform-monitor .health-dot.attention {
  background: #d7a21a;
  box-shadow: 0 0 0 3px #fff5d7;
}

.platform-monitor .usage-card-rule {
  background: var(--line-soft);
}

:global(.platform-monitor .usage-stats small) {
  color: #7e8d95;
}

:global(.platform-monitor .usage-stats b) {
  color: var(--ink-soft);
}

.platform-monitor .usage-stats .blue-number {
  color: #1784bc;
}

.platform-monitor .usage-stats .date-number {
  color: #52728b;
}

.platform-monitor .usage-bars {
  margin-top: 16px;
}

.platform-monitor .bar-row {
  color: #7e8d95;
}

:global(.platform-monitor .bar-row i) {
  background: #edf2f3;
}

:global(.platform-monitor .bar-row strong) {
  color: #397da5;
}

.platform-monitor .usage-card-foot {
  color: #8a989e;
  margin-top: auto;
  padding-top: 18px;
  line-height: 1.5;
  gap: 12px;
}

:global(.platform-monitor .usage-card-foot strong) {
  background: var(--tint-100);
  color: var(--accent-deep);
}

.platform-monitor .mini-badge {
  border: 1px solid transparent;
}

.platform-monitor .mini-badge.green {
  background: #e8f7f2;
  color: var(--accent-deep);
}

.platform-monitor .mini-badge.teal {
  background: var(--tint-100);
  color: var(--accent-deep);
}

.platform-monitor .mini-badge.yellow {
  background: #fff6d9;
  color: #a97900;
}

.platform-monitor .platform-empty {
  color: #9aabb0;
}

:global(.platform-monitor .platform-empty strong) {
  color: #526773;
}

:global(.platform-monitor .platform-empty span) {
  color: #9aa7ad;
}

:global(.platform-monitor .usage-card-foot > div) {
  gap: 10px;
}

.platform-monitor .usage-card-foot .card-action {
  height: 30px;
  padding: 0 10px;
  border-radius: 8px;
  font-weight: 700;
  box-shadow: none;
  flex: none;
  min-height: 31px;
  margin: 0;
}

:global(.platform-monitor .usage-card-foot .card-action svg) {
  margin-left: 3px;
  vertical-align: -2px;
}

:global(.platform-monitor .usage-card-foot > span) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: inherit;
  min-width: 0;
}

.platform-monitor .usage-card-head {
  align-items: flex-start;
  gap: 12px;
}

.platform-monitor .usage-identity {
  padding-top: 4px;
  flex: 1;
}

.account-card-identity {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 7px;
  margin: 8px 0 11px;
  color: #82929a;
  font-size: 11px;
}

:global(.account-card-identity > span:first-child) {
  min-width: 0;
  overflow-wrap: anywhere;
}

.account-platform-id {
  font-family: 'DM Mono', monospace;
  font-size: 10px;
  color: #607e88;
}

.account-card-badges {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}

.account-card-badges .mini-badge {
  font-family: inherit;
  font-size: 10px;
  font-weight: 600;
  line-height: 1.3;
}

.account-delivery-placeholder {
  display: flex;
  align-items: center;
  flex-direction: column;
  justify-content: center;
  min-height: 165px;
  margin-top: 14px;
  padding: 18px 16px;
  border: 1px dashed #e5e9e7;
  border-radius: 9px;
  background: #fcfdfc;
  color: #9aab9f;
  text-align: center;
  box-sizing: border-box;
}

:global(.account-delivery-placeholder > strong) {
  margin-top: 9px;
  color: #586d73;
  font-size: 13px;
}

:global(.account-delivery-placeholder > p) {
  max-width: 280px;
  margin: 7px 0 0;
  color: #87969d;
  font-family: inherit;
  font-size: 11px;
  line-height: 1.7;
}

.account-delivery-placeholder.progress {
  background: #f8fbff;
  border-color: #dae7f5;
  color: #6c9bc5;
}

.account-delivery-placeholder.danger {
  background: #fffafa;
  border-color: #f0dddd;
  color: #ce8d86;
}

.account-delivery-placeholder.warning {
  background: #fffdf8;
  border-color: #eee4ce;
  color: #c3a05e;
}

.account-material-status {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  justify-content: center;
  margin-top: 13px;
  color: #82938a;
  font-size: 10px;
}

@media (max-width: 620px) {
  .platform-monitor .usage-card {
    padding: 15px;
  }
}
</style>
