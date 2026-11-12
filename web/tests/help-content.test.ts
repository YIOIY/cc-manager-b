import { expect, test } from 'vitest'
import { helpGroups, helpTopics, searchTopics, topicText } from '../src/help/content'
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
