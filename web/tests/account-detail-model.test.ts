import { expect, test } from 'vitest'
import {
  dailySpend,
  downsample,
  mergeActivity,
  canEditPlatformProfile,
  type UsageSnapshot,
} from '../src/model/account-detail'
import type { AuditEvent, Job } from '../src/types/api'

const snap = (at: string, cost: number): UsageSnapshot => ({
  observed_at: at,
  cost_fen: cost,
  input_tokens: 0,
  output_tokens: 0,
  five_hour_used_percent: 0,
  five_hour_reset_at: '',
  week_used_percent: 0,
  week_reset_at: '',
})

test('daily spend is the difference between consecutive days last cumulative cost', () => {
  const list = [
    snap('2026-11-18T03:00:00', 100),
    snap('2026-11-18T20:00:00', 400),
    snap('2026-11-19T04:00:00', 450),
    snap('2026-11-19T21:00:00', 1000),
    snap('2026-11-20T10:00:00', 1000),
  ]
  // 11-18 has no baseline; 11-19 spent 1000-400, 11-20 spent nothing.
  expect(dailySpend(list)).toEqual([
    { date: '2026-11-19', fen: 600 },
    { date: '2026-11-20', fen: 0 },
  ])
})

test('a platform-side reset never produces negative spend', () => {
  const list = [snap('2026-11-18T20:00:00', 900), snap('2026-11-19T20:00:00', 50)]
  expect(dailySpend(list)).toEqual([{ date: '2026-11-19', fen: 0 }])
  expect(dailySpend([snap('2026-11-18T20:00:00', 900)])).toEqual([])
})

test('downsample keeps short series, thins long ones and always keeps the last point', () => {
  const short = [1, 2, 3]
  expect(downsample(short, 10)).toBe(short)
  const long = Array.from({ length: 1000 }, (_, i) => i)
  const thin = downsample(long, 240)
  expect(thin).toHaveLength(240)
  expect(thin[0]).toBe(0)
  expect(thin.at(-1)).toBe(999)
})

test('activity merges audit events and jobs newest first, labels them and caps the list', () => {
  const events = [
    {
      id: 1,
      action: 'account.assign',
      resource_type: 'account',
      resource_id: 'a',
      actor: 'admin',
      detail: 'to u1',
      created_at: '2026-11-20T10:00:00Z',
    },
    {
      id: 2,
      action: 'account.something_new',
      resource_type: 'account',
      resource_id: 'a',
      actor: 'system',
      detail: '',
      created_at: '2026-11-19T10:00:00Z',
    },
  ] as AuditEvent[]
  const jobs = [
    {
      id: 'j1',
      state: 'failed',
      code: 'PLATFORM_REJECTED',
      message: '平台拒绝',
      created_at: '2026-11-20T09:00:00Z',
      updated_at: '2026-11-20T09:05:00Z',
    },
    {
      id: 'j0',
      state: 'succeeded',
      code: '',
      message: '',
      created_at: '2026-11-18T09:00:00Z',
      updated_at: '2026-11-18T09:01:00Z',
    },
  ] as Job[]
  const items = mergeActivity(events, jobs)
  expect(items.map((i) => i.key)).toEqual(['a1', 'jj1', 'a2', 'jj0'])
  expect(items[0]).toMatchObject({ label: '分配给用户', actor: 'admin', detail: 'to u1' })
  expect(items[1]).toMatchObject({ label: '上号任务失败', tone: 'bad', detail: '平台拒绝' })
  expect(items[2]!.label).toBe('account.something_new') // unknown actions stay visible
  expect(mergeActivity(events, jobs, 2)).toHaveLength(2)
})

test('the edit-platform rule needs ownership, permission and a live console channel', () => {
  const access = { owns: () => true, can: (p: string) => p === 'accounts.platform_edit' }
  const card = {
    created_by_user_id: 'u',
    platform: { missing_since: '', web_channel: { configured: true, suspended: false } },
  }
  expect(canEditPlatformProfile(card as never, access)).toBe(true)
  expect(canEditPlatformProfile({ ...card, platform: { ...card.platform, missing_since: 'x' } } as never, access)).toBe(
    false,
  )
  expect(canEditPlatformProfile({ ...card, platform: null } as never, access)).toBe(false)
  expect(canEditPlatformProfile(card as never, { ...access, can: () => false })).toBe(false)
})
