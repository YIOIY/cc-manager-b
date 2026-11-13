import { expect, test } from 'vitest'
import { fenToUsdCents, formatRate, formatUsdCents } from '../src/model/money'

test('RMB fen convert to US cents at the platform rate, rounded to the cent', () => {
  // ¥751.04 at the platform's fixed 7.2 is $104.31, as the platform console shows.
  expect(fenToUsdCents(75104, 7.2)).toBe(10431)
  expect(fenToUsdCents(78832, 7.2)).toBe(10949)
  expect(fenToUsdCents(0, 7.2)).toBe(0)
  expect(fenToUsdCents(720, 7.2)).toBe(100)
})

test('no usable rate means no dollar amount rather than a wrong one', () => {
  for (const rate of [null, undefined, 0, -1, Number.NaN, Number.POSITIVE_INFINITY])
    expect(fenToUsdCents(75104, rate as never)).toBeNull()
  expect(fenToUsdCents(Number.NaN, 7.2)).toBeNull()
})

test('dollar amounts and the rate are written the way the card shows them', () => {
  expect(formatUsdCents(10431)).toBe('$104.31')
  expect(formatUsdCents(123456789)).toBe('$1,234,567.89')
  expect(formatUsdCents(5)).toBe('$0.05')
  expect(formatRate(7.2)).toBe('7.2')
  expect(formatRate(7)).toBe('7')
  expect(formatRate(7.25)).toBe('7.25')
  expect(formatRate(7.123456)).toBe('7.1235')
})
