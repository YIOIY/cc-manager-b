import { expect, test } from 'vitest'
import { deliveryDescription, outcomeText } from '../src/account-workflow'

test('outcomeText prefers the stored platform message', () => {
  expect(
    outcomeText({
      code: 'PLATFORM_REJECTED',
      message: '平台拒绝了本次上号，平台返回：账号重复录入,id:563921,平台账号id:25890',
    }),
  ).toContain('账号重复录入')
})

test('deliveryDescription surfaces job detail for failed cards', () => {
  expect(
    deliveryDescription('failed', {
      code: 'PLATFORM_REJECTED',
      message: '平台拒绝了本次上号，平台返回：账号重复录入,id:563921,平台账号id:25890',
      hint: '请核对账号资料及平台配置后重试',
    }),
  ).toBe('平台拒绝了本次上号，平台返回：账号重复录入,id:563921,平台账号id:25890（请核对账号资料及平台配置后重试）')
  expect(deliveryDescription('failed')).toContain('资料已保留')
  expect(deliveryDescription('pending')).toContain('还未提交')
})
