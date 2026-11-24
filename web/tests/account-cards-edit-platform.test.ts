import { afterEach, expect, test, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { Account, PlatformInfo } from '../src/types/api'

// The signed-in user is "me", not an admin: only accounts they own are editable.
vi.mock('../src/composables/useSession', () => ({
  useSession: () => ({
    can: (p: string) =>
      p === 'accounts.platform_edit' ||
      p === 'accounts.view' ||
      p === 'accounts.credentials' ||
      p === 'accounts.onboard',
    canOnboard: () => true,
    owns: (createdBy?: string | null) => createdBy === 'me',
    user: { value: { id: 'me', username: 'me', is_admin: false } },
  }),
}))

import AccountCards from '../src/components/AccountCards.vue'

const wrappers: VueWrapper[] = []
afterEach(() => wrappers.splice(0).forEach((w) => w.unmount()))

const stubs = {
  'el-button': { template: '<button @click="$emit(\'click\')"><slot /></button>' },
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
    alias: 'claude-x',
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
    usage: null,
    ...overrides,
  }
}

function account(info: PlatformInfo | null, owner = 'me'): Account {
  return {
    id: 'acc-1',
    account: 'jo@example.test',
    plan: 'claude/cc-max-20x',
    register_location: 'US',
    register_time: '2026-11-03',
    plan_start_time: '2026-11-11',
    plan_end_time: '2026-12-10',
    proxy_id: '',
    version: 1,
    archived: false,
    created_at: '',
    updated_at: '',
    platform_account_id: '20204',
    platform_alias: 'claude-x',
    platform_status: info?.status || '',
    platform_observed_at: '',
    platform_links: [{ connection_id: 'conn', connection_name: 'tx', upstream_id: '428802' } as never],
    platform: info,
    onboarding_state: 'linked',
    created_by_user_id: owner,
  } as Account
}

function render(row: Account) {
  const w = mount(AccountCards, { props: { accounts: [row], loading: false }, global: { stubs } })
  wrappers.push(w)
  return w
}

const editButton = (w: VueWrapper) => w.findAll('button').find((b) => b.text().includes('修改平台资料'))
const reauthButton = (w: VueWrapper) => w.findAll('button').find((b) => b.text().includes('重新授权'))

test('a linked account with a configured web channel offers the platform edit', async () => {
  const row = account(platform())
  const w = render(row)
  const btn = editButton(w)
  expect(btn).toBeTruthy()
  await btn!.trigger('click')
  expect(w.emitted('edit-platform')?.[0]?.[0]).toMatchObject({ id: 'acc-1' })
})

test('a bound proxy host appears on the card', () => {
  const w = render({ ...account(platform()), proxy_id: 'px-1', proxy_host: '203.0.113.44' })
  expect(w.text()).toContain('代理 203.0.113.44')
})

test('a linked login_failed account offers reauthorize', async () => {
  const w = render(
    account(platform({ status: 'login_failed', status_raw: 'login_failed', reason_text: 'token expired' })),
  )
  expect(w.text()).toContain('登录失败')
  expect(w.text()).toContain('重新授权后同步到平台')
  const btn = reauthButton(w)
  expect(btn).toBeTruthy()
  await btn!.trigger('click')
  expect(w.emitted('reauthorize')?.[0]?.[0]).toMatchObject({ id: 'acc-1' })
  expect(reauthButton(render(account(platform())))).toBeFalsy()
})

test('a linked sync_failed account offers reauthorize', async () => {
  const w = render(
    account(platform({ status: 'sync_failed', status_raw: 'sync_failed', reason_text: 'cookie expired' })),
  )
  expect(w.text()).toContain('同步失败')
  expect(w.text()).toContain('平台同步失败，可重新授权后同步到平台')
  const btn = reauthButton(w)
  expect(btn).toBeTruthy()
  await btn!.trigger('click')
  expect(w.emitted('reauthorize')?.[0]?.[0]).toMatchObject({ id: 'acc-1' })
})

test('the edit is hidden when the platform removed the account or the channel is unusable', () => {
  expect(editButton(render(account(platform({ missing_since: '2026-11-12T00:00:00Z' }))))).toBeFalsy()
  expect(editButton(render(account(platform({ web_channel: { configured: false, suspended: false } }))))).toBeFalsy()
  expect(editButton(render(account(platform({ web_channel: { configured: true, suspended: true } }))))).toBeFalsy()
  expect(editButton(render(account(null)))).toBeFalsy()
})

test('the edit is hidden on an account that belongs to someone else', () => {
  expect(editButton(render(account(platform(), 'someone-else')))).toBeFalsy()
  expect(editButton(render(account(platform(), '')))).toBeFalsy()
})
