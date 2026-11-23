import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { AttentionResponse, Dashboard, DashboardInsights } from '../src/types/api'

const push = vi.fn()
vi.mock('vue-router', () => ({ useRouter: () => ({ push }), useRoute: () => ({ meta: {}, query: {} }) }))
const apiMock = vi.fn()
vi.mock('../src/api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  errorMessage: (e: unknown) => String(e),
  session: { value: { authenticated: true } },
}))

import DashboardView from '../src/views/DashboardView.vue'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  apiMock.mockReset()
  push.mockReset()
})

const stubs = {
  PageHead: { template: '<div><slot /></div>' },
  'el-button': { template: '<button @click="$emit(\'click\')"><slot /></button>' },
  'el-tag': { template: '<span><slot /></span>' },
  'el-alert': { template: '<div><slot /></div>' },
}

function dashboard(overrides: Partial<Dashboard['platform']> = {}, accounts = 8): Dashboard {
  return {
    accounts: {
      total: accounts,
      ready: 1,
      archived: 0,
      platform_linked: 8,
      observed_recently: 0,
      by_plan: { 'claude/cc-max-20x': 8 },
    },
    jobs: { running: 0, queued: 0, succeeded: 1, failed: 0 },
    proxies: { healthy: 2, enabled: 2, disabled: 0 },
    platform: {
      today_requests: 0,
      today_cost_fen: 0,
      last_sync_at: '2026-11-12T03:40:21Z',
      last_sync_state: 'partial',
      sync_status: 'failing',
      error_code: 'PLATFORM_QUERY_DENIED',
      consecutive_failures: 16,
      web_sync_health: {
        today_requests: 0,
        today_cost_fen: 0,
        last_sync_at: '2026-11-12T04:07:27Z',
        last_sync_state: 'succeeded',
        sync_status: 'ok',
      },
      remote_counts_by_status: { using: 6, ban: 2 },
      near_limit_accounts: 2,
      today_model_cost_fen: 1488278,
      week_model_cost_fen: 4950682,
      total_model_cost_fen: 720000,
      cost_by_day: [
        { date: '2026-11-06', cost_fen: 100000, final: true },
        { date: '2026-11-07', cost_fen: 200000, final: true },
        { date: '2026-11-08', cost_fen: 300000, final: true },
        { date: '2026-11-09', cost_fen: 400000, final: true },
        { date: '2026-11-10', cost_fen: 500000, final: true },
        { date: '2026-11-11', cost_fen: 1962404, final: true },
        { date: '2026-11-12', cost_fen: 1488278, final: false },
      ],
      cost_by_model: [
        { model_name: 'claude-opus-5-5', cost_fen: 2825510 },
        { model_name: 'claude-fable-5-2', cost_fen: 1278841 },
      ],
      cost_by_account: [{ account_id: 'a1', account: 'cj@example.test', cost_fen: 1146404 }],
      ...overrides,
    },
  }
}

const attention: AttentionResponse = {
  items: [
    {
      kind: 'near_limit',
      severity: 'warn',
      account_id: 'a2',
      account: 'ca@example.test',
      alias: 'claude-ca',
      label: '5 小时窗口 88%',
      detail: '重置 00:50',
      upstream_id: '428242',
      reset_at: '2026-11-11T16:50:00Z',
      expires_at: '',
      value: 88,
    },
    {
      kind: 'banned',
      severity: 'danger',
      account_id: 'a3',
      account: 'gr@example.test',
      alias: 'claude-gr',
      label: 'ban',
      detail: '账号被暂停（封号）',
      upstream_id: '385753',
      reset_at: '',
      expires_at: '',
      value: 0,
    },
  ],
  counts: { near_limit: 2, banned: 2, expiring: 0, missing: 0, profile_diff: 0 },
}

