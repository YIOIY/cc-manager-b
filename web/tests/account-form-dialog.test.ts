import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import AccountFormDialog from '../src/components/AccountFormDialog.vue'
import { session } from '../src/api'
import type { OAuthResult } from '../src/model/account-form'
import type { Account } from '../src/types/api'

const { confirm } = vi.hoisted(() => ({ confirm: vi.fn().mockResolvedValue(undefined) }))
vi.mock('element-plus', () => ({ ElMessageBox: { confirm } }))
vi.mock('../src/composables/useSession', () => ({
  can: () => true,
  canOnboard: () => true,
  onboardAllowed: () => true,
  useSession: () => ({
    check: async () => {},
    onboardAllowed: () => true,
    canOnboard: () => true,
    can: () => true,
    owns: () => true,
    user: { value: { id: 'me', is_admin: true, permissions: [], can_onboard: true } },
  }),
}))
const fetchMock = vi.fn()
const wrappers: VueWrapper[] = []
const fixture: OAuthResult = {
  auth_json: {
    access_token: 'access-secret',
    refresh_token: 'refresh-secret',
    email: 'fixture@example.com',
    account_uuid: 'u',
    organization_uuid: 'o',
    organization_name: 'Org',
    expired: '2099-01-01T00:00:00Z',
    last_refresh: '2026-11-09T00:00:00Z',
    type: 'claude',
    verification_status: 'not_required',
  },
  plan: 'claude/cc-max-5x',
  register_time: '2025-02-01',
  warnings: [],
}
let authorization: OAuthResult
beforeEach(() => {
  session.value = { authenticated: true, csrf_token: 'csrf' }
  vi.stubGlobal('fetch', fetchMock)
  authorization = structuredClone(fixture)
  confirm.mockResolvedValue(undefined)
  localStorage.clear()
})
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  vi.unstubAllGlobals()
  fetchMock.mockReset()
  confirm.mockClear()
})
const input = {
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
  props: ['modelValue'],
}
const stubs = {
  'el-dialog': { template: '<div><slot name="header" /><slot /><slot name="footer" /></div>' },
  'el-form': { template: '<form @submit.prevent><slot /></form>' },
  'el-form-item': {
    template:
      '<div><label>{{ label }}</label><slot name="label" /><slot /><span class="stub-error">{{ error }}</span></div>',
    props: ['label', 'error'],
  },
  'el-input': input,
  'el-date-picker': input,
  'el-input-number': input,
  'el-select': {
    template:
      '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><slot /></select>',
    props: ['modelValue'],
  },
  'el-option': {
    template: '<option :value="value" :disabled="disabled">{{ label }}</option>',
    props: ['value', 'label', 'disabled'],
  },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-button': {
    template: '<button type="button" :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading', 'type'],
    emits: ['click'],
  },
  ClaudeOAuthPanel: defineComponent({
    props: { disabled: Boolean, summary: { type: Object, default: null } },
    emits: ['authorized', 'busy'],
    setup(props, { emit }) {
      return () =>
        h(
          'button',
          {
            type: 'button',
            class: 'fake-oauth',
            disabled: props.disabled,
            onClick: () => emit('authorized', authorization),
          },
          props.summary ? '账号已连接' : '登录 Claude',
        )
    },
  }),
}
const connections = [{ id: 'c', name: '平台 A', base_url: 'https://a', timezone: 'UTC', enabled: true }]
const proxies = [
  { id: 'px', name: '代理 A', host: 'proxy.example.com', port: 8080, scheme: 'http', enabled: true },
  { id: 'px2', name: '代理 B', host: 'other.example.com', port: 8080, scheme: 'http', enabled: true },
]
function setup(account: Account | null = null) {
  const w = mount(AccountFormDialog, {
    attachTo: document.body,
    props: { modelValue: true, account, proxies, connections },
    global: { stubs },
  })
  wrappers.push(w)
  return w
}
async function authorize(w: VueWrapper) {
  await w.get('#field-proxy select').setValue('px')
  await w.get('.fake-oauth').trigger('click')
  await flushPromises()
}
async function next(w: VueWrapper) {
  await w.get('[data-next]').trigger('click')
  await flushPromises()
}
async function fill(w: VueWrapper) {
  await w.get('#field-password input').setValue('delivery-secret')
  await w.get('#field-plan-start input').setValue('2026-01-01')
  await w.get('#field-plan-end input').setValue('2026-02-01')
  await w.get('#field-device-ids input, #field-device-ids textarea').setValue('dev-1,dev-2')
}
const response = (data: unknown, status = 200) => new Response(JSON.stringify({ data }), { status })

