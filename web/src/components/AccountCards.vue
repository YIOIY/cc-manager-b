<template>
  <div class="platform-card-grid">
    <article v-for="card in cards" :key="card.id" class="usage-card">
      <div class="usage-card-head">
        <div class="usage-identity">
          <span class="health-dot" :class="card.statusTone"></span>
          <strong :title="card.platform_alias || card.account">{{
            shortPlatformAlias(card.platform_alias) || card.account || '未命名账号'
          }}</strong>
        </div>
        <span class="delivery-status" :class="card.delivery.tone">{{ card.delivery.label }}</span>
      </div>
      <div class="account-card-identity">
        <span :title="card.account">{{ card.account }}</span>
        <span v-if="card.platform_account_id" class="account-platform-id">平台 ID {{ card.platform_account_id }}</span>
      </div>
      <div class="account-card-badges">
        <span v-if="card.platform?.provider" class="mini-badge provider">{{ card.platform.provider }}</span>
        <span class="mini-badge yellow">{{ planLabel(card.plan) || '未配置套餐' }}</span>
        <span
          v-if="card.deliveryState === 'linked'"
          class="mini-badge"
          :class="card.statusFailing ? 'attention' : 'teal'"
        >
          {{ card.statusLabel }}<Lock v-if="card.platform?.locked" :size="10" class="locked-icon" />
        </span>
        <span v-if="ownerLabel(card)" class="mini-badge creator" :title="'所属用户 ' + ownerLabel(card)"
          >所属 {{ ownerLabel(card) }}</span
        >
        <span v-else-if="isAdmin" class="mini-badge creator muted-owner" title="尚未分配给用户，仅管理员可见"
          >未分配</span
        >
        <span v-if="card.platform?.auto_created" class="mini-badge" title="由平台同步自动创建的本地账号">来自平台</span>
        <span v-if="card.platform?.missing_since" class="mini-badge attention" :title="card.platform.missing_since"
          >平台已移除</span
        >
        <span v-if="card.archived" class="mini-badge">已归档</span>
        <span v-if="card.platform?.remote_created_at" class="account-card-created"
          >创建时间 {{ card.platform.remote_created_at }}</span
        >
      </div>
      <template v-if="card.deliveryState === 'linked' && card.platform">
        <div class="usage-card-rule"></div>
        <div class="platform-profile">
          <div v-for="field in card.profileFields" :key="field.key">
            <small>{{ field.label }}</small>
            <span :title="field.value">{{ field.value || '—' }}</span>
            <em v-if="field.diff" class="profile-diff" :title="'本地：' + field.local">≠本地</em>
          </div>
        </div>
        <div
          v-if="card.statusFailing && card.platform.reason_text"
          class="platform-reason"
          :title="card.platform.reason_text"
        >
          <AlertCircle :size="14" />
          <span>{{ card.platform.reason_text }}</span>
          <small v-if="card.platform.reason_fetched_at">{{ formatDateTime(card.platform.reason_fetched_at) }}</small>
        </div>
        <div v-if="card.needsReauth" class="platform-reauth-hint">
          <span>Claude 登录失败，可重新授权后同步到平台。</span>
        </div>
        <PlatformUsageBlock
          :usage="card.platform.usage"
          :observed-at="card.platform.observed_at"
          :usd-rate="card.platform.usd_cny_rate"
        />
        <div v-if="hasValue(card.platform_requests)" class="usage-stats">
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
            <small>最近账单</small
            ><b class="purple-number">{{ billUsd(card.platform_cost_fen, card.platform?.usd_cny_rate) }}</b>
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
        <p :title="card.deliveryDetail">{{ card.deliveryDetail }}</p>
        <div v-if="card.deliveryState === 'pending' || card.deliveryState === 'failed'" class="account-material-status">
          <span>{{ card.password_configured ? '密码已配置' : '密码待补充' }}</span
          ><span>{{ card.credentials_configured ? '凭证已配置' : '凭证待补充' }}</span>
        </div>
      </div>
      <div class="usage-card-foot">
        <span
          v-if="card.footer"
          :title="card.deliveryState === 'linked' ? card.connectionNames : card.deliveryDetail"
          >{{ card.footer }}</span
        >
        <el-button
          v-if="isAdmin"
          class="card-action"
          size="small"
          title="将账号分配给指定用户"
          @click="emit('assign', card)"
        >
          分配 <UserRoundPlus :size="13" />
        </el-button>
        <el-button
          v-if="canEditPlatform(card)"
          class="card-action"
          size="small"
          title="直接修改供应商平台上的账号资料"
          @click="emit('edit-platform', card)"
        >
          修改平台资料 <PencilLine :size="13" />
        </el-button>
        <el-button
          v-if="card.needsReauth && canReauth"
          class="card-action"
          type="primary"
          size="small"
          title="重新登录 Claude 并更新平台凭证"
          @click="emit('reauthorize', card)"
        >
          重新授权 <KeyRound :size="13" />
        </el-button>
        <el-button
          class="card-action"
          :type="card.deliveryState === 'linked' || card.needsReauth ? '' : 'primary'"
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
import {
  RefreshCw,
  AlertCircle,
  Info,
  ArrowUpRight,
  ChartNoAxesCombined,
  Lock,
  PencilLine,
  UserRoundPlus,
  KeyRound,
} from 'lucide-vue-next'
import type { Account, PlatformInfo } from '../types/api'
import {
  deliveryDescription,
  deliveryState,
  deliveryStates,
  needsReauth,
  runtimeLabel,
  type DeliveryState,
} from '../account-workflow'
import { formatCount, formatDateTime, hasValue, planLabel } from '../utils/format'
import { fenToUsdCents, formatUsdCents } from '../model/money'
import { useSession } from '../composables/useSession'
import PlatformUsageBlock from './PlatformUsageBlock.vue'
import { formatTags, locationLabel, paymentLabel, shortPlatformAlias } from '../model/platform-card'

