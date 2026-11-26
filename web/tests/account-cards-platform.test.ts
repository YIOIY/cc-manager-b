import { afterEach, expect, test, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AccountCards from '../src/components/AccountCards.vue'
import { session } from '../src/api'
import type { Account, PlatformInfo } from '../src/types/api'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  vi.unstubAllGlobals()
})

const stubs = {
  'el-button': { template: '<button><slot /></button>' },
  'el-tooltip': { template: '<span><slot /></span>' },
}

function platform(overrides: Partial<PlatformInfo> = {}): PlatformInfo {
  return {
    status: 'using',
    status_raw: 'using',
    locked: false,
    reason_kind: '',
    reason_text: '',
    reason_fetched_at: '',
    status_changed_at: '',
    observed_at: '2026-11-11T13:20:00Z',
    aid: '20204',
    upstream_id: '428802',
    alias: 'claude-益充汇-20261111002',
    provider: 'claude',
    model: 'claude/cc-max-20x',
    models_usable: true,
    remote_created_at: '2026-11-11 19:36',
    missing_since: '',
    auto_created: true,
    source_channel: 'web',
    remark: '',
    web_channel: { configured: true, suspended: false },
    usd_cny_rate: 7.2,
    profile: {
      plan: 'claude/cc-max-20x',
      plan_start_time: '2026-11-11',
      plan_end_time: '2026-12-10',
      register_time: '2026-11-03',
      register_location: 'US',
      balance: '100',
      payment_method: 'google_pay',
      register_device: 'Mac',
      recharge_time: '',
      warranty_period: '',
      tags: [],
    },
    usage: {
      cost_usd: '4.89',
      cost_usd_cents: 489,
      input_tokens: 411638,
      output_tokens: 82648,
      five_hour_used_percent: 7,
      five_hour_reset_at: '2026-11-11T16:50:00Z',
      week_used_percent: 1,
      week_reset_at: '2026-11-18T05:00:00Z',
      observed_at: '2026-11-11T13:20:00Z',
    },
    ...overrides,
  }
}

function account(info: PlatformInfo | null): Account {
  return {
    id: 'acc-1',
    account: 'jonasuwile@example.test',
    plan: 'claude/cc-max-20x',
    register_location: 'US',
    register_time: '2026-11-03',
    plan_start_time: '2026-11-11',
    plan_end_time: '2026-12-31',
    proxy_id: '',
    version: 1,
    archived: false,
    created_at: '2026-11-11T00:00:00Z',
    updated_at: '2026-11-11T00:00:00Z',
    platform_account_id: '20204',
    platform_alias: 'claude-益充汇-20261111002',
    platform_status: info?.status || '',
    platform_observed_at: '2026-11-11T13:20:00Z',
    platform_links: [{ connection_id: 'conn', connection_name: 'tx', upstream_id: '428802' } as never],
    platform: info,
    onboarding_state: 'linked',
  }
}

function render(row: Account) {
  session.value = { authenticated: true, csrf_token: 'csrf', user: { permissions: ['accounts.view'] } } as never
  const w = mount(AccountCards, { props: { accounts: [row], loading: false }, global: { stubs } })
  wrappers.push(w)
  return w
}

test('a linked account mirrors the platform card and its usage block', () => {
  const text = render(account(platform())).text()
  for (const expected of [
    '7%',
    '入 412K',
    '出 83K',
    '$4.89',
    '美国',
    'GooglePay',
    '20204',
    '使用中',
    '套餐 11-11 → 12-10',
    'claude-20261111002',
  ]) {
    expect(text).toContain(expected)
  }
  expect(text).not.toContain('益充汇')
  expect(text).not.toContain('已关联')
  expect(text).not.toContain('汇率')
  expect(text).not.toContain('¥35.21')
  expect(text).toContain('≠本地') // plan_end_time differs from the local 2026-12-31
})

test('the full profile and platform origin stay available on hover', () => {
  const w = render(account(platform()))
  const profile = w.get('.platform-profile').attributes('title')
  expect(profile).toContain('套餐结束时间：2026-12-10（本地 2026-12-31）')
  expect(profile).toContain('注册时间：2026-11-03')
  expect(w.get('.usage-identity strong').attributes('title')).toContain('来自平台同步')
})

test('without usage the block says the platform returned none', () => {
  const text = render(account(platform({ usage: null }))).text()
  expect(text).toContain('平台未返回用量')
  expect(text).not.toContain('5h')
})

test('a banned account shows the reason and a copy control', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const w = render(
    account(
      platform({
        status: 'ban',
        status_raw: 'ban',
        reason_kind: 'claude-status',
        reason_text: '账号被平台封禁：违规使用',
        usage: null,
      }),
    ),
  )
  const text = w.text()
  expect(text).toContain('已封禁')
  expect(text).toContain('账号被平台封禁：违规使用')
  await w.get('.platform-reason-copy').trigger('click')
  expect(writeText).toHaveBeenCalledWith('账号被平台封禁：违规使用')
  expect(w.get('.platform-reason-copy').text()).toContain('已复制')
})

test('an offline account uses the stronger danger badge', () => {
  const w = render(
    account(
      platform({
        status: 'offline',
        status_raw: 'offline',
        reason_text: '',
        usage: null,
      }),
    ),
  )
  expect(w.text()).toContain('已下线')
  expect(w.get('.mini-badge.danger').text()).toContain('已下线')
  expect(w.find('.mini-badge.teal').exists()).toBe(false)
})

test('an account the platform never listed keeps the old layout', () => {
  const text = render(account(null)).text()
  expect(text).not.toContain('账号用量')
  expect(text).not.toContain('平台未返回用量')
})

test('the card no longer carries the request / input / output bars', () => {
  const w = render(account(platform()))
  expect(w.find('.usage-bars').exists()).toBe(false)
  expect(w.find('.bar-row').exists()).toBe(false)
})

test('the card shows only the dollar cost at the connection rate', () => {
  const w = render(account(platform()))
  expect(w.get('[data-usd]').text()).toMatch(/^\$[\d,]+\.\d{2}$/)
  expect(w.find('[data-rate]').exists()).toBe(false)
})

test('the card prefers the owner nickname over the login account', () => {
  const withNick = render({
    ...account(null),
    created_by_user_id: 'u1',
    created_by_nickname: '小李',
    created_by_username: 'ops',
  })
  expect(withNick.text()).toContain('所属 小李')
  expect(withNick.text()).not.toContain('所属 ops')

  const fallback = render({
    ...account(null),
    created_by_user_id: 'u1',
    created_by_nickname: '',
    created_by_username: 'ops',
  })
  expect(fallback.text()).toContain('所属 ops')

  const legacy = render(account(null))
  expect(legacy.text()).not.toContain('所属')
})
