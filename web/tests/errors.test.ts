import { expect, test } from 'vitest'
import { ApiError } from '../src/api'
import { applyFieldErrors, describeError, errorClipboardText } from '../src/errors'

test('ApiError exposes the extended envelope and infers category when absent', () => {
  const e = new ApiError(422, 'VALIDATION_FAILED', '有 2 项资料不符合要求', {
    category: 'validation',
    fields: [{ field: 'auth_json.refresh_token', code: 'REQUIRED', message: '缺少刷新令牌' }],
    requestId: 'r1',
  })
  expect(e.fields).toHaveLength(1)
  expect(e.requestId).toBe('r1')
  expect(e.category).toBe('validation')
  expect(e.action).toBe('none')
  expect(e.retryable).toBe(false)
  expect(new ApiError(409, 'VERSION_CONFLICT', 'x').category).toBe('conflict')
  expect(new ApiError(401, 'AUTH_REQUIRED', 'x').category).toBe('auth')
  expect(new ApiError(404, 'NOT_FOUND', 'x').category).toBe('not_found')
  expect(new ApiError(429, 'RATE_LIMITED', 'x').category).toBe('rate_limit')
  expect(new ApiError(503, 'DB_ERROR', 'x').category).toBe('internal')
  expect(new ApiError(0, 'NETWORK', 'x').category).toBe('internal')
  expect(new ApiError(400, 'INVALID_JSON', 'x').category).toBe('validation')
})

test('describeError and applyFieldErrors map an envelope onto a form', () => {
  const view = describeError(
    new ApiError(422, 'VALIDATION_FAILED', '有 2 项资料不符合要求', {
      hint: '请修改标红的字段',
      action: 'fix_input',
      fields: [
        { field: 'auth_json.refresh_token', code: 'REQUIRED', message: '缺少刷新令牌' },
        { field: 'plan_end_time', code: 'OUT_OF_RANGE', message: '套餐结束日期须至少晚于开始日期一天' },
      ],
      requestId: 'r2',
    }),
  )
  expect(view.title).toBe('有 2 项资料不符合要求')
  expect(view.detail).toBe('请修改标红的字段')
  expect(view.requestId).toBe('r2')
  expect(view.action).toBe('fix_input')
  expect(view.code).toBe('VALIDATION_FAILED')
  expect(applyFieldErrors(view.fields, { 'auth_json.refresh_token': 'refresh_token' })).toEqual({
    refresh_token: '缺少刷新令牌',
    plan_end_time: '套餐结束日期须至少晚于开始日期一天',
  })
  expect(describeError(new Error('boom')).title).toBe('boom')
  expect(describeError('x', '操作失败').title).toBe('操作失败')
  expect(describeError(new Error('')).title).toBe('操作失败')
  const text = errorClipboardText(view)
  expect(text).toContain('VALIDATION_FAILED')
  expect(text).toContain('请求编号 r2')
})
