import { test } from 'vitest'
import assert from 'node:assert/strict'
import { filterRows, listConfigs } from '../src/list-model.ts'

const rows = [
  {
    id: 'a1',
    account: 'alpha@example.com',
    plan: 'claude/cc-pro',
    archived: false,
    proxy_id: 'p1',
    platform_status: 'online',
    plan_end_time: '2026-12-01',
    created_at: '2026-11-01T10:00:00Z',
    platform_requests: 5,
  },
  {
    id: 'b2',
    account: 'beta@example.com',
    plan: 'claude/cc-max-20x',
    archived: true,
    proxy_id: '',
    platform_status: '',
    plan_end_time: '2026-11-15',
    created_at: '2026-10-01T10:00:00Z',
    platform_requests: 50,
  },
]
const accounts = listConfigs.accounts

test('keyword search matches every word across the configured fields', () => {
  assert.deepEqual(
    filterRows(rows, accounts, 'ALPHA online', {}).map((r) => r.id),
    ['a1'],
  )
  assert.deepEqual(filterRows(rows, accounts, 'example', {}).length, 2)
  assert.deepEqual(filterRows(rows, accounts, 'gamma', {}), [])
})

test('select, presence, date and number filters combine with AND semantics', () => {
  assert.deepEqual(
    filterRows(rows, accounts, '', { archived: 'true' }).map((r) => r.id),
    ['b2'],
  )
  assert.deepEqual(
    filterRows(rows, accounts, '', { proxy: 'present' }).map((r) => r.id),
    ['a1'],
  )
  assert.deepEqual(
    filterRows(rows, accounts, '', { proxy: 'absent' }).map((r) => r.id),
    ['b2'],
  )
  assert.deepEqual(
    filterRows(rows, accounts, '', { end_from: '2026-11-20' }).map((r) => r.id),
    ['a1'],
  )
  assert.deepEqual(
    filterRows(rows, accounts, '', { end_to: '2026-11-20' }).map((r) => r.id),
    ['b2'],
  )
  assert.deepEqual(filterRows(rows, accounts, '', { created_from: '2026-10-15', archived: 'true' }), [])
})

test('empty conditions are ignored and missing values never match a set filter', () => {
  assert.equal(filterRows(rows, accounts, '', { plan: '', platform_status: null, archived: undefined }).length, 2)
  assert.deepEqual(
    filterRows(rows, accounts, '', { platform_status: 'online' }).map((r) => r.id),
    ['a1'],
  )
  assert.deepEqual(filterRows(rows, accounts, '', { platform_status: 'on' }), [])
})

test('accounts list exposes abnormal toggle and platform status select', () => {
  assert.equal(accounts.abnormalToggle, true)
  const status = accounts.filters.find((f) => f.key === 'platform_status')
  assert.equal(status?.type, 'select')
  assert.ok(status?.options?.some((o) => o.value === 'login_failed'))
})
