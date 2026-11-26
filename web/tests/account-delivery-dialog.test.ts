import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AccountDeliveryDialog from '../src/components/AccountDeliveryDialog.vue'
import { session } from '../src/api'
import { useSession } from '../src/composables/useSession'
import type { Account, PlatformConnection } from '../src/types/api'
import { ElMessageBox } from 'element-plus'

vi.mock('element-plus', () => ({ ElMessageBox: { prompt: vi.fn() } }))

const sessionUser = {
  id: 'u',
  username: 'admin',
  role: 'admin',
  permissions: ['accounts.onboard', 'connections.create', 'accounts.delete'],
}
const sessionPayload = {
  data: {
    authenticated: true,
    csrf_token: 'csrf',
    user: sessionUser,
  },
}

const fetchMock = vi.fn()
beforeEach(async () => {
  vi.stubGlobal('fetch', fetchMock)
  session.value = { authenticated: true, csrf_token: 'csrf' }
  // establish the user who owns the account so permission-gated buttons render
  fetchMock.mockImplementationOnce(() => json(200, sessionPayload))
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
  created_by_user_id: 'u',
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
    if (String(url).endsWith('/auth/session')) return json(200, sessionPayload)
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

test('an uncertain outcome offers delete with a platform-check warning', async () => {
  fetchMock.mockImplementation((_url: string, init?: RequestInit) => {
    if (init?.method === 'DELETE') return json(200, { data: { id: 'acc-1', deleted: true } })
    return json(200, { data: {} })
  })
  vi.mocked(ElMessageBox.prompt).mockResolvedValueOnce({ value: 'user@example.test', action: 'confirm' } as never)
  const unknown = { ...account, onboarding_state: 'unknown' } as unknown as Account
  const wrapper = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: unknown, connections },
    global: { stubs },
  })
  await flushPromises()
  const remove = wrapper.findAll('button').find((b) => b.text().includes('删除账号'))!
  expect(remove).toBeTruthy()
  await remove.trigger('click')
  await flushPromises()
  expect(String(vi.mocked(ElMessageBox.prompt).mock.calls[0]![0])).toContain('请先确认平台没有该账号')
  expect(wrapper.emitted('deleted')).toEqual([['acc-1']])
})

test('unknown outcome with LOCAL_LINK_SAVE_FAILED offers no delete button', async () => {
  const unknown = {
    ...account,
    onboarding_state: 'unknown',
    submission_job: {
      id: 'job-1',
      connection_id: 'conn',
      state: 'unknown',
      code: 'LOCAL_LINK_SAVE_FAILED',
      updated_at: '2026-11-16T00:00:00Z',
    },
  } as unknown as Account
  const wrapper = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: unknown, connections },
    global: { stubs },
  })
  await flushPromises()
  expect(wrapper.findAll('button').some((b) => b.text().includes('删除账号'))).toBe(false)
})

async function asUser(permissions: string[]) {
  fetchMock.mockImplementationOnce(() =>
    json(200, {
      data: {
        authenticated: true,
        csrf_token: 'csrf',
        user: { ...sessionUser, permissions },
      },
    }),
  )
  await useSession().check()
  fetchMock.mockReset()
}

const linked = {
  id: 'acc-9',
  account: 'live@example.test',
  plan: 'claude/cc-max-20x',
  plan_end_time: '2026-12-31',
  version: 2,
  archived: false,
  platform_account_id: '30566',
  platform_alias: 'claude-20261120003',
  platform_status: 'ban',
  platform_observed_at: '2026-11-21T10:14:00Z',
  proxy_host: '203.0.113.9',
  credentials_configured: true,
  tags: ['coding'],
  remark: '测试备注',
  platform_links: [
    { connection_id: 'conn', connection_name: '平台 A', upstream_id: 'u1', ingestion_state: 'accepted' },
  ],
  platform: {
    status: 'ban',
    reason_text:
      'Your account is on hold and can not use Claude Code. View details or appeal: https://claude.ai/restricted',
    reason_fetched_at: '2026-11-21T10:14:46Z',
    status_changed_at: '2026-11-21T09:00:00Z',
    missing_since: '',
    web_channel: { configured: true, suspended: false },
    usd_cny_rate: 7.2,
    profile: {
      plan: 'claude/cc-max-20x',
      plan_start_time: '2026-11-20',
      plan_end_time: '2026-12-19',
      register_time: '2026-11-16',
      register_location: 'US',
      balance: '100',
      payment_method: 'apple_pay',
      register_device: 'Mac',
      recharge_time: '',
      warranty_period: '1',
      tags: ['coding'],
    },
  },
  onboarding_state: 'linked',
  created_by_user_id: 'u',
} as unknown as Account

