import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'

const route = reactive<{ query: Record<string, string> }>({ query: {} })
const replace = vi.fn((to: { query?: Record<string, string> }) => {
  route.query = { ...(to.query || {}) }
})
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
const copyText = vi.fn().mockResolvedValue(true)
vi.mock('../src/utils/clipboard', () => ({ copyText: (t: string) => copyText(t) }))

import HelpView from '../src/views/HelpView.vue'
import { helpTopics } from '../src/help/content'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  route.query = {}
  replace.mockClear()
  copyText.mockClear()
})

const stubs = {
  PageHead: { template: '<div><slot /></div>' },
  'el-input': {
    template: '<input class="search" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
    props: ['modelValue'],
    emits: ['update:modelValue'],
  },
  'el-button': { template: '<button @click="$emit(\'click\')"><slot /></button>', emits: ['click'] },
}

async function render(query: Record<string, string> = {}) {
  route.query = query
  const w = mount(HelpView, { global: { stubs } })
  wrappers.push(w)
  await flushPromises()
  return w
}

test('lists the manual and the FAQ and opens the first topic by default', async () => {
  const w = await render()
  const text = w.text()
  expect(text).toContain('操作手册')
  expect(text).toContain('常见问题')
  expect(w.findAll('[data-topic]')).toHaveLength(helpTopics.length)
  expect(w.get('[data-current-topic]').attributes('data-current-topic')).toBe(helpTopics[0]!.id)
  expect(w.get('article h2').text()).toBe(helpTopics[0]!.title)
})

test('a topic in the address is opened, and an unknown one falls back to the first', async () => {
  const w = await render({ topic: 'device-id' })
  expect(w.get('[data-current-topic]').attributes('data-current-topic')).toBe('device-id')
  expect(w.get('article').text()).toContain('userID')
  const other = await render({ topic: 'no-such-topic' })
  expect(other.get('[data-current-topic]').attributes('data-current-topic')).toBe(helpTopics[0]!.id)
})

test('choosing a topic puts it in the address and shows it', async () => {
  const w = await render()
  await w.get('[data-topic="faq-supplier-not-authorized"]').trigger('click')
  await flushPromises()
  expect(replace).toHaveBeenCalledWith({ name: 'help', query: { topic: 'faq-supplier-not-authorized' } })
  expect(w.get('[data-current-topic]').attributes('data-current-topic')).toBe('faq-supplier-not-authorized')
  expect(w.get('article').text()).toContain('当前供应商未获授权该厂商')
})

test('searching narrows the list and says so when nothing matches', async () => {
  const w = await render()
  await w.get('input.search').setValue('设备标识')
  await flushPromises()
  const ids = w.findAll('[data-topic]').map((n) => n.attributes('data-topic'))
  expect(ids).toContain('device-id')
  expect(ids.length).toBeLessThan(helpTopics.length)
  await w.get('input.search').setValue('绝对不会出现的词xyz')
  await flushPromises()
  expect(w.findAll('[data-topic]')).toHaveLength(0)
  expect(w.text()).toContain('没有找到相关内容')
})

test('a command block can be copied', async () => {
  const w = await render({ topic: 'device-id' })
  const block = w.get('[data-code]')
  await block.get('button').trigger('click')
  await flushPromises()
  expect(copyText).toHaveBeenCalledWith(block.get('code').text())
  expect(block.text()).toContain('已复制')
})

test('a link inside a topic jumps to the other topic', async () => {
  const w = await render({ topic: 'onboard' })
  const link = w.get('[data-link]')
  await link.trigger('click')
  await flushPromises()
  expect(replace).toHaveBeenCalledWith({ name: 'help', query: { topic: link.attributes('data-link') } })
})
