import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ClaudeOAuthPanel from '../src/components/ClaudeOAuthPanel.vue'
import { session } from '../src/api'

vi.mock('../src/composables/useSession', () => ({
  useSession: () => ({
    check: async () => {},
    onboardAllowed: () => true,
  }),
}))

const fetchMock = vi.fn()
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const pending = {
  id: 'flow',
  authorization_url: 'https://claude.com/cai/oauth/authorize?state=test',
  expires_at: '2026-11-09T15:00:00Z',
}
const stubs = {
  'el-button': {
    template: '<button :disabled="disabled || loading" @click="$emit(\'click\')"><slot /></button>',
    props: ['disabled', 'loading'],
  },
  'el-input': {
    template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
    props: ['modelValue'],
  },
  'el-alert': { template: '<div>{{ title }}</div>', props: ['title'] },
}
beforeEach(() => {
  session.value = { authenticated: true, csrf_token: 'csrf' }
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

test('generates a link with the selected proxy, emits credentials, and clears the pasted result', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ data: { auth_json: { access_token: 'fixture-token' }, warnings: [] } }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'proxy' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ proxy_id: 'proxy' })
  expect(wrapper.get('a').attributes('rel')).toContain('noopener')
  await wrapper.get('#claude-authorization-result').setValue('code#test')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ result: 'code#test' })
  expect(wrapper.emitted('authorized')?.[0]?.[0]).toEqual({
    auth_json: { access_token: 'fixture-token' },
    warnings: [],
  })
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('fixture-token')
  wrapper.unmount()
})

test('invalid input clears only the paste; a supplier failure retains the corrected paste and session', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ error: { code: 'OAUTH_RESULT_INVALID', message: '结果不匹配' } }, 422))
    .mockResolvedValueOnce(
      json({ error: { code: 'OAUTH_EXCHANGE_FAILED', message: 'Claude 暂时失败，请稍后重试' } }, 502),
    )
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper.get('#claude-authorization-result').setValue('wrong')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('')
  await wrapper.get('#claude-authorization-result').setValue('corrected')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('corrected')
  expect(wrapper.text()).toContain('Claude 暂时失败，请稍后重试')
  expect(fetchMock.mock.calls[2]![0]).toBe('/api/v1/claude-oauth/sessions/flow/exchange')
  wrapper.unmount()
})

test('rate-limited exchange keeps the link, disables the confirm button and counts down', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending })).mockResolvedValueOnce(
    json(
      {
        error: {
          code: 'OAUTH_RATE_LIMITED',
          message: 'Claude 授权暂时限流，请约 45 秒后再试',
          action: 'wait',
          details: { retry_after_seconds: 45 },
        },
      },
      429,
    ),
  )
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper.get('#claude-authorization-result').setValue('code#test')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect(wrapper.text()).toContain('请约 45 秒后再试')
  expect(wrapper.get('.oauth-exchange').text()).toContain('45s')
  expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeDefined()
  wrapper.unmount()
})

test('a cooldown start reuses the live link and counts the wait down', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: { ...pending, retry_after_seconds: 5 } }))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect((wrapper.get('#claude-authorization-url').element as HTMLInputElement).value).toBe(pending.authorization_url)
  await wrapper.get('#claude-authorization-result').setValue('code#test')
  expect(wrapper.get('.oauth-exchange').text()).toContain('5s')
  expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeDefined()
  wrapper.unmount()
})

test('identity rejection retains the link and paste without an expiry flag', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ error: { code: 'OAUTH_IDENTITY_REJECTED', message: '授权身份与预期不符' } }, 403))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper.get('#claude-authorization-result').setValue('code#test')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('授权身份与预期不符')
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('code#test')
  wrapper.unmount()
})

test('refreshing the link asks the server for a new session; the initial login does not', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ data: { ...pending, id: 'flow-2' } }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'proxy' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ proxy_id: 'proxy' })
  const refresh = wrapper.findAll('button').find((b) => b.text() === '作废并重新获取')
  await refresh!.trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ proxy_id: 'proxy', refresh: true })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  wrapper.unmount()
})

