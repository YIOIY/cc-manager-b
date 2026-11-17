import { test } from 'vitest'
import assert from 'node:assert/strict'
import {
  missingAccountFields,
  buildCredential,
  buildOnboardPayload,
  buildReauthorizePayload,
  parseDeviceIds,
  applyOAuthResult,
  completionChecklist,
  firstMissingAnchor,
  fieldAnchors,
  describeTokenExpiry,
} from '../src/model/account-form.ts'

const complete = {
  account: 'name@example.com',
  password: 'secret',
  plan: 'claude/cc-max-20x',
  register_location: 'US',
  register_time: '2026-11-01',
  plan_start_time: '2026-11-02',
  plan_end_time: '2026-12-02',
  platform_connection_id: 'conn',
  access_token: 'a',
  refresh_token: 'r',
  credential_email: 'Name@example.com',
  account_uuid: 'u',
  organization_uuid: 'o',
  organization_name: 'org',
  credential_type: 'claude',
  last_refresh: '2026-11-01T00:00:00Z',
  expired: '2026-12-01T00:00:00+08:00',
  credential_proxy_url: 'socks5h://user:pwd@1.2.3.4:1080',
  verification_status: 'not_required',
  claude_device_ids_text: 'dev-1, dev-2\ndev-3',
}

test('a complete form has nothing missing and builds a full payload', () => {
  assert.deepEqual(missingAccountFields(complete), [])
  const payload = buildOnboardPayload(complete)
  assert.equal(payload.platform_connection_id, 'conn')
  assert.deepEqual(payload.auth_json.claude_device_ids, ['dev-1', 'dev-2', 'dev-3'])
  assert.equal(payload.auth_json.email, 'Name@example.com')
  assert.equal(payload.auth_json.id_token, '')
  assert.equal('proxy_id' in payload, false)
})

test('reauthorize payload only carries credentials and always sends proxy_id', () => {
  const withPool = buildReauthorizePayload({ ...complete, proxy_id: 'pool-proxy', credential_proxy_url: '' })
  assert.equal(withPool.proxy_id, 'pool-proxy')
  assert.equal(withPool.auth_json.proxy_url, '')
  assert.equal(withPool.auth_json.access_token, 'a')
  const cleared = buildReauthorizePayload({ ...complete, proxy_id: '' })
  assert.equal(cleared.proxy_id, '')
  assert.equal(cleared.auth_json.proxy_url, 'socks5h://user:pwd@1.2.3.4:1080')
  assert.throws(() => buildReauthorizePayload({ account: 'a@b.co' }), /授权|凭证/)
})

test('a selected pool proxy satisfies the form without disclosing its password', () => {
  const edit = { ...complete, proxy_id: 'pool-proxy', credential_proxy_url: '' }
  assert.deepEqual(missingAccountFields(edit), [])
  assert.equal(buildOnboardPayload(edit).auth_json.proxy_url, '')
})

test('OAuth fills identity and confirmed profile data without retaining another credential', () => {
  const edit = { ...complete, account: '', password: '', account_uuid: 'old', claude_device_ids_text: 'old-device' }
  applyOAuthResult(edit, {
    auth_json: {
      access_token: 'new',
      refresh_token: 'new-refresh',
      email: 'new@example.com',
      account_uuid: 'new-id',
      expired: '2026-12-01T00:00:00Z',
    },
    plan: 'claude/cc-max-5x',
    register_time: '2025-02-01',
    warnings: [],
  })
  assert.equal(edit.account, 'new@example.com')
  assert.equal(edit.plan, 'claude/cc-max-5x')
  assert.equal(edit.register_time, '2025-02-01')
  assert.equal(edit.account_uuid, 'new-id')
  assert.equal(edit.claude_device_ids_text, '')
  assert.equal(edit.organization_name, '')
  assert.equal(edit.password, '')
})

test('OAuth cannot overwrite a different account or partially mutate on rejection', () => {
  const edit = { ...complete }
  assert.throws(
    () => applyOAuthResult(edit, { auth_json: { email: 'other@example.com', access_token: 'new' }, warnings: [] }),
    /不一致/,
  )
  assert.deepEqual(edit, complete)
})

test('missing fields are reported by label and date order is checked', () => {
  const missing = missingAccountFields({ ...complete, password: '', register_time: '2026-12-31', access_token: '' })
  assert.ok(missing.includes('密码'))
  assert.ok(missing.includes('注册日期早于套餐开始日期'))
  assert.ok(missing.includes('access_token'))
  assert.throws(() => buildOnboardPayload({ ...complete, password: '' }), /密码不能为空/)
  assert.throws(() => buildOnboardPayload({ ...complete, plan_end_time: '2026-01-01' }), /套餐开始日期不能晚于结束日期/)
})

