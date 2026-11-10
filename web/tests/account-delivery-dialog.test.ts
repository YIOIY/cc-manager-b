import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AccountDeliveryDialog from '../src/components/AccountDeliveryDialog.vue'
import { session } from '../src/api'
import { useSession } from '../src/composables/useSession'
import type { Account, PlatformConnection } from '../src/types/api'
import { ElMessageBox } from 'element-plus'

vi.mock('element-plus', () => ({ ElMessageBox: { prompt: vi.fn() } }))

const fetchMock = vi.fn()
beforeEach(async () => {
  vi.stubGlobal('fetch', fetchMock)
  session.value = { authenticated: true, csrf_token: 'csrf' }
  // establish an admin user so permission-gated buttons render
  fetchMock.mockImplementationOnce(() =>
    json(200, {
      data: {
        authenticated: true,
        csrf_token: 'csrf',
        user: {
          id: 'u',
          username: 'admin',
          role: 'admin',
          permissions: ['accounts.onboard', 'connections.create', 'accounts.delete'],
        },
      },
    }),
  )
  await useSession().check()
  fetchMock.mockReset()
})
afterEach(() => {
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const connections: PlatformConnection[] = [
  {
    id: 'conn',
    name: '平台 A',
    base_url: 'https://a.example',
    timezone: 'Asia/Shanghai',
    enabled: true,
    version: 1,
    token_configured: true,
  },
]
const account = {
  id: 'acc-1',
  account: 'user@example.test',
  plan: 'claude/cc-max-20x',
  version: 3,
  archived: false,
  platform_links: [],
  onboarding_state: 'pending',
} as unknown as Account

// Element Plus renders dialogs through a teleport; stub them so the slot
// content mounts inline and the test can read it.
const stubs = {
  'el-dialog': { template: '<div class="dialog"><slot /><slot name="footer" /></div>' },
  'el-button': {
    template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-form': { template: '<form><slot /></form>' },
  'el-form-item': { template: '<div><slot /></div>' },
  'el-select': { template: '<select><slot /></select>' },
  'el-option': { template: '<option />' },
}

test('opening a pending account runs preflight for the only enabled connection and gates the submit button', async () => {
  fetchMock.mockImplementation((url: string) => {
    if (url.endsWith('/preflight'))
      return json(200, { data: { can_submit: false, checks: [{ code: 'PASSWORD_REQUIRED', status: 'error' }] } })
    return json(200, { data: {} })
  })
  const wrapper = mount(AccountDeliveryDialog, { props: { modelValue: true, account, connections }, global: { stubs } })
  await flushPromises()
  const preflightCall = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/preflight'))
  expect(preflightCall).toBeTruthy()
  expect(JSON.parse((preflightCall![1] as RequestInit).body as string)).toEqual({ platform_connection_id: 'conn' })
  expect(wrapper.text()).toContain('缺少密码')
  const submit = wrapper.findAll('button').find((b) => b.text().includes('提交上号'))
  expect(submit?.attributes('disabled')).toBeDefined()
})

test('a passing preflight enables submit, which posts the expected version with an idempotency key', async () => {
  fetchMock.mockImplementation((url: string) => {
    if (url.endsWith('/preflight')) return json(200, { data: { can_submit: true, checks: [] } })
    if (url.endsWith('/submit')) return json(202, { data: { job_id: 'job', state: 'queued' } })
    return json(200, { data: {} })
  })
  const wrapper = mount(AccountDeliveryDialog, { props: { modelValue: true, account, connections }, global: { stubs } })
  await flushPromises()
  expect(wrapper.text()).toContain('检查通过')
  const submit = wrapper.findAll('button').find((b) => b.text().includes('提交上号'))!
  expect(submit.attributes('disabled')).toBeUndefined()
  await submit.trigger('click')
  await flushPromises()
  const submitCall = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/submit'))!
  const init = submitCall[1] as RequestInit
  expect(JSON.parse(init.body as string)).toEqual({ platform_connection_id: 'conn', expected_version: 3 })
  expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/[0-9a-f-]{36}/)
  expect(wrapper.emitted('changed')).toBeTruthy()
})

test('an account the platform never accepted can be deleted after confirmation', async () => {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    if (url.endsWith('/preflight')) return json(200, { data: { can_submit: false, checks: [] } })
    if (init?.method === 'DELETE') return json(200, { data: { id: 'acc-1', deleted: true } })
    return json(200, { data: {} })
  })
  vi.mocked(ElMessageBox.prompt).mockResolvedValueOnce({ value: 'user@example.test', action: 'confirm' } as never)
  const failed = { ...account, onboarding_state: 'failed' } as unknown as Account
  const wrapper = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: failed, connections },
    global: { stubs },
  })
  await flushPromises()
  const remove = wrapper.findAll('button').find((b) => b.text().includes('删除账号'))!
  expect(remove).toBeTruthy()
  await remove.trigger('click')
  await flushPromises()
  expect(ElMessageBox.prompt).toHaveBeenCalled()
  // the prompt only accepts the exact account name, so a slip cannot confirm
  const options = vi.mocked(ElMessageBox.prompt).mock.calls[0]![2] as {
    inputValidator: (v: string) => boolean | string
  }
  expect(options.inputValidator('user@example.test')).toBe(true)
  expect(options.inputValidator('USER@example.test')).toBe(true)
  expect(options.inputValidator('other@example.test')).not.toBe(true)
  const deleteCall = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')!
  expect(String(deleteCall[0])).toContain('/accounts/acc-1')
  expect(wrapper.emitted('deleted')).toEqual([['acc-1']])
})

test('a declined confirmation deletes nothing, and delivered accounts offer no delete button', async () => {
  fetchMock.mockImplementation(() => json(200, { data: { can_submit: false, checks: [] } }))
  vi.mocked(ElMessageBox.prompt).mockRejectedValueOnce('cancel')
  const wrapper = mount(AccountDeliveryDialog, { props: { modelValue: true, account, connections }, global: { stubs } })
  await flushPromises()
  await wrapper
    .findAll('button')
    .find((b) => b.text().includes('删除账号'))!
    .trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')).toBe(false)
  expect(wrapper.emitted('deleted')).toBeUndefined()
  const linked = { ...account, onboarding_state: 'linked' } as unknown as Account
  const linkedWrapper = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  expect(linkedWrapper.findAll('button').some((b) => b.text().includes('删除账号'))).toBe(false)
})
