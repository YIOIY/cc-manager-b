import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { AttentionResponse, Dashboard } from '../src/types/api'

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

async function render(dash: Dashboard, att: AttentionResponse = attention) {
  apiMock.mockImplementation(async (path: string) => {
    if (path === '/dashboard') return { data: dash }
    if (path === '/dashboard/attention') return { data: att }
    throw new Error('unexpected ' + path)
  })
  const w = mount(DashboardView, { global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  return w
}

test('the top row shows fleet counts and spend', async () => {
  const text = (await render(dashboard())).text()
  expect(text).toContain('平台账号')
  expect(text).toContain('8')
  expect(text).toContain('使用中 6')
  expect(text).toContain('已封禁 2')
  expect(text).toContain('¥14,882.78')
  expect(text).toContain('¥49,506.82')
  expect(text).toContain('未出齐')
})

test('the attention list renders items and navigates to the account', async () => {
  const w = await render(dashboard())
  const text = w.text()
  expect(text).toContain('5 小时窗口 88%')
  expect(text).toContain('重置 00:50')
  expect(text).toContain('已封禁')
  expect(text).toContain('账号被暂停（封号）')
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
