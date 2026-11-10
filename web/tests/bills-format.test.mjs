import { test } from 'vitest'
import assert from 'node:assert/strict'
import { formatBillCount, formatBillMoney, shiftBillDay, businessDay } from '../src/bills-format.ts'

test('large bill integers and cents preserve their exact value', () => {
  assert.equal(formatBillCount('9007199254740993'), '9,007,199,254,740,993')
  assert.equal(formatBillMoney('9007199254740993'), '¥90,071,992,547,409.93')
  assert.equal(formatBillMoney('1'), '¥0.01')
  assert.equal(formatBillMoney('0'), '¥0.00')
  assert.equal(formatBillMoney('-1'), '-¥0.01')
  assert.equal(formatBillCount(null), '—')
  assert.equal(formatBillMoney(undefined), '—')
})
test('calendar shortcuts preserve leap days and date boundaries', () => {
  assert.equal(shiftBillDay('2024-02-28', 1), '2024-02-29')
  assert.equal(shiftBillDay('2026-11-05', -90), '2026-08-07')
  assert.equal(shiftBillDay('2026-01-01', -1), '2025-12-31')
  assert.match(businessDay('Asia/Shanghai'), /^\d{4}-\d{2}-\d{2}$/)
})
