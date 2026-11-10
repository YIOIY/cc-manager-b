import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImportDialog from '../src/components/ImportDialog.vue'
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

const stubs = {
  'el-dialog': { template: '<div class="dialog"><slot /></div>' },
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  'el-input': {
    template: '<textarea :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-tag': { template: '<span class="tag" :title="$attrs.title"><slot /></span>' },
}

test('preview rows show each issue message and keep the code as a tooltip', async () => {
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        data: {
          import_id: 'imp',
          valid_count: 0,
          rows: [
            {
              row_index: 1,
              masked_identity: 'a***@example.com',
              action: 'create',
              password_configured: true,
              auth_configured: false,
              issues: [
                { code: 'PASSWORD_REQUIRED', field: 'password', message: '缺少密码' },
                { code: 'PLAN_INVALID', field: 'plan', message: '套餐不在允许范围内' },
              ],
            },
          ],
        },
      }),
      { status: 200 },
    ),
  )
  const wrapper = mount(ImportDialog, { props: { modelValue: true }, global: { stubs } })
  await wrapper.get('textarea').setValue('[{"account":"a@example.com","password":"","plan":"nope"}]')
  await wrapper
    .findAll('button')
    .find((b) => b.text().includes('校验并预览'))!
    .trigger('click')
  await flushPromises()
  const tag = wrapper.get('.import-results .tag')
  expect(tag.text()).toBe('缺少密码、套餐不在允许范围内')
  expect(tag.attributes('title')).toContain('PASSWORD_REQUIRED')
  expect(tag.text()).not.toContain('PASSWORD_REQUIRED')
})