test('starts with connection and proxy selection, and does not expose business fields before authorization', async () => {
  const w = setup()
  await flushPromises()
  expect(w.find('#field-password').exists()).toBe(false)
  expect(w.get('.fake-oauth').attributes('disabled')).toBeDefined()
  await w.get('#field-proxy select').setValue('px')
  expect(w.get('.fake-oauth').attributes('disabled')).toBeUndefined()
  await next(w)
  expect(w.find('#field-password').exists()).toBe(false)
  expect(fetchMock).not.toHaveBeenCalled()
})
test('authorization stays on the first step; filling gaps and review never submit until explicit confirmation', async () => {
  const w = setup()
  await authorize(w)
  expect(w.find('#field-password').exists()).toBe(false)
  await next(w)
  expect(w.get('.wizard-identity').text()).toContain('fixture@example.com')
  expect(w.find('#field-account input').exists()).toBe(false)
  expect(w.find('input[placeholder="粘贴 access token"]').exists()).toBe(false)
  await next(w)
  expect(w.find('#field-password').exists()).toBe(true)
  expect(fetchMock).not.toHaveBeenCalled()
  await fill(w)
  await next(w)
  expect(w.get('[data-review]').text()).toContain('代理 A')
  expect(w.get('[data-review]').text()).not.toContain('delivery-secret')
  expect(w.get('[data-review]').text()).not.toContain('access-secret')
  expect(fetchMock).not.toHaveBeenCalled()
  fetchMock.mockResolvedValueOnce(response({ account_id: 'new', job_id: 'job', state: 'queued' }, 202))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toContain('/accounts/onboard')
  const payload = JSON.parse(init.body)
  expect(payload.proxy_id).toBe('px')
  expect(payload.auth_json.claude_device_ids).toEqual(['dev-1', 'dev-2'])
  expect(payload.auth_json.email).toBe('fixture@example.com')
  // Accepted: the wizard shows a success page and only closes on 完成.
  expect(w.emitted('submitted')).toHaveLength(1)
  expect(w.emitted('saved')).toBeUndefined()
  expect(w.get('[data-submitted]').text()).toContain('已提交 fixture@example.com')
  await w.get('[data-finish]').trigger('click')
  expect(w.emitted('saved')?.[0]?.[0]).toMatchObject({ account_id: 'new' })
  expect(w.emitted('update:modelValue')?.at(-1)).toEqual([false])
})

test('after a submission the wizard can start the next account with the platform and proxy kept', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'new', job_id: 'job', state: 'queued' }, 202))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  await w.get('[data-add-another]').trigger('click')
  await flushPromises()
  expect(w.find('[data-submitted]').exists()).toBe(false)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.get('.fake-oauth').text()).toContain('登录 Claude')
  expect(w.get('.fake-oauth').attributes('disabled')).toBeUndefined() // proxy px still selected
  expect((w.get('#field-proxy select').element as HTMLSelectElement).value).toBe('px')
  expect(w.emitted('update:modelValue')).toBeUndefined()
  // The next submission is a fresh request, not a replay of the first.
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'new-2', job_id: 'job-2', state: 'queued' }, 202))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const [first, second] = fetchMock.mock.calls
  expect(first![1].headers['Idempotency-Key']).not.toBe(second![1].headers['Idempotency-Key'])
  expect(JSON.parse(second![1].body).auth_json.claude_device_ids).toEqual(['dev-1', 'dev-2'])
})

test('the last used proxy is preselected next time while an unusable one is ignored', async () => {
  localStorage.setItem('cc-manager:last-proxy', 'px2')
  const w = setup()
  await flushPromises()
  expect((w.get('#field-proxy select').element as HTMLSelectElement).value).toBe('px2')
  expect(w.get('.fake-oauth').attributes('disabled')).toBeUndefined()
  w.unmount()
  localStorage.setItem('cc-manager:last-proxy', 'gone')
  const again = setup()
  await flushPromises()
  // Nothing preselected: the login button stays blocked on the proxy.
  expect(again.get('.fake-oauth').attributes('disabled')).toBeDefined()
  expect(again.get('[data-oauth-blocker]').text()).toContain('先选择或添加代理')
})

