<template>
  <div class="claude-oauth-panel">
    <div v-if="!compact" class="oauth-intro">
      <span class="oauth-mark"><KeyRound :size="20" /></span>
      <div>
        <strong>连接你的 Claude 账号</strong>
        <p>授权后填入账号与凭证；交付密码、订阅日期与设备资料将在下一步补充。</p>
      </div>
    </div>
    <ol v-if="pending" class="oauth-steps">
      <li>生成链接</li>
      <li>登录并授权</li>
      <li>粘贴结果</li>
    </ol>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <template v-if="pending">
      <label class="oauth-label" for="claude-authorization-url">授权链接</label>
      <div class="oauth-link-row">
        <el-input id="claude-authorization-url" :model-value="pending.authorization_url" readonly />
        <el-button :disabled="busy" aria-label="复制授权链接" @click="copyLink"><Copy :size="16" /></el-button>
        <a
          :href="pending.authorization_url"
          target="_blank"
          rel="noopener noreferrer"
          class="oauth-open"
          aria-label="打开 Claude 授权页"
          ><ExternalLink :size="16" />打开授权页</a
        >
      </div>
      <div class="oauth-link-caption">
        <span>{{ copied ? '链接已复制' : '本次链接 30 分钟内有效，重新获取后旧链接失效。' }}</span
        ><el-button text :disabled="busy" @click="start">重新获取链接</el-button>
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
      />
      <el-button
        type="primary"
        :loading="busy"
        :disabled="!authorizationResult.trim() || disabled"
        class="oauth-exchange"
        @click="exchange"
        >确认授权结果</el-button
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
      <el-button text :loading="busy" :disabled="disabled" class="oauth-done-action" @click="start">重新授权</el-button>
    </div>
    <el-button v-else type="primary" :loading="busy" :disabled="disabled" @click="start">{{
      completed ? '重新授权' : '登录 Claude'
    }}</el-button>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { CheckCircle2, Copy, ExternalLink, KeyRound } from 'lucide-vue-next'
import { api, ApiError, errorMessage } from '../api'
import type { OAuthResult, OAuthSummary } from '../model/account-form'

const props = defineProps<{ proxyId?: string; disabled?: boolean; summary?: OAuthSummary | null }>()
const emit = defineEmits<{ authorized: [result: OAuthResult]; busy: [value: boolean]; started: [] }>()
interface Pending {
  id: string
  authorization_url: string
  expires_at: string
}
const pending = ref<Pending | null>(null)
const authorizationResult = ref('')
const error = ref('')
const busy = ref(false)
const copied = ref(false)
const completed = ref(false)
let generation = 0
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
  setBusy(false)
}
async function start() {
  if (busy.value || props.disabled) return
  reset()
  emit('started')
  const current = generation
  error.value = ''
  setBusy(true)
  try {
    const { data } = await api<Pending>('/claude-oauth/sessions', {
      method: 'POST',
      body: JSON.stringify({ proxy_id: props.proxyId || '' }),
    })
    if (current !== generation) {
      void cancel(data.id)
      return
    }
    pending.value = data
  } catch (e) {
    if (current === generation) error.value = errorMessage(e)
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
    emit('authorized', data)
  } catch (e) {
    if (current !== generation) return
    error.value = errorMessage(e)
    // A local format/state error leaves the pending exchange available.
    if (!(e instanceof ApiError && e.code === 'OAUTH_RESULT_INVALID')) {
      pending.value = null
      authorizationResult.value = ''
    }
  } finally {
    if (current === generation) setBusy(false)
  }
}
async function copyLink() {
  if (await copyText(pending.value!.authorization_url)) {
    copied.value = true
  } else {
    error.value = '无法自动复制，请选中链接手动复制。'
  }
}
watch(
  () => props.proxyId,
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
  background: #f4f9f9;
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
  color: #253735;
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
  margin-top: 14px;
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
