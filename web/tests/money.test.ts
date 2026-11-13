import { expect, test } from 'vitest'
import { describeRate, fenToUsdCents, formatUsdCents, rateIsStale, rateSourceLabel } from '../src/model/money'

test('RMB fen convert to US cents at the given rate, rounded to the cent', () => {
  expect(fenToUsdCents(75104, 6.6683)).toBe(11263)
  expect(fenToUsdCents(0, 6.6683)).toBe(0)
  expect(fenToUsdCents(1, 6.6683)).toBe(0)
  expect(fenToUsdCents(667, 6.67)).toBe(100)
})

test('no usable rate means no dollar amount rather than a wrong one', () => {
  for (const rate of [null, undefined, 0, -1, Number.NaN, Number.POSITIVE_INFINITY])
    expect(fenToUsdCents(75104, rate as never)).toBeNull()
  expect(fenToUsdCents(Number.NaN, 6.6683)).toBeNull()
})

test('dollar amounts and the rate are written the way the card shows them', () => {
  expect(formatUsdCents(11263)).toBe('$112.63')
  expect(formatUsdCents(123456789)).toBe('$1,234,567.89')
  expect(formatUsdCents(5)).toBe('$0.05')
  expect(describeRate(6.6683)).toBe('1 美元 = 6.6683 人民币')
  expect(describeRate(7)).toBe('1 美元 = 7.0000 人民币')
  expect(rateSourceLabel('ecb')).toBe('欧洲央行参考汇率')
  expect(rateSourceLabel('open-er-api')).toBe('open.er-api.com')
  expect(rateSourceLabel('')).toBe('')
})

test('a rate not refreshed for more than three days is flagged', () => {
  const now = new Date('2026-11-13T12:00:00Z')
  expect(rateIsStale('2026-11-13T06:00:00Z', now)).toBe(false)
  expect(rateIsStale('2026-11-10T13:00:00Z', now)).toBe(false)
  expect(rateIsStale('2026-11-10T11:00:00Z', now)).toBe(true)
  expect(rateIsStale('', now)).toBe(true)
})
