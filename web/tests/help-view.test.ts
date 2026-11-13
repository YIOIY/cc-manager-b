import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'

const route = reactive<{ query: Record<string, string> }>({ query: {} })
const push = vi.fn((to: { query?: Record<string, string> }) => {
  route.query = { ...(to.query || {}) }
})
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ push }) }))
const copyText = vi.fn().mockResolvedValue(true)
vi.mock('../src/utils/clipboard', () => ({ copyText: (t: string) => copyText(t) }))

import HelpView from '../src/views/HelpView.vue'
import { helpPath, helpTopics } from '../src/help/content'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  route.query = {}
  push.mockClear()
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
const manual = helpTopics.filter((t) => t.group === 'manual')
const faq = helpTopics.filter((t) => t.group === 'faq')

test('the home shows the onboarding route, the manual as cards and the questions by area', async () => {
  const w = await render()
  expect(w.find('article').exists()).toBe(false)
  expect(w.findAll('[data-path]').map((n) => n.text())).toHaveLength(helpPath.length)
  expect(w.findAll('[data-path]')[1]!.text()).toContain('连接账号')
  const cards = w.findAll('[data-card]')
  expect(cards).toHaveLength(manual.length)
  expect(cards[0]!.text()).toContain(manual[0]!.title)
  expect(cards[0]!.text()).toContain(manual[0]!.summary)
  expect(w.findAll('[data-question]')).toHaveLength(faq.length)
  const text = w.text()
  for (const label of ['上号路线', '操作手册', '常见问题', '平台连接', '金额与仪表盘']) expect(text).toContain(label)
})

test('opening a card, a route step or a question goes to that topic', async () => {
  const w = await render()
  await w.get('[data-card="device-id"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: 'device-id' } })
  await flushPromises()
  expect(w.get('[data-current-topic]').attributes('data-current-topic')).toBe('device-id')
  expect(w.get('article').text()).toContain('userID')

  const home = await render()
  await home.findAll('[data-path]')[0]!.trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: helpPath[0]!.topic } })
  const again = await render()
  await again.get('[data-question="faq-supplier-not-authorized"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: 'faq-supplier-not-authorized' } })
})

test('a topic in the address opens the article; an unknown one shows the home', async () => {
  const w = await render({ topic: 'faq-supplier-not-authorized' })
  expect(w.get('article h2').text()).toContain('当前供应商未获授权该厂商')
  expect(w.findAll('[data-topic]')).toHaveLength(helpTopics.length) // the contents list
  const other = await render({ topic: 'no-such-topic' })
  expect(other.find('article').exists()).toBe(false)
  expect(other.findAll('[data-card]').length).toBeGreaterThan(0)
})

test('the article highlights button names, shows the lead, and links back home', async () => {
  const w = await render({ topic: 'onboard' })
  const topic = helpTopics.find((t) => t.id === 'onboard')!
  expect(w.get('.help-lead').text()).toBe(topic.summary)
  const labels = w.findAll('.ui-label').map((n) => n.text())
  expect(labels).toContain('登录 Claude')
  expect(labels).toContain('确认并上号')
  expect(
    w
      .findAll('[data-flow-step]')
      .map((n) => n.text())
      .join(' '),
  ).toContain('补充资料')
  await w.get('[data-home]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: {} })
})

test('status meanings are laid out as a table', async () => {
  const w = await render({ topic: 'after-submit' })
  const rows = w.findAll('article table tbody tr')
  expect(rows).toHaveLength(5)
  expect(rows[3]!.text()).toContain('上号失败')
  expect(rows[3]!.find('.ui-label').text()).toBe('处理并重试')
})

test('related topics and the previous / next topic are offered at the end', async () => {
  const w = await render({ topic: 'onboard' })
  const related = w.findAll('[data-link]').map((n) => n.attributes('data-link'))
  expect(related).toEqual(['device-id', 'after-submit'])
  await w.get('[data-link="after-submit"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: 'after-submit' } })
  const back = await render({ topic: 'onboard' })
  expect(back.get('[data-previous]').text()).toContain('上号前的准备')
  expect(back.get('[data-next]').text()).toContain('获取设备标识')
  await back.get('[data-next]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: 'device-id' } })
  expect((await render({ topic: helpTopics[0]!.id })).find('[data-previous]').exists()).toBe(false)
})

test('searching lists hits with the matching text marked, and says so when nothing matches', async () => {
  const w = await render()
  await w.get('input.search').setValue('userID')
  await flushPromises()
  const hits = w.findAll('[data-hit]')
  expect(hits.map((h) => h.attributes('data-hit'))).toContain('device-id')
  expect(w.find('[data-card]').exists()).toBe(false) // results replace the home sections
  expect(w.get('[data-hit="device-id"] mark').text().toLowerCase()).toBe('userid')
  await w.get('[data-hit="device-id"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ name: 'help', query: { topic: 'device-id' } })
  await w.get('input.search').setValue('绝对不会出现的词xyz')
  await flushPromises()
  expect(w.findAll('[data-hit]')).toHaveLength(0)
  expect(w.text()).toContain('没有找到相关内容')
})

test('a suggested search word fills the search box', async () => {
  const w = await render()
  const chip = w.get('[data-suggest]')
  await chip.trigger('click')
  await flushPromises()
  expect((w.get('input.search').element as HTMLInputElement).value).toBe(chip.text())
  expect(w.findAll('[data-hit]').length).toBeGreaterThan(0)
})

test('the contents list in an article can be filtered', async () => {
  const w = await render({ topic: 'onboard' })
  await w.get('input.search').setValue('设备标识')
  await flushPromises()
  const ids = w.findAll('[data-topic]').map((n) => n.attributes('data-topic'))
  expect(ids).toContain('device-id')
  expect(ids.length).toBeLessThan(helpTopics.length)
})

test('a command block can be copied', async () => {
  const w = await render({ topic: 'device-id' })
  const block = w.get('[data-code]')
  await block.get('button').trigger('click')
  await flushPromises()
  expect(copyText).toHaveBeenCalledWith(block.get('code').text())
  expect(block.text()).toContain('已复制')
})
