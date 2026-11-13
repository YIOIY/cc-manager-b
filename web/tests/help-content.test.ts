import { expect, test } from 'vitest'
import {
  helpAreas,
  helpGroups,
  helpPath,
  helpTopics,
  neighbours,
  richParts,
  searchHits,
  searchTopics,
  topicText,
} from '../src/help/content'
import { routes } from '../src/router'

test('every topic has a unique id, a group, a title and content', () => {
  const ids = helpTopics.map((t) => t.id)
  expect(new Set(ids).size).toBe(ids.length)
  for (const t of helpTopics) {
    expect(t.id).toMatch(/^[a-z0-9-]+$/)
    expect(helpGroups.map((g) => g.key)).toContain(t.group)
    expect(t.title.trim()).not.toBe('')
    expect(t.blocks.length).toBeGreaterThan(0)
  }
  for (const g of helpGroups) expect(helpTopics.some((t) => t.group === g.key)).toBe(true)
})

test('links between topics point at topics that exist', () => {
  const ids = new Set(helpTopics.map((t) => t.id))
  for (const t of helpTopics)
    for (const b of t.blocks) if (b.type === 'link') expect(ids.has(b.topic), `${t.id} -> ${b.topic}`).toBe(true)
  // The two topics the onboarding dialog links to.
  expect(ids.has('onboard')).toBe(true)
  expect(ids.has('device-id')).toBe(true)
})

test('the content carries no server addresses or credentials', () => {
  const all = helpTopics.map(topicText).join('\n')
  expect(all).not.toMatch(/\b\d{1,3}(\.\d{1,3}){3}\b/)
  expect(all).not.toMatch(/ghp_|password\s*[:=]|token\s*[:=]/i)
})

test('search matches titles, keywords and body text, case-insensitively', () => {
  expect(searchTopics(helpTopics, '')).toHaveLength(helpTopics.length)
  expect(searchTopics(helpTopics, '   ')).toHaveLength(helpTopics.length)
  expect(searchTopics(helpTopics, '设备').map((t) => t.id)).toContain('device-id')
  expect(searchTopics(helpTopics, '403').map((t) => t.id)).toContain('faq-supplier-not-authorized')
  expect(searchTopics(helpTopics, 'USERID').map((t) => t.id)).toContain('device-id')
  expect(searchTopics(helpTopics, '绝对不会出现的词xyz')).toEqual([])
})

test('the help page is in the navigation for every signed-in user', () => {
  const help = routes.find((r) => r.name === 'help')
  expect(help?.path).toBe('/help')
  expect(help?.meta?.nav).toBe(true)
  expect(help?.meta?.permission).toBeUndefined()
  expect(help?.meta?.title).toBe('帮助文档')
  // Pages a user is redirected to by default stay ahead of it.
  expect(routes.findIndex((r) => r.name === 'help')).toBeGreaterThan(routes.findIndex((r) => r.name === 'users'))
})

test('every topic has a one-line summary; manual topics have an icon and questions an area', () => {
  const areas = helpAreas.map((a) => a.key)
  for (const t of helpTopics) {
    expect(t.summary.trim(), t.id).not.toBe('')
    expect(t.summary.length, t.id).toBeLessThanOrEqual(60)
    if (t.group === 'manual') expect(t.icon, t.id).toBeTruthy()
    else expect(areas, t.id).toContain(t.area)
  }
  for (const a of helpAreas)
    expect(
      helpTopics.some((t) => t.area === a.key),
      a.key,
    ).toBe(true)
})

test('the onboarding route walks through existing topics in order', () => {
  const ids = new Set(helpTopics.map((t) => t.id))
  expect(helpPath.map((n) => n.title)).toEqual(['准备', '连接账号', '补充资料', '确认上号', '查看结果'])
  for (const node of helpPath) expect(ids.has(node.topic), node.title).toBe(true)
})

test('button names written in corner brackets are split out for highlighting', () => {
  expect(richParts('点「登录 Claude」生成链接，再点「确认授权结果」。')).toEqual([
    { text: '点', ui: false },
    { text: '登录 Claude', ui: true },
    { text: '生成链接，再点', ui: false },
    { text: '确认授权结果', ui: true },
    { text: '。', ui: false },
  ])
  expect(richParts('没有按钮')).toEqual([{ text: '没有按钮', ui: false }])
  expect(richParts('「开头」和未闭合的「括号')).toEqual([
    { text: '开头', ui: true },
    { text: '和未闭合的「括号', ui: false },
  ])
})

test('search hits carry the surrounding text of the first match', () => {
  const hits = searchHits(helpTopics, 'userid')
  const hit = hits.find((h) => h.topic.id === 'device-id')!
  expect(hit.match.toLowerCase()).toBe('userid')
  expect((hit.before + hit.match + hit.after).length).toBeLessThanOrEqual(90)
  expect(hit.before + hit.match + hit.after).toContain('userID')
  // A match in the title or keywords only falls back to the summary.
  const byKeyword = searchHits(helpTopics, 'PLATFORM_AUTH_FAILED').find(
    (h) => h.topic.id === 'faq-supplier-not-authorized',
  )!
  expect(byKeyword.match).toBe('')
  expect(byKeyword.before).toBe(byKeyword.topic.summary)
  expect(searchHits(helpTopics, '  ')).toEqual([])
  expect(searchHits(helpTopics, '绝对不会出现的词xyz')).toEqual([])
})

test('each topic knows the one before and after it', () => {
  const first = helpTopics[0]!
  const last = helpTopics[helpTopics.length - 1]!
  expect(neighbours(first.id)).toEqual({ previous: null, next: helpTopics[1] })
  expect(neighbours(last.id).next).toBeNull()
  expect(neighbours('onboard').previous?.id).toBe('prepare')
  expect(neighbours('nope')).toEqual({ previous: null, next: null })
})

test('the new block kinds are used and searchable', () => {
  const onboard = helpTopics.find((t) => t.id === 'onboard')!
  const flow = onboard.blocks.find((b) => b.type === 'flow')
  expect(flow && flow.type === 'flow' && flow.steps.map((s) => s.title)).toEqual(['连接账号', '补充资料', '确认上号'])
  const states = helpTopics.find((t) => t.id === 'after-submit')!.blocks.find((b) => b.type === 'table')
  expect(states && states.type === 'table' && states.rows.map((r) => r[0])).toEqual([
    '待上号',
    '上号中',
    '已上号',
    '上号失败',
    '结果待确认',
  ])
  expect(searchTopics(helpTopics, '处理并重试').map((t) => t.id)).toContain('after-submit')
})