test('saved password and credential are kept when editing', () => {
  const editing = {
    id: 'x',
    password_configured: true,
    credentials_configured: true,
    ...complete,
    password: '',
    access_token: '',
  }
  assert.deepEqual(missingAccountFields(editing), [])
  assert.equal(buildCredential(editing), null)
  const payload = buildOnboardPayload(editing)
  assert.equal('auth_json' in payload, false)
  assert.equal(payload.proxy_id, '')
  assert.throws(() => buildCredential({ ...editing, replace_credentials: true }), /Claude 凭证缺少/)
})

test('credential validation rejects malformed values', () => {
  assert.throws(() => buildCredential({ ...complete, credential_email: 'nope' }), /email 格式不正确/)
  assert.throws(() => buildCredential({ ...complete, credential_email: 'other@example.com' }), /必须与账号一致/)
  assert.throws(() => buildCredential({ ...complete, expired: 'yesterday' }), /RFC3339/)
  assert.throws(() => buildCredential({ ...complete, credential_proxy_url: 'ftp://x' }), /proxy_url/)
  assert.throws(() => buildCredential({ ...complete, claude_device_ids_text: ' , ' }), /设备资料/)
  assert.deepEqual(parseDeviceIds('a，b, c\n\nd'), ['a', 'b', 'c', 'd'])
})

test('pasting a credential JSON fills the form, derives constants and reports gaps', async () => {
  const { applyCredentialJson, defaultAccountDates } = await import('../src/model/account-form.ts')
  const edit = { account: 'name@example.com' }
  const result = applyCredentialJson(
    edit,
    JSON.stringify({
      access_token: 'a',
      refresh_token: 'r',
      account_uuid: 'u',
      organization_uuid: 'o',
      organization_name: 'org',
      expired: '2026-12-01T00:00:00Z',
      proxy_url: 'http://1.2.3.4:8080',
      claude_device_ids: ['dev-1', ' dev-2 ', ''],
    }),
    new Date('2026-11-09T10:00:00Z'),
  )
  assert.equal(edit.access_token, 'a')
  assert.equal(edit.claude_device_ids_text, 'dev-1\ndev-2')
  assert.equal(edit.credential_type, 'claude')
  assert.equal(edit.verification_status, 'not_required')
  assert.equal(edit.last_refresh, '2026-11-09T10:00:00Z')
  assert.equal(edit.credential_email, 'name@example.com')
  assert.deepEqual(result.missing, [])
  assert.equal(result.accountFilled, false)
  assert.deepEqual(missingAccountFields({ ...complete, ...edit }), [])

  // import-style record fills account + password too and reports what is still missing
  const partial = {}
  const r2 = applyCredentialJson(
    partial,
    JSON.stringify({ account: 'x@example.com', password: 'pw', auth_json: { access_token: 'a' } }),
  )
  assert.equal(partial.account, 'x@example.com')
  assert.equal(partial.password, 'pw')
  assert.equal(r2.accountFilled, true)
  assert.ok(r2.missing.includes('refresh_token'))
  assert.ok(r2.missing.includes('claude_device_ids'))

  assert.throws(() => applyCredentialJson({}, '{not json'), /JSON/)
  assert.throws(() => applyCredentialJson({}, '"text"'), /对象/)

  const dates = defaultAccountDates(new Date('2026-01-31T12:00:00Z'))
  assert.equal(dates.register_time, '2026-01-31')
  assert.equal(dates.plan_start_time, '2026-01-31')
  assert.equal(dates.plan_end_time, '2026-02-28')
  assert.equal(defaultAccountDates(new Date('2026-11-09T00:00:00Z')).plan_end_time, '2026-12-09')
})

const afterOAuth = {
  account: 'a@b.co',
  register_location: 'US',
  plan: 'claude/cc-pro',
  register_time: '2025-01-01',
  access_token: 'a',
  refresh_token: 'r',
  credential_email: 'a@b.co',
  account_uuid: 'u',
  organization_uuid: 'o',
  organization_name: 'n',
  credential_type: 'claude',
  last_refresh: '2026-11-01T00:00:00Z',
  expired: '2026-12-01T00:00:00Z',
  verification_status: 'not_required',
}

