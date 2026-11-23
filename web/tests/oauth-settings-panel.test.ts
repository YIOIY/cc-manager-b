import { beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OAuthSettingsPanel from '../src/components/OAuthSettingsPanel.vue'
import { session } from '../src/api'

vi.mock('../src/composables/useSession', () => ({
  useSession: () => ({ can: vi.fn(() => true) }),
}))

const fetchMock = vi.fn()
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const settings = {
  global_concurrency: 3,
  exit_concurrency: 2,
  exchange_interval_seconds: 3,
  probe_cache_seconds: 2,
  manual_preheat_seconds_min: 0,
  manual_preheat_seconds_max: 0,
  batch_preheat_seconds_min: 5,
  batch_preheat_seconds_max: 9,
  version: 4,
}

beforeEach(() => {
  fetchMock.mockReset()
  session.value = { authenticated: true, csrf_token: 'csrf' }
  vi.stubGlobal('fetch', fetchMock)
})

test('loads current settings and saves with the expected version', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: settings })).mockResolvedValueOnce(
    json({
      data: {
        ...settings,
        global_concurrency: 5,
        version: 5,
      },
    }),
  )
  const wrapper = mount(OAuthSettingsPanel, {
    global: {
      stubs: {
        transition: true,
        'el-button': {
          template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
          props: ['disabled', 'loading'],
        },
        'el-dialog': { template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>', props: ['modelValue'] },
        'el-input-number': {
          template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', Number($event.target.value))" />',
          props: ['modelValue'],
          emits: ['update:modelValue'],
        },
      },
    },
  })
  await flushPromises()
  expect(wrapper.text()).toContain('全局并发')
  expect(wrapper.text()).toContain('手动预热')
  await wrapper.get('button').trigger('click')
  await flushPromises()

  const inputs = wrapper.findAll('input')
  await inputs[0]!.setValue('5')
  await flushPromises()

  const primaryButtons = wrapper.findAll('button').filter((b) => b.text().includes('保存'))
  expect(primaryButtons.length).toBe(1)
  expect(primaryButtons[0]!.attributes('disabled')).toBeUndefined()
  await primaryButtons[0]!.trigger('click')
  await flushPromises()

  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/oauth-settings')
  expect(fetchMock.mock.calls[1]![1].method).toBe('PUT')
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toMatchObject({
    global_concurrency: 5,
    expected_version: 4,
  })
  wrapper.unmount()
})

test('load errors are visible', async () => {
  fetchMock.mockResolvedValueOnce(json({ error: { code: 'PERMISSION_REQUIRED', message: '无权限' } }, 403))
  const wrapper = mount(OAuthSettingsPanel, {
    global: {
      stubs: {
        transition: true,
        'el-button': {
          template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
          props: ['disabled', 'loading'],
        },
        'el-alert': { template: '<div>{{ title }}</div>', props: ['title'] },
      },
    },
  })
  await flushPromises()
  expect(wrapper.text()).toContain('无权限')
  wrapper.unmount()
})
