import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ConnectionFormDialog from '../src/components/ConnectionFormDialog.vue'
import { session } from '../src/api'
import type { PlatformConnection } from '../src/types/api'

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
  'el-dialog': {
    template: '<div class="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
    props: ['title'],
  },
  'el-form': { template: '<form @submit.prevent><slot /></form>' },
  'el-form-item': { template: '<div :data-label="label"><slot /></div>', props: ['label'] },
  'el-input': inputStub,
  'el-switch': {
    template:
      '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-checkbox': { template: '<input type="checkbox" />' },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  ErrorNotice: { template: '<div />' },
}

const connection: PlatformConnection = {
  id: 'KK-conn',
  name: 'TX CCMAX',
  base_url: 'https://120.233.254.152:8000',
  timezone: 'Asia/Shanghai',
  enabled: false,
  version: 7,
  token_configured: false,
  web_configured: false,
  web_username: 'zhuxiangkezhi-cpa',
}

const field = (w: VueWrapper, label: string) =>
  w.get(`[data-label="${label}"]`).get('input').element as HTMLInputElement

test('editing a connection opens it filled in and saves it in place', async () => {
  // The dialog lives in the page closed, then opens when 修改 is clicked.
  const w = mount(ConnectionFormDialog, { props: { modelValue: false, connection }, global: { stubs } })
  wrappers.push(w)
  await w.setProps({ modelValue: true })
  await flushPromises()
  expect(w.get('h2').text()).toBe('修改平台连接')
  expect(field(w, '名称').value).toBe('TX CCMAX')
  expect(field(w, 'Base URL').value).toBe('https://120.233.254.152:8000')
  expect(field(w, '登录账号').value).toBe('zhuxiangkezhi-cpa')
  await w.get('[data-label="OpenAPI Token"] input').setValue('new-token')
  await w.get('[data-label="启用状态"] input').setValue(true)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'KK-conn' } }), { status: 200 }))
  await w
    .findAll('button')
    .find((b) => b.text() === '保存修改')!
    .trigger('click')
  await flushPromises()
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toContain('/api/v1/platform-connections/KK-conn')
  expect(init.method).toBe('PUT')
  expect(JSON.parse(init.body)).toMatchObject({
    name: 'TX CCMAX',
    base_url: 'https://120.233.254.152:8000',
    token: 'new-token',
    enabled: true,
    expected_version: 7,
  })
  expect(w.emitted('saved')?.[0]).toEqual([true])
})

test('creating after an edit starts from a blank form', async () => {
  const w = mount(ConnectionFormDialog, { props: { modelValue: false, connection }, global: { stubs } })
  wrappers.push(w)
  await w.setProps({ modelValue: true })
  await w.setProps({ modelValue: false, connection: null })
  await w.setProps({ modelValue: true })
  await flushPromises()
  expect(w.get('h2').text()).toBe('新建平台连接')
  expect(field(w, '名称').value).toBe('')
})
