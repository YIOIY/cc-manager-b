// Display helpers for API-provided USD amounts. Conversion from RMB fen
// happens on the server using each connection's fixed usd_cny_rate.

/** Prefixed dollar string from API `cost_usd` ("104.31" → "$104.31"). */
export function formatUsd(amount: string | null | undefined): string {
  if (amount === null || amount === undefined || amount === '') return '—'
  const negative = amount.startsWith('-')
  const raw = negative ? amount.slice(1) : amount
  const n = Number(raw)
  if (!Number.isFinite(n)) return '—'
  const body = n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return (negative ? '-$' : '$') + body
}

/** Prefixed dollar string from integer US cents. */
export function formatUsdCents(cents: number): string {
  return '$' + (cents / 100).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/** 7.2 → "7.2", 7 → "7", 7.123456 → "7.1235". */
export function formatRate(rate: number): string {
  return String(Number(rate.toFixed(4)))
}
