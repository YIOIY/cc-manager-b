import { expect, test } from 'vitest'
import {
  accountQuery,
  attentionLabel,
  dailyAverageCents,
  dayChange,
  hourlyBars,
  hourlySummary,
  jobOutcome,
  shareSegments,
  spendBars,
} from '../src/model/dashboard'
import type { AttentionItem, HourlyCost, RecentJob, SpendDay } from '../src/types/api'

function day(date: string, cost_usd_cents: number, final: boolean): SpendDay {
  return { date, cost_usd_cents, cost_usd: (cost_usd_cents / 100).toFixed(2), final }
}

function hour(hour: string, cost_usd_cents: number): HourlyCost {
  return { hour, cost_usd_cents, cost_usd: (cost_usd_cents / 100).toFixed(2) }
}

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
  expect(spendBars([day('a', 0, true), day('b', 100, true), day('c', 50, false)])).toEqual([0, 100, 50])
  expect(spendBars([day('a', 0, true), day('b', 0, false)])).toEqual([0, 0])
  expect(spendBars([])).toEqual([])
})

test('the day-over-day change compares the last two complete days', () => {
  const days = [day('11-10', 100, true), day('11-11', 150, true), day('11-12', 30, false)]
  expect(dayChange(days)).toEqual({ percent: 50, up: true })
  expect(dayChange([day('a', 200, true), day('b', 150, true)])).toEqual({
    percent: 25,
    up: false,
  })
  expect(dayChange([day('a', 0, true), day('b', 150, true)])).toBeNull()
  expect(dayChange(days.slice(1))).toBeNull()
  expect(dailyAverageCents(days)).toBe(125)
  expect(dailyAverageCents([day('x', 30, false)])).toBe(0)
})

test('share segments keep the given order, drop empty groups and add up to the whole', () => {
  expect(shareSegments({ ban: 2, using: 9, check_fail: 0 }, ['using', 'ban'])).toEqual([
    { key: 'using', count: 9, percent: 81.8 },
    { key: 'ban', count: 2, percent: 18.2 },
  ])
  expect(shareSegments({ b: 1, a: 3 })).toEqual([
    { key: 'a', count: 3, percent: 75 },
    { key: 'b', count: 1, percent: 25 },
  ])
  expect(shareSegments({})).toEqual([])
})

test('hourly bars scale to the busiest hour and the summary names it', () => {
  const hours = [
    hour('2026-11-13T01:00:00Z', 200),
    hour('2026-11-13T02:00:00Z', 0),
    hour('2026-11-13T03:00:00Z', 2),
    hour('2026-11-13T04:00:00Z', 100),
  ]
  expect(hourlyBars(hours).map((b) => b.height)).toEqual([100, 0, 3, 50])
  expect(hourlySummary(hours)).toEqual({ total: 302, peak: 200, peakHour: '2026-11-13T01:00:00Z' })
  expect(hourlyBars([hour('x', 0)]).map((b) => b.height)).toEqual([0])
  expect(hourlySummary([])).toEqual({ total: 0, peak: 0, peakHour: null })
})

test('a recent onboarding job reads as its outcome', () => {
  const job: RecentJob = {
    id: 'j',
    account_id: 'a',
    account: 'x@example.test',
    connection: 'TX',
    state: 'succeeded',
    code: 'ACCEPTED_PENDING_VERIFICATION',
    updated_at: '',
  }
  expect(jobOutcome(job)).toEqual({ label: '平台已接收', tone: 'success' })
  expect(jobOutcome({ ...job, state: 'failed', code: 'PLATFORM_AUTH_FAILED' })).toEqual({
    label: '平台拒绝了本次请求的身份验证',
    tone: 'danger',
  })
  expect(jobOutcome({ ...job, state: 'failed', code: '' })).toEqual({ label: '上号失败', tone: 'danger' })
  expect(jobOutcome({ ...job, state: 'queued', code: '' })).toEqual({ label: '提交中', tone: 'info' })
  expect(jobOutcome({ ...job, state: 'unknown', code: '' })).toEqual({ label: '结果待确认', tone: 'warning' })
})
