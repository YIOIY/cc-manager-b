<template>
  <section class="proxy-network-panel panel" aria-label="代理检测路径">
    <div class="proxy-network-heading">
      <span class="heading-icon small"><Network :size="18" /></span>
      <div class="proxy-network-title">
        <h2>
          本机检测路径
          <span v-if="settings" class="proxy-mode-tag" :class="{ 'is-upstream': settings.mode === 'upstream' }">{{
            settings.mode === 'upstream' ? '前置代理' : '直连'
          }}</span>
        </h2>
        <p>本机检测与 Claude 授权请求使用此网络设置；前置代理不会提交给供应商。</p>
      </div>
      <el-button v-if="can('proxies.settings')" :loading="loading" :disabled="saving" @click="openSettings">
        <Settings2 :size="15" />修改路径
      </el-button>
    </div>

    <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" show-icon />
    <div v-else class="proxy-route-row">
      <div class="proxy-route" :class="{ 'is-incomplete': incomplete }">
        <span class="proxy-route-node">本地应用</span>
        <ArrowRight :size="14" />
        <template v-if="settings?.mode === 'upstream'">
          <span v-if="settings.upstream_url" class="proxy-route-node is-upstream" :title="settings.upstream_url">
            <span class="proxy-route-kicker">前置代理</span>{{ settings.upstream_url }}
          </span>
          <span v-else class="proxy-route-node is-missing">前置代理未填写</span>
          <ArrowRight :size="14" />
        </template>
        <span class="proxy-route-node">池中代理</span>
        <ArrowRight :size="14" />
        <span class="proxy-route-node">检测网站</span>
      </div>
      <ul class="proxy-route-meta">
        <li v-if="settings?.mode !== 'upstream'"><Globe :size="13" />TUN 开启时由系统路由决定</li>
        <li v-if="settings?.mode === 'upstream'"><KeyRound :size="13" />{{ authSummary }}</li>
        <li><Timer :size="13" />超时 {{ settings?.timeout_seconds ?? '—' }} 秒</li>
        <li v-if="incomplete" class="is-warning"><AlertCircle :size="13" />路径未完成配置，检测会失败</li>
      </ul>
    </div>
  </section>

  <el-dialog
    v-model="settingsOpen"
    title="修改本机检测路径"
    width="600px"
    class="proxy-network-dialog"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @closed="saveError = ''"
  >
    <el-form label-position="top" :disabled="saving" class="proxy-settings-form" @submit.prevent="save">
      <section class="proxy-settings-step">
        <header><b>1</b><span>连接方式</span></header>
        <div class="proxy-mode-cards" role="radiogroup" aria-label="连接方式">
          <button
            type="button"
            class="proxy-mode-card"
            :class="{ 'is-active': draft.mode === 'direct' }"
            role="radio"
            :aria-checked="draft.mode === 'direct'"
            @click="draft.mode = 'direct'"
          >
            <Globe :size="18" />
            <strong>直连 / 系统 TUN</strong>
            <small>本机直接连接池中代理。已开 TUN 或 VPN 时由系统路由接管。</small>
          </button>
          <button
            type="button"
            class="proxy-mode-card"
            :class="{ 'is-active': draft.mode === 'upstream' }"
            role="radio"
            :aria-checked="draft.mode === 'upstream'"
            @click="draft.mode = 'upstream'"
          >
            <Waypoints :size="18" />
            <strong>通过本机前置代理</strong>
            <small>先经过本机代理客户端，再连接池中代理。需要境外来源 IP 时使用。</small>
          </button>
        </div>
      </section>

      <section v-if="draft.mode === 'upstream'" class="proxy-settings-step">
        <header><b>2</b><span>前置代理</span></header>
        <el-form-item label="代理地址" required :error="urlError">
          <el-input
            v-model="draft.upstream_url"
            placeholder="http://127.0.0.1:26800"
            autocomplete="off"
            clearable
            @input="urlTouched = true"
          />
          <p class="field-hint">填写代理客户端实际监听的地址与端口，支持 http://、https://、socks5://、socks5h://。</p>
        </el-form-item>
        <div class="proxy-auth-grid">
          <el-form-item label="用户名（可选）"><el-input v-model="draft.username" autocomplete="off" /></el-form-item>
          <el-form-item label="密码（可选）">
            <el-input
              v-model="draft.password"
              type="password"
              show-password
              autocomplete="new-password"
              :disabled="draft.clear_password"
              :placeholder="draft.password_configured ? '已保存，留空保留' : '无认证可留空'"
            />
          </el-form-item>
        </div>
        <el-checkbox v-if="draft.password_configured" v-model="draft.clear_password">清除已保存的密码</el-checkbox>
      </section>

      <section class="proxy-settings-step">
        <header>
          <b>{{ draft.mode === 'upstream' ? 3 : 2 }}</b
          ><span>检测超时</span>
        </header>
        <div class="proxy-timeout-row">
          <el-input-number v-model="draft.timeout_seconds" :min="5" :max="60" :step="5" />
          <span class="proxy-timeout-unit">秒</span>
          <p class="field-hint">连通性通过后再尝试获取出口 IP，IP 获取失败不影响已通过的结果。</p>
        </div>
      </section>
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer>
      <div class="proxy-settings-footer">
        <span v-if="!draftDirty" class="muted">没有改动</span>
        <span v-else class="muted">保存后立即生效，下次检测和授权使用新路径</span>
        <div>
          <el-button :disabled="saving" @click="settingsOpen = false">取消</el-button>
          <el-button type="primary" :loading="saving" :disabled="!draftDirty || !!urlError" @click="save"
            >保存</el-button
          >
        </div>
      </div>
    </template>
  </el-dialog>

  <el-dialog
    v-model="checksOpen"
    :title="'本机检测 · ' + (selected?.name || selected?.host || '代理')"
    width="760px"
    class="proxy-network-dialog"
  >
    <el-alert
      title="本机检测与供应商网络独立；通过不代表供应商一定可用，失败也不阻断上号。"
      type="info"
      :closable="false"
      show-icon
    />
    <div class="proxy-check-toolbar">
      <span>最近 100 次检测 · 按时间倒序</span
      ><el-button type="primary" :disabled="!selected?.enabled" :loading="testing" @click="test(selected)"
        >重新检测</el-button
      >
    </div>
    <el-alert v-if="checkError" :title="checkError" type="error" :closable="false" />
    <div v-if="testing" class="proxy-check-running">
      <RefreshCw :size="18" class="is-spinning" />正在检测，最多 {{ settings?.timeout_seconds || 20 }} 秒…
    </div>
    <div v-loading="checksLoading" class="proxy-check-list">
      <article v-for="check in checks" :key="check.id" class="proxy-check" :class="check.ok ? 'is-ok' : 'is-failed'">
        <div class="proxy-check-title">
          <el-tag :type="check.ok ? 'success' : 'warning'">{{ check.ok ? '本机可达' : '本机检测失败' }}</el-tag
          ><span>{{ routeLabel(check.route_mode) }}</span
          ><time>{{ formatTime(check.checked_at) }}</time>
        </div>
        <strong>{{ check.message || check.error_code || '历史检测记录' }}</strong>
        <dl>
          <div>
            <dt>检测耗时</dt>
            <dd>{{ check.latency_ms }} ms</dd>
          </div>
          <div>
            <dt>目标代理出口 IP</dt>
            <dd>{{ check.exit_ip || '未获取' }}</dd>
          </div>
          <div v-if="!check.ok">
            <dt>失败阶段</dt>
            <dd>{{ stageLabel(check.stage) }}</dd>
          </div>
        </dl>
        <p v-if="check.error_code" class="proxy-check-code">{{ check.error_code }}</p>
        <p v-if="check.warning" class="proxy-check-warning">{{ check.warning }}</p>
      </article>
      <el-empty v-if="!checks.length && !testing && !checksLoading" description="尚无本机检测记录" :image-size="70" />
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  AlertCircle,
  ArrowRight,
  Globe,
  KeyRound,
  Network,
  RefreshCw,
  Settings2,
  Timer,
  Waypoints,
} from 'lucide-vue-next'
import { api, errorMessage } from '../api'
import { formatDateTime } from '../utils/format'
import { useSession } from '../composables/useSession'
type Settings = {
  mode: 'direct' | 'upstream'
  upstream_url: string
  username: string
  password_configured: boolean
  timeout_seconds: number
  version: number
}
type Draft = Settings & { password: string; clear_password: boolean }
const emit = defineEmits<{ changed: [] }>()
const { can } = useSession()
const settings = ref<Settings>(),
  draft = ref<Draft>({
    mode: 'direct',
    upstream_url: '',
    username: '',
    password: '',
    password_configured: false,
    clear_password: false,
    timeout_seconds: 20,
    version: 1,
  })