test('completion checklist lists what OAuth cannot provide and tracks progress', () => {
  const items = completionChecklist(afterOAuth, { connection: '平台 A' })
  assert.deepEqual(
    items.map((i) => i.key),
    ['password', 'plan_dates', 'device_ids', 'connection', 'proxy'],
  )
  assert.deepEqual(
    items.map((i) => i.done),
    [false, false, false, false, false],
  )
  assert.equal(firstMissingAnchor(afterOAuth), fieldAnchors.password)
  const progressed = {
    ...afterOAuth,
    password: 'p',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    proxy_id: 'px',
    platform_connection_id: 'c',
  }
  const next = completionChecklist(progressed, { connection: '平台 A', proxy: '印度代理' })
  assert.deepEqual(
    next.filter((i) => !i.done).map((i) => i.key),
    ['device_ids'],
  )
  assert.equal(next.find((i) => i.key === 'proxy').hint, '已选 印度代理')
  assert.equal(next.find((i) => i.key === 'connection').hint, '已选 平台 A')
  assert.equal(firstMissingAnchor(progressed), fieldAnchors.deviceIds)
  assert.equal(firstMissingAnchor({ ...progressed, claude_device_ids_text: 'd1' }), null)
})

test('conditional checklist items appear only while missing; saved credentials count as done', () => {
  const bare = completionChecklist({})
  assert.deepEqual(
    bare.slice(0, 3).map((i) => i.key),
    ['account', 'plan', 'register_time'],
  )
  const editing = completionChecklist({
    id: 'x',
    credentials_configured: true,
    password_configured: true,
    account: 'a@b.co',
    plan: 'p',
    register_time: '2025-01-01',
    platform_connection_id: 'c',
  })
  assert.deepEqual(
    editing.filter((i) => !i.done).map((i) => i.key),
    ['plan_dates'],
  )
  assert.equal(
    firstMissingAnchor({
      account: 'a@b.co',
      password: 'p',
      plan: 'p',
      register_location: 'US',
      register_time: '2025-01-01',
      plan_start_time: '2026-01-01',
      plan_end_time: '2026-02-01',
      platform_connection_id: 'c',
      proxy_id: 'px',
      claude_device_ids_text: 'd',
    }),
    fieldAnchors.credentials,
  )
})

test('token expiry is described safely', () => {
  const now = new Date('2026-11-09T12:00:00Z')
  assert.equal(describeTokenExpiry('2026-11-09T20:00:00Z', now), '令牌 8 小时后到期')
  assert.equal(describeTokenExpiry('2026-11-09T12:30:00Z', now), '令牌 30 分钟后到期')
  assert.equal(describeTokenExpiry('2026-11-12T12:00:00Z', now), '令牌 3 天后到期')
  assert.equal(describeTokenExpiry('2026-11-09T11:00:00Z', now), '令牌已过期')
  assert.equal(describeTokenExpiry('nope', now), '')
  assert.equal(describeTokenExpiry(undefined, now), '')
})

test('missing credential fields surface as one checklist item and date-order errors anchor to the dates', () => {
  const pasted = {
    account: 'a@b.co',
    password: 'p',
    plan: 'p',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    platform_connection_id: 'c',
    proxy_id: 'px',
    claude_device_ids_text: 'd',
    access_token: 'a',
    refresh_token: 'r',
  }
  const items = completionChecklist(pasted)
  const creds = items.find((i) => i.key === 'credentials')
  assert.ok(creds, 'credentials item present')
  assert.equal(creds.done, false)
  assert.equal(creds.anchor, fieldAnchors.credentials)
  assert.match(creds.hint, /organization_name/)
  assert.equal(firstMissingAnchor(pasted), fieldAnchors.credentials)
  // once the credential is complete the item disappears
  assert.equal(
    completionChecklist(complete).some((i) => i.key === 'credentials'),
    false,
  )
  // saved credentials never ask for credential fields
  assert.equal(
    completionChecklist({ id: 'x', credentials_configured: true }).some((i) => i.key === 'credentials'),
    false,
  )
  // only a date-ordering problem left → point at the plan dates, not the credential grid
  const misordered = { ...complete, plan_start_time: '2026-12-01', plan_end_time: '2026-11-15' }
  assert.deepEqual(missingAccountFields(misordered), ['套餐开始日期早于结束日期'])
  assert.equal(firstMissingAnchor(misordered), fieldAnchors.planStart)
})

test('a hand-written proxy_url is acknowledged in the proxy row', () => {
  const items = completionChecklist({ ...complete, proxy_id: '' })
  const proxy = items.find((i) => i.key === 'proxy')
  assert.equal(proxy.done, true)
  assert.equal(proxy.hint, '已填写 proxy_url')
})
