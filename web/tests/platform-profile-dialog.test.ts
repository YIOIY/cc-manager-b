import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import PlatformProfileDialog from '../src/components/PlatformProfileDialog.vue'
import { session } from '../src/api'
import type { Account, PlatformInfo } from '../src/types/api'

const fetchMock = vi.fn()
const wrappers: VueWrapper[] = []
beforeEach(() => {
  session.value = { authenticated: true, csrf_token: 'csrf' } as never
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

const inputStub = {
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
  props: ['modelValue'],
  emits: ['update:modelValue'],
}
const stubs = {
  'el-dialog': { template: '<div class="dialog"><slot /><slot name="footer" /></div>' },
  'el-form': { template: '<form @submit.prevent><slot /></form>' },
  'el-form-item': { template: '<div :data-label="label"><slot /></div>', props: ['label'] },
  'el-input': inputStub,
  // Element Plus date pickers emit undefined when cleared.
  'el-date-picker': {
    template:
      '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value === \'\' ? undefined : $event.target.value)" />',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-input-number': {
    template:
      '<input class="number" :value="modelValue" @input="$emit(\'update:modelValue\', Number($event.target.value))" />',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-select': {
    template:
      '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><slot /></select>',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-option': { template: '<option :value="value">{{ label }}</option>', props: ['value', 'label'] },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  ErrorNotice: { template: '<div class="notice">{{ String(error?.message || "") }}</div>', props: ['error'] },
}

function platform(): PlatformInfo {
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
    usage: null,
  }
}

function account(): Account {
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
    platform_status: 'using',
    platform_observed_at: '2026-11-11T13:20:00Z',
    platform_links: [{ connection_id: 'conn', connection_name: 'tx', upstream_id: '428802' } as never],
    platform: platform(),
    onboarding_state: 'linked',
  } as Account
}

const field = (w: VueWrapper, label: string) => w.get(`[data-label="${label}"]`)
const button = (w: VueWrapper, text: string) => w.findAll('button').find((b) => b.text() === text)!

async function open() {
  const w = mount(PlatformProfileDialog, { props: { modelValue: true, account: account() }, global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  return w
}

test('the form starts from the platform values and the local fill only changes what differs', async () => {
  const w = await open()
  expect((field(w, '套餐结束日期').get('input').element as HTMLInputElement).value).toBe('2026-12-10')
  expect((field(w, '备注').get('input').element as HTMLInputElement).value).toBe('')
  await button(w, '用本地资料填充').trigger('click')
  await flushPromises()
  expect((field(w, '套餐结束日期').get('input').element as HTMLInputElement).value).toBe('2026-12-31')
  expect((field(w, '注册地区').get('input').element as HTMLInputElement).value).toBe('US')
  await button(w, '下一步').trigger('click')
  await flushPromises()
  const rows = w.findAll('[data-compare]')
  expect(rows.map((r) => r.attributes('data-compare'))).toEqual(['plan_end_time'])
  expect(w.text()).toContain('2026-12-10')
  expect(w.text()).toContain('2026-12-31')
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ data: { verified: true, changed: ['plan_end_time'], platform: platform() } }), {
      status: 200,
    }),
  )
  await button(w, '提交到平台').trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls[0]![0]).toContain('/api/v1/accounts/acc-1/platform-profile')
  const body = JSON.parse(fetchMock.mock.calls[0]![1].body)
  expect(body.changes).toEqual({ plan_end_time: '2026-12-31' })
  expect(body.base).toEqual({
    plan: 'claude/cc-max-20x',
    register_time: '2026-11-03',
    plan_start_time: '2026-11-11',
    plan_end_time: '2026-12-10',
    register_location: 'US',
    balance: '100',
    payment_method: 'google_pay',
    register_device: 'Mac',
    recharge_time: '',
    warranty_period: '',
    remark: '',
  })
  expect(w.emitted('saved')).toBeTruthy()
})

test('without any change the submit step cannot be sent', async () => {
  const w = await open()
  await button(w, '下一步').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('没有改动')
  expect((button(w, '提交到平台').element as HTMLButtonElement).disabled).toBe(true)
  expect(fetchMock).not.toHaveBeenCalled()
})

test('a stale snapshot asks the user to reopen the dialog', async () => {
  const w = await open()
  await field(w, '备注').get('input').setValue('1 cc-manager')
  await button(w, '下一步').trigger('click')
  await flushPromises()
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ error: { code: 'VERSION_CONFLICT', message: '平台记录已变化，请刷新后重试' } }), {
      status: 409,
    }),
  )
  await button(w, '提交到平台').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('平台资料刚更新，请重新打开')
  expect(w.emitted('saved')).toBeFalsy()
})

test('an unverified result still closes with a hint', async () => {
  const w = await open()
  await field(w, '备注').get('input').setValue('x')
  await button(w, '下一步').trigger('click')
  await flushPromises()
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({ data: { verified: false, hint: '平台已接受修改，但刷新平台记录失败', changed: ['remark'] } }),
      {
        status: 200,
      },
    ),
  )
  await button(w, '提交到平台').trigger('click')
  await flushPromises()
  expect(w.emitted('saved')?.[0]?.[0]).toMatchObject({ verified: false })
})

test('clearing a date picker is shown as an empty new value instead of breaking the dialog', async () => {
  const w = await open()
  await field(w, '套餐结束日期').get('input').setValue('')
  await button(w, '下一步').trigger('click')
  await flushPromises()
  const rows = w.findAll('[data-compare]')
  expect(rows.map((r) => r.attributes('data-compare'))).toEqual(['plan_end_time'])
  expect(rows[0]!.text()).toContain('（空）')
  expect((button(w, '提交到平台').element as HTMLButtonElement).disabled).toBe(false)
})

test('a conflict asks the list to reload so the next open sees the fresh snapshot', async () => {
  const w = await open()
  await field(w, '备注').get('input').setValue('1 cc-manager')
  await button(w, '下一步').trigger('click')
  await flushPromises()
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ error: { code: 'VERSION_CONFLICT', message: '平台记录已变化' } }), { status: 409 }),
  )
  await button(w, '提交到平台').trigger('click')
  await flushPromises()
  expect(w.emitted('conflict')).toBeTruthy()
})
