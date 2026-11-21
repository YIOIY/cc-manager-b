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

test('an invalid result keeps the paste; a supplier failure keeps the link and retries the same session', async () => {
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
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  expect((wrapper.get('#claude-authorization-result').element as HTMLInputElement).value).toBe('wrong')
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

test('an identity rejection clears the dead link and the paste', async () => {
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
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
  expect(wrapper.text()).toContain('登录 Claude')
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
  const refresh = wrapper.findAll('button').find((b) => b.text() === '重新获取链接')
  await refresh!.trigger('click')
  await flushPromises()
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({ proxy_id: 'proxy', refresh: true })
  expect(fetchMock.mock.calls[2]![0]).toBe('/api/v1/claude-oauth/sessions/flow')
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
