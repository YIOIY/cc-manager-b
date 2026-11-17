import { ErrorMessages, type ErrorCode } from './generated/error-codes'
export type DeliveryState = 'pending' | 'submitting' | 'linked' | 'failed' | 'unknown'

export const deliveryStates: Record<
  DeliveryState,
  { label: string; tone: string; action: string; description: string }
> = {
  pending: { label: '待上号', tone: 'pending', action: '去上号', description: '账号已保存在本地，还未提交到平台。' },
  submitting: {
    label: '上号中',
    tone: 'progress',
    action: '查看进度',
    description: '正在提交到平台，结果会自动更新。',
  },
  linked: {
    label: '已上号',
    tone: 'success',
    action: '查看详情',
    description: '平台已接收账号，消耗与账单将单独同步。',
  },
  failed: {
    label: '上号失败',
    tone: 'danger',
    action: '处理并重试',
    description: '本次上号未成功，资料已保留，请检查后重试。',
  },
  unknown: {
    label: '结果待确认',
    tone: 'warning',
    action: '查看详情',
    description: '尚未确认平台是否接收，请先在平台核实上次结果。',
  },
}

export interface DeliveryInput {
  platform_links?: Array<{ ingestion_state?: string }>
  onboarding_state?: string
}

export function deliveryState(account: DeliveryInput): DeliveryState {
  if (account.platform_links?.some((link) => link.ingestion_state === 'accepted')) return 'linked'
  const state = account.onboarding_state
  if (state && Object.hasOwn(deliveryStates, state)) return state as DeliveryState
  return 'pending'
}

export function runtimeLabel(status: string) {
  return (
    (
      {
        online: '在线',
        using: '使用中',
        ready: '待登录',
        offline: '已下线',
        ban: '已封禁',
        banned: '已封禁',
        init: '初始化',
        bound: '已绑定',
        cooking: '养号中',
        cooking_failed: '养号失败',
        login_failed: '登录失败',
        re_login: '待重新登录',
        re_login_failed: '重新登录失败',
        pending_activation: '待激活',
        bound_failed: '绑定失败',
        sync_failed: '同步失败',
        wait_check: '待验证',
        check_fail: '验证失败',
      } as Record<string, string>
    )[status] ||
    status ||
    '平台状态待同步'
  )
}

/** Platform statuses where Claude login failed and a credential refresh may help. */
export const reauthStatuses = new Set(['login_failed', 're_login', 're_login_failed'])

export function needsReauth(status: string | undefined | null): boolean {
  return !!status && reauthStatuses.has(status)
}

/** Text for a submission outcome: the stored message, else generated copy. */
export function outcomeText(job?: { code?: string; message?: string } | null): string {
  if (job?.message) return job.message
  const code = job?.code as ErrorCode | undefined
  if (code && ErrorMessages[code]) return ErrorMessages[code]
  return '请核对账号资料与平台配置'
}

/** @deprecated kept for old call sites; same lookup as outcomeText. */
export function deliveryIssue(code: string) {
  return outcomeText({ code })
}
