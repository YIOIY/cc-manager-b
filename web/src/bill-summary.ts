export interface SummaryRange {
  start_ds: string
  end_ds: string
}
export interface SummaryAccount {
  id: string
  account: string
  alias: string
  supplier_account_id: string
  linked: boolean
  archived: boolean
}
export interface SummaryRow {
  account_id: string
  account: string
  alias: string
  supplier_account_id: string
  state: 'returned' | 'no_data' | 'invalid'
  error_code?: string
  request_count: string | null
  prompt_tokens: string | null
  completion_tokens: string | null
  cache_read_input_tokens: string | null
  cache_write_input_tokens: string | null
  cost_fen: string | null
}
export interface SummaryResult {
  connection_id: string
  timezone: string
  requested_range: SummaryRange
  range: SummaryRange
  range_adjusted: boolean
  queried_at: string
  account_count: number
  returned_count: number
  no_data_count: number
  invalid_count: number
  complete: boolean
  items: SummaryRow[]
  totals: Omit<SummaryRow, 'account_id' | 'account' | 'alias' | 'supplier_account_id' | 'state' | 'error_code'> & {
    is_partial: boolean
  }
}

export function eligibleBillAccount(account: SummaryAccount): boolean {
  return (
    account.linked &&
    /^[1-9]\d*$/.test(account.supplier_account_id) &&
    Number.isSafeInteger(Number(account.supplier_account_id))
  )
}

export function summaryValidation(connectionId: string, ids: string[], dates: string[], today: string): string {
  if (!connectionId) return '请选择一个启用的平台连接'
  if (!ids.length) return '请选择至少一个已上号账号'
  if (ids.length > 100) return '单次最多查询 100 个账号，请缩小选择范围'
  if (new Set(ids).size !== ids.length || ids.some((id) => !/^[1-9]\d*$/.test(id) || !Number.isSafeInteger(Number(id))))
    return '交付 ID 无效，请重新选择账号'
  if (
    dates.length !== 2 ||
    dates.some(
      (d) =>
        !/^\d{4}-\d{2}-\d{2}$/.test(d) ||
        !Number.isFinite(Date.parse(d + 'T12:00:00Z')) ||
        new Date(d + 'T12:00:00Z').toISOString().slice(0, 10) !== d,
    )
  )
    return '请选择有效的开始日期和结束日期'
  if (dates[0]! > dates[1]!) return '结束日期不得早于开始日期'
  const earliest = new Date(today + 'T12:00:00Z')
  earliest.setUTCDate(earliest.getUTCDate() - 90)
  if (dates[0]! < earliest.toISOString().slice(0, 10)) return '开始日期不得早于平台业务日期的 90 天前'
  return ''
}

export const summaryStates = {
  returned: { label: '已返回', tone: 'success' },
  no_data: { label: '未返回', tone: 'info' },
  invalid: { label: '数据异常', tone: 'warning' },
} as const