test('the disabled login button says what is missing and jumps to that field', async () => {
  const w = setup()
  await flushPromises()
  expect(w.get('[data-oauth-blocker]').text()).toContain('先选择或添加代理')
  await w.get('#field-proxy select').setValue('px')
  expect(w.find('[data-oauth-blocker]').exists()).toBe(false)
  await w.get('#field-connection select').setValue('')
  expect(w.get('[data-oauth-blocker]').text()).toContain('先选择上号平台')
})
test('returning from review preserves input and an invalid date prevents review', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  await w.get('[data-back]').trigger('click')
  expect((w.get('#field-password input').element as HTMLInputElement).value).toBe('delivery-secret')
  await w.get('#field-plan-end input').setValue('2025-12-01')
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.text()).toContain('套餐结束日期须至少晚于开始日期一天')
  expect(fetchMock).not.toHaveBeenCalled()
})
test('changing proxy requires reauthorization while preserving business inputs', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await w.get('[data-back]').trigger('click')
  await w.get('#field-proxy select').setValue('px2')
  await flushPromises()
  expect(w.text()).toContain('代理已变更')
  expect(w.get('.fake-oauth').text()).toBe('登录 Claude')
  await next(w)
  expect(w.find('#field-password').exists()).toBe(false)
  await w.get('.fake-oauth').trigger('click')
  await next(w)
  if (w.find('[data-review]').exists()) await w.get('[data-back]').trigger('click')
  expect((w.get('#field-password input').element as HTMLInputElement).value).toBe('delivery-secret')
})
test('explicit account replacement clears old password, subscription dates and device identity', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await w.get('[data-back]').trigger('click')
  await w.get('[data-change-account]').trigger('click')
  await flushPromises()
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: 'new@example.com', account_uuid: 'new' } }
  await w.get('.fake-oauth').trigger('click')
  await next(w)
  expect(w.get('.wizard-identity').text()).toContain('new@example.com')
  for (const id of ['field-password', 'field-plan-start', 'field-plan-end', 'field-device-ids'])
    expect((w.get('#' + id + ' input, #' + id + ' textarea').element as HTMLInputElement).value).toBe('')
})
test('complete JSON imports can reach review and partial imports retain missing-field guidance', async () => {
  const w = setup()
  await w.get('#field-proxy select').setValue('px')
  await w.get('[data-import-input]').setValue(
    JSON.stringify({
      account: 'fixture@example.com',
      password: 'import-secret',
      plan: 'claude/cc-pro',
      register_time: '2025-01-01',
      plan_start_time: '2026-01-01',
      plan_end_time: '2026-02-01',
      auth_json: { ...fixture.auth_json, claude_device_ids: ['d'] },
    }),
  )
  await w.get('[data-import]').trigger('click')
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  expect(fetchMock).not.toHaveBeenCalled()
})
test('partial imports expose required credential fields instead of falsely permitting review', async () => {
  const w = setup()
  await w.get('#field-proxy select').setValue('px')
  await w.get('[data-import-input]').setValue('{"access_token":"a","refresh_token":"r"}')
  await w.get('[data-import]').trigger('click')
  await next(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.text()).toContain('补充')
  expect(fetchMock).not.toHaveBeenCalled()
})
test('server field errors return from review to their editable fields and preserve the request id', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'VALIDATION_FAILED',
          category: 'validation',
          message: '有资料不符合要求',
          fields: [
            { field: 'auth_json.refresh_token', code: 'REQUIRED', message: '缺少刷新令牌' },
            { field: 'plan_end_time', code: 'OUT_OF_RANGE', message: '请调整结束日期' },
          ],
        },
        request_id: 'req-f1',
      }),
      { status: 422 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.get('#field-plan-end').text()).toContain('请调整结束日期')
  expect(w.text()).toContain('缺少刷新令牌')
  expect(w.text()).toContain('req-f1')
})
test('existing accounts keep saved credentials and passwords without exposing or resending them', async () => {
  const w = setup({
    id: 'old',
    account: 'old@example.com',
    plan: 'claude/cc-pro',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    proxy_id: 'px',
    credentials_configured: true,
    password_configured: true,
  } as Account)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'old', job_id: 'job', state: 'queued' }, 202))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toContain('/accounts/old/onboard')
  const payload = JSON.parse(init.body)
  expect(payload.auth_json).toBeUndefined()
  expect(payload.password).toBeUndefined()
})
test('close asks before discarding entered credentials, cancellation keeps the form open', async () => {
  const w = setup()
  await authorize(w)
  confirm.mockRejectedValueOnce('cancel')
  await w.get('[data-cancel]').trigger('click')
  await flushPromises()
  expect(w.emitted('update:modelValue')).toBeUndefined()
  await w.get('[data-cancel]').trigger('click')
  await flushPromises()
  expect(w.emitted('update:modelValue')?.at(-1)).toEqual([false])
  await w.setProps({ modelValue: false })
  await w.setProps({ modelValue: true })
  expect(w.get('.fake-oauth').text()).toBe('登录 Claude')
  expect(w.find('#field-password').exists()).toBe(false)
})