function billUsd(fen: number | undefined, rate: number | undefined) {
  if (!hasValue(fen)) return '未同步'
  const cents = fenToUsdCents(Number(fen), rate)
  return cents === null ? '—' : formatUsdCents(cents)
}

const props = defineProps<{ accounts: Account[]; loading: boolean }>()
const emit = defineEmits<{
  select: [account: Account]
  'edit-platform': [account: Account]
  reauthorize: [account: Account]
  assign: [account: Account]
}>()
const { can, owns, user } = useSession()
const isAdmin = computed(() => !!user.value?.is_admin)
const canReauth = computed(() => can('accounts.credentials') || can('accounts.onboard'))

// The platform edit needs ownership, the permission, a record the platform
// still lists, and a console channel that is configured and not suspended.
function canEditPlatform(card: Account): boolean {
  const p = card.platform
  return (
    owns(card.created_by_user_id) &&
    can('accounts.platform_edit') &&
    !!p &&
    !p.missing_since &&
    !!p.web_channel?.configured &&
    !p.web_channel?.suspended
  )
}

function ownerLabel(row: Account): string {
  return (row.created_by_nickname || row.created_by_username || '').trim()
}

type ProfileField = { key: string; label: string; value: string; local: string; diff: boolean }
type Card = Account & {
  deliveryState: DeliveryState
  delivery: (typeof deliveryStates)[DeliveryState]
  statusLabel: string
  statusTone: string
  statusFailing: boolean
  needsReauth: boolean
  connectionNames: string
  footer: string
  profileFields: ProfileField[]
}

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

