import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OnboardingProxyPicker from '../src/components/OnboardingProxyPicker.vue'
import { session } from '../src/api'
const permission = vi.hoisted(() => ({ allowed: true }))
vi.mock('../src/composables/useSession', () => ({ can: () => permission.allowed }))
const stubs = {
  'el-input': {
    props: ['modelValue'],
    template: '<input :value="modelValue" @input="$emit(\'update:modelValue\',$event.target.value)" />',
  },
  'el-button': {
    props: ['disabled', 'loading'],
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
  },
  'el-select': {
    props: ['modelValue'],
    template:
      '<select :value="modelValue" @change="$emit(\'update:modelValue\',$event.target.value)"><slot /></select>',
  },
  'el-option': {
    props: ['value', 'label', 'disabled'],
    template: '<option :value="value" :disabled="disabled">{{label}}</option>',
  },
}
afterEach(() => {
  vi.unstubAllGlobals()
  permission.allowed = true
})
test('parses an authenticated address, creates once and returns a pool id without exposing the password', async () => {
  session.value = { authenticated: true, csrf_token: 'csrf' }
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { id: 'created' } }), { status: 201 }))
  vi.stubGlobal('fetch', fetch)
  const w = mount(OnboardingProxyPicker, { props: { modelValue: '', proxies: [] }, global: { stubs } })
  await w.get('input[value="new"]').setValue()
  await w.get('[data-proxy-address]').setValue('http://alice:secret@proxy.example.com:8080')
  expect(w.get('[data-save-proxy]').text()).toContain('检测并保存')
  await w.get('[data-save-proxy]').trigger('click')
  await flushPromises()
  expect(fetch).toHaveBeenCalledTimes(1)
  const body = JSON.parse(fetch.mock.calls[0]![1].body)
  expect(body).toMatchObject({
    scheme: 'http',
    host: 'proxy.example.com',
    port: 8080,
    username: 'alice',
    password: 'secret',
  })
  expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['created'])
  expect(JSON.stringify(w.emitted('created'))).not.toContain('secret')
  w.unmount()
})
test('a malformed address makes no request and a failed create preserves input for correction', async () => {
  session.value = { authenticated: true, csrf_token: 'csrf' }
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(JSON.stringify({ error: { message: '保存失败', code: 'FAILED' } }), { status: 500 }),
    )
  vi.stubGlobal('fetch', fetch)
  const w = mount(OnboardingProxyPicker, { props: { modelValue: '', proxies: [] }, global: { stubs } })
  await w.get('input[value="new"]').setValue()
  await w.get('[data-proxy-address]').setValue('ftp://host:20')
  await w.get('[data-save-proxy]').trigger('click')
  await flushPromises()
  expect(fetch).not.toHaveBeenCalled()
  await w.get('[data-proxy-address]').setValue('host:8080:user:pass')
  await w.get('[data-save-proxy]').trigger('click')
  await flushPromises()
  expect((w.get('[data-proxy-address]').element as HTMLInputElement).value).toBe('host:8080:user:pass')
  expect(w.emitted('created')).toBeUndefined()
  w.unmount()
})
test('users without proxy-create permission can select existing proxies but cannot create one', () => {
  permission.allowed = false
  const w = mount(OnboardingProxyPicker, { props: { modelValue: '', proxies: [] }, global: { stubs } })
  expect(w.get('input[value="new"]').attributes('disabled')).toBeDefined()
  expect(w.get('input[value="existing"]').attributes('disabled')).toBeUndefined()
  expect(w.get('input[value="auto"]').attributes('disabled')).toBeDefined()
  w.unmount()
})
test('with no usable proxy the picker opens on the new-proxy form and reports it as pending', async () => {
  const w = mount(OnboardingProxyPicker, {
    props: {
      modelValue: '',
      proxies: [{ id: 'off', name: '停用', host: 'h', port: 1, scheme: 'http', enabled: false }],
    },
    global: { stubs },
  })
  expect((w.get('input[value="new"]').element as HTMLInputElement).checked).toBe(true)
  expect(w.find('[data-proxy-address]').exists()).toBe(true)
  expect(w.emitted('pending')?.[0]).toEqual([true])
  w.unmount()
  const withPool = mount(OnboardingProxyPicker, {
    props: { modelValue: '', proxies: [{ id: 'p', name: 'A', host: 'h', port: 1, scheme: 'http', enabled: true }] },
    global: { stubs },
  })
  expect((withPool.get('input[value="existing"]').element as HTMLInputElement).checked).toBe(true)
  expect(withPool.emitted('pending')).toBeUndefined()
  withPool.unmount()
})