const loading = ref(false),
  loadError = ref(''),
  settingsOpen = ref(false),
  saving = ref(false),
  saveError = ref('')
const urlTouched = ref(false)
const PROXY_URL = /^(https?|socks5h?):\/\/[^\s/?#]+(:\d{1,5})?\/?$/i
const incomplete = computed(() => settings.value?.mode === 'upstream' && !settings.value.upstream_url)
const authSummary = computed(() => {
  if (!settings.value) return ''
  if (settings.value.username)
    return settings.value.password_configured
      ? `认证 ${settings.value.username}`
      : `用户名 ${settings.value.username}，未设密码`
  return '无认证'
})
const urlError = computed(() => {
  if (draft.value.mode !== 'upstream' || !urlTouched.value) return ''
  const value = draft.value.upstream_url.trim()
  if (!value) return '请填写前置代理地址'
  if (!PROXY_URL.test(value)) return '格式应为 协议://主机:端口，不能含路径或参数'
  return ''
})
const draftDirty = computed(() => {
  const s = settings.value
  if (!s) return false
  const d = draft.value
  return (
    d.mode !== s.mode ||
    (d.mode === 'upstream' &&
      (d.upstream_url.trim() !== s.upstream_url ||
        d.username.trim() !== s.username ||
        !!d.password ||
        d.clear_password)) ||
    d.timeout_seconds !== s.timeout_seconds
  )
})
const checksOpen = ref(false),
  selected = ref<any>(),
  checks = ref<any[]>([]),
  testing = ref(false),
  checksLoading = ref(false),
  checkError = ref('')
let checkSequence = 0
const routeLabel = (mode: string) =>
  mode === 'upstream' ? '前置代理' : mode === 'direct' ? '直连 / 系统 TUN' : '历史记录（路径未知）'
const stageLabel = (stage: string) =>
  (
    ({
      settings: '配置校验',
      upstream: '前置代理连接 / 隧道',
      target_proxy: '目标代理连接 / 认证',
      target_request: '检测网站访问',
      complete: '已完成',
    }) as Record<string, string>
  )[stage] || '未记录'
const formatTime = (value: string) => formatDateTime(value)
async function loadSettings() {
  loading.value = true
  loadError.value = ''
  try {
    settings.value = (await api<Settings>('/proxy-settings')).data
    return true
  } catch (e) {
    loadError.value = errorMessage(e)
    return false
  } finally {
    loading.value = false
  }
}
async function openSettings() {
  if (saving.value) return
  if (!settings.value && !(await loadSettings())) return
  draft.value = { ...settings.value!, password: '', clear_password: false }
  urlTouched.value = false
  saveError.value = ''
  settingsOpen.value = true
}
async function save() {
  if (saving.value || !draftDirty.value) return
  urlTouched.value = true
  if (urlError.value) return
  saving.value = true
  saveError.value = ''
  try {
    const body = {
      mode: draft.value.mode,
      upstream_url:
        draft.value.mode === 'upstream' ? draft.value.upstream_url.trim() : settings.value?.upstream_url || '',
      username: draft.value.mode === 'upstream' ? draft.value.username.trim() : settings.value?.username || '',
      password: draft.value.password,
      clear_password: draft.value.clear_password,
      timeout_seconds: draft.value.timeout_seconds,
      expected_version: draft.value.version,
    }
    settings.value = (await api<Settings>('/proxy-settings', { method: 'PUT', body: JSON.stringify(body) })).data
    draft.value.password = ''
    settingsOpen.value = false
    emit('changed')
  } catch (e) {
    saveError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}
async function loadChecks() {
  const id = selected.value?.id
  if (!id) return
  const seq = ++checkSequence
  checksLoading.value = true
  try {
    const result = (await api<any[]>('/proxies/' + id + '/checks')).data
    if (seq === checkSequence && selected.value?.id === id) checks.value = result
  } catch (e) {
    if (seq === checkSequence) checkError.value = errorMessage(e)
  } finally {
    if (seq === checkSequence) checksLoading.value = false
  }
}
async function details(row: any) {
  selected.value = row
  checks.value = []
  checkError.value = ''
  checksOpen.value = true
  await loadChecks()
}
async function test(row: any) {
  if (testing.value || !row?.enabled) return
  testing.value = true
  selected.value = row
  checksOpen.value = true
  checkError.value = ''
  checks.value = []
  checkSequence++
  try {
    await api('/proxies/' + row.id + '/test', { method: 'POST' })
    await loadChecks()
    emit('changed')
  } catch (e) {
    checkError.value = errorMessage(e)
  } finally {
    testing.value = false
  }
}
defineExpose({ test, details })
onMounted(loadSettings)
</script>

<style scoped>
.proxy-network-panel {
  padding: 16px 20px 14px;
  margin-bottom: 18px;
}
.proxy-network-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.proxy-network-title {
  flex: 1;
  min-width: 0;
}
.proxy-network-title h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 2px;
  font-size: 15px;
}
.proxy-mode-tag {
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--tint-100);
  color: var(--accent-deep);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.proxy-mode-tag.is-upstream {
  background: #eef0ff;
  color: #4553c4;
}
.proxy-network-title p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}
.proxy-network-heading > .el-button {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}
.proxy-route-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 24px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed var(--line-tint);
}
.proxy-route {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  color: var(--line-strong);
}
.proxy-route-node {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  background: var(--surface-soft);
  border: 1px solid var(--line-soft);
  border-radius: 7px;
  color: var(--ink-mid);
  font-size: 12px;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.proxy-route-node.is-upstream {
  color: #4553c4;
  background: #eef0ff;
  border-color: #dfe2ff;
  font-family: 'DM Mono', monospace;
}
.proxy-route-kicker {
  font-family: Manrope, system-ui, sans-serif;
  font-size: 10px;
  letter-spacing: 0.08em;
  color: #7a85d8;
}
.proxy-route-node.is-missing {
  color: #a9681a;
  background: #fff7e8;
  border-style: dashed;
  border-color: #efd7a7;
}
.proxy-route-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  margin: 0;
  padding: 0;
  list-style: none;
  color: var(--muted);
  font-size: 12px;
}
.proxy-route-meta li {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  white-space: nowrap;
}
.proxy-route-meta li.is-warning {
  color: #a9681a;
}
.proxy-network-panel .el-alert {
  margin-top: 12px;
}
:global(.proxy-network-dialog) {
  max-width: calc(100vw - 32px);
  border-radius: 14px;
}
.proxy-settings-form {
  display: grid;
  gap: 22px;
  margin-top: 4px;
}
.proxy-settings-step > header {
  display: flex;
  justify-content: flex-start;
  align-items: center;
  gap: 9px;
  padding: 0;
  border: 0;
  background: none;
  margin-bottom: 12px;
  font-size: 13px;
  font-weight: 700;
  color: #243041;
}
.proxy-settings-step > header b {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 6px;
  background: var(--tint-100);
  color: var(--accent-deep);
  font-size: 11px;
}
.proxy-settings-step .el-form-item {
  margin-bottom: 14px;
}
.proxy-mode-cards {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.proxy-mode-card {
  display: grid;
  gap: 6px;
  justify-items: start;
  padding: 14px 15px;
  text-align: left;
  background: #fafbfc;
  border: 1px solid var(--line);
  border-radius: 11px;
  color: var(--muted-strong);
  cursor: pointer;
  transition:
    border-color 0.15s,
    background 0.15s;
}
.proxy-mode-card:hover {
  border-color: #b9c3d2;
}
.proxy-mode-card.is-active {
  background: var(--tint-50);
  border-color: var(--accent);
  box-shadow: 0 0 0 3px rgba(24, 168, 137, 0.12);
  color: var(--accent-deep);
}
.proxy-mode-card strong {
  color: var(--ink-navy);
  font-size: 13px;
}
.proxy-mode-card.is-active strong {
  color: #0b6f5a;
}
.proxy-mode-card small {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}
.proxy-timeout-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.proxy-timeout-row .field-hint {
  flex-basis: 100%;
  margin: 0;
}
.proxy-settings-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.proxy-settings-footer .muted {
  font-size: 12px;
}
.proxy-auth-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.proxy-timeout-unit {
  margin-left: 10px;
  color: #7e8898;
}
.field-hint {
  width: 100%;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
  margin: 8px 0 0;
}
.proxy-check-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 20px 0 15px;
  color: var(--muted);
  font-size: 12px;
}
.proxy-check-list {
  max-height: 52vh;
  min-height: 100px;
  overflow: auto;
}
.proxy-check {
  padding: 17px 18px;
  margin-bottom: 12px;
  border: 1px solid #e6eaf1;
  border-left: 3px solid #d6a65b;
  border-radius: 9px;
}
.proxy-check.is-ok {
  border-left-color: #59aa91;
}
.proxy-check-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  color: #8490a2;
  font-size: 12px;
}
.proxy-check-title time {
  margin-left: auto;
}
.proxy-check strong {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.proxy-check dl {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  margin: 14px 0 0;
}
.proxy-check dl div {
  display: grid;
  gap: 5px;
}
.proxy-check dt {
  font-size: 11px;
  color: #8590a1;
}
.proxy-check dd {
  margin: 0;
  font-size: 12px;
  color: var(--ink-mid);
}
.proxy-check-code,
.proxy-check-warning {
  font-size: 12px;
  margin: 12px 0 0;
  line-height: 1.6;
  color: #9b7949;
}
.proxy-check-running {
  display: flex;
  align-items: center;
  gap: 9px;
  color: #6170d4;
  font-size: 13px;
  margin: 14px 0;
}
@media (max-width: 640px) {
  .proxy-network-panel {
    padding: 16px;
  }
  .proxy-network-heading {
    flex-wrap: wrap;
  }
  .proxy-network-heading > .el-button {
    margin-left: 54px;
  }
  .proxy-mode-cards {
    grid-template-columns: 1fr;
  }
  .proxy-auth-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }
  .proxy-check-title {
    flex-wrap: wrap;
  }
  .proxy-check-title time {
    width: 100%;
    margin-left: 0;
  }
}
</style>
