import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { Feedback, FeedbackList } from '../src/types/api'

const apiMock = vi.fn()
vi.mock('../src/api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  ApiError: class extends Error {},
  session: { value: { authenticated: true } },
}))
const openFeedback = vi.fn()
vi.mock('../src/composables/useFeedback', async () => {
  const { ref } = await import('vue')
  return {
    openFeedback: (...a: unknown[]) => openFeedback(...a),
    feedbackChanged: ref(0),
    refreshFeedbackSummary: vi.fn(),
  }
})

import FeedbackView from '../src/views/FeedbackView.vue'
import { feedbackChanged } from '../src/composables/useFeedback'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  apiMock.mockReset()
  openFeedback.mockReset()
})

const stubs = {
  PageHead: { template: '<div><slot /></div>' },
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
    emits: ['click'],
  },
  'el-input': {
    template:
      '<textarea v-if="type === \'textarea\'" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)"></textarea><input v-else class="search" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" @keyup.enter="$emit(\'keyup\', $event)" />',
    props: ['modelValue', 'type'],
    emits: ['update:modelValue', 'keyup'],
  },
  'el-select': {
    template:
      '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><slot /></select>',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-option': { template: '<option :value="value">{{ label }}</option>', props: ['value', 'label'] },
  'el-alert': { template: '<div class="alert">{{ title }}</div>', props: ['title'] },
  'el-pagination': { template: '<div class="pager" />' },
  'el-tag': { template: '<span class="tag"><slot /></span>' },
}

function item(overrides: Partial<Feedback> = {}): Feedback {
  return {
    id: 'f1',
    username: 'ops',
    kind: 'problem',
    title: '上号 403',
    body: '提交后提示当前供应商未获授权该厂商',
    page: '/accounts',
    context: { error_code: 'PLATFORM_AUTH_FAILED', request_id: 'req-123', user_agent: 'UA' },
    app_version: 'sha-abc',
    status: 'open',
    reply: '',
    handled_by: '',
    handled_at: '',
    version: 1,
    created_at: '2026-11-13T05:00:00Z',
    updated_at: '2026-11-13T05:00:00Z',
    mine: true,
    ...overrides,
  }
}
function list(items: Feedback[], canManage: boolean, counts: Partial<FeedbackList['counts']> = {}): FeedbackList {
  return {
    items,
    pagination: { page: 1, page_size: 20, total: items.length },
    counts: { open: 0, in_progress: 0, resolved: 0, closed: 0, ...counts },
    can_manage: canManage,
  }
}

async function render(data: FeedbackList) {
  apiMock.mockImplementation(async (path: string, options?: { method?: string }) => {
    if (path.startsWith('/feedback?') && !options?.method) return { data }
    if (options?.method === 'PATCH') return { data: { id: 'f1', version: 2 } }
    throw new Error('unexpected ' + path)
  })
  const w = mount(FeedbackView, { global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  return w
}
const button = (w: VueWrapper, text: string) => w.findAll('button').find((b) => b.text().includes(text))!

test('a user sees their own feedback with its status, and the reply once it is handled', async () => {
  const w = await render(
    list(
      [
        item(),
        item({
          id: 'f2',
          kind: 'suggestion',
          title: '希望能导出',
          body: '仪表盘导出',
          status: 'resolved',
          reply: '下个版本加上',
          handled_by: 'admin',
          handled_at: '2026-11-13T06:00:00Z',
          context: {},
        }),
      ],
      false,
      { open: 1, resolved: 1 },
    ),
  )
  const rows = w.findAll('[data-feedback]')
  expect(rows.map((r) => r.attributes('data-feedback'))).toEqual(['f1', 'f2'])
  expect(rows[0]!.text()).toContain('上号 403')
  expect(rows[0]!.text()).toContain('待处理')
  expect(rows[0]!.text()).toContain('遇到问题')
  expect(rows[0]!.text()).toContain('PLATFORM_AUTH_FAILED')
  expect(rows[1]!.text()).toContain('已解决')
  expect(rows[1]!.text()).toContain('下个版本加上')
  expect(rows[1]!.text()).toContain('admin')
  expect(w.find('[data-handle]').exists()).toBe(false)
  expect(w.get('[data-status="open"]').text()).toContain('1')
})

test('the submit button opens the feedback dialog and a new submission reloads the list', async () => {
  const w = await render(list([], false))
  expect(w.text()).toContain('还没有反馈')
  await button(w, '提交反馈').trigger('click')
  expect(openFeedback).toHaveBeenCalled()
  const calls = apiMock.mock.calls.length
  feedbackChanged.value++
  await flushPromises()
  expect(apiMock.mock.calls.length).toBe(calls + 1)
})

test('filtering by status asks the server for that status', async () => {
  const w = await render(list([item()], true, { open: 1, resolved: 4 }))
  await w.get('[data-status="resolved"]').trigger('click')
  await flushPromises()
  const last = apiMock.mock.calls[apiMock.mock.calls.length - 1]![0] as string
  expect(last).toContain('status=resolved')
  expect(last).toContain('page=1')
})

test('a manager sees who sent it and can change the status and reply', async () => {
  const w = await render(list([item({ mine: false })], true, { open: 1 }))
  const row = w.get('[data-feedback="f1"]')
  expect(row.text()).toContain('ops')
  await row.get('[data-handle]').trigger('click')
  await flushPromises()
  await row.get('select').setValue('in_progress')
  await row.get('textarea').setValue('  已定位  ')
  await button(w, '保存处理结果').trigger('click')
  await flushPromises()
  const patch = apiMock.mock.calls.find((c) => c[1]?.method === 'PATCH')!
  expect(patch[0]).toBe('/feedback/f1')
  expect(JSON.parse(patch[1].body)).toEqual({ status: 'in_progress', reply: '已定位', expected_version: 1 })
})

test('a failed load says so instead of showing an empty list', async () => {
  apiMock.mockRejectedValue(new Error('数据存储暂时不可用'))
  const w = mount(FeedbackView, { global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  expect(w.text()).toContain('数据存储暂时不可用')
  expect(w.text()).not.toContain('还没有反馈')
})
