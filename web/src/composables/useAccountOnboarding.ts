import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { api, ApiError, errorMessage } from '../api'
import { plans } from '../list-model'
import {
  buildOnboardPayload,
  buildReauthorizePayload,
  missingAccountFields,
  keepsSavedCredential,
  type AccountForm,
  type AccountDateKey,
  applyCredentialJson,
  applyOAuthResult,
  credentialFields,
  type OAuthResult,
  type OAuthSummary,
  hasValue,
  completionChecklist,
  firstMissingAnchor,
  describeTokenExpiry,
  fieldAnchors,
  accountRequiredFields,
  accountDateErrors,
  parseDeviceIds,
} from '../model/account-form'
import { credentialInputs } from '../model/credential-inputs'
import { applyFieldErrors } from '../errors'
import { randomId } from '../utils/id'
import type { Account, OnboardResult, ReauthorizeResult, ConnectionOption, ProxyOption } from '../types/api'
import { deliveryState } from '../account-workflow'
import { normalizeAccountTag } from '../model/platform-card'
import { useSession } from './useSession'

export interface AccountOnboardingProps {
  modelValue: boolean
  account: Account | null
  connectionId?: string
  /** Open credential synchronization for an already-linked account. */
  forceReauth?: boolean
  proxies: ProxyOption[]
  connections: ConnectionOption[]
}
export type AccountOnboardingEmit = {
  (event: 'update:modelValue', value: boolean): void
  (event: 'saved', result: OnboardResult): void
  /** A first-time onboarding was accepted; the dialog stays open for the next account. */
  (event: 'submitted', result: OnboardResult): void
  (event: 'reauthorized', result: ReauthorizeResult): void
  (event: 'proxy-created'): void
}

