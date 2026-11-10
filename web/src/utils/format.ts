import { plans } from '../list-model.ts'

export const hasValue = (value: unknown): boolean => value !== '' && value !== null && value !== undefined

/** Local date-time, 24h clock; falls back to the raw value when unparsable. */
export function formatDateTime(value: unknown, timeZone?: string): string {
  if (!hasValue(value)) return '—'
  const date = new Date(String(value))
  if (Number.isNaN(date.getTime())) return String(value)
  return date.toLocaleString('zh-CN', { hour12: false, ...(timeZone ? { timeZone } : {}) })
}

/** Integer counters, BigInt-safe so supplier totals beyond 2^53 stay exact. */
export function formatCount(value: unknown, missing = '—'): string {
  if (!hasValue(value)) return missing
  try {
    return BigInt(String(value)).toLocaleString('zh-CN')
  } catch {
    const n = Number(value)
    return Number.isFinite(n) ? n.toLocaleString('zh-CN') : missing
  }
}

/** Fen (1/100 CNY) to a currency string with two decimals, exact for large values. */
export function formatMoneyFen(value: unknown, missing = '—'): string {
  if (!hasValue(value)) return missing
  try {
    const amount = BigInt(String(value))
    const n = amount < 0n ? -amount : amount
    return (amount < 0n ? '-' : '') + '¥' + (n / 100n).toLocaleString('zh-CN') + '.' + String(n % 100n).padStart(2, '0')
  } catch {
    return missing
  }
}

export function formatPercent(value: unknown, missing = '—'): string {
  if (!hasValue(value)) return missing
  const n = Number(value)
  return Number.isFinite(n) ? n.toFixed(2) + '%' : missing
}

export function planLabel(value: unknown): string {
  return plans.find((p) => p.value === value)?.label || String(value ?? '')
}
