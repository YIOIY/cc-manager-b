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

test('the cost shows only dollars converted at the platform fixed rate', () => {
  const w = render(7.2)
  expect(w.get('[data-usd]').text()).toBe('$104.31')
  expect(w.text()).not.toContain('¥751.04')
  expect(w.text()).not.toContain('汇率')
  expect(w.find('[data-rate]').exists()).toBe(false)
})

test('a connection with another rate converts at that rate', () => {
  const w = render(7.3)
  expect(w.get('[data-usd]').text()).toBe('$102.88')
  expect(w.text()).not.toContain('汇率')
})

test('without a rate the cost shows a placeholder', () => {
  for (const rate of [undefined, 0]) {
    const w = render(rate)
    expect(w.text()).toContain('—')
    expect(w.find('[data-usd]').exists()).toBe(false)
    expect(w.text()).not.toContain('¥751.04')
  }
})
