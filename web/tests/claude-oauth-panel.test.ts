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

test('invalid state permits correction; failed exchange requires a fresh link', async () => {
  fetchMock
    .mockResolvedValueOnce(json({ data: pending }))
    .mockResolvedValueOnce(json({ error: { code: 'OAUTH_RESULT_INVALID', message: '结果不匹配' } }, 422))
    .mockResolvedValueOnce(json({ error: { code: 'OAUTH_EXCHANGE_FAILED', message: '请重新授权' } }, 502))
  const wrapper = mount(ClaudeOAuthPanel, { global: { stubs } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
  await wrapper.get('#claude-authorization-result').setValue('wrong')
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(true)
  await wrapper.get('.oauth-exchange').trigger('click')
  await flushPromises()
  expect(wrapper.find('#claude-authorization-result').exists()).toBe(false)
  expect(wrapper.text()).toContain('请重新授权')
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
