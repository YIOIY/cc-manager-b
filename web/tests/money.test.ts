import { expect, test } from 'vitest'
import { formatRate, formatUsd, formatUsdCents } from '../src/model/money'

test('API dollar strings and cent integers render as the card shows them', () => {
  expect(formatUsd('104.31')).toBe('$104.31')
  expect(formatUsd('0.05')).toBe('$0.05')
  expect(formatUsd('2067.05')).toBe('$2,067.05')
  expect(formatUsd(null)).toBe('—')
  expect(formatUsd('')).toBe('—')
  expect(formatUsdCents(10431)).toBe('$104.31')
  expect(formatUsdCents(123456789)).toBe('$1,234,567.89')
  expect(formatUsdCents(5)).toBe('$0.05')
  expect(formatRate(7.2)).toBe('7.2')
  expect(formatRate(7)).toBe('7')
  expect(formatRate(7.25)).toBe('7.25')
  expect(formatRate(7.123456)).toBe('7.1235')
})
