import { api } from './api'

export interface BillConnection {
  id: string
  name: string
  base_url: string
  timezone: string
  enabled: boolean
}
export interface BillAccount {
  id: string
  account: string
  alias: string
  supplier_account_id: string
  archived: boolean
  linked: boolean
}
export interface BillRun {
  run_id: string
  state: string
  connection_id: string
  from: string
  to: string
  timezone: string
  total_items: number
  completed: number
  account_count: number
  created_at: string
  counts: Record<string, number>
  http_requests: number
  http_retries: number
}
export interface BillSelection {
  connection_id: string
  account_ids: string[]
  from: string
  to: string
  include_archived: boolean
}
export const billPost = async <T = any>(path: string, body: unknown, key?: string) =>
  (
    await api<T>('/platform-usage' + path, {
      method: 'POST',
      headers: key ? { 'Idempotency-Key': key } : {},
      body: JSON.stringify(body),
    })
  ).data
export const billGet = async <T = any>(path: string) => (await api<T>('/platform-usage' + path)).data

export { formatBillCount, formatBillMoney, businessDay, shiftBillDay } from './bills-format'
export const billStateLabels: Record<string, string> = {
  queued: '排队中',
  running: '同步中',
  succeeded: '已完成',
  partial: '部分失败',
  failed: '失败',
  interrupted: '已中断',
  updated: '已更新',
  unchanged: '未变化',
  no_data: '无返回',
  superseded: '已被更新任务替代',
  verified: '已验证',
  legacy_unverified: '历史待复核',
  stale: '数据已过期',
  timezone_changed: '时区已变更',
}
export function billTone(state: string): 'success' | 'warning' | 'danger' | 'info' {
  return ['verified', 'succeeded', 'updated', 'unchanged'].includes(state)
    ? 'success'
    : ['failed', 'interrupted'].includes(state)
      ? 'danger'
      : ['partial', 'no_data', 'stale', 'legacy_unverified', 'timezone_changed'].includes(state)
        ? 'warning'
        : 'info'
}
