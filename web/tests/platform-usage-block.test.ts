import { afterEach, expect, test, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import type { PlatformUsage } from '../src/types/api'

const apiMock = vi.fn()
vi.mock('../src/api', () => ({ api: (...args: unknown[]) => apiMock(...args) }))

import PlatformUsageBlock from '../src/components/PlatformUsageBlock.vue'
import { resetExchangeRate } from '../src/composables/useExchangeRate'

const wrappers: VueWrapper[] = []
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount())
  apiMock.mockReset()
  resetExchangeRate()
  vi.useRealTimers()
})

const usage: PlatformUsage = {
  cost_fen: 75104,
  cost_yuan: '751.04',
  input_tokens: 1827348,
  output_tokens: 860720,
  five_hour_used_percent: 16,
  five_hour_reset_at: '2026-11-13T03:40:00Z',
  week_used_percent: 8,
  week_reset_at: '2026-11-14T22:00:00Z',
  observed_at: '2026-11-13T01:23:42Z',
}
const rate = {
  pair: 'USD/CNY',
  rate: 6.6683,
  source: 'ecb',
  rate_date: '2026-11-12',
  fetched_at: '2026-11-13T01:00:00Z',
}

async function render() {
  const w = mount(PlatformUsageBlock, { props: { usage } })
  wrappers.push(w)
  await flushPromises()
  return w
}

test('the cost shows RMB, the dollar equivalent and the rate it was converted at', async () => {
  vi.useFakeTimers({ now: new Date('2026-11-13T02:00:00Z'), toFake: ['Date'] })
  apiMock.mockResolvedValue({ data: rate })
  const w = await render()
  expect(apiMock).toHaveBeenCalledWith('/exchange-rate')
  const text = w.text()
  expect(text).toContain('¥751.04')
  expect(w.get('[data-usd]').text()).toBe('≈ $112.63')
  const line = w.get('[data-rate]')
  expect(line.text()).toContain('1 美元 = 6.6683 人民币')
  expect(line.text()).toContain('2026-11-12')
  expect(line.text()).not.toContain('可能过期')
  expect(line.attributes('title')).toContain('欧洲央行参考汇率')
})

test('without a rate only RMB is shown, with a note', async () => {
  apiMock.mockResolvedValue({ data: { ...rate, rate: null } })
  const w = await render()
  expect(w.text()).toContain('¥751.04')
  expect(w.find('[data-usd]').exists()).toBe(false)
  expect(w.text()).toContain('汇率暂不可用')
})

test('a failing rate request never breaks the card', async () => {
  apiMock.mockRejectedValue(new Error('offline'))
  const w = await render()
  expect(w.text()).toContain('¥751.04')
  expect(w.find('[data-usd]').exists()).toBe(false)
  expect(w.text()).toContain('汇率暂不可用')
})

test('an old rate is marked as possibly out of date', async () => {
  vi.useFakeTimers({ now: new Date('2026-11-20T02:00:00Z'), toFake: ['Date'] })
  apiMock.mockResolvedValue({ data: rate })
  const w = await render()
  expect(w.get('[data-rate]').text()).toContain('可能过期')
})

test('many cards on a page share one rate request', async () => {
  apiMock.mockResolvedValue({ data: rate })
  await render()
  await render()
  await render()
  expect(apiMock).toHaveBeenCalledTimes(1)
})
