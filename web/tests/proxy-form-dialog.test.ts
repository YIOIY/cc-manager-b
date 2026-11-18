import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ProxyFormDialog from '../src/components/ProxyFormDialog.vue'
import { session } from '../src/api'

const fetchMock = vi.fn()
beforeEach(() => {
  session.value = { authenticated: true, csrf_token: 'csrf' }
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
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
  'el-checkbox': { template: '<input type="checkbox" />' },
  'el-switch': { template: '<input type="checkbox" />' },
}
const field = (wrapper: ReturnType<typeof mount>, label: string) => wrapper.get(`[data-label="${label}"]`)

test('pasting a full proxy URL into the host field fills every other field', async () => {
  const wrapper = mount(ProxyFormDialog, { props: { modelValue: true, proxy: null }, global: { stubs } })
  await flushPromises()
  await field(wrapper, '主机地址').get('input').setValue('socks5://JLvTg9sVZpcG:t2nXh7HWSrN9@204.1.88.141:7690')
  await flushPromises()
  expect((field(wrapper, '主机地址').get('input').element as HTMLInputElement).value).toBe('204.1.88.141')
  expect((field(wrapper, '端口').get('input').element as HTMLInputElement).value).toBe('7690')
  expect((field(wrapper, '协议').get('select').element as HTMLSelectElement).value).toBe('socks5')
  expect((field(wrapper, '用户名').get('input').element as HTMLInputElement).value).toBe('JLvTg9sVZpcG')
  expect((field(wrapper, '密码').get('input').element as HTMLInputElement).value).toBe('t2nXh7HWSrN9')
  expect(wrapper.text()).toContain('已识别')
  // saving sends the split fields, not the pasted string
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'p1' } }), { status: 201 }))
  expect(wrapper.text()).toContain('本机检测')
  await wrapper
    .findAll('button')
    .find((b) => b.text() === '检测并保存')!
    .trigger('click')
  await flushPromises()
  const body = JSON.parse(fetchMock.mock.calls[0]![1].body)
  expect(body).toMatchObject({
    scheme: 'socks5',
    host: '204.1.88.141',
    port: 7690,
    username: 'JLvTg9sVZpcG',
    password: 't2nXh7HWSrN9',
  })
  wrapper.unmount()
})

test('a plain host is kept as typed and shows no recognition hint', async () => {
  const wrapper = mount(ProxyFormDialog, { props: { modelValue: true, proxy: null }, global: { stubs } })
  await flushPromises()
  await field(wrapper, '主机地址').get('input').setValue('proxy.example.com')
  await flushPromises()
  expect((field(wrapper, '主机地址').get('input').element as HTMLInputElement).value).toBe('proxy.example.com')
  expect((field(wrapper, '端口').get('input').element as HTMLInputElement).value).toBe('8080')
  expect(wrapper.text()).not.toContain('已识别')
  wrapper.unmount()
})
