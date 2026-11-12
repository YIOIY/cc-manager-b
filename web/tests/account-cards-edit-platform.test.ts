import { afterEach, expect, test, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { Account, PlatformInfo } from '../src/types/api'

vi.mock('../src/composables/useSession', () => ({
  useSession: () => ({ can: (p: string) => p === 'accounts.platform_edit' || p === 'accounts.view' }),
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

function account(info: PlatformInfo | null): Account {
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
  } as Account
}

function render(row: Account) {
  const w = mount(AccountCards, { props: { accounts: [row], allAccounts: [row], loading: false }, global: { stubs } })
  wrappers.push(w)
  return w
}

const editButton = (w: VueWrapper) => w.findAll('button').find((b) => b.text().includes('修改平台资料'))

test('a linked account with a configured web channel offers the platform edit', async () => {
  const row = account(platform())
  const w = render(row)
  const btn = editButton(w)
  expect(btn).toBeTruthy()
  await btn!.trigger('click')
  expect(w.emitted('edit-platform')?.[0]?.[0]).toMatchObject({ id: 'acc-1' })
})

test('the edit is hidden when the platform removed the account or the channel is unusable', () => {
  expect(editButton(render(account(platform({ missing_since: '2026-11-12T00:00:00Z' }))))).toBeFalsy()
  expect(editButton(render(account(platform({ web_channel: { configured: false, suspended: false } }))))).toBeFalsy()
  expect(editButton(render(account(platform({ web_channel: { configured: true, suspended: true } }))))).toBeFalsy()
  expect(editButton(render(account(null)))).toBeFalsy()
})
