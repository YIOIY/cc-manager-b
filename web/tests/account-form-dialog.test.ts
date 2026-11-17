import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import AccountFormDialog from '../src/components/AccountFormDialog.vue'
import { session } from '../src/api'
import type { OAuthResult } from '../src/model/account-form'
import type { Account } from '../src/types/api'

const { confirm } = vi.hoisted(() => ({ confirm: vi.fn().mockResolvedValue(undefined) }))
vi.mock('element-plus', () => ({ ElMessageBox: { confirm } }))
vi.mock('../src/composables/useSession', () => ({ can: () => true }))
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
  expect(w.emitted('saved')).toHaveLength(1)
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
  expect(w.text()).toContain('套餐开始日期')
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
  // device ids are optional, so a complete form advances straight to review
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