test('an existing account can authorize through its saved proxy without exposing the proxy URL', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, {
    props: { savedAccountId: 'existing' },
    global: { stubs },
  })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({ proxy_id: '', account_id: 'existing' })
  wrapper.unmount()
})

test('closing during link generation cancels the late session and never fills the next form', async () => {
  let resolve!: (response: Response) => void
  fetchMock
    .mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r
        }),
    )
    .mockResolvedValue(json({ data: { cancelled: true } }))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  wrapper.unmount()
  resolve(json({ data: pending }))
  await flushPromises()
  expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/claude-oauth/sessions/flow')
  expect(fetchMock.mock.calls[1]![1].method).toBe('DELETE')
  expect(wrapper.emitted('authorized')).toBeUndefined()
})

test('a summary collapses the panel into an authorized card until re-authorization starts', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, {
    props: {
      summary: { email: 'demo@example.com', organization: 'Demo Org', plan: 'Max 20x', expiry: '令牌 8 小时后到期' },
    },
    global: { stubs },
  })
  expect(wrapper.find('.oauth-done').exists()).toBe(true)
  expect(wrapper.get('.oauth-done-email').text()).toBe('demo@example.com')
  expect(wrapper.findAll('.oauth-done-meta span').map((n) => n.text())).toEqual([
    'Demo Org',
    'Max 20x',
    '令牌 8 小时后到期',
  ])
  expect(wrapper.find('.oauth-steps').exists()).toBe(false)
  await wrapper.get('button').trigger('click') // 重新授权
  await flushPromises()
  expect(wrapper.find('.oauth-done').exists()).toBe(false)
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  wrapper.unmount()
})

test('changing the proxy after authorization does not warn about a link that no longer exists', async () => {
  const wrapper = mount(ClaudeOAuthPanel, {
    props: { proxyId: '', summary: { email: 'demo@example.com' } },
    global: { stubs },
  })
  await wrapper.setProps({ proxyId: 'proxy-2' })
  await flushPromises()
  expect(wrapper.text()).not.toContain('代理已变更')
  expect(wrapper.find('.oauth-done').exists()).toBe(true)
  wrapper.unmount()
})

test('an authorization without an email falls back to a visible placeholder', () => {
  const wrapper = mount(ClaudeOAuthPanel, { props: { summary: { email: '' } }, global: { stubs } })
  expect(wrapper.get('.oauth-done-email').text()).toBe('未返回邮箱')
  wrapper.unmount()
})

test('pasting a complete callback address confirms the authorization without another click', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ data: { auth_json: { access_token: 'fixture-token' }, warnings: [] } }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'proxy' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  const url = 'https://platform.claude.com/oauth/code/callback?code=abc&state=test'
  await wrapper.get('#claude-authorization-result').trigger('paste', { clipboardData: { getData: () => url } })
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ result: url })
  expect(wrapper.emitted('authorized')).toHaveLength(1)
  wrapper.unmount()
})