// The platform card's two-column profile, with a marker where the local
// record disagrees with the platform.
function profileFields(row: Account, p: PlatformInfo): ProfileField[] {
  const f = p.profile
  const field = (
    key: string,
    label: string,
    value: string,
    local = '',
    display?: (v: string) => string,
  ): ProfileField => ({
    key,
    label,
    value: display ? display(value) : value,
    local: display ? display(local) : local,
    diff: !!value && !!local && value !== local,
  })
  const list: ProfileField[] = [
    field('alias', '账号别名', shortPlatformAlias(p.alias) || p.alias),
    field('aid', '平台账号 ID', p.aid || p.upstream_id),
    field('plan', '套餐', f.plan, row.plan),
    field('register_time', '注册时间', f.register_time, row.register_time),
    field('plan_start_time', '套餐开始时间', f.plan_start_time, row.plan_start_time),
    field('plan_end_time', '套餐结束时间', f.plan_end_time, row.plan_end_time),
    field('register_location', '注册地', f.register_location, row.register_location, locationLabel),
    field('balance', '套餐余量', f.balance ? f.balance + '%' : ''),
    field('payment_method', '支付方式', f.payment_method, '', paymentLabel),
    field('register_device', '注册设备', f.register_device),
  ]
  if (f.recharge_time) list.push(field('recharge_time', '充值日期', f.recharge_time))
  if (f.warranty_period) list.push(field('warranty_period', '质保期', f.warranty_period))
  if (f.tags?.length) list.push(field('tags', '标签', formatTags(f.tags)))
  return list
}

const cards = computed<Card[]>(() =>
  props.accounts.map((row) => {
    const state = deliveryState(row)
    const names = (row.platform_links || [])
      .map((l) => l.connection_name)
      .filter(Boolean)
      .join('、')
    const platformStatus = row.platform?.status || row.platform_status
    const failing = failingStatuses.has(platformStatus)
    const detail = deliveryDescription(state, row.submission_job)
    return {
      ...row,
      deliveryState: state,
      delivery: deliveryStates[state],
      deliveryDetail: detail,
      statusLabel: runtimeLabel(platformStatus),
      statusTone:
        state === 'linked'
          ? failing
            ? 'attention'
            : 'online'
          : state === 'failed' || state === 'unknown'
            ? 'attention'
            : 'unknown',
      statusFailing: failing,
      needsReauth: state === 'linked' && needsReauth(platformStatus),
      connectionNames: names,
      profileFields: row.platform ? profileFields(row, row.platform) : [],
      footer: state === 'linked' ? '' : state === 'submitting' ? '上号结果自动更新' : '尚未完成平台上号',
    }
  }),
)
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

.mini-badge.attention {
  background: #3d1414;
  color: #ff8a80;
}

.mini-badge.provider {
  background: #1aa383;
  color: white;
  text-transform: none;
}

.mini-badge.creator {
  background: #1a2a3a;
  color: #8ec8e8;
  text-transform: none;
}

.mini-badge.muted-owner {
  opacity: 0.85;
  color: #a8b4c0;
}

.locked-icon {
  margin-left: 4px;
  vertical-align: -1px;
}

.account-card-created {
  margin-left: auto;
  font-size: 11px;
  color: var(--text-tertiary, #909399);
}

.platform-profile {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px 14px;
  margin-top: 10px;
}

.platform-profile > div {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  font-size: 12px;
}

.platform-profile small {
  flex: none;
  color: var(--text-secondary, #606266);
}

.platform-profile span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.profile-diff {
  flex: none;
  font-style: normal;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 4px;
  background: #39360e;
  color: #e7d231;
}

.platform-reason {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 10px;
  padding: 8px 10px;
  border-radius: 8px;
  background: #3d1414;
  color: #ff8a80;
  font-size: 12px;
}

.platform-reason span {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.platform-reason small {
  flex: none;
  opacity: 0.8;
}

.platform-reauth-hint {
  margin-top: 8px;
  color: #c9a227;
  font-size: 12px;
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

.platform-monitor .mini-badge.creator {
  background: #eef5fa;
  color: #3a6f8f;
  border-color: #c5d8e6;
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

.platform-monitor .usage-card-foot .card-action:first-of-type {
  margin-left: auto;
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
  max-width: 100%;
  margin: 7px 0 0;
  color: #87969d;
  font-family: inherit;
  font-size: 11px;
  line-height: 1.55;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 5;
  overflow: hidden;
  word-break: break-word;
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
