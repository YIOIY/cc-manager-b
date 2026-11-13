import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'

const route = reactive({ path: '/accounts', query: { q: 'secret@example.test' } })
const push = vi.fn()
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push }) }))

import FeedbackDialog from '../src/components/FeedbackDialog.vue'
import { feedbackChanged, openFeedback, useFeedbackDialog } from '../src/composables/useFeedback'
import { session } from '../src/api'

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
  push.mockReset()
  useFeedbackDialog().open.value = false
})

const stubs = {
  'el-dialog': {
    template: '<div v-if="modelValue" class="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
    props: ['modelValue', 'title'],
  },
  'el-form': { template: '<form @submit.prevent><slot /></form>' },
  'el-form-item': {
    template: '<div :data-label="label"><slot /><p v-if="error" class="field-error">{{ error }}</p></div>',
    props: ['label', 'error'],
  },
  'el-input': {
    template:
      '<textarea v-if="type === \'textarea\'" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)"></textarea><input v-else :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
    props: ['modelValue', 'type'],
    emits: ['update:modelValue'],
  },
  'el-radio-group': { template: '<div class="kinds"><slot /></div>', props: ['modelValue'] },
  'el-radio-button': {
    template:
      '<button type="button" class="kind" :data-kind="value" @click="$parent.$emit(\'update:modelValue\', value)"><slot /></button>',
    props: ['value'],
  },
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  ErrorNotice: {
    template: '<div class="notice">{{ error ? String(error.message || "") : "" }}</div>',
    props: ['error'],
  },
}
const button = (w: VueWrapper, text: string) => w.findAll('button').find((b) => b.text() === text)!

async function show(prefill?: Parameters<typeof openFeedback>[0]) {
  const w = mount(FeedbackDialog, { global: { stubs } })
  wrappers.push(w)
  openFeedback(prefill)
  await flushPromises()
  return w
}

test('feedback is sent with the page path and browser, never the query string', async () => {
  const w = await show()
  expect(w.text()).toContain('/accounts')
  expect(w.text()).not.toContain('secret@example.test')
  await w.get('[data-label="描述"] textarea').setValue('  新建账号时代理选不了  ')
  await w.get('[data-label="标题"] input').setValue('代理选不了')
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'f1' } }), { status: 201 }))
  const before = feedbackChanged.value
  await button(w, '提交反馈').trigger('click')
  await flushPromises()
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toContain('/api/v1/feedback')
  expect(init.method).toBe('POST')
  const body = JSON.parse(init.body)
  expect(body).toMatchObject({ kind: 'problem', title: '代理选不了', body: '新建账号时代理选不了', page: '/accounts' })
  expect(body.context.user_agent).toBeTruthy()
  expect(JSON.stringify(body)).not.toContain('secret@example.test')
  expect(w.text()).toContain('已收到')
  expect(feedbackChanged.value).toBe(before + 1)
  await button(w, '查看我的反馈').trigger('click')
  expect(push).toHaveBeenCalledWith({ name: 'feedback' })
  expect(useFeedbackDialog().open.value).toBe(false)
})

test('opened from an error it carries the error code and request id', async () => {
  const w = await show({
    title: '平台拒绝了本次请求的身份验证',
    errorCode: 'PLATFORM_AUTH_FAILED',
    requestId: 'req-42',
  })
  expect((w.get('[data-label="标题"] input').element as HTMLInputElement).value).toBe('平台拒绝了本次请求的身份验证')
  expect(w.text()).toContain('PLATFORM_AUTH_FAILED')
  expect(w.text()).toContain('req-42')
  await w.get('[data-label="描述"] textarea').setValue('点确认并上号后出现')
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'f2' } }), { status: 201 }))
  await button(w, '提交反馈').trigger('click')
  await flushPromises()
  const body = JSON.parse(fetchMock.mock.calls[0]![1].body)
  expect(body.context).toMatchObject({ error_code: 'PLATFORM_AUTH_FAILED', request_id: 'req-42' })
})

test('the kind can be changed and an empty description is not sent', async () => {
  const w = await show()
  await button(w, '提交反馈').trigger('click')
  await flushPromises()
  expect(fetchMock).not.toHaveBeenCalled()
  expect(w.text()).toContain('请描述遇到的问题或建议')
  await w.get('[data-kind="suggestion"]').trigger('click')
  await w.get('[data-label="描述"] textarea').setValue('希望能导出')
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'f3' } }), { status: 201 }))
  await button(w, '提交反馈').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body).kind).toBe('suggestion')
})

test('a server error keeps the text so it can be sent again', async () => {
  const w = await show()
  await w.get('[data-label="描述"] textarea').setValue('写了很长的一段')
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ error: { code: 'STORAGE_UNAVAILABLE', message: '数据存储暂时不可用' } }), {
      status: 503,
    }),
  )
  await button(w, '提交反馈').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('数据存储暂时不可用')
  expect((w.get('[data-label="描述"] textarea').element as HTMLTextAreaElement).value).toBe('写了很长的一段')
  expect(w.text()).not.toContain('已收到')
})

test('opening again starts from a clean form', async () => {
  const w = await show({ title: '旧标题', errorCode: 'X', requestId: 'r' })
  await w.get('[data-label="描述"] textarea').setValue('旧内容')
  useFeedbackDialog().open.value = false
  await flushPromises()
  openFeedback()
  await flushPromises()
  expect((w.get('[data-label="标题"] input').element as HTMLInputElement).value).toBe('')
  expect((w.get('[data-label="描述"] textarea').element as HTMLTextAreaElement).value).toBe('')
  expect(w.text()).not.toContain('req')
})
