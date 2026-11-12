import { expect, test } from 'vitest'
import { accountQuery, attentionLabel, spendBars } from '../src/model/dashboard'
import type { AttentionItem } from '../src/types/api'

const base: AttentionItem = {
  kind: 'near_limit',
  severity: 'warn',
  account_id: 'acc',
  account: 'jo@example.test',
  alias: 'claude-x',
  label: '5 小时窗口 88%',
  detail: '重置 00:50',
  upstream_id: '428802',
  reset_at: '',
  expires_at: '',
  value: 88,
}

test('attention labels render each kind in Chinese', () => {
  expect(attentionLabel(base)).toBe('5 小时窗口 88%')
  expect(attentionLabel({ ...base, kind: 'banned', label: 'ban', detail: '账号被暂停' })).toBe('已封禁')
  expect(attentionLabel({ ...base, kind: 'banned', label: 'check_fail' })).toBe('验证失败')
  expect(attentionLabel({ ...base, kind: 'expiring', label: '套餐 12-04 到期（22 天）', value: 22 })).toBe(
    '套餐 12-04 到期（22 天）',
  )
  expect(attentionLabel({ ...base, kind: 'expiring', label: '已到期 3 天', value: -3 })).toBe('已到期 3 天')
  expect(attentionLabel({ ...base, kind: 'missing', label: '平台已移除' })).toBe('平台已移除')
  expect(attentionLabel({ ...base, kind: 'profile_diff', label: '资料与平台不一致' })).toBe('资料与平台不一致')
})

test('clicking an item searches the accounts page by email', () => {
  expect(accountQuery(base)).toEqual({ q: 'jo@example.test' })
  expect(accountQuery({ ...base, account: '', upstream_id: '428802' })).toEqual({ q: '428802' })
})

test('spend bars scale to the largest day and never divide by zero', () => {
  expect(
    spendBars([
      { date: 'a', cost_fen: 0, final: true },
      { date: 'b', cost_fen: 100, final: true },
      { date: 'c', cost_fen: 50, final: false },
    ]),
  ).toEqual([0, 100, 50])
  expect(
    spendBars([
      { date: 'a', cost_fen: 0, final: true },
      { date: 'b', cost_fen: 0, final: false },
    ]),
  ).toEqual([0, 0])
  expect(spendBars([])).toEqual([])
})
