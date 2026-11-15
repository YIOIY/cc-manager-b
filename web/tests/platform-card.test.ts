import { expect, test } from 'vitest'
import {
  formatResetAt,
  formatTokens,
  locationLabel,
  paymentLabel,
  shortPlatformAlias,
  usageStale,
  usageTone,
} from '../src/model/platform-card'

test('usage tone grades the 5-hour window', () => {
  expect(usageTone(7)).toBe('ok')
  expect(usageTone(84)).toBe('warn')
  expect(usageTone(80)).toBe('warn')
  expect(usageTone(95)).toBe('danger')
  expect(usageTone(100)).toBe('danger')
  expect(usageTone(null)).toBe('none')
  expect(usageTone(undefined)).toBe('none')
})

test('reset times render in the given zone', () => {
  expect(formatResetAt('2026-11-11T16:50:00Z', 'Asia/Shanghai')).toBe('2026-11-12 00:50:00')
  expect(formatResetAt('2026-11-18T05:00:00Z', 'Asia/Shanghai')).toBe('2026-11-18 13:00:00')
  expect(formatResetAt('', 'Asia/Shanghai')).toBe('—')
  expect(formatResetAt(undefined, 'Asia/Shanghai')).toBe('—')
  expect(formatResetAt('not a date', 'Asia/Shanghai')).toBe('—')
})

test('usage is stale after thirty minutes', () => {
  const now = new Date('2026-11-11T12:00:00Z')
  expect(usageStale('2026-11-11T11:31:00Z', now)).toBe(false)
  expect(usageStale('2026-11-11T11:29:00Z', now)).toBe(true)
  expect(usageStale('', now)).toBe(true)
})

test('platform labels map known values and keep the rest', () => {
  expect(locationLabel('US')).toBe('美国')
  expect(locationLabel('JP')).toBe('JP')
  expect(locationLabel('')).toBe('—')
  expect(paymentLabel('google_pay')).toBe('GooglePay')
  expect(paymentLabel('apple_pay')).toBe('ApplePay')
  expect(paymentLabel('wechat')).toBe('wechat')
})

test('token counts use thousands separators', () => {
  expect(formatTokens(411638)).toBe('411,638')
  expect(formatTokens(0)).toBe('0')
  expect(formatTokens(null)).toBe('—')
})

test('shortPlatformAlias keeps provider and trailing id', () => {
  expect(shortPlatformAlias('claude-住享科智-20261113006')).toBe('claude-20261113006')
  expect(shortPlatformAlias('claude-abc-def-20261113006')).toBe('claude-20261113006')
  expect(shortPlatformAlias('claude-only')).toBe('claude-only')
  expect(shortPlatformAlias('')).toBe('')
})
