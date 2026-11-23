<template>
  <div class="claude-oauth-panel">
    <div v-if="!compact" class="oauth-intro">
      <span class="oauth-mark"><KeyRound :size="20" /></span>
      <div>
        <strong>连接你的 Claude 账号</strong>
        <p>
          {{
            reauthorizing || accountId || savedAccountId
              ? '重新授权后更新凭证；设备与代理资料默认沿用，需要更换时可修改。'
              : '授权后填入账号与凭证；交付密码、订阅日期与设备资料将在下一步补充。'
          }}
        </p>
      </div>
    </div>
    <ol v-if="pending" class="oauth-steps">
      <li>生成链接</li>
      <li>登录并授权</li>
      <li>粘贴结果</li>
    </ol>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <p v-if="retryAfter > 0" role="status">请等待 {{ retryAfter }} 秒后重试，当前授权现场已保留。</p>
    <div v-if="error && proxyProblem" class="oauth-proxy-problem">
      <span>换一条代理后重新生成链接即可，已填写的资料会保留。</span>
      <el-button text type="primary" data-change-proxy :disabled="busy" @click="emit('change-proxy')"
        >更换代理</el-button
      >
    </div>
    <template v-if="pending">
      <label class="oauth-label" for="claude-authorization-url">授权链接</label>
      <div class="oauth-link-row">
        <a
          :href="pending.authorization_url"
          target="_blank"
          rel="noopener noreferrer"
          class="oauth-open oauth-open-primary"
          aria-label="打开 Claude 授权页"
          ><ExternalLink :size="16" />打开授权页</a
        >
        <el-input id="claude-authorization-url" :model-value="pending.authorization_url" readonly />
        <el-button :disabled="busy" aria-label="复制授权链接" title="复制链接（打开失败时备用）" @click="copyLink"
          ><Copy :size="16"
        /></el-button>
      </div>
      <div class="oauth-link-caption">
        <span>{{ copied ? '链接已复制' : '本次链接 30 分钟内有效，重新获取后旧链接失效。' }}</span
        ><el-button text :disabled="busy || disabled || retryAfter > 0" @click="start(true)">作废并重新获取</el-button>
      </div>
      <label class="oauth-label" for="claude-authorization-result">授权结果</label>
      <el-input
        id="claude-authorization-result"
        v-model="authorizationResult"
        type="password"
        show-password
        autocomplete="off"
        placeholder="粘贴授权码、code#state 或授权后的完整地址"
        :disabled="busy"
        @keyup.enter.prevent="exchange"
        @paste="onPaste"
      />
      <p class="oauth-paste-hint">
        {{
          resumable
            ? '令牌已获取，正在补读账号资料；可直接点「继续获取资料」，无需再次粘贴。'
            : '粘贴完整的回调地址或 code#state 会自动确认。'
        }}
      </p>
      <el-button
        type="primary"
        :loading="busy"
        :disabled="(!authorizationResult.trim() && !resumable) || disabled || retryAfter > 0 || bindingChanged"
        class="oauth-exchange"
        @click="exchange"
        >{{
          retryAfter > 0
            ? `${resumable ? '继续获取资料' : '确认授权结果'}（${retryAfter}s）`
            : resumable
              ? '继续获取资料'
              : '确认授权结果'
        }}</el-button
      >
    </template>
    <div v-else-if="summary" class="oauth-done">
      <span class="oauth-done-mark"><CheckCircle2 :size="20" /></span>
      <div class="oauth-done-body">
        <div class="oauth-done-title">
          <strong>账号已连接</strong><span class="oauth-done-email">{{ summary.email || '未返回邮箱' }}</span>
        </div>
        <p v-if="summaryMetaParts.length" class="oauth-done-meta">
          <span v-for="part in summaryMetaParts" :key="part">{{ part }}</span>
        </p>
      </div>
      <el-button
        text
        :loading="busy"
        :disabled="disabled || retryAfter > 0"
        class="oauth-done-action"
        @click="start(!!sessionId)"
        >{{ sessionId ? '作废并重新获取' : '重新授权' }}</el-button
      >
    </div>
    <el-button
      v-else
      type="primary"
      :loading="busy"
      :disabled="disabled || retryAfter > 0"
      @click="start(!!sessionId)"
      >{{ sessionId ? '作废并重新获取' : completed ? '重新授权' : '登录 Claude' }}</el-button
    >
  </div>
