// Pure account-form logic shared by the completeness hint and the submit
// path, so both agree on what "complete" means. No Vue imports here so the
// file can be unit-tested with node --test.

export interface CredentialPayload {
  id_token: string
  access_token: string
  refresh_token: string
  last_refresh: string
  email: string
  account_uuid: string
  organization_uuid: string
  organization_name: string
  claude_device_ids: string[]
  type: string
  expired: string
  proxy_url: string
  verification_status: string
}

export type CredentialKey =
  | 'access_token'
  | 'refresh_token'
  | 'credential_email'
  | 'account_uuid'
  | 'organization_uuid'
  | 'organization_name'
  | 'credential_type'
  | 'last_refresh'
  | 'expired'
  | 'credential_proxy_url'
  | 'verification_status'
  | 'id_token'
  | 'claude_device_ids_text'

export type AccountDateKey = 'register_time' | 'plan_start_time' | 'plan_end_time'

/** Editable state of the account form; every input binds to a known key. */
export interface AccountForm extends Partial<Record<CredentialKey, string>>, Partial<Record<AccountDateKey, string>> {
  id?: string
  account?: string
  password?: string
  password_configured?: boolean
  credentials_configured?: boolean
  replace_credentials?: boolean
  plan?: string
  register_location?: string
  proxy_id?: string
  platform_connection_id?: string
  balance?: number
  tags?: string[]
  payment_method?: string
  register_device?: string
  recharge_time?: string
  warranty_period?: number | string
  remark?: string
  submission_job?: { connection_id?: string }
  [key: string]: unknown
}

export const hasValue = (value: unknown): boolean => value !== '' && value !== null && value !== undefined

export const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
export const RFC3339_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/
export const PROXY_URL_PATTERN = /^(https?|socks5h?):\/\/[^\s]+$/i

export const accountRequiredFields: ReadonlyArray<readonly [string, string]> = [
  ['account', '账号'],
  ['password', '密码'],
  ['plan', '套餐'],
  ['register_location', '注册地区'],
  ['register_time', '注册日期'],
  ['plan_start_time', '套餐开始日期'],
  ['plan_end_time', '套餐结束日期'],
  ['platform_connection_id', '平台连接'],
]

/** form key → auth_json field (device ids are handled separately). */
export const credentialFields: ReadonlyArray<readonly [CredentialKey, keyof CredentialPayload]> = [
  ['access_token', 'access_token'],
  ['refresh_token', 'refresh_token'],
  ['credential_email', 'email'],
  ['account_uuid', 'account_uuid'],
  ['organization_uuid', 'organization_uuid'],
  ['organization_name', 'organization_name'],
  ['credential_type', 'type'],
  ['last_refresh', 'last_refresh'],
  ['expired', 'expired'],
  ['credential_proxy_url', 'proxy_url'],
  ['verification_status', 'verification_status'],
]

export const credentialLabels: Record<string, string> = {
  access_token: 'access_token',
  refresh_token: 'refresh_token',
  credential_email: '凭证 email',
  account_uuid: 'account_uuid',
  organization_uuid: 'organization_uuid',
  organization_name: 'organization_name',
  credential_type: 'type',
  last_refresh: 'last_refresh',
  expired: 'expired',
  credential_proxy_url: 'proxy_url',
  verification_status: 'verification_status',
  claude_device_ids_text: 'claude_device_ids',
}

export const accountPayloadKeys = [
  'account',
  'password',
  'plan',
  'register_location',
  'register_time',
  'plan_start_time',
  'plan_end_time',
  'proxy_id',
  'balance',
  'tags',
  'payment_method',
  'register_device',
  'recharge_time',
  'warranty_period',
  'remark',
] as const

export function keepsSavedCredential(edit: AccountForm): boolean {
  return !!edit.id && !!edit.credentials_configured && !edit.replace_credentials
}

function credentialMissing(edit: AccountForm, key: CredentialKey): boolean {
  if (key === 'credential_proxy_url' && hasValue(edit.proxy_id)) return false
  return !hasValue(edit[key])
}