test('two confirm clicks in the same event turn create only one pending request', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  let finish!: (response: Response) => void
  fetchMock.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve
      }),
  )
  const button = w.get('[data-submit]').element as HTMLButtonElement
  button.click()
  button.click()
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  finish(response({ account_id: 'new', job_id: 'job', state: 'queued' }, 202))
  await flushPromises()
})

test('credential format errors reveal advanced fields and keep the form out of review', async () => {
  const w = setup()
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, expired: 'invalid' } }
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.get('.credential-manual').attributes('open')).toBeDefined()
  expect(w.text()).toContain('RFC3339')
  expect(fetchMock).not.toHaveBeenCalled()
})

test('replacing credentials with another imported identity does not reuse the previous delivery data', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await w.get('[data-back]').trigger('click')
  await w.get('[data-import-input]').setValue(JSON.stringify({ ...fixture.auth_json, email: 'other@example.com' }))
  await w.get('[data-import]').trigger('click')
  await next(w)
  expect(w.get('.wizard-identity').text()).toContain('other@example.com')
  expect((w.get('#field-password input').element as HTMLInputElement).value).toBe('')
  expect((w.get('#field-plan-start input').element as HTMLInputElement).value).toBe('')
  expect((w.get('#field-device-ids input').element as HTMLInputElement).value).toBe('')
})

test('an OAuth response without email allows the missing identity to be filled and submitted consistently', async () => {
  const w = setup()
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: undefined } }
  await authorize(w)
  await next(w)
  expect(w.get('#field-account input').attributes('disabled')).toBeUndefined()
  await w.get('#field-account input').setValue('manual-identity@example.com')
  await fill(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'new', job_id: 'job', state: 'queued' }, 202))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const payload = JSON.parse(fetchMock.mock.calls[0]![1].body)
  expect(payload.account).toBe('manual-identity@example.com')
  expect(payload.auth_json.email).toBe('manual-identity@example.com')
})

test('an unsaved new proxy blocks advancing even when the saved credential already contains a proxy', async () => {
  const w = setup({
    id: 'old',
    account: 'old@example.com',
    plan: 'claude/cc-pro',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    credentials_configured: true,
    password_configured: true,
  } as Account)
  await w.get('input[value="new"]').setValue()
  await w.get('[data-proxy-address]').setValue('host:8080')
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(fetchMock).not.toHaveBeenCalled()
  expect(w.text()).toContain('保存')
})

test('conflicting or unidentified credential imports do not replace an already connected identity', async () => {
  const w = setup()
  await authorize(w)
  for (const value of [
    { account: 'other@example.com', auth_json: fixture.auth_json },
    { ...fixture.auth_json, email: undefined },
  ]) {
    await w.get('[data-import-input]').setValue(JSON.stringify(value))
    await w.get('[data-import]').trigger('click')
    expect(w.text()).toMatch(/邮箱不一致|缺少账号邮箱/)
    await next(w)
    expect(w.get('.wizard-identity').text()).toContain('fixture@example.com')
    await w.get('[data-back]').trigger('click')
  }
})

