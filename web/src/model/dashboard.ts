// Pure helpers for the dashboard: labels for the attention list, the query
// a row navigates with, and bar widths for the 7-day spend strip.
import { runtimeLabel } from '../account-workflow'
import type { AttentionItem, SpendDay } from '../types/api'

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
  for (const d of days) if (d.cost_fen > max) max = d.cost_fen
  if (max === 0) return days.map(() => 0)
  return days.map((d) => Math.round((d.cost_fen / max) * 100))
}

const severityTone: Record<string, 'danger' | 'warning' | 'info'> = { danger: 'danger', warn: 'warning', info: 'info' }

export function severityTag(severity: string): 'danger' | 'warning' | 'info' {
  return severityTone[severity] ?? 'info'
}