test('pasting a bare code waits for the explicit confirmation', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'proxy' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper
    .get('#claude-authorization-result')
    .trigger('paste', { clipboardData: { getData: () => 'only-a-code' } })
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

test('a proxy that cannot reach Claude offers to change the proxy instead of a bare error', async () => {
  fetchMock.mockResolvedValueOnce(
    json({ error: { code: 'OAUTH_PROXY_UNREACHABLE', message: '所选代理连不上 Claude', category: 'validation' } }, 422),
  )
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'proxy' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('所选代理连不上 Claude')
  await wrapper.get('[data-change-proxy]').trigger('click')
  // The button stub re-emits the native click, so count at least one.
  expect(wrapper.emitted('change-proxy')?.length).toBeGreaterThan(0)
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
  wrapper.unmount()
})

test.each([
  ['OAUTH_RATE_LIMITED', 2, 'Enter'],
  ['OAUTH_RATE_LIMITED', 2, 'paste'],
  ['OAUTH_BUSY', 1, 'Enter'],
  ['OAUTH_BUSY', 1, 'paste'],
] as const)(
  '%s blocks %s-second cooldown bypass through %s, then retries the same session',
  async (code, seconds, input) => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    fetchMock
      .mockResolvedValueOnce(json({ data: pending }))
      .mockResolvedValueOnce(
        json({ error: { code, message: '请稍后重试', details: { retry_after_seconds: seconds } } }, 429),
      )
      .mockResolvedValueOnce(json({ data: { auth_json: { access_token: 'fixture-token' }, warnings: [] } }))
    const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
    try {
      await wrapper.get('button').trigger('click')
      await flushPromises()
      await wrapper.get('#claude-authorization-result').setValue('code#test')
      await wrapper.get('.oauth-exchange').trigger('click')
      await flushPromises()
      const result = wrapper.get('#claude-authorization-result')
      if (input === 'Enter') await result.trigger('keyup', { key: 'Enter' })
      else await result.trigger('paste', { clipboardData: { getData: () => 'code#test' } })
      await flushPromises()
      expect(fetchMock).toHaveBeenCalledTimes(2)
      expect((result.element as HTMLInputElement).value).toBe('code#test')
      expect(wrapper.get('.oauth-exchange').text()).toContain(`${seconds}s`)
      await vi.advanceTimersByTimeAsync(seconds * 1000 - 1)
      expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeDefined()
      await vi.advanceTimersByTimeAsync(1)
      expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeUndefined()
      await result.trigger('keyup', { key: 'Enter' })
      await flushPromises()
      expect(fetchMock.mock.calls[2]![0]).toBe('/api/v1/claude-oauth/sessions/flow/exchange')
      expect(wrapper.emitted('authorized')).toHaveLength(1)
    } finally {
      wrapper.unmount()
    }
  },
)

test('a cooldown prevents refreshing the link without clearing the remaining wait', async () => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  fetchMock
    .mockResolvedValueOnce(json({ data: { ...pending, retry_after_seconds: 2 } }))
    .mockResolvedValueOnce(json({ data: { ...pending, id: 'flow-2' } }))
    .mockResolvedValue(json({ data: { cancelled: true } }))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  try {
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const refresh = wrapper.findAllComponents(stubs['el-button']).find((button) => button.text() === '作废并重新获取')!
    // Exercise the handler as well as the button state, so its guard cannot be bypassed.
    refresh.vm.$emit('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(refresh.attributes('disabled')).toBeDefined()
    expect(wrapper.get('.oauth-exchange').text()).toContain('2s')
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.get('.oauth-exchange').text()).toContain('1s')
    await vi.advanceTimersByTimeAsync(1000)
    expect(refresh.attributes('disabled')).toBeUndefined()
    await refresh.trigger('click')
    await flushPromises()
    expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ proxy_id: '', refresh: true })
  } finally {
    wrapper.unmount()
  }
})

test('a failed start cooldown prevents generating another link until it expires', async () => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  fetchMock
    .mockResolvedValueOnce(
      json({ error: { code: 'OAUTH_BUSY', message: '请稍后重试', details: { retry_after_seconds: 1 } } }, 429),
    )
    .mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  try {
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const login = wrapper.getComponent(stubs['el-button'])
    login.vm.$emit('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(login.attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(1000)
    expect(login.attributes('disabled')).toBeUndefined()
    await login.trigger('click')
    await flushPromises()
    expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  } finally {
    wrapper.unmount()
  }
})

test.each([400, 404, 409])(
  'an explicitly expired session after upstream %s clears the dead link and pasted result',
  async (status) => {
    fetchMock
      .mockResolvedValueOnce(json({ data: pending }))
      .mockResolvedValueOnce(
        json(
          {
            error: {
              code: 'OAUTH_EXCHANGE_FAILED',
              message: '授权码已失效，请重新生成链接',
              details: { upstream_status: status, oauth_session_expired: true },
            },
          },
          422,
        ),
      )
      .mockResolvedValueOnce(json({ data: { ...pending, id: 'flow-2' } }))
    const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
    try {
      await wrapper.get('button').trigger('click')
      await flushPromises()
      await wrapper.get('#claude-authorization-result').setValue('spent-code')
      await wrapper.get('.oauth-exchange').trigger('click')
      await flushPromises()
      expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
      expect(wrapper.text()).toContain('授权码已失效')
      await wrapper.get('button').trigger('click')
      await flushPromises()
      expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('')
    } finally {
      wrapper.unmount()
    }
  },
)

