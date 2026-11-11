import { afterEach, expect, test } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AccountCards from '../src/components/AccountCards.vue'
import { session } from '../src/api'
import type { Account, PlatformInfo } from '../src/types/api'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
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
    observed_at: '2026-11-11T13:20:00Z',
    aid: '20204',
    upstream_id: '428802',
    alias: 'claude-住享科智-20261111002',
    provider: 'claude',
    model: 'claude/cc-max-20x',
    models_usable: true,
    remote_created_at: '2026-11-11 19:36',
    missing_since: '',
    auto_created: true,
    source_channel: 'web',
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
      cost_fen: 3521,
      cost_yuan: '35.21',
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
    platform_alias: 'claude-住享科智-20261111002',
    platform_status: info?.status || '',
    platform_observed_at: '2026-11-11T13:20:00Z',
    platform_links: [{ connection_id: 'conn', connection_name: 'tx', upstream_id: '428802' } as never],
    platform: info,
    onboarding_state: 'linked',
  }
}

function render(row: Account) {
  session.value = { authenticated: true, csrf_token: 'csrf', user: { permissions: ['accounts.view'] } } as never
  const w = mount(AccountCards, { props: { accounts: [row], allAccounts: [row], loading: false }, global: { stubs } })
  wrappers.push(w)
  return w
}

test('a linked account mirrors the platform card and its usage block', () => {
  const text = render(account(platform())).text()
  for (const expected of [
    '7.00%',
    '411,638',
    '82,648',
    '¥35.21',
    '美国',
    'GooglePay',
    '20204',
    '使用中',
    '来自平台',
    '2026-12-10',
  ]) {
    expect(text).toContain(expected)
  }
  expect(text).toContain('≠本地') // plan_end_time differs from the local 2026-12-31
})

test('without usage the block says the platform returned none', () => {
  const text = render(account(platform({ usage: null }))).text()
  expect(text).toContain('平台未返回用量')
  expect(text).not.toContain('7.00%')
})

test('a banned account shows the reason', () => {
  const text = render(
    account(
      platform({
        status: 'ban',
        status_raw: 'ban',
        reason_kind: 'claude-status',
        reason_text: '账号被平台封禁：违规使用',
        usage: null,
      }),
    ),
  ).text()
  expect(text).toContain('已封禁')
  expect(text).toContain('账号被平台封禁：违规使用')
})

test('an account the platform never listed keeps the old layout', () => {
  const text = render(account(null)).text()
  expect(text).not.toContain('账号用量')
  expect(text).not.toContain('平台未返回用量')
})
