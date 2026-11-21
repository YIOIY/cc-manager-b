<template>
  <div class="claude-oauth-panel">
    <div v-if="!compact" class="oauth-intro">
      <span class="oauth-mark"><KeyRound :size="20" /></span>
      <div>
        <strong>连接你的 Claude 账号</strong>
        <p>
          {{
            reauthorizing || savedAccountId
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
        ><el-button text :disabled="busy" @click="start(true)">重新获取链接</el-button>
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
      <p class="oauth-paste-hint">粘贴完整的回调地址或 code#state 会自动确认。</p>
      <el-button
        type="primary"
        :loading="busy"
        :disabled="!authorizationResult.trim() || disabled || retryAfter > 0"
        class="oauth-exchange"
        @click="exchange"
        >{{ retryAfter > 0 ? `确认授权结果（${retryAfter}s）` : '确认授权结果' }}</el-button
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
      <el-button text :loading="busy" :disabled="disabled" class="oauth-done-action" @click="start()">重新授权</el-button>
    </div>
    <el-button v-else type="primary" :loading="busy" :disabled="disabled" @click="start()">{{
      completed ? '重新授权' : '登录 Claude'
    }}</el-button>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { CheckCircle2, Copy, ExternalLink, KeyRound } from 'lucide-vue-next'
import { api, ApiError, errorMessage } from '../api'
import { useSession } from '../composables/useSession'
import type { OAuthResult, OAuthSummary } from '../model/account-form'
import { authorizationResultComplete } from '../model/oauth-result'

const props = defineProps<{
  proxyId?: string
  savedAccountId?: string
  reauthorizing?: boolean
  disabled?: boolean
  summary?: OAuthSummary | null
}>()
const emit = defineEmits<{
  authorized: [result: OAuthResult]
  busy: [value: boolean]
  started: []
  /** The selected proxy cannot reach Claude; the parent should bring the proxy field into view. */
  'change-proxy': []
}>()
const { check, onboardAllowed } = useSession()
interface Pending {
  id: string
  authorization_url: string
  expires_at: string
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

/** Codes where the link itself is dead and only a new authorization helps. */
const deadLinkCodes = new Set(['OAUTH_SESSION_EXPIRED', 'OAUTH_IDENTITY_REJECTED'])
/** Every other proxy-shaped failure keeps the link; only these offer a swap. */
const proxyErrorCodes = new Set(['OAUTH_PROXY_UNREACHABLE', 'OAUTH_PROXY_INVALID', 'PROXY_INVALID'])
function isProxyProblem(e: unknown) {
  if (!(e instanceof ApiError)) return false
  return proxyErrorCodes.has(e.code)
}

function clearRetryCountdown() {
  if (retryTimer) clearInterval(retryTimer)
  retryTimer = undefined
  retryAfter.value = 0
}
function startRetryCountdown(seconds: unknown) {
  const parsed = Math.ceil(Number(seconds))
  const total = Number.isFinite(parsed) && parsed > 0 ? parsed : 30
  clearRetryCountdown()
  retryAfter.value = total
  retryTimer = setInterval(() => {
    retryAfter.value -= 1
    if (retryAfter.value <= 0) clearRetryCountdown()
  }, 1000)
}
/** 429 / busy gate carries retry_after_seconds; the link stays usable. */
function retryWait(e: unknown) {
  if (!(e instanceof ApiError)) return 0
  if (e.code !== 'OAUTH_RATE_LIMITED' && e.code !== 'OAUTH_BUSY') return 0
  startRetryCountdown(e.details?.retry_after_seconds)
  return retryAfter.value
}

/** Authorized and idle: show the compact card instead of the walkthrough. */
const compact = computed(() => !!props.summary && !pending.value)
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
  void cancel(pending.value?.id)
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
  if (busy.value || props.disabled) return
  generation++
  const current = generation
  // A refresh retires the old link server-side; tell the server too so the
  // session does not linger if this browser never comes back.
  const previous = refresh ? pending.value?.id : undefined
  emit('started')
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
        ...(props.savedAccountId ? { account_id: props.savedAccountId } : {}),
        ...(refresh ? { refresh: true } : {}),
      }),
    })
    if (current !== generation) {
      void cancel(data.id)
      return
    }
    if (previous) void cancel(previous)
    pending.value = data
    copied.value = false
    // A cooldown start returns the existing link with the remaining wait: the
    // browser login is already done, only the exchange has to hold back.
    if (data.retry_after_seconds) startRetryCountdown(data.retry_after_seconds)
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
  if (!pending.value || busy.value || props.disabled || !authorizationResult.value.trim()) return
  const current = generation
  const id = pending.value.id
  setBusy(true)
  error.value = ''
  proxyProblem.value = false
  try {
    const { data } = await api<OAuthResult>('/claude-oauth/sessions/' + id + '/exchange', {
      method: 'POST',
      body: JSON.stringify({ result: authorizationResult.value.trim() }),
      timeoutMs: 45000,
    })
    if (current !== generation) return
    pending.value = null
    authorizationResult.value = ''
    completed.value = true
    clearRetryCountdown()
    emit('authorized', data)
  } catch (e) {
    if (current !== generation) return
    error.value = errorMessage(e)
    proxyProblem.value = isProxyProblem(e)
    retryWait(e)
    // Only a dead link needs a new authorization. A typo, a rate limit, a
    // supplier 5xx or a broken exit all keep the pasted code and the link, so
    // the user retries the same attempt instead of redoing the browser login.
    if (e instanceof ApiError && deadLinkCodes.has(e.code)) {
      pending.value = null
      authorizationResult.value = ''
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
  () => [props.proxyId, props.savedAccountId],
  () => {
    // Only a link that is still waiting to be used is invalidated by a proxy
    // change; after authorization the credentials are already in the form.
    const hadPending = !!pending.value
    reset()
    if (hadPending) error.value = '代理已变更，请重新生成授权链接。'
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
