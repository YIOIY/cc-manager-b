import type { Account, Job, AuditEvent, Permission } from '../types/api'

export interface UsageSnapshot {
  observed_at: string
  cost_usd_cents: number
  cost_usd?: string
  input_tokens: number
  output_tokens: number
  five_hour_used_percent: number
  five_hour_reset_at: string
  week_used_percent: number
  week_reset_at: string
}

export interface DaySpend {
  date: string
  cents: number
}

const pad = (n: number) => String(n).padStart(2, '0')
/** Local calendar day, matching how the rest of the console shows times. */
export function localDay(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/**
 * Cost in a snapshot is cumulative. A day's spend is the difference between
 * its last snapshot and the previous day's last one; the first day has no
 * baseline, and a drop (a platform-side reset) counts as zero.
 */
export function dailySpend(list: UsageSnapshot[]): DaySpend[] {
  const lastOfDay = new Map<string, number>()
  for (const s of list) lastOfDay.set(localDay(s.observed_at), s.cost_usd_cents)
  const days = [...lastOfDay.keys()].sort()
  const out: DaySpend[] = []
  for (let i = 1; i < days.length; i++) {
    const diff = lastOfDay.get(days[i]!)! - lastOfDay.get(days[i - 1]!)!
    out.push({ date: days[i]!, cents: Math.max(0, diff) })
  }
  return out
}

/** Evenly thin a long series to at most `max` points, always keeping the last. */
export function downsample<T>(list: T[], max = 240): T[] {
  if (list.length <= max) return list
  const step = list.length / max
  const out: T[] = []
  for (let i = 0; i < max; i++) out.push(list[Math.floor(i * step)]!)
  out[out.length - 1] = list[list.length - 1]!
  return out
}

export interface ActivityItem {
  key: string
  at: string
  label: string
  detail: string
  actor: string
  tone: 'ok' | 'bad' | 'info'
}

const auditLabels: Record<string, string> = {
  'account.create': '创建账号',
  'account.update': '修改账号资料',
  'account.assign': '分配给用户',
  'account.onboard': '提交上号',
  'account.submit': '提交上号',
  'account.reauthorize': '重新授权并同步',
  'account.platform_profile_update': '修改平台资料',
  'account.platform_relink': '重新关联平台账号',
  'account.platform_import': '从平台导入',
  'account.delete': '删除账号',
}
const jobStates: Record<string, { label: string; tone: ActivityItem['tone'] }> = {
  queued: { label: '上号任务排队', tone: 'info' },
  running: { label: '上号任务进行中', tone: 'info' },
  succeeded: { label: '上号任务完成', tone: 'ok' },
  failed: { label: '上号任务失败', tone: 'bad' },
}

/** Audit events and onboarding jobs of one account, newest first. */
export function mergeActivity(events: AuditEvent[], jobs: Job[], limit = 8): ActivityItem[] {
  const items: ActivityItem[] = [
    ...events.map<ActivityItem>((e) => ({
      key: 'a' + e.id,
      at: e.created_at,
      label: auditLabels[e.action] || e.action,
      detail: e.detail || '',
      actor: e.actor,
      tone: 'info',
    })),
    ...jobs.map<ActivityItem>((j) => {
      const state = jobStates[j.state] || { label: '上号任务 ' + j.state, tone: 'info' as const }
      return {
        key: 'j' + j.id,
        at: j.updated_at || j.created_at,
        label: state.label,
        detail: j.state === 'failed' ? j.message || j.code : '',
        actor: '',
        tone: state.tone,
      }
    }),
  ]
  return items.sort((x, y) => (x.at < y.at ? 1 : x.at > y.at ? -1 : 0)).slice(0, limit)
}

/** The same rule the cards use for the 「修改平台资料」 action. */
export function canEditPlatformProfile(
  card: Pick<Account, 'created_by_user_id' | 'platform'>,
  access: { owns: (id: string | undefined) => boolean; can: (permission: Permission) => boolean },
): boolean {
  const p = card.platform
  return (
    access.owns(card.created_by_user_id) &&
    access.can('accounts.platform_edit') &&
    !!p &&
    !p.missing_since &&
    !!p.web_channel?.configured &&
    !p.web_channel?.suspended
  )
}

const failingStatuses = new Set([
  'ban',
  'banned',
  'check_fail',
  'login_failed',
  'bound_failed',
  'cooking_failed',
  're_login_failed',
  'sync_failed',
  'fault',
  'suspended',
  'arrearage',
])
export const isFailingStatus = (status: string | undefined | null) => !!status && failingStatuses.has(status)

/** One row of the profile grid; empty values are left out by the caller. */
export interface DetailRow {
  label: string
  value: string
  /** Local record disagrees with the platform. */
  diff?: string
}
