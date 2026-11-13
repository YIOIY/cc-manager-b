// Converting the platform's RMB amounts to US dollars for display. The RMB
// figure is what the platform reports; the dollar figure is derived from a
// reference rate, so callers show it as an approximation next to the rate.

/** RMB fen → US cents at `rate` (CNY per USD); null when the rate is unusable. */
export function fenToUsdCents(fen: number, rate: number | null | undefined): number | null {
  if (typeof rate !== 'number' || !Number.isFinite(rate) || rate <= 0 || !Number.isFinite(fen)) return null
  return Math.round(fen / rate)
}

export function formatUsdCents(cents: number): string {
  return '$' + (cents / 100).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function describeRate(rate: number): string {
  return `1 美元 = ${rate.toFixed(4)} 人民币`
}

const sourceLabels: Record<string, string> = { ecb: '欧洲央行参考汇率', 'open-er-api': 'open.er-api.com' }

export function rateSourceLabel(source: string): string {
  return sourceLabels[source] ?? source
}

const STALE_MS = 3 * 24 * 60 * 60 * 1000

/** True when the rate was last refreshed more than three days ago (or never). */
export function rateIsStale(fetchedAt: string, now: Date = new Date()): boolean {
  const at = Date.parse(fetchedAt)
  return !Number.isFinite(at) || now.getTime() - at > STALE_MS
}