</template>

<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { CheckCircle2, Copy, ExternalLink, KeyRound } from 'lucide-vue-next'
import { api, ApiError, errorMessage } from '../api'
import { useSession } from '../composables/useSession'
import type { OAuthResult, OAuthSummary } from '../model/account-form'
import { authorizationResultComplete } from '../model/oauth-result'

const props = defineProps<{
  proxyId?: string
  savedAccountId?: string
  accountId?: string
  email?: string
  /** Parent owns a session until explicit discard or final submission. */
  sessionId?: string
  reauthorizing?: boolean
  disabled?: boolean
  summary?: OAuthSummary | null
}>()
const emit = defineEmits<{
  authorized: [result: OAuthResult]
  busy: [value: boolean]
  started: []
  session: [id: string]
  /** The selected proxy cannot reach Claude; the parent should bring the proxy field into view. */
  'change-proxy': []
}>()
const { check, onboardAllowed } = useSession()
interface Pending {
  id: string
  authorization_url: string
  expires_at: string
  proxy_id?: string
  account_id?: string
  /** Server marks recovered, already-authorized sessions so the client can
   * finish the exchange without another browser round trip. */
  stage?: string
  /** Set when the server handed back a live link during a 429 cooldown. */
  retry_after_seconds?: number
}
const pending = ref<Pending | null>(null)
const authorizationResult = ref('')
const error = ref('')
const busy = ref(false)
const copied = ref(false)
const completed = ref(false)
/** The last error came from the proxy path, so changing proxies is the fix. */
const proxyProblem = ref(false)
/** Seconds left before the exchange may be retried (429 / busy gate). */
const retryAfter = ref(0)
let retryTimer: ReturnType<typeof setInterval> | undefined
let generation = 0
let retryDeadline = 0
const boundProxy = ref('')
const boundAccount = ref('')
const bindingChanged = computed(
  () =>
    !!pending.value &&
    (boundProxy.value !== (props.proxyId || '') ||
      boundAccount.value !== (props.accountId || props.savedAccountId || '')),
)

/** Codes where the link itself is dead and only a new authorization helps. */
const deadLinkCodes = new Set(['OAUTH_SESSION_EXPIRED'])
/** Every other proxy-shaped failure keeps the link; only these offer a swap. */
const proxyErrorCodes = new Set([
  'OAUTH_PROXY_UNREACHABLE',
  'OAUTH_PROXY_INVALID',
  'OAUTH_PROXY_CHANGED',
  'PROXY_BINDING_CHANGED',
  'PROXY_INVALID',
])
function isProxyProblem(e: unknown) {
  if (!(e instanceof ApiError)) return false
  return proxyErrorCodes.has(e.code)
}

function clearRetryCountdown() {
  if (retryTimer) clearInterval(retryTimer)
  retryTimer = undefined
  retryAfter.value = 0
  retryDeadline = 0
}
function startRetryCountdown(seconds: unknown) {
  const parsed = Math.ceil(Number(seconds))
  const total = Number.isFinite(parsed) && parsed > 0 ? parsed : 30
  clearRetryCountdown()
  retryDeadline = Date.now() + total * 1000
  retryAfter.value = total
  retryTimer = setInterval(updateRetryCountdown, 1000)
}
function updateRetryCountdown() {
  if (!retryDeadline) return
  retryAfter.value = Math.max(0, Math.ceil((retryDeadline - Date.now()) / 1000))
  if (!retryAfter.value) clearRetryCountdown()
}
/** 429 / busy gate carries retry_after_seconds; the link stays usable. */
function retryWait(e: unknown) {
  if (!(e instanceof ApiError)) return 0
  if (!['OAUTH_RATE_LIMITED', 'OAUTH_BUSY', 'RATE_LIMITED'].includes(e.code)) return 0
  startRetryCountdown(e.details?.retry_after_seconds)
  return retryAfter.value
}

/** Authorized and idle: show the compact card instead of the walkthrough. */
const compact = computed(() => !!props.summary && !pending.value)
/** The server already holds tokens for this session (profile stage or a
 * finished exchange whose response was lost), so it can be resumed without
 * pasting the one-time code again. */