export function useAccountOnboarding(props: AccountOnboardingProps, emit: AccountOnboardingEmit) {
  const { check, onboardAllowed } = useSession()
  const step = ref(1)
  const edit = ref<AccountForm>({})
  const source = ref<'oauth' | 'import' | 'manual' | 'saved' | ''>('')
  const credentialPaste = ref('')
  const notice = ref<unknown>(null)
  const fieldErrors = ref<Record<string, string>>({})
  const oauthBusy = ref(false)
  const proxyBusy = ref(false)
  const proxyDirty = ref(false)
  const proxyPending = ref(false)
  const saving = ref(false)
  const busy = computed(() => saving.value || oauthBusy.value || proxyBusy.value)
  const authorizationStarted = ref(false)
  const oauthGeneration = ref(0)
  const proxyChanged = ref(false)
  /** Set after a retryable platform push failure while credentials stay in the form. */
  const pushFailed = ref(false)
  /** The accepted first-time submission while the success page is shown. */
  const submitted = ref<OnboardResult | null>(null)
  const addedProxies = ref<ProxyOption[]>([])
  const availableProxies = computed(() => [
    ...props.proxies,
    ...addedProxies.value.filter((p) => !props.proxies.some((item) => item.id === p.id)),
  ])
  const savedCredentials = computed(() => keepsSavedCredential(edit.value))
  const connected = computed(
    () =>
      savedCredentials.value || source.value === 'manual' || !!(edit.value.access_token && edit.value.refresh_token),
  )
  const connectionReady = computed(() =>
    props.connections.some((c) => c.id === edit.value.platform_connection_id && c.enabled),
  )
  const poolProxyReady = computed(() => availableProxies.value.some((p) => p.id === edit.value.proxy_id && p.enabled))
  const canReuseCredentialFields = computed(() => linkedReauth.value && !!edit.value.credentials_configured)
  const savedProxyAccountId = computed(() =>
    canReuseCredentialFields.value && !edit.value.proxy_id ? edit.value.id : undefined,
  )
  const proxyReady = computed(
    () =>
      !proxyPending.value &&
      (poolProxyReady.value ||
        (!edit.value.proxy_id &&
          (savedCredentials.value || canReuseCredentialFields.value || !!edit.value.credential_proxy_url))),
  )
  const selectedConnectionName = computed(
    () =>
      props.connections.find((c) => c.id === edit.value.platform_connection_id)?.name ||
      props.connections.find((c) => c.id === edit.value.platform_connection_id)?.base_url ||
      '未选择',
  )
  const selectedProxyName = computed(() => {
    const p = availableProxies.value.find((p) => p.id === edit.value.proxy_id)
    return p
      ? `${p.name || p.host} · ${p.scheme.toUpperCase()} · ${p.host}:${p.port}`
      : edit.value.proxy_id
        ? '所选代理不可用'
        : edit.value.credential_proxy_url
          ? '使用凭证中的代理（认证信息已隐藏）'
          : savedCredentials.value || canReuseCredentialFields.value
            ? '沿用已保存凭证中的代理'
            : '未选择'
  })
  const planLabel = computed(() => plans.find((p) => p.value === edit.value.plan)?.label || edit.value.plan || '')
  const sourceLabel = computed(
    () =>
      ({ oauth: '授权完成', import: '凭证已导入', saved: '已保存', manual: '手动录入', '': '待连接' })[source.value],
  )
  const oauthIdentity = ref<(Omit<OAuthSummary, 'expiry'> & { expired?: string }) | null>(null)
  const now = ref(Date.now())
  const oauthSummary = computed<OAuthSummary | null>(() =>
    oauthIdentity.value
      ? { ...oauthIdentity.value, expiry: describeTokenExpiry(oauthIdentity.value.expired, new Date(now.value)) }
      : null,
  )
  const oauthWarnings = ref<string[]>([])
  const knownKeys = ref(new Set<string>())
  const knownValues = ref<Record<string, unknown>>({})
  const editKnownFields = ref(false)
  const credentialDetailsOpen = ref(false)
  const deviceImportOpen = ref(false)
  const devicePaste = ref('')
  const flashAnchor = ref('')
  const linkedReauth = computed(
    () => !!props.forceReauth && !!edit.value.id && deliveryState(props.account || {}) === 'linked',
  )
  const linkedConnectionId = computed(
    () =>
      props.account?.platform_links?.find((l) => l.ingestion_state === 'accepted')?.connection_id ||
      props.connectionId ||
      '',
  )
  const linkedConnectionName = computed(() => {
    const id = linkedConnectionId.value
    const row = props.connections.find((c) => c.id === id)
    return (
      row?.name ||
      row?.base_url ||
      props.account?.platform_links?.find((l) => l.connection_id === id)?.connection_name ||
      '已绑定平台'
    )
  })
  const missingFields = computed(() => {
    if (!linkedReauth.value) return missingAccountFields(edit.value)
    // Reauth only needs a fresh credential; profile gaps are irrelevant.
    try {
      buildReauthorizePayload(edit.value)
      return [] as string[]
    } catch (e) {
      return [errorMessage(e, '凭证待补充')]
    }
  })
  const checklist = computed(() => {
    const items = completionChecklist(edit.value, {
      connection: selectedConnectionName.value,
      proxy: selectedProxyName.value,
    })
    if (!linkedReauth.value) return items
    return items.filter(
      (i) => i.key === 'connection' || i.key === 'proxy' || i.key === 'device_ids' || i.key === 'credentials',
    )
  })
  const pendingAnchors = computed(() => new Set(checklist.value.filter((i) => !i.done).map((i) => i.anchor)))
  const gapCount = computed(() => pendingAnchors.value.size)
  const visibleCredentialInputs = computed(() =>
    credentialInputs.filter(
      (f) => f.key !== 'claude_device_ids_text' && !(f.key === 'credential_proxy_url' && edit.value.proxy_id),
    ),
  )
  const subscriptionDates = [
    { key: 'plan_start_time', label: '本期订阅开始', anchor: fieldAnchors.planStart },
    { key: 'plan_end_time', label: '本期订阅结束', anchor: fieldAnchors.planEnd },
  ] as const
  const LAST_CONNECTION_KEY = 'cc-manager:last-connection'
  const LAST_PROXY_KEY = 'cc-manager:last-proxy'
  const enabledConnections = computed(() => props.connections.filter((c) => c.enabled))
  let initialForm = ''
  let idempotencyKey = ''
  let lastAttempt = ''
  let clock: ReturnType<typeof setInterval> | undefined
  let flashTimer: ReturnType<typeof setTimeout> | undefined
  let retainedDevices: { email: string; ids: string } | null = null
  function defaultConnection() {
    if (enabledConnections.value.length === 1) return enabledConnections.value[0]!.id
    let last = ''
    try {
      last = localStorage.getItem(LAST_CONNECTION_KEY) || ''
    } catch {
      /* optional preference */
    }
    return enabledConnections.value.some((c) => c.id === last) ? last : ''
  }
  function remembered(key: string) {
    try {
      return localStorage.getItem(key) || ''
    } catch {
      return ''
    }
  }
  function remember(key: string, value: string) {
    try {
      localStorage.setItem(key, value)
    } catch {
      /* optional preference */
    }
  }
  // The proxy used last time, as long as it is still in the pool and enabled.
  function defaultProxy() {
    const last = remembered(LAST_PROXY_KEY)
    return props.proxies.some((p) => p.id === last && p.enabled) ? last : ''
  }
  function blank(): AccountForm {
    return {
      plan: '',
      register_location: 'US',
      register_time: '',
      plan_start_time: '',
      plan_end_time: '',
      tags: [],
      credential_type: 'claude',
      verification_status: 'not_required',
      platform_connection_id: defaultConnection(),
      proxy_id: defaultProxy(),
    }
  }
  function clearCredential(target: AccountForm) {
    for (const [key] of credentialFields) target[key] = ''
    target.id_token = ''
    target.claude_device_ids_text = ''
  }
  function showProfile(key: string) {
    return editKnownFields.value || !knownKeys.value.has(key) || !!fieldErrors.value[key]
  }
  function knownSource(key: string) {
    return knownKeys.value.has(key) && knownValues.value[key] === edit.value[key] ? sourceLabel.value : '手动填写'
  }
  function fieldClass(anchor: string) {
    return { 'is-flash': flashAnchor.value === anchor }
  }
  function captureKnown(keys = ['account', 'plan', 'register_time']) {
    knownKeys.value = new Set(keys.filter((k) => hasValue(edit.value[k])))
    knownValues.value = Object.fromEntries([...knownKeys.value].map((key) => [key, edit.value[key]]))
    editKnownFields.value = false
  }
  function resetNotices() {
    notice.value = null
    fieldErrors.value = {}
  }
  function fillFromOAuth(result: OAuthResult) {
    try {
      applyOAuthResult(edit.value, result)
      if (retainedDevices && result.auth_json.email?.trim().toLowerCase() === retainedDevices.email)
        edit.value.claude_device_ids_text = retainedDevices.ids
      retainedDevices = null
      oauthIdentity.value = {
        email: result.auth_json.email?.trim() || '',
        organization: result.auth_json.organization_name,
        plan: plans.find((p) => p.value === result.plan)?.label,
        expired: result.auth_json.expired,
      }
      now.value = Date.now()
      oauthWarnings.value = [...result.warnings]
      source.value = 'oauth'
      proxyChanged.value = false
      credentialPaste.value = ''
      captureKnown([
        ...(result.auth_json.email ? ['account'] : []),
        ...(result.plan ? ['plan'] : []),
        ...(result.register_time ? ['register_time'] : []),
      ])
      resetNotices()
    } catch (e) {
      notice.value = e
    }
  }
  function onAuthorizationStarted() {
    authorizationStarted.value = true
    if (source.value === 'oauth') {
      if (edit.value.claude_device_ids_text)
        retainedDevices = {
          email: String(edit.value.account || '')
            .trim()
            .toLowerCase(),
          ids: edit.value.claude_device_ids_text,
        }
      clearCredential(edit.value)
      oauthIdentity.value = null
      source.value = ''
    }
  }
  function proxyCreated(proxy: ProxyOption) {
    addedProxies.value.push(proxy)
    emit('proxy-created')
  }
  function changeProxy(id: string) {
    if (id === (edit.value.proxy_id || '')) return
    const keepSaved = linkedReauth.value && keepsSavedCredential(edit.value)
    const wasConnected = connected.value
    if (edit.value.claude_device_ids_text)
      retainedDevices = {
        email: String(edit.value.account || '')
          .trim()
          .toLowerCase(),
        ids: edit.value.claude_device_ids_text,
      }
    edit.value.proxy_id = id
    if (keepSaved) {
      // Reauth with sealed credentials: only the pool binding changes.
      proxyChanged.value = false
      resetNotices()
      return
    }
    if (wasConnected && source.value !== 'manual') {
      clearCredential(edit.value)
      if (edit.value.id) edit.value.replace_credentials = true
      source.value = ''
      oauthIdentity.value = null
      proxyChanged.value = true
    } else if (authorizationStarted.value) proxyChanged.value = true
    oauthGeneration.value++
    oauthBusy.value = false
    resetNotices()
  }
  function replaceSavedCredential() {
    edit.value.replace_credentials = true
    source.value = ''
    oauthIdentity.value = null
    clearCredential(edit.value)
  }
  /** User-initiated reauth: clear saved/form credentials and return to step 1. */
  function startReauthorize() {
    if (linkedReauth.value && edit.value.claude_device_ids_text) {
      retainedDevices = {
        email: String(edit.value.account || '')
          .trim()
          .toLowerCase(),
        ids: edit.value.claude_device_ids_text,
      }
    }
    replaceSavedCredential()
    pushFailed.value = false
    step.value = 1
    oauthGeneration.value++
    authorizationStarted.value = false
    proxyChanged.value = false
    credentialPaste.value = ''
    oauthWarnings.value = []
    resetNotices()
    scrollTop()
  }
  async function changeAccount() {
    if (busy.value || edit.value.id) return
    try {
      await ElMessageBox.confirm(
        '更换账号会清空当前账号的密码、订阅日期、设备资料和凭证，平台与代理选择会保留。',
        '更换账号',
        { confirmButtonText: '更换账号', cancelButtonText: '继续填写', type: 'warning' },
      )
    } catch {
      return
    }
    const { proxy_id, platform_connection_id } = edit.value
    edit.value = { ...blank(), proxy_id, platform_connection_id }
    source.value = ''
    oauthIdentity.value = null
    oauthWarnings.value = []
    knownKeys.value = new Set()
    retainedDevices = null
    credentialPaste.value = ''
    oauthGeneration.value++
    authorizationStarted.value = false
    resetNotices()
  }
  function fillFromPaste() {
    try {
      let value: unknown = JSON.parse(credentialPaste.value)
      if (Array.isArray(value)) value = value[0]
      if (!value || typeof value !== 'object') throw new Error('请粘贴单条凭证 JSON 对象')
      const record = value as Record<string, unknown>
      const auth =
        typeof record.auth_json === 'string'
          ? (JSON.parse(record.auth_json) as Record<string, unknown>)
          : ((record.auth_json || record) as Record<string, unknown>)
      if (!auth || typeof auth !== 'object' || Array.isArray(auth)) throw new Error('凭证应为 JSON 对象')
      const authEmail = String(auth.email || '').trim()
      const recordEmail = String(record.account || '').trim()
      if (authEmail && recordEmail && authEmail.toLowerCase() !== recordEmail.toLowerCase())
        throw new Error('账号与凭证中的邮箱不一致，请检查导入内容')
      const email = authEmail || recordEmail
      if (!email && edit.value.account) throw new Error('导入凭证缺少账号邮箱，无法确认是否属于当前账号')
      if (edit.value.id && email && email.toLowerCase() !== edit.value.account?.toLowerCase())
        throw new Error('导入凭证必须属于当前账号')
      const different = email && edit.value.account && email.toLowerCase() !== edit.value.account.toLowerCase()
      const next: AccountForm = different
        ? { ...blank(), proxy_id: edit.value.proxy_id, platform_connection_id: edit.value.platform_connection_id }
        : { ...edit.value }
      clearCredential(next)
      if (email) next.account = email
      applyCredentialJson(next, JSON.stringify({ ...record, auth_json: auth }))
      for (const key of ['plan', 'register_time', 'plan_start_time', 'plan_end_time', 'password'] as const)
        if (typeof record[key] === 'string') next[key] = record[key]
      if (next.id) next.replace_credentials = true
      edit.value = next
      source.value = 'import'
      oauthIdentity.value = null
      oauthWarnings.value = []
      credentialPaste.value = ''
      retainedDevices = null
      proxyChanged.value = false
      oauthGeneration.value++
      captureKnown([...(email ? ['account'] : []), ...['plan', 'register_time'].filter((key) => hasValue(record[key]))])
      resetNotices()
    } catch (e) {
      notice.value = e instanceof SyntaxError ? new Error('不是合法的 JSON，请粘贴完整凭证对象') : e
    }
  }
  function importDevices() {
    try {
      const data = JSON.parse(devicePaste.value)
      const raw = (data.auth_json || data).claude_device_ids
      const ids = Array.isArray(raw)
        ? raw
            .map(String)
            .map((v) => v.trim())
            .filter(Boolean)
        : parseDeviceIds(raw)
      if (!ids.length) throw new Error('JSON 中没有有效的 claude_device_ids')
      edit.value.claude_device_ids_text = ids.join('\n')
      devicePaste.value = ''
      deviceImportOpen.value = false
    } catch (e) {
      notice.value = e instanceof SyntaxError ? new Error('设备资料不是合法的 JSON') : e
    }
  }
  async function startManual() {
    source.value = 'manual'
    await advance()
  }
  function isReauthCredentialAnchor(anchor: string) {
    return (
      anchor === fieldAnchors.deviceIds || anchor === fieldAnchors.credentials || anchor.startsWith('credential-field-')
    )
  }
  async function jumpTo(anchor: string) {
    if ([fieldAnchors.proxy, fieldAnchors.connection].includes(anchor as typeof fieldAnchors.proxy)) {
      step.value = 1
    } else if (linkedReauth.value) {
      // Saved-credential retry keeps devices on step 1; replace-credential path uses step 2.
      step.value = savedCredentials.value ? 1 : isReauthCredentialAnchor(anchor) ? 2 : 1
      if (anchor === fieldAnchors.credentials || anchor.startsWith('credential-field-'))
        credentialDetailsOpen.value = true
    } else {
      step.value = 2
      if (anchor === fieldAnchors.credentials || anchor.startsWith('credential-field-'))
        credentialDetailsOpen.value = true
      if (
        [fieldAnchors.account, fieldAnchors.plan, fieldAnchors.registerTime].includes(
          anchor as typeof fieldAnchors.account,
        )
      )
        editKnownFields.value = true
    }
    await nextTick()
    const el = document.getElementById(anchor)
    el?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
    el?.querySelector<HTMLElement>('input,textarea')?.focus({ preventScroll: true })
    flashAnchor.value = anchor
    if (flashTimer) clearTimeout(flashTimer)
    flashTimer = setTimeout(() => (flashAnchor.value = ''), 1200)
  }
  function scrollTop() {
    void nextTick(() => document.querySelector('.account-wizard .el-dialog__body')?.scrollTo?.({ top: 0 }))
  }
  function goBack() {
    if (busy.value) return
    // Replacing credentials: review → step 2 so device/credential fields stay editable.
    // Saved-credential sync has no step-2 form; review → step 1.
    if (linkedReauth.value && step.value === 3) {
      step.value = savedCredentials.value ? 1 : 2
    } else step.value = Math.max(1, step.value - 1)
    notice.value = null
    scrollTop()
  }
  function backToConnection() {
    step.value = 1
    scrollTop()
  }
  function checkConnectionAndProxy() {
    if (!connectionReady.value) {
      fieldErrors.value.platform_connection_id = '请选择启用的上号平台'
      void jumpTo(fieldAnchors.connection)
      return false
    }
    if (!proxyReady.value) {
      fieldErrors.value.proxy_id = '请选择启用的代理，或保存并使用新代理'
      void jumpTo(fieldAnchors.proxy)
      return false
    }
    return true
  }
  async function validateReauth() {
    try {
      buildReauthorizePayload(edit.value)
      return true
    } catch (e) {
      notice.value = e
      const message = errorMessage(e)
      if (message.includes('设备')) {
        fieldErrors.value.claude_device_ids_text = message
        await jumpTo(fieldAnchors.deviceIds)
      } else {
        credentialDetailsOpen.value = true
        await jumpTo(fieldAnchors.credentials)
      }
      return false
    }
  }
  const dateFieldAnchors: Record<AccountDateKey, string> = {
    register_time: fieldAnchors.registerTime,
    plan_start_time: fieldAnchors.planStart,
    plan_end_time: fieldAnchors.planEnd,
  }

  async function validateDetails() {
    if (linkedReauth.value) return validateReauth()
    const dateErrors = accountDateErrors(edit.value)
    for (const [key, label] of accountRequiredFields) {
      if (key === 'register_time' || key === 'plan_start_time' || key === 'plan_end_time') continue
      if (!hasValue(edit.value[key]) && !(key === 'password' && edit.value.password_configured))
        fieldErrors.value[key] = `请填写${label}`
    }
    Object.assign(fieldErrors.value, dateErrors)
    if (gapCount.value || Object.keys(dateErrors).length) {
      const firstDate = (['register_time', 'plan_start_time', 'plan_end_time'] as const).find((key) => dateErrors[key])
      notice.value = new Error(
        gapCount.value ? `还有 ${gapCount.value} 组资料待补充，已定位到第一项` : dateErrors[firstDate!]!,
      )
      await jumpTo(
        (firstDate && dateFieldAnchors[firstDate]) || firstMissingAnchor(edit.value) || fieldAnchors.credentials,
      )
      return false
    }
    try {
      buildOnboardPayload(edit.value)
      return true
    } catch (e) {
      notice.value = e
      const message = errorMessage(e)
      const orderErrors = accountDateErrors(edit.value, { requirePresent: false })
      const orderKey = (['register_time', 'plan_start_time', 'plan_end_time'] as const).find((key) => orderErrors[key])
      if (orderKey) {
        Object.assign(fieldErrors.value, orderErrors)
        await jumpTo(dateFieldAnchors[orderKey])
      } else if (message.includes('设备')) {
        fieldErrors.value.claude_device_ids_text = message
        await jumpTo(fieldAnchors.deviceIds)
      } else {
        credentialDetailsOpen.value = true
        await jumpTo(fieldAnchors.credentials)
      }
      return false
    }
  }
  async function advance() {
    if (busy.value) return
    resetNotices()
    if (!checkConnectionAndProxy()) return
    if (step.value === 1) {
      if (!connected.value) {
        notice.value = new Error('请先授权或导入凭证')
        return
      }
      if (linkedReauth.value) {
        // Profile fields are not part of reauth; credentials live on step 1,
        // device ids on step 2. Skip straight to review only when both are ready.
        if (await validateReauth()) step.value = 3
        else if (connected.value && !parseDeviceIds(edit.value.claude_device_ids_text).length) step.value = 2
      } else {
        step.value = 2
        if (!missingFields.value.length && (await validateDetails())) step.value = 3
      }
    } else if (await validateDetails()) step.value = 3
    scrollTop()
  }
  const accountFieldMap: Record<string, string> = Object.fromEntries([
    ...credentialFields.map(([key, field]) => ['auth_json.' + field, key]),
    ['auth_json.id_token', 'id_token'],
    ['auth_json.claude_device_ids', 'claude_device_ids_text'],
    ['auth_json', 'credentialPaste'],
  ])
  function anchorForKey(key: string) {
    const map: Record<string, string> = {
      account: fieldAnchors.account,
      password: fieldAnchors.password,
      plan: fieldAnchors.plan,
      register_time: fieldAnchors.registerTime,
      plan_start_time: fieldAnchors.planStart,
      plan_end_time: fieldAnchors.planEnd,
      platform_connection_id: fieldAnchors.connection,
      proxy_id: fieldAnchors.proxy,
      claude_device_ids_text: fieldAnchors.deviceIds,
    }
    return map[key] || fieldAnchors.credentials
  }
  async function save() {
    if (busy.value || step.value !== 3) return
    saving.value = true
    try {
      resetNotices()
      if (!checkConnectionAndProxy() || !(await validateDetails())) return
      if (linkedReauth.value) {
        const payload = buildReauthorizePayload(edit.value)
        const body: Record<string, unknown> = {
          proxy_id: payload.proxy_id,
          expected_version: props.account?.version,
        }
        if (payload.auth_json) body.auth_json = payload.auth_json
        if (payload.claude_device_ids?.length) body.claude_device_ids = payload.claude_device_ids
        const result = await api<ReauthorizeResult>('/accounts/' + edit.value.id + '/reauthorize', {
          method: 'POST',
          body: JSON.stringify(body),
        })
        pushFailed.value = false
        edit.value = {}
        credentialPaste.value = ''
        emit('update:modelValue', false)
        emit('reauthorized', result.data)
        return
      }
      try {
        await check()
      } catch {
        /* API still enforces */
      }
      if (!onboardAllowed()) {
        notice.value = new Error('当前账号已被禁止上号，请联系管理员')
        return
      }
      const payload = buildOnboardPayload(edit.value)
      const serialized = JSON.stringify(payload)
      if (lastAttempt && lastAttempt !== serialized) idempotencyKey = randomId()
      lastAttempt = serialized
      const result = await api<OnboardResult>(
        edit.value.id ? '/accounts/' + edit.value.id + '/onboard' : '/accounts/onboard',
        { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: serialized },
      )
      remember(LAST_CONNECTION_KEY, String(edit.value.platform_connection_id || ''))
      remember(LAST_PROXY_KEY, String(edit.value.proxy_id || ''))
      lastAttempt = ''
      credentialPaste.value = ''
      if (edit.value.id) {
        edit.value = {}
        emit('update:modelValue', false)
        emit('saved', result.data)
        return
      }
      // First-time onboarding usually comes in batches: stay open, keep the
      // platform and proxy, and offer the next account.
      submitted.value = result.data
      authorizationStarted.value = false
      proxyDirty.value = false
      initialForm = JSON.stringify(edit.value)
      emit('submitted', result.data)
    } catch (e) {
      notice.value = e
      if (e instanceof ApiError && e.fields.length) {
        fieldErrors.value = applyFieldErrors(e.fields, accountFieldMap)
        const keys = Object.keys(fieldErrors.value)
        // Onboarding may force re-entry of a rejected saved credential; linked
        // reauth keeps the saved marker so re-authorization stays optional.
        if (
          !linkedReauth.value &&
          savedCredentials.value &&
          e.fields.some((field) => field.field === 'auth_json' || field.field.startsWith('auth_json.'))
        ) {
          replaceSavedCredential()
          oauthGeneration.value++
          step.value = 1
          scrollTop()
          return
        }
        pushFailed.value = false
        // Saved-credential retry: device gaps overlay without Claude OAuth.
        // Other sealed-field errors keep the marker; user may retry or re-auth.
        if (
          linkedReauth.value &&
          savedCredentials.value &&
          e.fields.some((field) => field.field === 'auth_json' || field.field.startsWith('auth_json.'))
        ) {
          const deviceError = e.fields.some(
            (field) => field.field === 'auth_json.claude_device_ids' || field.field.includes('claude_device_ids'),
          )
          if (deviceError) {
            fieldErrors.value.claude_device_ids_text =
              e.fields.find((field) => field.field.includes('claude_device_ids'))?.message || '请填写设备资料'
            step.value = 1
            await jumpTo(fieldAnchors.deviceIds)
            return
          }
          const expiredError = e.fields.find((field) => field.field === 'auth_json.expired')
          if (expiredError) {
            fieldErrors.value.expired = expiredError.message || '访问令牌已过期，请重新授权'
            step.value = 1
            scrollTop()
            return
          }
          scrollTop()
          return
        }
        if (keys.some((k) => credentialInputs.some((f) => f.key === k))) credentialDetailsOpen.value = true
        await jumpTo(anchorForKey(keys[0]!))
        return
      }
      if (e instanceof ApiError && e.status === 409) {
        pushFailed.value = false
        return
      }
      if (
        e instanceof ApiError &&
        (e.message.includes('平台已更新凭证') || e.code === 'STORAGE_UNAVAILABLE') &&
        linkedReauth.value
      ) {
        // Platform accepted the push but local persist failed — not a plain retry.
        pushFailed.value = false
        return
      }
      if (
        e instanceof ApiError &&
        (e.retryable || e.status >= 500 || e.status === 0 || e.action === 'retry' || e.action === 'check_platform')
      ) {
        pushFailed.value = true
        step.value = 3
        return
      }
      if (!(e instanceof ApiError)) {
        pushFailed.value = true
        step.value = 3
      }
    } finally {
      saving.value = false
    }
  }
  async function confirmDiscard() {
    if (busy.value) return false
    if (
      JSON.stringify(edit.value) === initialForm &&
      !credentialPaste.value &&
      !proxyDirty.value &&
      !authorizationStarted.value
    )
      return true
    try {
      await ElMessageBox.confirm(
        linkedReauth.value
          ? '退出会清空本次未成功同步的新凭证。下次打开将使用库内已保存的凭证；若本次推送未成功，刚兑换的令牌不会保留。已保存的新代理会保留在代理池。'
          : '资料尚未提交，退出会清空本次填写的账号资料和授权凭证。已保存的新代理会保留在代理池。',
        linkedReauth.value ? '退出重新授权？' : '退出上号？',
        { confirmButtonText: '退出', cancelButtonText: '继续填写', type: 'warning' },
      )
      return true
    } catch {
      return false
    }
  }
  function finish() {
    const result = submitted.value
    submitted.value = null
    edit.value = {}
    emit('update:modelValue', false)
    if (result) emit('saved', result)
  }
  function addAnother() {
    const { proxy_id, platform_connection_id } = edit.value
    edit.value = { ...blank(), proxy_id, platform_connection_id }
    submitted.value = null
    source.value = ''
    oauthIdentity.value = null
    oauthWarnings.value = []
    knownKeys.value = new Set()
    retainedDevices = null
    credentialPaste.value = ''
    credentialDetailsOpen.value = false
    editKnownFields.value = false
    oauthGeneration.value++
    authorizationStarted.value = false
    proxyChanged.value = false
    pushFailed.value = false
    step.value = 1
    resetNotices()
    initialForm = JSON.stringify(edit.value)
    idempotencyKey = randomId()
    scrollTop()
  }
  async function requestClose() {
    if (submitted.value) return finish()
    if (await confirmDiscard()) emit('update:modelValue', false)
  }
  async function beforeClose(done: () => void) {
    if (submitted.value) {
      finish()
      return
    }
    if (await confirmDiscard()) done()
  }
  /** Why 「登录 Claude」 is disabled right now, with the field that fixes it. */
  const oauthBlocker = computed(() => {
    if (savedCredentials.value || source.value === 'import' || source.value === 'manual') return null
    if (!connectionReady.value) return { text: '先选择上号平台', anchor: fieldAnchors.connection }
    if (proxyPending.value) return { text: '新代理还没检测保存', anchor: fieldAnchors.proxy }
    if (!poolProxyReady.value && !savedProxyAccountId.value)
      return { text: '先选择或添加代理', anchor: fieldAnchors.proxy }
    return null
  })
  function stopTimers() {
    if (clock) clearInterval(clock)
    if (flashTimer) clearTimeout(flashTimer)
    clock = undefined
    flashTimer = undefined
  }
  watch(
    () => props.modelValue,
    (open) => {
      stopTimers()
      step.value = 1
      oauthIdentity.value = null
      oauthWarnings.value = []
      source.value = ''
      oauthBusy.value = false
      proxyBusy.value = false
      proxyDirty.value = false
      proxyPending.value = false
      credentialPaste.value = ''
      credentialDetailsOpen.value = false
      editKnownFields.value = false
      deviceImportOpen.value = false
      devicePaste.value = ''
      knownKeys.value = new Set()
      authorizationStarted.value = false
      proxyChanged.value = false
      pushFailed.value = false
      submitted.value = null
      addedProxies.value = []
      retainedDevices = null
      lastAttempt = ''
      resetNotices()
      oauthGeneration.value++
      if (!open) {
        edit.value = {}
        return
      }
      const row = props.account
      edit.value = row
        ? {
            ...row,
            balance: row.balance === undefined || row.balance === '' ? undefined : Number(row.balance),
            tags: [...new Set((row.tags || []).map(normalizeAccountTag))],
            credential_type: 'claude',
            platform_connection_id: props.connectionId || row.submission_job?.connection_id || defaultConnection(),
          }
        : blank()
      if (keepsSavedCredential(edit.value)) {
        source.value = 'saved'
        captureKnown()
      }
      if (props.forceReauth && edit.value.id) {
        // Keep sealed credentials by default; user may startReauthorize().
        const bound = linkedConnectionId.value
        if (bound) edit.value.platform_connection_id = bound
        if (!keepsSavedCredential(edit.value)) {
          replaceSavedCredential()
          source.value = ''
        }
      }
      initialForm = JSON.stringify(edit.value)
      idempotencyKey = randomId()
      now.value = Date.now()
      clock = setInterval(() => (now.value = Date.now()), 60000)
    },
    { immediate: true },
  )
  watch(
    () => edit.value.account,
    (account, previous) => {
      if (source.value === 'oauth' && oauthIdentity.value?.email) return
      if (!hasValue(edit.value.credential_email) || edit.value.credential_email === previous)
        edit.value.credential_email = account
    },
  )
  watch(
    () => ({ ...edit.value }),
    (current, previous) => {
      for (const key of Object.keys(fieldErrors.value))
        if (current[key] !== previous[key]) delete fieldErrors.value[key]
    },
  )
  onBeforeUnmount(() => {
    stopTimers()
    edit.value = {}
    lastAttempt = ''
    retainedDevices = null
  })
  const stepLabels = computed(() => {
    if (!linkedReauth.value) return ['连接账号', '补充资料', '确认上号']
    if (savedCredentials.value) return ['同步凭证', '确认更新']
    return ['重新授权', '补充设备', '确认更新']
  })
  const displayStep = computed(() => {
    if (submitted.value) return stepLabels.value.length + 1
    if (!linkedReauth.value) return step.value
    if (savedCredentials.value) return step.value >= 3 ? 2 : 1
    return step.value
  })
  const submitLabel = computed(() => {
    if (saving.value) return linkedReauth.value ? '正在同步…' : '正在保存并提交…'
    if (linkedReauth.value) {
      if (pushFailed.value) return '重试同步'
      if (savedCredentials.value) return '同步到平台'
      return '确认并更新凭证'
    }
    return pushFailed.value ? '重试提交' : '确认并上号'
  })
  return {
    stepLabels,
    submitted,
    oauthBlocker,
    finish,
    addAnother,
    step,
    displayStep,
    linkedReauth,
    linkedConnectionName,
    edit,
    source,
    credentialPaste,
    notice,
    fieldErrors,
    oauthBusy,
    proxyBusy,
    proxyDirty,
    proxyPending,
    saving,
    busy,
    authorizationStarted,
    oauthGeneration,
    proxyChanged,
    pushFailed,
    submitLabel,
    availableProxies,
    savedCredentials,
    connected,
    connectionReady,
    poolProxyReady,
    canReuseCredentialFields,
    savedProxyAccountId,
    selectedConnectionName,
    selectedProxyName,
    planLabel,
    sourceLabel,
    oauthSummary,
    oauthWarnings,
    editKnownFields,
    credentialDetailsOpen,
    deviceImportOpen,
    devicePaste,
    checklist,
    gapCount,
    missingFields,
    visibleCredentialInputs,
    subscriptionDates,
    enabledConnections,
    fieldAnchors,
    plans,
    showProfile,
    knownSource,
    fieldClass,
    fillFromOAuth,
    onAuthorizationStarted,
    proxyCreated,
    changeProxy,
    replaceSavedCredential,
    startReauthorize,
    changeAccount,
    fillFromPaste,
    importDevices,
    startManual,
    jumpTo,
    goBack,
    backToConnection,
    advance,
    save,
    requestClose,
    beforeClose,
  }
}