test('saved credential errors return to reauthorization while retaining business fields and request details', async () => {
  const w = setup({
    id: 'old',
    account: 'old@example.com',
    plan: 'claude/cc-pro',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    credentials_configured: true,
    password_configured: true,
    proxy_id: 'px',
  } as Account)
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'VALIDATION_FAILED',
          message: '凭证需更新',
          fields: [{ field: 'auth_json.refresh_token', code: 'INVALID', message: '刷新凭证无效' }],
        },
        request_id: 'req-saved',
      }),
      { status: 422 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.get('.fake-oauth').isVisible()).toBe(true)
  expect(w.text()).toContain('req-saved')
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: 'old@example.com' } }
  await w.get('.fake-oauth').trigger('click')
  await next(w)
  expect(w.get('[data-review]').text()).toContain('2026-01-01')
})

test('editing a field clears its stale inline error while preserving other missing-field errors', async () => {
  const w = setup()
  await authorize(w)
  await next(w)
  await next(w)
  expect(w.get('#field-password .stub-error').text()).toContain('请填写')
  await w.get('#field-password input').setValue('corrected-password')
  expect(w.get('#field-password .stub-error').text()).toBe('')
  expect(w.get('#field-plan-start .stub-error').text()).toContain('请填写')
})

test('the manual and the device-id guide open in a new tab so the form is not lost', async () => {
  const w = setup()
  const manual = w.get('a[href="/help?topic=onboard"]')
  expect(manual.text()).toContain('上号手册')
  expect(manual.attributes('target')).toBe('_blank')
  await authorize(w)
  await next(w)
  const guide = w.get('a[href="/help?topic=device-id"]')
  expect(guide.text()).toContain('如何获取')
  expect(guide.attributes('target')).toBe('_blank')
  expect(guide.attributes('rel')).toContain('noopener')
})

function linkedAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 'acc-1',
    account: 'old@example.com',
    plan: 'claude/cc-pro',
    register_location: 'US',
    register_time: '2025-01-01',
    plan_start_time: '2026-01-01',
    plan_end_time: '2026-02-01',
    proxy_id: 'px',
    version: 2,
    credentials_configured: true,
    password_configured: true,
    platform_status: 'sync_failed',
    platform_links: [{ ingestion_state: 'accepted', connection_id: 'c' }],
    ...overrides,
  } as Account
}

function setupReauth(account: Account = linkedAccount()) {
  const w = mount(AccountFormDialog, {
    attachTo: document.body,
    props: {
      modelValue: true,
      account,
      proxies,
      connections,
      forceReauth: true,
      connectionId: 'c',
    },
    global: { stubs },
  })
  wrappers.push(w)
  return w
}

test('reauthorizing a saved account uses fresh tokens without asking for saved device and proxy fields again', async () => {
  const w = setupReauth(linkedAccount({ proxy_id: '' }))
  await w.get('[data-reauthorize]').trigger('click')
  expect(w.get('.fake-oauth').attributes('disabled')).toBeUndefined()
  authorization = {
    ...fixture,
    auth_json: {
      ...fixture.auth_json,
      email: 'old@example.com',
      account_uuid: undefined,
      organization_name: undefined,
    },
  }
  await w.get('.fake-oauth').trigger('click')
  await next(w)
  expect(w.get('[data-review]').isVisible()).toBe(true)
  expect(w.text()).toMatch(/沿用已保存或自动生成/)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const body = JSON.parse(fetchMock.mock.calls[0]![1].body)
  expect(body.auth_json.access_token).toBe(fixture.auth_json.access_token)
  expect(body.auth_json.account_uuid).not.toBe('undefined')
  expect(body.auth_json.claude_device_ids).toEqual([])
})

test('unusable saved device fields can still be filled after fresh authorization', async () => {
  const w = setupReauth()
  await w.get('[data-reauthorize]').trigger('click')
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: 'old@example.com' } }
  await authorize(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'VALIDATION_FAILED',
          message: '设备资料缺失',
          fields: [{ field: 'auth_json.claude_device_ids', code: 'REQUIRED', message: '请补填设备标识' }],
        },
      }),
      { status: 422 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.get('[data-reauth-devices]').isVisible()).toBe(true)
  await w.get('[data-reauth-devices] #field-device-ids input').setValue('replacement-device')
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body).auth_json.claude_device_ids).toEqual(['replacement-device'])
})