const history = {
  data: {
    list: [
      {
        observed_at: '2026-11-19T20:00:00Z',
        // API already converted: 100 fen ÷ 7.2 → 14¢
        cost_usd_cents: 14,
        cost_usd: '0.14',
        five_hour_used_percent: 10,
        week_used_percent: 2,
        input_tokens: 1,
        output_tokens: 1,
        five_hour_reset_at: '',
        week_reset_at: '',
      },
      {
        observed_at: '2026-11-20T20:00:00Z',
        // 900 fen ÷ 7.2 → 125¢; day spend vs previous = 111¢ → $1.11
        cost_usd_cents: 125,
        cost_usd: '1.25',
        five_hour_used_percent: 40,
        week_used_percent: 5,
        input_tokens: 2,
        output_tokens: 2,
        five_hour_reset_at: '',
        week_reset_at: '',
      },
    ],
  },
}

function routes(extra: Record<string, unknown> = {}) {
  fetchMock.mockImplementation((url: string) => {
    if (url.includes('/platform-usage-history')) return json(200, history)
    if (url.includes('/audit-events'))
      return json(200, {
        data: [
          {
            id: 7,
            action: 'account.assign',
            resource_type: 'account',
            resource_id: 'acc-9',
            actor: 'admin',
            detail: '',
            created_at: '2026-11-20T10:00:00Z',
          },
          {
            id: 8,
            action: 'account.assign',
            resource_type: 'account',
            resource_id: 'acc-90',
            actor: 'x',
            detail: 'other',
            created_at: '2026-11-20T11:00:00Z',
          },
        ],
      })
    if (url.includes('/jobs'))
      return json(200, {
        data: [
          extra.job ?? {
            id: 'j',
            state: 'succeeded',
            account_id: 'acc-9',
            code: '',
            message: '',
            created_at: '2026-11-19T10:00:00Z',
            updated_at: '2026-11-19T10:01:00Z',
          },
        ],
      })
    throw new Error('unexpected ' + url)
  })
}

test('a linked account detail shows the full reason, the whole profile and a usage trend', async () => {
  await asUser(['accounts.usage', 'accounts.platform_edit'])
  routes()
  const w = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  // The reason is shown in full, not cut to a card line.
  expect(w.get('[data-reason]').text()).toContain('https://claude.ai/restricted')
  expect(w.get('[data-copy-reason]').text()).toContain('复制错误信息')
  expect(w.get('[data-profile]').text()).toContain('套餐CC Max 20x')
  const profile = w.get('[data-profile]').text()
  for (const expected of [
    '2026-11-20 → 2026-12-19',
    '2026-11-16',
    '美国',
    'ApplePay',
    'Mac',
    '203.0.113.9',
    '测试备注',
    '已保存',
  ])
    expect(profile, expected).toContain(expected)
  // The plan end differs from the local record: flagged, with the local value on hover.
  expect(w.get('[data-profile] .profile-diff').attributes('title')).toContain('2026-12-31')
  expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/accounts/acc-9/platform-usage-history?days=7'))).toBe(
    true,
  )
  expect(w.find('.trend-lines').exists()).toBe(true)
  // Day spend is last-of-day cents delta: 125 - 14 = $1.11.
  expect(w.get('.spend-bar').attributes('title')).toContain('$1.11')
  await w.get('[data-days="30"]').trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls.some(([u]) => String(u).includes('days=30'))).toBe(true)
  w.unmount()
})

test('activity is requested only for sources the user may see, and only exact account matches are listed', async () => {
  await asUser(['audit.view'])
  routes()
  const onlyAudit = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  const urls = fetchMock.mock.calls.map(([u]) => String(u))
  expect(urls.some((u) => u.includes('/audit-events?resource_type=account&resource_id=acc-9'))).toBe(true)
  expect(urls.some((u) => u.includes('/jobs'))).toBe(false)
  expect(urls.some((u) => u.includes('platform-usage-history'))).toBe(false)
  const text = onlyAudit.get('.account-activity').text()
  expect(text).toContain('分配给用户')
  expect(text).not.toContain('other') // acc-90 also matches the "contains" filter
  onlyAudit.unmount()

  await asUser([])
  routes()
  const none = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  expect(none.find('.account-activity').exists()).toBe(false)
  expect(fetchMock).not.toHaveBeenCalled()
  none.unmount()
})

test('the detail offers the platform edit when allowed and copies the account and platform id', async () => {
  await asUser(['accounts.platform_edit'])
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const w = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  await w.get('[data-edit-platform]').trigger('click')
  expect(w.emitted('edit-platform')?.[0]?.[0]).toMatchObject({ id: 'acc-9' })
  await w.get('[data-copy-account]').trigger('click')
  await w.get('[data-copy-platform-id]').trigger('click')
  await flushPromises()
  expect(writeText.mock.calls.map((c) => c[0])).toEqual(['live@example.test', '30566'])
  expect(w.get('[role="status"]').text()).toContain('已复制平台 ID')
  w.unmount()

  await asUser([])
  const denied = mount(AccountDeliveryDialog, {
    props: { modelValue: true, account: linked, connections },
    global: { stubs },
  })
  await flushPromises()
  expect(denied.find('[data-edit-platform]').exists()).toBe(false)
  denied.unmount()
})
