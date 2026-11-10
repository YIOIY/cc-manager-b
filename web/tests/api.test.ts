import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { api, ApiError, onUnauthorized, session } from '../src/api'

const fetchMock = vi.fn()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  session.value = { authenticated: true, csrf_token: 'csrf-123' }
})
afterEach(() => {
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

const reply = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

test('mutating requests carry the CSRF token and credentials; GET does not send it', async () => {
  fetchMock.mockImplementation(() => reply(200, { data: { ok: true } }))
  await api('/accounts', { method: 'POST', body: '{}' })
  await api('/accounts')
  const [postUrl, postInit] = fetchMock.mock.calls[0] as [string, RequestInit]
  const [, getInit] = fetchMock.mock.calls[1] as [string, RequestInit]
  expect(postUrl).toBe('/api/v1/accounts')
  expect(postInit.credentials).toBe('include')
  expect((postInit.headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-123')
  expect((getInit.headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined()
})

test('error envelopes become ApiError with the server code and message', async () => {
  fetchMock.mockImplementation(() => reply(409, { error: { code: 'VERSION_CONFLICT', message: '已更新' } }))
  await expect(api('/accounts/x', { method: 'PATCH', body: '{}' })).rejects.toMatchObject({
    status: 409,
    code: 'VERSION_CONFLICT',
    message: '已更新',
  })
})

test('a 401 marks the session unauthenticated and notifies listeners, except for the login call itself', async () => {
  const listener = vi.fn()
  const off = onUnauthorized(listener)
  fetchMock.mockImplementation(() => reply(401, { error: { code: 'AUTH_REQUIRED', message: '请先登录' } }))
  await expect(api('/auth/login', { method: 'POST', body: '{}' })).rejects.toBeInstanceOf(ApiError)
  expect(listener).not.toHaveBeenCalled()
  await expect(api('/dashboard')).rejects.toBeInstanceOf(ApiError)
  expect(listener).toHaveBeenCalledTimes(1)
  expect(session.value?.authenticated).toBe(false)
  off()
})

test('network failures surface as a readable ApiError instead of a raw TypeError', async () => {
  fetchMock.mockImplementation(() => Promise.reject(new TypeError('Failed to fetch')))
  await expect(api('/dashboard')).rejects.toMatchObject({ code: 'NETWORK' })
})

test('login retries once with a fresh anonymous session when the CSRF token is stale', async () => {
  const { useSession } = await import('../src/composables/useSession')
  session.value = { authenticated: false, csrf_token: 'stale' }
  let logins = 0
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).endsWith('/auth/login')) {
      logins++
      const token = (init?.headers as Record<string, string>)['X-CSRF-Token']
      return token === 'fresh'
        ? reply(200, { data: { authenticated: true, csrf_token: 'fresh' } })
        : reply(403, { error: { code: 'CSRF_FAILED', message: '会话验证失败' } })
    }
    return reply(200, {
      data: {
        authenticated: logins > 0,
        csrf_token: 'fresh',
        user: logins > 0 ? { id: 'u', username: 'admin', roles: [], permissions: [], is_admin: true } : undefined,
      },
    })
  })
  await useSession().login('admin', 'secret-password-123')
  expect(logins).toBe(2)
  expect(useSession().authenticated.value).toBe(true)
})

test('the extended error envelope and request id reach the ApiError', async () => {
  fetchMock.mockImplementation(() =>
    Promise.resolve(
      new Response(
        JSON.stringify({
          error: {
            code: 'VALIDATION_FAILED',
            category: 'validation',
            message: '有 1 项资料不符合要求',
            hint: '请修改',
            retryable: false,
            action: 'fix_input',
            fields: [{ field: 'password', code: 'REQUIRED', message: '缺少密码' }],
            details: { resource: 'account' },
          },
          request_id: 'req-body',
        }),
        { status: 422, headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req-header' } },
      ),
    ),
  )
  const err = (await api('/accounts', { method: 'POST', body: '{}' }).catch((e) => e)) as ApiError
  expect(err).toBeInstanceOf(ApiError)
  expect(err.fields).toEqual([{ field: 'password', code: 'REQUIRED', message: '缺少密码' }])
  expect(err.requestId).toBe('req-body')
  expect(err.hint).toBe('请修改')
  expect(err.action).toBe('fix_input')
  expect(err.details).toEqual({ resource: 'account' })
})

test('without a body request id the header is used', async () => {
  fetchMock.mockImplementation(() =>
    Promise.resolve(
      new Response(JSON.stringify({ error: { code: 'NOT_FOUND', message: '账号不存在' } }), {
        status: 404,
        headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req-header' },
      }),
    ),
  )
  const err = (await api('/accounts/x').catch((e) => e)) as ApiError
  expect(err.requestId).toBe('req-header')
  expect(err.category).toBe('not_found')
})

test('a 429 notifies rate-limit listeners with the wait seconds', async () => {
  const { onRateLimited } = await import('../src/api')
  const listener = vi.fn()
  const off = onRateLimited(listener)
  fetchMock.mockImplementation(() =>
    Promise.resolve(
      new Response(
        JSON.stringify({
          error: { code: 'RATE_LIMITED', message: '操作过于频繁', details: { retry_after_seconds: 7 } },
        }),
        { status: 429, headers: { 'Content-Type': 'application/json', 'Retry-After': '7' } },
      ),
    ),
  )
  await expect(api('/proxies', { method: 'POST', body: '{}' })).rejects.toMatchObject({ code: 'RATE_LIMITED' })
  expect(listener).toHaveBeenCalledWith(7)
  off()
})