const resumable = computed(() => pending.value?.stage === 'ready' || pending.value?.stage === 'profile')
const summaryMetaParts = computed(() =>
  [props.summary?.organization, props.summary?.plan, props.summary?.expiry].filter((v): v is string => !!v),
)

function setBusy(value: boolean) {
  busy.value = value
  emit('busy', value)
}
async function cancel(id?: string) {
  if (id) await api('/claude-oauth/sessions/' + id, { method: 'DELETE' }).catch(() => {})
}
function reset() {
  generation++
  if (!props.sessionId) void cancel(pending.value?.id)
  pending.value = null
  authorizationResult.value = ''
  copied.value = false
  proxyProblem.value = false
  clearRetryCountdown()
  setBusy(false)
}
// refresh=false reuses the server-side link when one is still waiting, so
// reopening the panel does not mint a fresh PKCE round trip every time.
async function start(refresh = false) {
  updateRetryCountdown()
  if (busy.value || props.disabled || retryAfter.value > 0) return
  generation++
  const current = generation
  // A refresh retires the old link atomically on the server.
  const previous = pending.value?.id
  clearRetryCountdown()
  error.value = ''
  proxyProblem.value = false
  setBusy(true)
  try {
    try {
      await check()
    } catch {
      /* API still enforces */
    }
    if (!onboardAllowed()) {
      error.value = '当前账号已被禁止上号，请联系管理员'
      return
    }
    const { data } = await api<Pending>('/claude-oauth/sessions', {
      method: 'POST',
      body: JSON.stringify({
        proxy_id: props.proxyId || '',
        ...(props.accountId || props.savedAccountId ? { account_id: props.accountId || props.savedAccountId } : {}),
        ...(props.email?.trim() ? { email: props.email.trim() } : {}),
        ...(refresh ? { refresh: true } : {}),
      }),
      timeoutMs: 45_000,
    })
    if (current !== generation) {
      void cancel(data.id)
      return
    }
    // The start endpoint atomically replaces the old session on explicit refresh.
    // Never issue a trailing DELETE that could race a reused session.
    if (data.id !== previous) authorizationResult.value = ''
    pending.value = data
    boundProxy.value = data.proxy_id ?? props.proxyId ?? ''
    boundAccount.value = data.account_id ?? props.accountId ?? props.savedAccountId ?? ''
    emit('session', data.id)
    emit('started')
    copied.value = false
    // A cooldown start returns the existing link with the remaining wait: the
    // browser login is already done, only the exchange has to hold back.
    if (data.retry_after_seconds) startRetryCountdown(data.retry_after_seconds)
    // A ready session from reuse finished its authorization under an earlier
    // click. Finish it silently when the bindings match; otherwise the old
    // flow stays visible behind an explicit replacement prompt.
    if (data.stage === 'ready') {
      if (bindingChanged.value) error.value = '代理或账号已变更，请点击「作废并重新获取」。'
      else void nextTick(() => exchange())
    }
  } catch (e) {
    if (current === generation) {
      error.value = errorMessage(e)
      proxyProblem.value = isProxyProblem(e)
      retryWait(e)
    }
  } finally {
    if (current === generation) setBusy(false)
  }
}
async function exchange() {
  updateRetryCountdown()
  if (
    !pending.value ||
    busy.value ||
    props.disabled ||
    retryAfter.value > 0 ||
    bindingChanged.value ||
    (!authorizationResult.value.trim() && !resumable.value)
  )
    return
  const current = generation
  const id = pending.value.id
  setBusy(true)
  error.value = ''
  proxyProblem.value = false
  try {
    const { data } = await api<OAuthResult>('/claude-oauth/sessions/' + id + '/exchange', {
      method: 'POST',
      body: JSON.stringify({ result: authorizationResult.value.trim() }),
      // The server queues for at most 30 s and then spends up to 60 s on the
      // exchange itself, so a legitimate request can take ~90 s. Giving up
      // earlier is harmless to the code — the server finishes and caches the
      // result — but it would make the user wait for nothing.
      timeoutMs: 95_000,
    })
    if (current !== generation) return
    pending.value = null
    authorizationResult.value = ''
    completed.value = true
    clearRetryCountdown()
    if (data.oauth_session_id) emit('session', data.oauth_session_id)
    emit('authorized', data)
  } catch (e) {
    if (current !== generation) return
    error.value = errorMessage(e)
    proxyProblem.value = isProxyProblem(e)
    retryWait(e)
    if (e instanceof ApiError && e.code === 'OAUTH_RESULT_INVALID') authorizationResult.value = ''
    // The explicit flag covers terminal upstream failures that share an error
    // code with retryable failures. Local input errors keep the link only.
    if (e instanceof ApiError && (deadLinkCodes.has(e.code) || e.details?.oauth_session_expired === true)) {
      pending.value = null
      authorizationResult.value = ''
      emit('session', '')
    }
  } finally {
    if (current === generation) setBusy(false)
  }
}
function onPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text') || ''
  if (!authorizationResultComplete(text)) return
  event.preventDefault()
  authorizationResult.value = text.trim()
  void exchange()
}
async function copyLink() {
  if (await copyText(pending.value!.authorization_url)) {
    copied.value = true
  } else {
    error.value = '无法自动复制，请选中链接手动复制。'
  }
}
watch(
  () => [props.proxyId, props.accountId, props.savedAccountId],
  () => {
    // Preserve the old flow until the user explicitly replaces it.
    if (bindingChanged.value) error.value = '代理或账号已变更，请点击「作废并重新获取」。'
    else if (pending.value) error.value = ''
  },
)
onBeforeUnmount(reset)
</script>