const hours = Array.from({ length: 24 }, (_, i) => ({
  hour: new Date(Date.UTC(2026, 10, 12, 5 + i)).toISOString().replace('.000Z', 'Z'),
  cost_fen: i === 20 ? 12000 : i === 23 ? 6000 : 0,
}))
const insights: DashboardInsights = {
  usd_cny_rate: 7.2,
  fleet: {
    accounts: 8,
    cost_fen_total: 720000,
    input_tokens: 487159683,
    output_tokens: 124015127,
    avg_five_hour_percent: 24.1,
    avg_week_percent: 31.5,
  },
  quota: [
    {
      account_id: 'a2',
      account: 'ca@example.test',
      alias: 'claude-ca',
      status: 'using',
      five_hour_percent: 88,
      five_hour_reset_at: '2026-11-13T06:50:00Z',
      week_percent: 35,
      week_reset_at: '2026-11-18T05:00:00Z',
      cost_fen: 258624,
    },
    {
      account_id: 'a1',
      account: 'cj@example.test',
      alias: 'claude-cj',
      status: 'using',
      five_hour_percent: 12,
      five_hour_reset_at: '',
      week_percent: 71,
      week_reset_at: '',
      cost_fen: 720997,
    },
    {
      account_id: 'a3',
      account: 'gr@example.test',
      alias: 'claude-gr',
      status: 'ban',
      five_hour_percent: null,
      five_hour_reset_at: '',
      week_percent: null,
      week_reset_at: '',
      cost_fen: null,
    },
  ],
  hourly: hours,
  composition: {
    by_delivery: { linked: 8, pending: 1, submitting: 0, failed: 1, unknown: 0 },
    expiry: { expired: 0, within_7: 1, within_30: 5, later: 2, unknown: 0 },
  },
  recent_jobs: [
    {
      id: 'j2',
      account_id: 'a9',
      account: 'ka@example.test',
      connection: 'TX CCMAX',
      state: 'failed',
      code: 'PLATFORM_AUTH_FAILED',
      updated_at: '2026-11-12T14:01:07Z',
    },
    {
      id: 'j1',
      account_id: 'a1',
      account: 'cj@example.test',
      connection: 'TX CCMAX',
      state: 'succeeded',
      code: 'ACCEPTED_PENDING_VERIFICATION',
      updated_at: '2026-11-12T09:05:27Z',
    },
  ],
}