test('device overrides already entered for the same account survive starting fresh authorization', async () => {
  const w = setupReauth()
  await w.get('[data-saved-devices] input').setValue('chosen-device')
  await w.get('[data-reauthorize]').trigger('click')
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: 'old@example.com' } }
  await authorize(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body).auth_json.claude_device_ids).toEqual(['chosen-device'])
})

test('sync_failed reauth keeps sealed credentials and omits auth_json on submit', async () => {
  const w = setupReauth()
  expect(w.text()).toContain('可直接用已保存凭证同步到平台')
  expect(w.find('.fake-oauth').exists()).toBe(false)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  expect(w.get('[data-submit]').text()).toContain('同步到平台')
  fetchMock.mockResolvedValueOnce(
    response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1', credential: { source: 'saved' } }),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toContain('/accounts/acc-1/reauthorize')
  const payload = JSON.parse(init.body)
  expect(payload.auth_json).toBeUndefined()
  expect(payload.proxy_id).toBe('px')
  expect(payload.expected_version).toBe(2)
})

test('expired saved credential shows an explicit notice and keeps reauthorize', async () => {
  const w = setupReauth()
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'VALIDATION_FAILED',
          message: '提交的资料不符合要求',
          category: 'validation',
          fields: [{ field: 'auth_json.expired', code: 'INVALID_FORMAT', message: '访问令牌已过期，请重新授权' }],
        },
      }),
      { status: 422 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.get('[data-saved-expired]').text()).toContain('访问令牌已过期，请重新授权')
  expect(w.findAll('[data-reauthorize]').length).toBeGreaterThan(0)
})

