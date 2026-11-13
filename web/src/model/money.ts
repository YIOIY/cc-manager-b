// Showing the platform's RMB amounts in US dollars. The platform prices in
// dollars and reports RMB at a fixed rate it publishes (7.2 since September
// 2026); dividing by that rate gives back the dollar figure, exactly as the
// platform's own console does. The rate comes with each platform connection.

/** RMB fen → US cents at `rate` (RMB per dollar); null when the rate is unusable. */
export function fenToUsdCents(fen: number, rate: number | null | undefined): number | null {
  if (typeof rate !== 'number' || !Number.isFinite(rate) || rate <= 0 || !Number.isFinite(fen)) return null
  return Math.round(fen / rate)
}

export function formatUsdCents(cents: number): string {
  return '$' + (cents / 100).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/** 7.2 → "7.2", 7 → "7", 7.123456 → "7.1235". */
export function formatRate(rate: number): string {
  return String(Number(rate.toFixed(4)))
}
