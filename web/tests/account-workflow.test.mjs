import { test } from 'vitest'
import assert from 'node:assert/strict'
import { deliveryState, deliveryStates, runtimeLabel } from '../src/account-workflow.ts'

test('usage failures never turn accepted accounts into undelivered inventory', () => {
  assert.equal(
    deliveryState({ onboarding_state: 'failed', platform_links: [{ ingestion_state: 'accepted' }] }),
    'linked',
  )
  assert.equal(deliveryStates.linked.action, '查看详情')
  assert.equal(runtimeLabel('using'), '使用中')
})
test('unsubmitted, pending and uncertain accounts offer distinct actions', () => {
  assert.equal(deliveryState({}), 'pending')
  assert.equal(deliveryStates.pending.label, '待上号')
  assert.equal(deliveryStates.submitting.action, '查看进度')
  assert.equal(deliveryStates.unknown.action, '查看详情')
  assert.equal(deliveryStates.failed.action, '处理并重试')
})

test('job outcome text prefers the stored message and falls back to generated copy', async () => {
  const { outcomeText } = await import('../src/account-workflow.ts')
  assert.equal(
    outcomeText({ code: 'PLATFORM_REJECTED', message: '平台拒绝了本次上号，平台返回：x' }),
    '平台拒绝了本次上号，平台返回：x',
  )
  assert.equal(outcomeText({ code: 'PLATFORM_REJECTED' }), '平台拒绝了本次上号')
  assert.equal(outcomeText({ code: 'NO_SUCH' }), '请核对账号资料与平台配置')
  assert.equal(outcomeText(undefined), '请核对账号资料与平台配置')
})
