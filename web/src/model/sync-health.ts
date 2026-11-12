// What the dashboard says about the platform metrics sync. Pure so it can be
// unit tested; the copy comes from the server (registry) with generated
// fallbacks for older servers or records.
import type { DashboardPlatform } from '../types/api'
import { ErrorMessages, type ErrorCode } from '../generated/error-codes'

export interface SyncHealthView {
  tone: 'success' | 'danger' | 'info'
  label: string
  detail?: string
  hint?: string
}

const pad = (n: number) => String(n).padStart(2, '0')

export function describeSyncHealth(p: DashboardPlatform): SyncHealthView {
  const status = p.sync_status ?? (p.last_sync_state ? (p.last_sync_state === 'succeeded' ? 'ok' : 'failing') : 'never')
  if (status === 'never') return { tone: 'info', label: '尚未同步' }
  if (status === 'ok') return { tone: 'success', label: '正常' }
  const message = p.error_message || (p.error_code ? ErrorMessages[p.error_code as ErrorCode] : '') || '平台同步失败'
  const parts = [message]
  if (p.consecutive_failures) parts.push(`连续失败 ${p.consecutive_failures} 次`)
  if (p.next_attempt_at) {
    const next = new Date(p.next_attempt_at)
    if (!Number.isNaN(next.getTime())) parts.push(`下次重试 ${pad(next.getHours())}:${pad(next.getMinutes())}`)
  }
  const view: SyncHealthView = { tone: 'danger', label: '同步异常', detail: parts.join(' · ') }
  if (p.error_hint) view.hint = p.error_hint
  return view
}

/**
 * The OpenAPI metrics sync: a platform that denies queries is a known,
 * supplier-side condition, not an alarm for the operator.
 */
export function describeMetricsHealth(p: DashboardPlatform): SyncHealthView {
  if (p.error_code === 'PLATFORM_QUERY_DENIED' && (p.sync_status ?? 'failing') === 'failing') {
    return { tone: 'info', label: '平台未开通', detail: 'OpenAPI 查询权限未开通，等供应商处理' }
  }
  return describeSyncHealth(p)
}
