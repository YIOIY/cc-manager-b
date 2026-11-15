// Display helpers for the platform account card and its usage block. Pure
// functions so the thresholds and formats are testable without a component.

export type UsageTone = 'ok' | 'warn' | 'danger' | 'none'

export const USAGE_WARN_PERCENT = 80
export const USAGE_DANGER_PERCENT = 95
export const USAGE_STALE_MS = 30 * 60 * 1000

export function usageTone(percent: number | null | undefined): UsageTone {
  if (percent === null || percent === undefined || Number.isNaN(Number(percent))) return 'none'
  const value = Number(percent)
  if (value >= USAGE_DANGER_PERCENT) return 'danger'
  if (value >= USAGE_WARN_PERCENT) return 'warn'
  return 'ok'
}

const pad = (n: number) => String(n).padStart(2, '0')

/** Renders an RFC3339 instant as YYYY-MM-DD HH:mm:ss in the given zone (default: the browser's). */
export function formatResetAt(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  if (!timeZone) {
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  }
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).formatToParts(date)
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? '00'
  const hour = get('hour') === '24' ? '00' : get('hour')
  return `${get('year')}-${get('month')}-${get('day')} ${hour}:${get('minute')}:${get('second')}`
}

export function usageStale(observedAt: string | null | undefined, now: Date = new Date()): boolean {
  if (!observedAt) return true
  const observed = new Date(observedAt).getTime()
  if (Number.isNaN(observed)) return true
  return now.getTime() - observed > USAGE_STALE_MS
}

const locations: Record<string, string> = {
  US: '美国',
  CN: '中国',
  GB: '英国',
  SG: '新加坡',
  HK: '中国香港',
  TW: '中国台湾',
}

export function locationLabel(code: string | null | undefined): string {
  if (!code) return '—'
  return locations[code] ?? code
}

const payments: Record<string, string> = {
  google_pay: 'GooglePay',
  apple_pay: 'ApplePay',
  paypal: 'PayPal',
  credit_card: '信用卡',
  alipay: '支付宝',
  wechat_pay: '微信支付',
}

export function paymentLabel(value: string | null | undefined): string {
  if (!value) return '—'
  return payments[value] ?? value
}

export function formatTokens(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '—'
  return Number(n).toLocaleString('en-US')
}

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return '—'
  return `${Number(value).toFixed(2)}%`
}

/** Shorten platform aliases like `claude-住享科智-20261113006` to `claude-20261113006`. */
export function shortPlatformAlias(alias: string | null | undefined): string {
  const value = (alias || '').trim()
  if (!value) return ''
  const parts = value.split('-').filter(Boolean)
  if (parts.length < 3) return value
  return `${parts[0]}-${parts[parts.length - 1]}`
}
