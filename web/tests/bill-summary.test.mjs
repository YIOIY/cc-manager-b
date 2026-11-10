import { test } from 'vitest'
import assert from 'node:assert/strict'
import { eligibleBillAccount, summaryValidation } from '../src/bill-summary.ts'

test('interval query requires 1–100 numeric delivery IDs and both valid dates', () => {
  const today = '2026-11-06',
    dates = ['2026-08-08', today]
  assert.equal(summaryValidation('conn', ['385753'], dates, today), '')
  assert.equal(summaryValidation('conn', ['385753'], ['2026-11-06', '2026-11-07'], today), '')
  for (const [connection, ids, range] of [
    ['', ['1'], dates],
    ['conn', [], dates],
    ['conn', Array.from({ length: 101 }, (_, i) => String(i + 1)), dates],
    ['conn', ['1', '1'], dates],
    ['conn', ['aid-x'], dates],
    ['conn', ['1'], []],
    ['conn', ['1'], ['2026-02-30', today]],
    ['conn', ['1'], ['2026-08-07', today]],
    ['conn', ['1'], [today, '2026-11-05']],
  ])
    assert.notEqual(summaryValidation(connection, ids, range, today), '')
})

test('only linked accounts with exact usable delivery IDs enter query selection', () => {
  const account = {
    id: 'local',
    account: 'demo@example.test',
    alias: '',
    supplier_account_id: '385753',
    archived: false,
    linked: true,
  }
  assert.equal(eligibleBillAccount(account), true)
  assert.equal(eligibleBillAccount({ ...account, linked: false }), false)
  for (const supplier_account_id of ['', '0', '-1', '1.5', '9007199254740993'])
    assert.equal(eligibleBillAccount({ ...account, supplier_account_id }), false)
})