test('reauth 502 keeps the same token for retry; startReauthorize requires a new token', async () => {
  const w = setupReauth(linkedAccount({ credentials_configured: false }))
  authorization = {
    ...fixture,
    auth_json: { ...fixture.auth_json, email: 'old@example.com', access_token: 'token-a' },
  }
  await authorize(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: { code: 'PLATFORM_UNAVAILABLE', message: '平台暂时无法访问', retryable: true, action: 'retry' },
        request_id: 'req-502',
      }),
      { status: 502 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.find('[data-review]').exists()).toBe(true)
  expect(w.get('[data-submit]').text()).toContain('重试同步')
  expect(w.text()).toContain('req-502')
  fetchMock.mockResolvedValueOnce(
    response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1', credential: { source: 'replaced' } }),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body).auth_json.access_token).toBe('token-a')

  await w.setProps({ modelValue: false })
  await w.setProps({
    modelValue: true,
    account: linkedAccount({ credentials_configured: false }),
    forceReauth: true,
  })
  authorization = {
    ...fixture,
    auth_json: { ...fixture.auth_json, email: 'old@example.com', access_token: 'token-b' },
  }
  await authorize(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: { code: 'PLATFORM_UNAVAILABLE', message: '平台暂时无法访问', retryable: true, action: 'retry' },
        request_id: 'req-502b',
      }),
      { status: 502 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  await w.get('[data-reauthorize]').trigger('click')
  expect(w.find('.fake-oauth').exists()).toBe(true)
  authorization = {
    ...fixture,
    auth_json: { ...fixture.auth_json, email: 'old@example.com', access_token: 'token-c' },
  }
  await authorize(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  fetchMock.mockResolvedValueOnce(
    response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1', credential: { source: 'replaced' } }),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls.at(-1)![1].body).auth_json.access_token).toBe('token-c')
})

test('linked reauth OAuth without device ids mints a UUID and skips the device step', async () => {
  const w = setupReauth(linkedAccount({ credentials_configured: false }))
  authorization = { ...fixture, auth_json: { ...fixture.auth_json, email: 'old@example.com' } }
  await authorize(w)
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  expect(w.text()).toContain('已填写')
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: { code: 'PLATFORM_UNAVAILABLE', message: '平台暂时无法访问', retryable: true, action: 'retry' },
      }),
      { status: 502 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const first = JSON.parse(fetchMock.mock.calls[0]![1].body).auth_json.claude_device_ids
  expect(first).toHaveLength(1)
  expect(first[0]).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i)
  fetchMock.mockResolvedValueOnce(
    response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1', credential: { source: 'replaced' } }),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body).auth_json.claude_device_ids).toEqual(first)
})

test('changing a saved credential proxy requires reauthorization before submission', async () => {
  const w = setupReauth()
  // The only request allowed here is the explicit rebind of the bound exit.
  fetchMock.mockResolvedValueOnce(response({ id: 'acc-1', updated: true, version: 3 }))
  await w.get('#field-proxy select').setValue('px2')
  await flushPromises()
  expect(w.text()).toContain('代理已变更')
  expect(w.find('.fake-oauth').exists()).toBe(true)
  expect(w.get('[data-next]').attributes('disabled')).toBeDefined()
  expect(w.find('[data-submit]').exists()).toBe(false)
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(fetchMock.mock.calls[0]![1].method).toBe('PATCH')
})

test('linked reauth credential gaps open step 2 and review can return there to edit', async () => {
  const w = setupReauth(linkedAccount({ credentials_configured: false }))
  authorization = {
    ...fixture,
    auth_json: { ...fixture.auth_json, email: 'old@example.com', organization_uuid: undefined },
  }
  await authorize(w)
  await next(w)
  expect(w.find('[data-reauth-devices]').isVisible()).toBe(true)
  expect(w.get('.credential-manual').attributes('open')).toBeDefined()
  expect(w.text()).toMatch(/凭证缺少|organization_uuid/)
  await w
    .get('[data-reauth-devices] #field-device-ids input, [data-reauth-devices] #field-device-ids textarea')
    .setValue('dev-1')
  await w.get('#credential-field-organization_uuid input').setValue('org-fixed')
  await next(w)
  expect(w.find('[data-review]').isVisible()).toBe(true)
  await w.get('[data-back]').trigger('click')
  await flushPromises()
  expect(w.find('[data-reauth-devices]').isVisible()).toBe(true)
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(
    (
      w.get('[data-reauth-devices] #field-device-ids input, [data-reauth-devices] #field-device-ids textarea')
        .element as HTMLInputElement
    ).value,
  ).toContain('dev-1')
})

test('saved-credential device errors return to step 1 for overlay without OAuth', async () => {
  const w = setupReauth()
  expect(w.find('[data-saved-devices]').exists()).toBe(true)
  expect(w.find('.fake-oauth').exists()).toBe(false)
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'VALIDATION_FAILED',
          message: '设备资料无效',
          fields: [{ field: 'auth_json.claude_device_ids', code: 'INVALID_FORMAT', message: '设备标识无效' }],
        },
        request_id: 'req-saved-device',
      }),
      { status: 422 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.find('[data-review]').exists()).toBe(false)
  expect(w.find('[data-saved-devices]').exists()).toBe(true)
  expect(w.text()).toMatch(/无需重新授权|设备/)
  expect(w.text()).toContain('req-saved-device')
  expect(w.find('.fake-oauth').exists()).toBe(false)
  await w.get('[data-saved-devices] input, [data-saved-devices] textarea').setValue('device-overlay')
  await next(w)
  expect(w.find('[data-review]').exists()).toBe(true)
  expect(w.text()).toContain('本次补填')
  fetchMock.mockResolvedValueOnce(
    response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1', credential: { source: 'saved' } }),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  const body = JSON.parse(fetchMock.mock.calls[1]![1].body)
  expect(body.auth_json).toBeUndefined()
  expect(body.claude_device_ids).toEqual(['device-overlay'])
})

test('retains the ready session across wizard steps, sends proof, and releases it on discard', async () => {
  authorization.oauth_session_id = 'ready-session'
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  expect(fetchMock).not.toHaveBeenCalled()
  fetchMock.mockResolvedValueOnce(response({ cancelled: true }))
  await w.get('[data-cancel]').trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls[0]![0]).toBe('/api/v1/claude-oauth/sessions/ready-session')
  expect(fetchMock.mock.calls[0]![1].method).toBe('DELETE')
})