export function parseDeviceIds(text: unknown): string[] {
  return String(text ?? '')
    .split(/[\n,，]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/

const accountDateLabels: Record<AccountDateKey, string> = {
  register_time: '注册日期',
  plan_start_time: '套餐开始日期',
  plan_end_time: '套餐结束日期',
}

const accountDateKeys: AccountDateKey[] = ['register_time', 'plan_start_time', 'plan_end_time']

/**
 * Per-field date problems for the onboard wizard. Empty values are reported
 * when requirePresent is true (default); format and ordering always apply to
 * whatever is filled. Does not change the YYYY-MM-DD payload format.
 */
export function accountDateErrors(
  edit: Pick<AccountForm, AccountDateKey>,
  options: { requirePresent?: boolean } = {},
): Partial<Record<AccountDateKey, string>> {
  const requirePresent = options.requirePresent !== false
  const errors: Partial<Record<AccountDateKey, string>> = {}
  const values: Partial<Record<AccountDateKey, string>> = {}
  for (const key of accountDateKeys) {
    const value = String(edit[key] ?? '').trim()
    if (!value) {
      if (requirePresent) errors[key] = `请填写${accountDateLabels[key]}`
      continue
    }
    if (!DATE_PATTERN.test(value)) {
      errors[key] = `${accountDateLabels[key]}格式不正确`
      continue
    }
    values[key] = value
  }
  if (values.register_time && values.plan_start_time && values.register_time > values.plan_start_time) {
    errors.register_time = '注册日期不能晚于套餐开始日期'
  }
  if (values.plan_start_time && values.plan_end_time && values.plan_start_time >= values.plan_end_time) {
    errors.plan_end_time = '套餐结束日期须至少晚于开始日期一天'
  }
  return errors
}

function firstAccountDateError(errors: Partial<Record<AccountDateKey, string>>): AccountDateKey | null {
  return accountDateKeys.find((key) => errors[key]) || null
}

/** Labels of everything still missing; empty means the form can be submitted. */
export function missingAccountFields(edit: AccountForm): string[] {
  const missing: string[] = []
  for (const [key, label] of accountRequiredFields) {
    if (key === 'password' && edit.password_configured) continue
    if (accountDateKeys.includes(key as AccountDateKey)) continue
    if (!hasValue(edit[key])) missing.push(label)
  }
  for (const message of Object.values(accountDateErrors(edit))) {
    if (message) missing.push(message)
  }
  if (!keepsSavedCredential(edit)) {
    for (const [key] of credentialFields) if (credentialMissing(edit, key)) missing.push(credentialLabels[key]!)
    if (!parseDeviceIds(edit.claude_device_ids_text).length) missing.push(credentialLabels.claude_device_ids_text!)
  }
  return missing
}

/**
 * Builds auth_json from the per-field inputs. Returns null when the saved
 * credential is kept or nothing was entered; throws with a user-facing
 * message when the entered credential is incomplete or malformed.
 */
export function buildCredential(edit: AccountForm): CredentialPayload | null {
  if (keepsSavedCredential(edit)) return null
  const entered =
    credentialFields.some(([key]) => hasValue(edit[key])) ||
    hasValue(edit.id_token) ||
    hasValue(edit.claude_device_ids_text)
  if (!entered) return null
  const missing = credentialFields.filter(([key]) => credentialMissing(edit, key)).map(([, field]) => field as string)
  if (missing.length) throw new Error('Claude 凭证缺少：' + missing.join('、'))
  const email = String(edit.credential_email).trim()
  if (!EMAIL_PATTERN.test(email)) throw new Error('凭证 email 格式不正确')
  if (edit.account && email.toLowerCase() !== String(edit.account).trim().toLowerCase())
    throw new Error('凭证 email 必须与账号一致')
  for (const key of ['last_refresh', 'expired'] as const) {
    if (!RFC3339_PATTERN.test(String(edit[key]))) throw new Error(`${key} 必须是有效的 RFC3339 时间`)
  }
  const proxy = edit.proxy_id ? '' : String(edit.credential_proxy_url ?? '').trim()
  if (!edit.proxy_id && !PROXY_URL_PATTERN.test(proxy))
    throw new Error('proxy_url 必须以 http://、https://、socks5:// 或 socks5h:// 开头')
  const deviceIds = parseDeviceIds(edit.claude_device_ids_text)
  if (!deviceIds.length) throw new Error('设备资料不能为空，请填写至少一个设备标识')
  return {
    id_token: String(edit.id_token ?? ''),
    access_token: String(edit.access_token),
    refresh_token: String(edit.refresh_token),
    last_refresh: String(edit.last_refresh),
    email,
    account_uuid: String(edit.account_uuid),
    organization_uuid: String(edit.organization_uuid),
    organization_name: String(edit.organization_name),
    claude_device_ids: deviceIds,
    type: String(edit.credential_type),
    expired: String(edit.expired),
    proxy_url: proxy,
    verification_status: String(edit.verification_status),
  }
}

/** Validates the whole form and returns the onboarding request body. */
export function buildOnboardPayload(edit: AccountForm): Record<string, unknown> {
  for (const [key, label] of accountRequiredFields) {
    if (key === 'password' && edit.password_configured) continue
    if (accountDateKeys.includes(key as AccountDateKey)) continue
    if (!hasValue(edit[key])) throw new Error(`${label}不能为空`)
  }
  const dateErrors = accountDateErrors(edit)
  const dateKey = firstAccountDateError(dateErrors)
  if (dateKey) throw new Error(dateErrors[dateKey]!)
  const account = String(edit.account)
  if (account.includes('@') && !EMAIL_PATTERN.test(account)) throw new Error('账号邮箱格式不正确')
  const credential = buildCredential(edit)
  if (!credential && !edit.credentials_configured) throw new Error('Claude 凭证为必填，请补齐全部字段')
  const payload: Record<string, unknown> = { platform_connection_id: edit.platform_connection_id }
  if (credential) payload.auth_json = credential
  if (edit.id) payload.proxy_id = edit.proxy_id || ''
  for (const key of accountPayloadKeys) if (hasValue(edit[key])) payload[key] = edit[key]
  return payload
}

/**
 * Credential refresh for an already-linked account: only auth_json + proxy_id.
 * Always sends proxy_id (possibly "") so clearing the pool binding is explicit.
 */
export function buildReauthorizePayload(edit: AccountForm): {
  auth_json: CredentialPayload
  proxy_id: string
} {
  const credential = buildCredential(edit)
  if (!credential) throw new Error('请先完成 Claude 授权或导入凭证')
  return { auth_json: credential, proxy_id: String(edit.proxy_id ?? '') }
}

/** Default dates for a brand-new account: registered today, plan starts today and runs one month. */
export function defaultAccountDates(today = new Date()): Record<AccountDateKey, string> {
  const iso = (d: Date) => d.toISOString().slice(0, 10)
  // Same day next month, clamped to that month's last day (Jan 31 → Feb 28).
  const y = today.getUTCFullYear()
  const m = today.getUTCMonth() + 1
  const lastDay = new Date(Date.UTC(y, m + 1, 0)).getUTCDate()
  const end = new Date(Date.UTC(y, m, Math.min(today.getUTCDate(), lastDay)))
  return { register_time: iso(today), plan_start_time: iso(today), plan_end_time: iso(end) }
}

export interface CredentialFillResult {
  /** labels of fields that received a value */
  filled: string[]
  /** labels of required credential fields still empty afterwards */
  missing: string[]
  /** account / password were also taken from an import-style object */
  accountFilled: boolean
}

/**
 * Fills the credential inputs from a pasted JSON object. Accepts the bare
 * auth_json object or an import-style record ({account, password, auth_json}).
 * Fields the supplier treats as constants get defaults when absent so a
 * minimal export (tokens + ids) is enough. Throws on malformed JSON.
 */
export function applyCredentialJson(edit: AccountForm, text: string, now = new Date()): CredentialFillResult {
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch {
    throw new Error('不是合法的 JSON，请粘贴完整的凭证对象')
  }
  if (Array.isArray(value)) value = value[0]
  if (!value || typeof value !== 'object') throw new Error('请粘贴一个 JSON 对象')
  const record = value as Record<string, unknown>
  let auth = record
  let accountFilled = false
  if (record.auth_json && typeof record.auth_json === 'object') {
    auth = record.auth_json as Record<string, unknown>
    if (typeof record.account === 'string' && !hasValue(edit.account)) {
      edit.account = record.account
      accountFilled = true
    }
    if (typeof record.password === 'string' && !hasValue(edit.password)) {
      edit.password = record.password
      accountFilled = true
    }
  } else if (typeof auth.auth_json === 'string') {
    return applyCredentialJson(edit, auth.auth_json, now)
  }
  const filled: string[] = []
  const set = (key: CredentialKey, raw: unknown) => {
    if (raw === undefined || raw === null || raw === '') return
    edit[key] = String(raw)
    filled.push(credentialLabels[key]!)
  }
  for (const [key, field] of credentialFields) set(key, auth[field])
  set('id_token', auth.id_token)
  if (Array.isArray(auth.claude_device_ids)) {
    const ids = auth.claude_device_ids.map((id) => String(id).trim()).filter(Boolean)
    if (ids.length) {
      edit.claude_device_ids_text = ids.join('\n')
      filled.push(credentialLabels.claude_device_ids_text!)
    }
  } else if (typeof auth.claude_device_ids === 'string') {
    set('claude_device_ids_text', auth.claude_device_ids)
  }
  // Constants and derivable values: only when the export left them out.
  if (!hasValue(edit.credential_type)) edit.credential_type = 'claude'
  if (!hasValue(edit.verification_status)) edit.verification_status = 'not_required'
  if (!hasValue(edit.last_refresh)) edit.last_refresh = now.toISOString().replace(/\.\d{3}Z$/, 'Z')
  if (!hasValue(edit.credential_email) && hasValue(edit.account) && EMAIL_PATTERN.test(String(edit.account)))
    edit.credential_email = String(edit.account).trim()
  const missing = credentialFields
    .filter(([key]) => credentialMissing(edit, key))
    .map(([key]) => credentialLabels[key]!)
  if (!parseDeviceIds(edit.claude_device_ids_text).length) missing.push(credentialLabels.claude_device_ids_text!)
  return { filled, missing, accountFilled }
}

/** Compact description of a completed authorization, shown by ClaudeOAuthPanel. */
export interface OAuthSummary {
  email: string
  organization?: string
  plan?: string
  expiry?: string
}

export interface OAuthResult {
  auth_json: Partial<CredentialPayload>
  plan?: string
  register_time?: string
  warnings: string[]
}

/** A fresh authorization replaces credentials atomically, never mixing tokens
 * or device identities from different accounts. Unknown fields stay blank. */
export function applyOAuthResult(edit: AccountForm, result: OAuthResult): CredentialFillResult {
  const email = result.auth_json.email?.trim()
  if (email && edit.account && email.toLowerCase() !== edit.account.trim().toLowerCase())
    throw new Error('授权邮箱与当前账号不一致，请登录对应账号后重新授权；新账号请先清空账号栏')
  const next: AccountForm = { ...edit }
  for (const [key] of credentialFields) next[key] = ''
  next.id_token = ''
  next.claude_device_ids_text = ''
  if (email) next.account = email
  const filled = applyCredentialJson(next, JSON.stringify(result.auth_json))
  if (result.plan) next.plan = result.plan
  if (result.register_time) next.register_time = result.register_time
  Object.assign(edit, next)
  return filled
}

// ---------------------------------------------------------------------------
// Completion checklist: what OAuth cannot provide and the user must add.
// Anchors are DOM ids the dialog puts on its form items so a checklist entry
// can scroll to the field.

export const fieldAnchors = {
  account: 'field-account',
  password: 'field-password',
  plan: 'field-plan',
  registerTime: 'field-register-time',
  planStart: 'field-plan-start',
  planEnd: 'field-plan-end',
  connection: 'field-connection',
  proxy: 'field-proxy',
  deviceIds: 'field-device-ids',
  credentials: 'field-credentials',
} as const

export interface ChecklistItem {
  key: string
  label: string
  hint: string
  done: boolean
  anchor: string
}

/**
 * Ordered checklist of the fields a Claude authorization never fills.
 * Fixed items are always present with a done flag; identity items (account,
 * plan, register_time) are only listed while they are still missing, since
 * OAuth normally provides them.
 */
export function completionChecklist(
  edit: AccountForm,
  names: { connection?: string; proxy?: string } = {},
): ChecklistItem[] {
  const saved = keepsSavedCredential(edit)
  const items: ChecklistItem[] = []
  if (!hasValue(edit.account))
    items.push({
      key: 'account',
      label: '账号邮箱',
      hint: '授权未返回邮箱，请填写登录账号',
      done: false,
      anchor: fieldAnchors.account,
    })
  if (!hasValue(edit.plan))
    items.push({
      key: 'plan',
      label: '套餐',
      hint: '授权未识别套餐，请手动选择',
      done: false,
      anchor: fieldAnchors.plan,
    })
  if (!hasValue(edit.register_time))
    items.push({
      key: 'register_time',
      label: '注册日期',
      hint: '授权未返回注册时间',
      done: false,
      anchor: fieldAnchors.registerTime,
    })
  const deviceCount = parseDeviceIds(edit.claude_device_ids_text).length
  items.push(
    {
      key: 'password',
      label: '交付密码',
      hint: 'Claude 授权不返回密码',
      done: hasValue(edit.password) || !!edit.password_configured,
      anchor: fieldAnchors.password,
    },
    {
      key: 'plan_dates',
      label: '套餐开始 / 结束日期',
      hint: '按账单或供应商约定确认',
      done: hasValue(edit.plan_start_time) && hasValue(edit.plan_end_time),
      anchor: fieldAnchors.planStart,
    },
    {
      key: 'device_ids',
      label: '设备资料',
      hint: saved ? '沿用已保存资料' : deviceCount ? `已填写 ${deviceCount} 个` : '授权不返回，从有效凭证中获取',
      done: saved || deviceCount > 0,
      anchor: fieldAnchors.deviceIds,
    },
    {
      key: 'connection',
      label: '平台连接',
      hint:
        hasValue(edit.platform_connection_id) && names.connection
          ? `已选 ${names.connection}`
          : '选择要提交的供应商平台',
      done: hasValue(edit.platform_connection_id),
      anchor: fieldAnchors.connection,
    },
    {
      key: 'proxy',
      label: '交付代理',
      hint:
        hasValue(edit.proxy_id) && names.proxy
          ? `已选 ${names.proxy}`
          : !hasValue(edit.proxy_id) && hasValue(edit.credential_proxy_url)
            ? '已填写 proxy_url'
            : '选择代理池记录或填写 proxy_url',
      done: saved || hasValue(edit.proxy_id) || hasValue(edit.credential_proxy_url),
      anchor: fieldAnchors.proxy,
    },
  )
  // Credential fields OAuth normally supplies; listed only while some are
  // missing (a thin paste, or a profile call that failed upstream).
  if (!saved) {
    const gaps = credentialFields
      .filter(([key]) => key !== 'credential_proxy_url' && credentialMissing(edit, key))
      .map(([, field]) => field as string)
    if (gaps.length)
      items.push({
        key: 'credentials',
        label: '凭证字段',
        hint: `缺少 ${gaps.join('、')}`,
        done: false,
        anchor: fieldAnchors.credentials,
      })
  }
  return items
}

const accountDateAnchors: Record<AccountDateKey, string> = {
  register_time: fieldAnchors.registerTime,
  plan_start_time: fieldAnchors.planStart,
  plan_end_time: fieldAnchors.planEnd,
}

/** Anchor of the first thing still missing, or null when the form is complete. */
export function firstMissingAnchor(edit: AccountForm): string | null {
  const pending = completionChecklist(edit).find((item) => !item.done)
  if (pending) return pending.anchor
  // Everything is present; what remains can only be a date format/ordering problem.
  const dateKey = firstAccountDateError(accountDateErrors(edit, { requirePresent: false }))
  if (dateKey) return accountDateAnchors[dateKey]
  const rest = missingAccountFields(edit)
  if (!rest.length) return null
  return fieldAnchors.credentials
}

/** Human-readable remaining lifetime of the access token; empty when unknown. */
export function describeTokenExpiry(expired: unknown, now = new Date()): string {
  if (typeof expired !== 'string' || !expired) return ''
  const at = Date.parse(expired)
  if (Number.isNaN(at)) return ''
  const minutes = Math.round((at - now.getTime()) / 60000)
  if (minutes <= 0) return '令牌已过期'
  if (minutes < 60) return `令牌 ${minutes} 分钟后到期`
  const hours = Math.round(minutes / 60)
  if (hours < 48) return `令牌 ${hours} 小时后到期`
  return `令牌 ${Math.round(hours / 24)} 天后到期`
}
