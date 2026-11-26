// Pure helpers for the dashboard: labels for the attention list, the query
// a row navigates with, and bar widths for the 7-day spend strip.
import { runtimeLabel } from '../account-workflow'
import type { AttentionItem, HourlyCost, RecentJob, SpendDay } from '../types/api'
import { ErrorMessages, type ErrorCode } from '../generated/error-codes'

export function attentionLabel(item: AttentionItem): string {
  switch (item.kind) {
    case 'banned':
      return runtimeLabel(item.label)
    case 'missing':
      return '平台已移除'
    case 'profile_diff':
      return '资料与平台不一致'
    default:
      return item.label
  }
}

export function accountQuery(item: AttentionItem): { q: string } {
  return { q: item.account || item.upstream_id }
}

export function spendBars(days: SpendDay[]): number[] {
  let max = 0
  for (const d of days) if (d.cost_usd_cents > max) max = d.cost_usd_cents
  if (max === 0) return days.map(() => 0)
  return days.map((d) => Math.round((d.cost_usd_cents / max) * 100))
}

const severityTone: Record<string, 'danger' | 'warning' | 'info'> = { danger: 'danger', warn: 'warning', info: 'info' }

export function severityTag(severity: string): 'danger' | 'warning' | 'info' {
  return severityTone[severity] ?? 'info'
}

/** Change of the last complete day against the one before it; null when there is nothing to compare. */
export function dayChange(days: SpendDay[]): { percent: number; up: boolean } | null {
  const done = days.filter((d) => d.final)
  if (done.length < 2) return null
  const last = done[done.length - 1]!.cost_usd_cents
  const before = done[done.length - 2]!.cost_usd_cents
  if (before <= 0) return null
  return { percent: Math.round((Math.abs(last - before) / before) * 100), up: last >= before }
}

/** Mean spend of the complete days, in USD cents. */
export function dailyAverageCents(days: SpendDay[]): number {
  const done = days.filter((d) => d.final)
  return done.length ? Math.round(done.reduce((sum, d) => sum + d.cost_usd_cents, 0) / done.length) : 0
}

export interface ShareSegment {
  key: string
  count: number
  percent: number
}

/** Non-empty groups as shares of the whole: the keys in `order` first, the rest by size. */
export function shareSegments(counts: Record<string, number>, order: string[] = []): ShareSegment[] {
  const entries = Object.entries(counts).filter(([, n]) => n > 0)
  const total = entries.reduce((sum, [, n]) => sum + n, 0)
  if (!total) return []
  const rank = (key: string) => (order.includes(key) ? order.indexOf(key) : order.length)
  return entries
    .sort((a, b) => rank(a[0]) - rank(b[0]) || b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([key, count]) => ({ key, count, percent: Math.round((count / total) * 1000) / 10 }))
}

/** Bar heights (0–100) for the hourly spend; an hour with any spend stays visible. */
export function hourlyBars(hours: HourlyCost[]): (HourlyCost & { height: number })[] {
  const max = hours.reduce((m, h) => Math.max(m, h.cost_usd_cents), 0)
  return hours.map((h) => ({
    ...h,
    height: max && h.cost_usd_cents > 0 ? Math.max(3, Math.round((h.cost_usd_cents / max) * 100)) : 0,
  }))
}

export function hourlySummary(hours: HourlyCost[]): { total: number; peak: number; peakHour: string | null } {
  let total = 0
  let peak = 0
  let peakHour: string | null = null
  for (const h of hours) {
    total += h.cost_usd_cents
    if (h.cost_usd_cents > peak) {
      peak = h.cost_usd_cents
      peakHour = h.hour
    }
  }
  return { total, peak, peakHour }
}

export type OutcomeTone = 'success' | 'danger' | 'warning' | 'info'

/** How a recent onboarding submission ended, in the words the rest of the app uses. */
export function jobOutcome(job: RecentJob): { label: string; tone: OutcomeTone } {
  switch (job.state) {
    case 'succeeded':
      return { label: '平台已接收', tone: 'success' }
    case 'failed':
      return { label: ErrorMessages[job.code as ErrorCode] || '上号失败', tone: 'danger' }
    case 'queued':
    case 'running':
      return { label: '提交中', tone: 'info' }
    default:
      return { label: '结果待确认', tone: 'warning' }
  }
}

export const deliveryLabels: Record<string, string> = {
  linked: '已上号',
  pending: '待上号',
  submitting: '上号中',
  failed: '上号失败',
  unknown: '结果待确认',
}
export const deliveryOrder = ['linked', 'submitting', 'pending', 'unknown', 'failed']

export const expiryLabels: Record<string, string> = {
  expired: '已到期',
  within_7: '7 天内',
  within_30: '30 天内',
  later: '30 天以上',
  unknown: '未填写',
}
export const expiryOrder = ['expired', 'within_7', 'within_30', 'later', 'unknown']