test('an exchange can finish after the 30-second queue and 40-second supplier budget', async () => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  // Native AbortSignal.timeout owns a clock outside Vitest's fake timers.
  // Keep its abort behavior while routing its deadline through the test clock.
  vi.spyOn(AbortSignal, 'timeout').mockImplementation((milliseconds) => {
    const controller = new AbortController()
    setTimeout(() => controller.abort(new DOMException('signal timed out', 'TimeoutError')), milliseconds)
    return controller.signal
  })
  fetchMock.mockResolvedValueOnce(json({ data: pending })).mockImplementationOnce(
    (_url, options: RequestInit) =>
      new Promise<Response>((resolve, reject) => {
        options.signal!.addEventListener('abort', () => reject(options.signal!.reason), { once: true })
        setTimeout(
          () => resolve(json({ data: { auth_json: { access_token: 'fixture-token' }, warnings: [] } })),
          70_000,
        )
      }),
  )
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  try {
    await wrapper.get('button').trigger('click')
    await flushPromises()
    await wrapper.get('#claude-authorization-result').setValue('code#test')
    await wrapper.get('.oauth-exchange').trigger('click')
    await vi.advanceTimersByTimeAsync(45_001)
    expect(wrapper.text()).not.toContain('请求超时')
    expect(wrapper.emitted('busy')?.at(-1)).toEqual([true])
    await vi.advanceTimersByTimeAsync(24_999)
    await flushPromises()
    expect(wrapper.emitted('authorized')?.[0]?.[0]).toEqual({
      auth_json: { access_token: 'fixture-token' },
      warnings: [],
    })
    expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
  } finally {
    wrapper.unmount()
  }
})

test('pool-backed reauthorization includes the account identity and known email', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, {
    props: { proxyId: 'pool', accountId: 'account-1', email: 'known@example.com' },
    global: { stubs },
  })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({
    proxy_id: 'pool',
    account_id: 'account-1',
    email: 'known@example.com',
  })
  wrapper.unmount()
})

test('changing a pending proxy preserves the original link until explicit replacement', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: { ...pending, proxy_id: 'old' } }))
    .mockResolvedValueOnce(json({ data: { ...pending, id: 'replacement', proxy_id: 'new' } }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'old' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper.get('#claude-authorization-result').setValue('original-code')
  await wrapper.setProps({ proxyId: 'new' })
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeDefined()
  const refresh = wrapper.findAll('button').find((button) => button.text() === '作废并重新获取')!
  await refresh.trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ proxy_id: 'new', refresh: true })
  expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('')
  wrapper.unmount()
})

test('generic write rate limits use a deadline rather than elapsed timer callbacks', async () => {
  vi.useFakeTimers()
  fetchMock
    .mockResolvedValueOnce(
      json({ error: { code: 'RATE_LIMITED', message: 'wait', details: { retry_after_seconds: 30 } } }, 429),
    )
    .mockResolvedValueOnce(json({ data: pending }))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(wrapper.get('button').attributes('disabled')).toBeDefined()
  vi.setSystemTime(Date.now() + 31_000)
  await vi.advanceTimersByTimeAsync(1_000)
  expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  wrapper.unmount()
})

test('start recovers an already authorized session without a code or another login', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: { ...pending, proxy_id: 'pool', stage: 'ready' } }))
    .mockResolvedValueOnce(
      json({ data: { oauth_session_id: 'flow', auth_json: { access_token: 'cached' }, warnings: [] } }),
    )
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'pool' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/claude-oauth/sessions/flow/exchange')
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ result: '' })
  expect(wrapper.emitted('authorized')?.[0]?.[0]).toMatchObject({ oauth_session_id: 'flow' })
  wrapper.unmount()
})

test('a reused ready session on another proxy requires explicit replacement before recovery', async () => {
  fetchMock.mockResolvedValueOnce(json({ data: { ...pending, proxy_id: 'old', stage: 'ready' } }))
  const wrapper = mount(ClaudeOAuthPanel, { props: { proxyId: 'new' }, global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(wrapper.get('.oauth-exchange').attributes('disabled')).toBeDefined()
  expect(wrapper.text()).toContain('代理或账号已变更')
  expect(wrapper.emitted('authorized')).toBeUndefined()
  wrapper.unmount()
})
