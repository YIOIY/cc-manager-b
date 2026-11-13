import { afterEach, expect, test } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { PlatformUsage } from '../src/types/api'
import PlatformUsageBlock from '../src/components/PlatformUsageBlock.vue'

const wrappers: VueWrapper[] = []
afterEach(() => wrappers.splice(0).forEach((w) => w.unmount()))

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

function render(usdRate?: number) {
  const w = mount(PlatformUsageBlock, { props: { usage, usdRate } })
  wrappers.push(w)
  return w
}

test('the cost shows RMB and dollars at the platform fixed rate, like the platform console', () => {
  const w = render(7.2)
  expect(w.text()).toContain('¥751.04')
  expect(w.get('[data-usd]').text()).toBe('$104.31')
  const line = w.get('[data-rate]')
  expect(line.text()).toContain('汇率 7.2')
  expect(line.text()).toContain('平台固定汇率')
  expect(line.attributes('title')).toContain('1 美元 = 7.2 人民币')
})

test('a connection with another rate converts at that rate', () => {
  const w = render(7.3)
  expect(w.get('[data-usd]').text()).toBe('$102.88')
  expect(w.get('[data-rate]').text()).toContain('汇率 7.3')
})

test('without a rate only RMB is shown', () => {
  for (const rate of [undefined, 0]) {
    const w = render(rate)
    expect(w.text()).toContain('¥751.04')
    expect(w.find('[data-usd]').exists()).toBe(false)
    expect(w.find('[data-rate]').exists()).toBe(false)
  }
})
