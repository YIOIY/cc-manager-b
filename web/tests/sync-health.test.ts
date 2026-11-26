import { expect, test } from 'vitest'
import { describeMetricsHealth, describeSyncHealth } from '../src/model/sync-health'

const base = { today_requests: 0, today_cost_usd_cents: 0, last_sync_at: '', last_sync_state: '' }

test('never synced reads as not started', () => {
  expect(describeSyncHealth({ ...base, sync_status: 'never' })).toEqual({ tone: 'info', label: '尚未同步' })
})

test('a healthy sync is green with no detail', () => {
  const v = describeSyncHealth({
    ...base,
    sync_status: 'ok',
    last_sync_state: 'succeeded',
    last_sync_at: '2026-11-10T10:00:00Z',
  })
  expect(v.tone).toBe('success')
  expect(v.label).toBe('正常')
  expect(v.detail).toBeUndefined()
})

test('a failing sync explains why, how often and when it retries', () => {
  const v = describeSyncHealth({
    ...base,
    sync_status: 'failing',
    last_sync_state: 'partial',
    error_code: 'PLATFORM_QUERY_DENIED',
    error_message: '平台拒绝查询这些账号的数据',
    error_hint: '请联系供应商核实 OpenAPI 令牌的查询权限',
    consecutive_failures: 2399,
    next_attempt_at: '2026-11-10T11:59:00Z',
  })
  expect(v.tone).toBe('danger')
  expect(v.label).toBe('同步异常')
  expect(v.detail).toMatch(/^平台拒绝查询这些账号的数据 · 连续失败 2399 次 · 下次重试 \d{2}:\d{2}$/)
  expect(v.hint).toBe('请联系供应商核实 OpenAPI 令牌的查询权限')
})

test('a failing sync without message falls back to generated copy and omits unknown retry time', () => {
  const v = describeSyncHealth({
    ...base,
    sync_status: 'failing',
    error_code: 'PLATFORM_UNAVAILABLE',
    consecutive_failures: 1,
  })
  expect(v.detail).toBe('平台暂时无法访问 · 连续失败 1 次')
})

test('older servers without sync_status still map the raw state', () => {
  expect(describeSyncHealth({ ...base, last_sync_state: 'partial', last_sync_at: 'x' }).label).toBe('同步异常')
  expect(describeSyncHealth({ ...base, last_sync_state: 'succeeded', last_sync_at: 'x' }).label).toBe('正常')
})

test('the web channel health reads like the metrics health', () => {
  const v = describeSyncHealth({
    ...base,
    sync_status: 'failing',
    last_sync_state: 'failed',
    error_code: 'WEB_LOGIN_FAILED',
    consecutive_failures: 3,
  })
  expect(v.tone).toBe('danger')
  expect(v.label).toBe('同步异常')
  expect(v.detail).toContain('网页后台登录失败')
  expect(v.detail).toContain('连续失败 3 次')
})

test('the OpenAPI metrics sync reads as not enabled when the platform denies queries', () => {
  const v = describeMetricsHealth({
    ...base,
    sync_status: 'failing',
    last_sync_state: 'partial',
    error_code: 'PLATFORM_QUERY_DENIED',
    consecutive_failures: 16,
  })
  expect(v.tone).toBe('info')
  expect(v.label).toBe('平台未开通')
  expect(v.detail).not.toContain('连续失败')
  const other = describeMetricsHealth({
    ...base,
    sync_status: 'failing',
    last_sync_state: 'failed',
    error_code: 'WEB_LOGIN_FAILED',
  })
  expect(other.tone).toBe('danger')
})