test('onboard submits the retained OAuth session and does not cancel a committed session', async () => {
  authorization.oauth_session_id = 'ready-session'
  const w = setup()
  await authorize(w)
  await next(w)
  await fill(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'new', job_id: 'job' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body).oauth_session_id).toBe('ready-session')
  await w.get('[data-finish]').trigger('click')
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

test('an unknown platform outcome is not offered as a blind retry', async () => {
  const w = setupReauth()
  await next(w)
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        error: {
          code: 'SUBMIT_OUTCOME_UNKNOWN',
          message: '需要核实平台结果',
          action: 'check_platform',
          retryable: true,
        },
      }),
      { status: 502 },
    ),
  )
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(w.get('[data-submit]').attributes('disabled')).toBeDefined()
  expect(w.get('[data-submit]').text()).not.toContain('重试')
})

test('reauthorize sends the retained session proof with fresh credentials', async () => {
  const w = setupReauth(linkedAccount({ credentials_configured: false }))
  authorization = {
    ...fixture,
    oauth_session_id: 'ready-reauth',
    auth_json: { ...fixture.auth_json, email: 'old@example.com' },
  }
  await authorize(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body).oauth_session_id).toBe('ready-reauth')
})

test('fresh explicit device IDs take precedence over retained device IDs', async () => {
  const w = setupReauth()
  await w.get('[data-saved-devices] input').setValue('old-device')
  await w.get('[data-reauthorize]').trigger('click')
  authorization = {
    ...fixture,
    auth_json: { ...fixture.auth_json, email: 'old@example.com', claude_device_ids: ['real-device'] },
  }
  await authorize(w)
  await next(w)
  fetchMock.mockResolvedValueOnce(response({ account_id: 'acc-1', platform_pushed: true, upstream_id: '1' }))
  await w.get('[data-submit]').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body).auth_json.claude_device_ids).toEqual(['real-device'])
})

test('a proxy saved inside the wizard becomes the selection and enables login for a new account', async () => {
  const w = setup()
  await flushPromises()
  expect(w.get('.fake-oauth').attributes('disabled')).toBeDefined()
  fetchMock.mockResolvedValueOnce(response({ id: 'px3' }))
  await w.get('input[value="new"]').setValue()
  await w.get('[data-proxy-address]').setValue('http://proxy.example.com:8080')
  // The picker reports busy while it saves, then hands over the new id as the
  // selection; the busy signal must not swallow that selection.
  await w.get('[data-save-proxy]').trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls[0]![0]).toBe('/api/v1/proxies')
  expect(w.text()).toContain('新代理已保存并选中')
  expect((w.get('#field-proxy select').element as HTMLSelectElement).value).toBe('px3')
  expect(w.get('.fake-oauth').attributes('disabled')).toBeUndefined()
  // No PATCH: a new account has nothing to rebind on the server.
  expect(fetchMock.mock.calls.some((c) => c[1]?.method === 'PATCH')).toBe(false)
})

test('changing the proxy of an existing account rebinds it explicitly before any new authorization', async () => {
  const w = setupReauth(linkedAccount({ proxy_id: 'px', version: 2 }))
  await flushPromises()
  fetchMock.mockResolvedValueOnce(response({ id: 'acc-1', updated: true, version: 3 }))
  await w.get('#field-proxy select').setValue('px2')
  await flushPromises()
  // The server refuses an OAuth start on any exit other than the bound one, so
  // the dialog performs the designed explicit rebind (PATCH) first.
  const patch = fetchMock.mock.calls.find((c) => c[1]?.method === 'PATCH')
  expect(patch?.[0]).toBe('/api/v1/accounts/acc-1')
  expect(JSON.parse(patch![1].body)).toEqual({ proxy_id: 'px2', expected_version: 2 })
  expect((w.get('#field-proxy select').element as HTMLSelectElement).value).toBe('px2')
  expect(w.text()).toContain('代理已变更')
})

test('a refused proxy rebind keeps the previous proxy selected and explains why', async () => {
  const w = setupReauth(linkedAccount({ proxy_id: 'px', version: 2 }))
  await flushPromises()
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({ error: { code: 'ACCOUNT_IN_PROGRESS', message: '账号正在授权，请完成或显式取消授权后再修改' } }),
      { status: 409 },
    ),
  )
  await w.get('#field-proxy select').setValue('px2')
  await flushPromises()
  // No rebind happened, so nothing asks for a re-authorization either.
  expect(w.text()).toContain('账号正在授权')
  expect(w.text()).not.toContain('代理已变更')
  expect(fetchMock.mock.calls.filter((c) => c[1]?.method === 'PATCH')).toHaveLength(1)
})
