import { afterEach, expect, test } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { PlatformUsage } from '../src/types/api'
import PlatformUsageBlock from '../src/components/PlatformUsageBlock.vue'

const wrappers: VueWrapper[] = []
afterEach(() => wrappers.splice(0).forEach((w) => w.unmount()))

const usage: PlatformUsage = {
  cost_usd: '104.31',
  cost_usd_cents: 10431,
  input_tokens: 1827348,
  output_tokens: 860720,
  five_hour_used_percent: 16,
  five_hour_reset_at: '2026-11-13T03:40:00Z',
  week_used_percent: 8,
  week_reset_at: '2026-11-14T22:00:00Z',
  observed_at: '2026-11-13T01:23:42Z',
}

function render(usageOverride?: PlatformUsage | null) {
  const w = mount(PlatformUsageBlock, { props: { usage: usageOverride === undefined ? usage : usageOverride } })
  wrappers.push(w)
  return w
}

test('the cost shows the dollar amount from the API', () => {
  const w = render()
  expect(w.get('[data-usd]').text()).toBe('$104.31')
  expect(w.text()).not.toContain('¥')
  expect(w.text()).not.toContain('汇率')
  expect(w.find('[data-rate]').exists()).toBe(false)
})

test('without a dollar amount the cost shows a placeholder', () => {
  const w = render({ ...usage, cost_usd: '' })
  expect(w.text()).toContain('—')
  expect(w.find('[data-usd]').exists()).toBe(false)
})

test('without usage the block says the platform returned none', () => {
  expect(render(null).text()).toContain('平台未返回用量')
})

test('usage windows render as bars with compact tokens and exact values on hover', () => {
  const w = render()
  expect(w.get('[data-window="5h"]').text()).toContain('16%')
  expect(w.get('[data-window="7d"]').text()).toContain('8%')
  expect(w.get('[data-window="5h"] .usage-fill').attributes('style')).toContain('width: 16%')
  const tokens = w.get('.usage-summary span')
  expect(tokens.text()).toBe('入 1.83M · 出 861K')
  expect(tokens.attributes('title')).toContain('1,827,348')
})