async function render(dash: Dashboard, att: AttentionResponse = attention, ins: DashboardInsights | Error = insights) {
  apiMock.mockImplementation(async (path: string) => {
    if (path === '/dashboard') return { data: dash }
    if (path === '/dashboard/attention') return { data: att }
    if (path === '/dashboard/insights') {
      if (ins instanceof Error) throw ins
      return { data: ins }
    }
    throw new Error('unexpected ' + path)
  })
  const w = mount(DashboardView, { global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  return w
}

test('the top row shows fleet counts and spend', async () => {
  const text = (await render(dashboard())).text()
  expect(text).toContain('可用容量')
  expect(text).toContain('6 / 8 在用')
  expect(text).toContain('5h 24% · 7d 32%')
  expect(text).toContain('$2,067.05')
  expect(text).toContain('$6,875.95')
  expect(text).not.toContain('¥14,882.78')
  expect(text).not.toContain('汇率')
  expect(text).toContain('未出齐')
})

test('the attention list renders items and navigates to the account', async () => {
  const [nearLimit, banned] = attention.items
  const w = await render(dashboard(), {
    ...attention,
    items: [{ ...nearLimit!, alias: 'claude-住享科智-20261112003' }, banned!],
  })
  const text = w.text()
  expect(text).toContain('5 小时窗口 88%')
  expect(text).toContain('重置 00:50')
  expect(text).toContain('已封禁')
  expect(text).toContain('账号被暂停（封号）')
  expect(text).toContain('claude-20261112003')
  expect(text).not.toContain('住享科智')
  const row = w.find('[data-attention="428242"]')
  expect(row.exists()).toBe(true)
  await row.trigger('click')
  expect(push).toHaveBeenCalledWith({ name: 'accounts', query: { q: 'ca@example.test' } })
  const near = w.find('[data-stat="near_limit"]')
  await near.trigger('click')
  expect(push).toHaveBeenCalledWith({ name: 'accounts', query: { five_hour_min: '80' } })
})

test('a denied OpenAPI sync is shown as not enabled, not as an alarm', async () => {
  const text = (await render(dashboard())).text()
  expect(text).toContain('平台未开通')
  expect(text).not.toContain('连续失败 16 次')
  expect(text).not.toContain('请求数')
  expect(text).not.toContain('账号完成度')
})

test('without the web channel the top row falls back to local counts with a hint', async () => {
  const dash = dashboard(
    {
      remote_counts_by_status: {},
      near_limit_accounts: 0,
      today_model_cost_fen: 0,
      week_model_cost_fen: 0,
      total_model_cost_fen: 0,
      cost_by_day: [],
      cost_by_model: [],
      cost_by_account: [],
    },
    3,
  )
  const text = (
    await render(dash, { items: [], counts: { near_limit: 0, banned: 0, expiring: 0, missing: 0, profile_diff: 0 } })
  ).text()
  expect(text).toContain('配置网页登录后显示平台状态')
  expect(text).toContain('暂无需要处理的账号')
  expect(text).toContain('暂无账单数据')
})

test('amounts are shown only in dollars, with the daily average and the total spend', async () => {
  const w = await render(dashboard())
  const text = w.text()
  expect(text).toContain('$2,067.05') // ¥14,882.78 ÷ 7.2
  expect(text).toContain('$6,875.95') // ¥49,506.82 ÷ 7.2
  expect(text).toContain('日均')
  expect(w.get('.stat-total').text()).toContain('总消费')
  expect(w.get('.stat-total').text()).toContain('$1,000.00')
  expect(text).not.toContain('¥')
})

test('the action and expiry stats count what needs a hand and when plans run out', async () => {
  const w = await render(dashboard(), attention, {
    ...insights,
    composition: { ...insights.composition, next_expiry: '2026-11-18' },
  })
  const action = w.get('[data-stat="action"]').text()
  expect(action).toContain('需要动手')
  expect(action).toMatch(/封禁 \/ 失败 \d+/)
  expect(action).toMatch(/接近限额 \d+/)
  const expiry = w.get('[data-stat="expiry"]').text()
  expect(expiry).toContain('6 个 30 天内到期')
  expect(expiry).toContain('7 天内 1')
  expect(expiry).toContain('最早 11-18')
})

test('idle accounts are listed with days left and open the account', async () => {
  const idle = [
    {
      account_id: 'i1',
      account: 'idle@example.test',
      alias: 'claude-idle',
      week_percent: 2,
      cost_fen: 100,
      plan_end: '2026-12-18',
      days_left: 27,
    },
    {
      account_id: 'i2',
      account: 'unknown@example.test',
      alias: '',
      week_percent: 0,
      cost_fen: null,
      plan_end: '',
      days_left: null,
    },
  ]
  const w = await render(dashboard(), attention, { ...insights, idle })
  const rows = w.findAll('[data-idle]')
  expect(rows.map((r) => r.attributes('data-idle'))).toEqual(['i1', 'i2'])
  expect(rows[0]!.text()).toContain('7d 2%')
  expect(rows[0]!.text()).toContain('剩 27 天')
  expect(rows[1]!.text()).toContain('到期未知')
  await rows[0]!.trigger('click')
  expect(push).toHaveBeenCalledWith({ name: 'accounts', query: { q: 'idle@example.test' } })
})

test('without idle accounts the panel says so', async () => {
  const text = (await render(dashboard())).text()
  expect(text).toContain('没有闲置账号')
})

test('the quota board lists every account with both windows and opens the account', async () => {
  const w = await render(dashboard())
  const rows = w.findAll('[data-quota]')
  expect(rows.map((r) => r.attributes('data-quota'))).toEqual(['a2', 'a1', 'a3'])
  expect(rows[0]!.text()).toContain('ca@example.test')
  expect(rows[0]!.text()).toContain('88%')
  expect(rows[0]!.text()).toContain('35%')
  expect(rows[2]!.text()).toContain('已封禁')
  await rows[1]!.trigger('click')
  expect(push).toHaveBeenCalledWith({ name: 'accounts', query: { q: 'cj@example.test' } })
})

test('composition, hourly spend and recent onboarding are shown', async () => {
  const text = (await render(dashboard())).text()
  for (const expected of [
    '账号构成',
    '平台状态',
    '上号状态',
    '到期',
    '7 天内',
    '近 24 小时',
    '最近上号',
    'ka@example.test',
    '平台拒绝了本次请求的身份验证',
    '平台已接收',
    '系统状态',
  ])
    expect(text, expected).toContain(expected)
  expect(text).not.toContain('汇率')
})

test('a failing insights request leaves headline placeholders and says what is missing', async () => {
  const text = (await render(dashboard(), attention, new Error('boom'))).text()
  expect(text).toContain('今日消费—')
  expect(text).toContain('5 小时窗口 88%')
  expect(text).toContain('配额数据暂时读取失败')
  expect(text).not.toContain('¥')
})

test('recent onboarding is hidden from users who cannot see the task centre', async () => {
  const w = await render(dashboard(), attention, { ...insights, recent_jobs: null })
  expect(w.text()).not.toContain('最近上号')
})
