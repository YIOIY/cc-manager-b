import { afterEach, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ErrorNotice from '../src/components/ErrorNotice.vue'
import { ApiError } from '../src/api'

afterEach(() => vi.unstubAllGlobals())

const envelope = new ApiError(503, 'STORAGE_UNAVAILABLE', '数据存储暂时不可用', {
  category: 'storage',
  hint: '请稍后重试',
  retryable: true,
  action: 'retry',
  requestId: 'req-42',
})

test('renders title, hint and a copyable request id', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const wrapper = mount(ErrorNotice, { props: { error: envelope } })
  expect(wrapper.text()).toContain('数据存储暂时不可用')
  expect(wrapper.text()).toContain('请稍后重试')
  expect(wrapper.text()).toContain('req-42')
  expect(wrapper.classes()).toContain('tone-danger')
  await wrapper.get('.error-notice-copy').trigger('click')
  expect(writeText).toHaveBeenCalledOnce()
  expect(writeText.mock.calls[0]![0]).toContain('STORAGE_UNAVAILABLE')
  expect(writeText.mock.calls[0]![0]).toContain('请求编号 req-42')
})

test('validation errors use the amber tone and list fields only when asked', () => {
  const v = new ApiError(422, 'VALIDATION_FAILED', '有 2 项资料不符合要求', {
    category: 'validation',
    fields: [
      { field: 'password', code: 'REQUIRED', message: '缺少密码' },
      { field: 'plan_end_time', code: 'OUT_OF_RANGE', message: '套餐结束日期不能早于开始日期' },
    ],
  })
  const compact = mount(ErrorNotice, { props: { error: v, compact: true } })
  expect(compact.classes()).toContain('tone-pending')
  expect(compact.text()).not.toContain('缺少密码')
  const full = mount(ErrorNotice, { props: { error: v } })
  expect(full.findAll('.error-notice-field')).toHaveLength(2)
  expect(full.text()).toContain('密码：缺少密码')
})

test('renders nothing without an error and plain errors keep their message', () => {
  expect(
    mount(ErrorNotice, { props: { error: null } })
      .find('.error-notice')
      .exists(),
  ).toBe(false)
  const plain = mount(ErrorNotice, { props: { error: new Error('boom') } })
  expect(plain.text()).toContain('boom')
  expect(plain.find('.error-notice-copy').exists()).toBe(false)
})

test('an error can be reported with its code and request id', async () => {
  const feedback = await import('../src/composables/useFeedback')
  const wrapper = mount(ErrorNotice, { props: { error: envelope } })
  await wrapper.get('.error-notice-report').trigger('click')
  expect(feedback.useFeedbackDialog().open.value).toBe(true)
  expect(feedback.useFeedbackDialog().prefill.value).toMatchObject({
    title: '数据存储暂时不可用',
    errorCode: 'STORAGE_UNAVAILABLE',
    requestId: 'req-42',
  })
  feedback.useFeedbackDialog().open.value = false
})