<style scoped>
.claude-oauth-panel {
  padding: 20px;
  border: 1px solid #dce7e8;
  border-radius: 14px;
  background: #f5f6f8;
}
.oauth-intro {
  display: flex;
  gap: 12px;
  align-items: center;
}
.oauth-mark {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  background: #e0eeec;
  color: #38766d;
  flex-shrink: 0;
}
.oauth-intro strong {
  font-size: 15px;
  color: #1c1f23;
}
.oauth-intro p,
.oauth-network-hint {
  margin: 5px 0 0;
  font-size: 12px;
  color: #6a7b79;
  line-height: 1.7;
}
.oauth-steps {
  display: flex;
  gap: 24px;
  padding-left: 20px;
  margin: 17px 0;
  font-size: 12px;
  color: #57746f;
}
.oauth-label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  margin: 16px 0 8px;
}
.oauth-link-row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.oauth-link-row .el-input {
  min-width: 0;
  flex: 1;
}
.oauth-open {
  height: 32px;
  padding: 0 10px;
  border: 1px solid #dcdfe6;
  border-radius: 5px;
  background: white;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: #425b57;
  font-size: 13px;
  white-space: nowrap;
  text-decoration: none;
  flex-shrink: 0;
}
.oauth-link-caption {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  color: #6a7b79;
  font-size: 12px;
}
.oauth-exchange {
  margin-top: 10px;
}
.oauth-paste-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: #6a7b79;
}
.oauth-open-primary {
  background: var(--accent, #2f7cf6);
  border-color: var(--accent, #2f7cf6);
  color: #fff;
  font-weight: 600;
  padding: 0 14px;
}
.oauth-open-primary:hover {
  filter: brightness(1.05);
}
.oauth-proxy-problem {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 8px;
  font-size: 12px;
  color: #6a7b79;
}
.oauth-network-hint {
  margin-top: 14px;
}
@media (max-width: 560px) {
  .claude-oauth-panel {
    padding: 14px;
  }
  .oauth-steps {
    gap: 20px;
  }
  .oauth-link-caption {
    flex-wrap: wrap;
  }
}
.oauth-done {
  display: flex;
  align-items: center;
  gap: 12px;
}
.oauth-done-mark {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: var(--tint-100);
  color: var(--accent-dark);
  flex-shrink: 0;
}
.oauth-done-body {
  flex: 1;
  min-width: 0;
}
.oauth-done-title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}
.oauth-done-title strong {
  font-size: 12px;
  font-weight: 600;
  color: var(--accent-dark);
}
.oauth-done-email {
  font-size: 15px;
  font-weight: 600;
  color: var(--ink-soft);
  word-break: break-all;
}
.oauth-done-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 3px 0 0;
  font-size: 12px;
  color: var(--muted);
}
.oauth-done-action {
  --el-button-text-color: var(--accent-dark);
  --el-button-hover-text-color: var(--accent-strong);
  --el-button-hover-bg-color: var(--tint-100);
}
</style>
