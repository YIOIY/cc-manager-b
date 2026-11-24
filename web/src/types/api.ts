// Shapes returned by /api/v1. Field names mirror the Go handlers; optional
// fields are omitted by the server when the value is unknown.

export type Permission =
  | 'dashboard.view'
  | 'accounts.view'
  | 'accounts.create'
  | 'accounts.update'
  | 'accounts.archive'
  | 'accounts.delete'
  | 'accounts.import'
  | 'accounts.credentials'
  | 'accounts.onboard'
  | 'accounts.usage'
  | 'accounts.platform_edit'
  | 'proxies.view'
  | 'proxies.create'
  | 'proxies.update'
  | 'proxies.archive'
  | 'proxies.delete'
  | 'proxies.test'
  | 'proxies.settings'
  | 'oauth.settings.view'
  | 'oauth.settings'
  | 'jobs.view'
  | 'bills.view'
  | 'bills.query'
  | 'bills.sync'
  | 'channel_usage.view'
  | 'connections.view'
  | 'connections.create'
  | 'connections.update'
  | 'connections.delete'
  | 'audit.view'
  | 'users.view'
  | 'users.create'
  | 'users.update'
  | 'users.delete'
  | 'users.reset_password'
  | 'roles.view'
  | 'roles.manage'
  | 'feedback.manage'
  | 'announcement.manage'

export interface RoleRef {
  id: string
  name: string
  builtin: boolean
}

export interface SessionUser {
  id: string
  username: string
  nickname: string
  can_onboard: boolean
  roles: RoleRef[]
  permissions: Permission[]
  is_admin: boolean
}

export interface Session {
  authenticated: boolean
  csrf_token: string
  user?: SessionUser
}

export interface User {
  id: string
  username: string
  nickname: string
  enabled: boolean
  can_onboard: boolean
  version: number
  created_at: string
  updated_at: string
  last_login_at: string
  roles: RoleRef[]
  permissions: Permission[]
  is_admin: boolean
}

export interface RoleRecord {
  id: string
  name: string
  description: string
  builtin: boolean
  permissions: Permission[]
  version: number
  user_count: number
  created_at: string
  updated_at: string
}

export interface PermissionDef {
  key: Permission
  label: string
  hint?: string
}

export interface PermissionModule {
  key: string
  label: string
  /** navigation entry revealed by the module's ".view" permission; absent for sub-features */
  page?: string
  permissions: PermissionDef[]
}

export interface Envelope<T> {
  data: T
}

export interface ApiErrorBody {
  error: { code: string; message: string }
}

export type Plan = 'claude/cc-max-20x' | 'claude/cc-max-5x' | 'claude/cc-pro'

export type OnboardingState = 'pending' | 'submitting' | 'linked' | 'failed' | 'unknown'

export interface PlatformLink {
  connection_id: string
  connection_name: string
  upstream_id: string
  platform_account_id: string
  alias: string
  status: string
  ingestion_state: string
}

export interface SubmissionJob {
  action?: string
  retryable?: boolean
  allowed_actions?: string[]
  id: string
  connection_id: string
  state: string
  code: string
  message?: string
  hint?: string
  updated_at: string
}

export interface Account {
  id: string
  account: string
  plan: Plan | string
  register_location: string
  register_time: string
  plan_start_time: string
  plan_end_time: string
  proxy_id: string
  /** Bound pool proxy host/IP when proxy_id resolves; empty when unbound or missing. */
  proxy_host?: string
  version: number
  archived: boolean
  created_at: string
  updated_at: string
  platform_account_id: string
  platform_alias: string
  platform_status: string
  platform_observed_at: string
  platform_requests?: number
  platform_prompt_tokens?: number
  platform_completion_tokens?: number
  platform_success_rate_percent?: number
  platform_cost_fen?: number
  platform_links: PlatformLink[]
  /** Which channel the displayed platform_status came from ('' when never observed). */
  platform_channel?: 'web' | 'openapi' | ''
  /** Snapshot from the supplier web console; null when the platform never listed the linked delivery. */
  platform?: PlatformInfo | null
  onboarding_state: OnboardingState
  credentials_configured?: boolean
  password_configured?: boolean
  submission_job?: SubmissionJob
  /** Owning system user (assigned / creator); empty = admin-only until assigned. */
  created_by_user_id?: string
  created_by_nickname?: string
  created_by_username?: string
  balance?: number | string
  tags?: string[]
  remark?: string
  payment_method?: string
  register_device?: string
  recharge_time?: string
  warranty_period?: number | string
}

/** The console's account profile (its auth_config minus the password). */
export interface PlatformProfile {
  plan: string
  plan_start_time: string
  plan_end_time: string
  register_time: string
  register_location: string
  balance: string
  payment_method: string
  register_device: string
  recharge_time: string
  warranty_period: string
  tags: string[]
}

/** The console's "账号用量" block. Percentages are 0–100, cost in fen. */
export interface PlatformUsage {
  cost_fen: number
  cost_yuan: string
  input_tokens: number
  output_tokens: number
  five_hour_used_percent: number
  five_hour_reset_at: string
  week_used_percent: number
  week_reset_at: string
  observed_at: string
}

export interface PlatformInfo {
  status: string
  status_raw: string
  locked: boolean
  reason_kind: string
  reason_text: string
  reason_fetched_at: string
  /** When the console status last changed (local sync clock); empty before v23. */
  status_changed_at: string
  observed_at: string
  aid: string
  upstream_id: string
  alias: string
  provider: string
  model: string
  models_usable: boolean
  remote_created_at: string
  missing_since: string
  auto_created: boolean
  source_channel: 'web' | 'openapi'
  /** The console's free-text remark for the delivery. */
  remark: string
  /** Whether this connection's console channel can carry a profile edit. */
  web_channel: { configured: boolean; suspended: boolean }
  /** The platform's fixed RMB-per-dollar rate (from the connection), for showing RMB costs in dollars. */
  usd_cny_rate: number
  profile: PlatformProfile
  usage: PlatformUsage | null
}

export type FeedbackKind = 'problem' | 'suggestion' | 'other'
export type FeedbackStatus = 'open' | 'in_progress' | 'resolved' | 'closed'

/** One entry of 用户之声. */
export interface Feedback {
  id: string
  username: string
  kind: FeedbackKind
  title: string
  body: string
  /** Path of the page it was sent from (no query string). */
  page: string
  context: Partial<Record<'error_code' | 'request_id' | 'user_agent' | 'viewport', string>>
  app_version: string
  status: FeedbackStatus
  reply: string
  handled_by: string
  handled_at: string
  version: number
  created_at: string
  updated_at: string
  /** Sent by the signed-in user. */
  mine: boolean
}

export interface FeedbackList {
  items: Feedback[]
  pagination: { page: number; page_size: number; total: number }
  /** How many there are of each status, within the other filters. */
  counts: Record<FeedbackStatus, number>
  can_manage: boolean
}

/** Result of POST /accounts/{id}/platform-profile. */
export interface PlatformProfileResult {
  verified: boolean
  hint?: string
  changed: string[]
  platform: PlatformInfo | null
}

/** One row of GET /platform-connections/{id}/remote-accounts. */
export interface RemoteAccount extends PlatformInfo {
  email: string
  account_id: string
  account: string
  superseded_by: string
  first_seen_at: string
  profile_diff: string[]
  profile_incomplete: boolean
  profile_missing_fields: string[]
}

export interface RemoteAccountsResponse {
  list: RemoteAccount[]
  summary: {
    total: number
    linked: number
    auto_created: number
    unmatched: number
    superseded: number
    missing: number
  }
  counts_by_status: Record<string, number>
  page: number
  page_size: number
  total: number
}

export interface BillModelRow {
  bill_date: string
  account_id: string
  account: string
  upstream_id: string
  model_name: string
  bill_model_name: string
  cost_fen: number
  cost_yuan: string
  final: boolean
  fetched_at: string
  generation: number
}

export interface BillModelsResponse {
  list: BillModelRow[]
  by_account: { account_id: string; account: string; cost_fen: number; cost_yuan: string }[]
  by_model: { model_name: string; cost_fen: number; cost_yuan: string }[]
  total_cost_fen: number
  total_cost_yuan: string
  coverage: {
    days_requested: number
    days_present: number
    today: string
    today_final: boolean
    today_included: boolean
  }
  page: number
  page_size: number
  total: number
}

/** Upstream delivery option for 渠道用量 filters. */
export interface ChannelUsageAccount {
  upstream_id: string
  /** Same display id as /accounts cards (aid, else delivery id). */
  platform_account_id: string
  account: string
  account_id: string
  owner_user_id: string
  owner_nickname: string
  owner_username: string
}

export interface ChannelUsageOwner {
  id: string
  nickname: string
  username: string
}

export interface ChannelUsageAccountsResponse {
  list: ChannelUsageAccount[]
  /** Present for admins only. */
  owners?: ChannelUsageOwner[]
}

export interface ChannelUsageRow {
  bill_date: string
  upstream_id: string
  platform_account_id: string
  account: string
  owner_user_id: string
  owner_nickname: string
  owner_username: string
  cost_fen: number
  cost_usd_cents: number
  cost_usd: string
  final: boolean
}

export interface ChannelUsageResponse {
  list: ChannelUsageRow[]
  total_cost_fen: number
  total_cost_usd: string
  total_usd_cents: number
  usd_cny_rate: number
  page: number
  page_size: number
  total: number
  from: string
  to: string
  connection_id: string
}

export interface UsageHistoryPoint {
  observed_at: string
  cost_fen: number
  cost_yuan: string
  input_tokens: number
  output_tokens: number
  five_hour_used_percent: number
  five_hour_reset_at: string
  week_used_percent: number
  week_reset_at: string
}

export interface ProxyCheck {
  ok: boolean
  latency_ms: number
  exit_ip: string
  error_code: string
  checked_at: string
  route_mode: string
  message: string
}

/** Picker option from GET /lookups/proxies: identity only, no checks or auth. */
export interface ProxyOption {
  id: string
  name: string
  scheme: string
  host: string
  port: number
  enabled: boolean
}

export interface Proxy extends ProxyOption {
  version: number
  username: string
  password_configured: boolean
  latest_check: ProxyCheck | null
  /** Owning system user; empty = admin-only until assigned. */
  created_by_user_id?: string
  created_by_nickname?: string
  created_by_username?: string
}

/** Picker option from GET /lookups/platform-connections; never carries the token. */
export interface ConnectionOption {
  id: string
  name: string
  base_url: string
  timezone: string
  enabled: boolean
  /** Whether a console login is saved for the web channel. */
  web_configured?: boolean
}

export interface PlatformConnection extends ConnectionOption {
  version: number
  token_configured: boolean
  /** The platform's fixed RMB-per-dollar rate, 7.2 unless changed. */
  usd_cny_rate?: number
  web_username?: string
  web_suspended_at?: string
  web_suspended_reason?: string
}

export interface Job {
  id: string
  kind: string
  state: string
  account_id: string
  connection_id: string
  code: string
  message: string
  created_at: string
  updated_at: string
}

export interface AuditEvent {
  id: number
  action: string
  resource_type: string
  resource_id: string
  actor: string
  detail: string
  created_at: string
}

export interface Dashboard {
  accounts: {
    total: number
    ready: number
    archived: number
    platform_linked: number
    observed_recently: number
    by_plan: Record<string, number>
  }
  jobs: { running: number; queued: number; succeeded: number; failed: number }
  proxies: { healthy: number; enabled: number; disabled: number }
  platform: DashboardPlatform
}

/** Platform usage summary plus the health of the metrics sync. */
export interface DashboardPlatform {
  today_requests: number
  today_cost_fen: number
  last_sync_at: string
  last_sync_state: string
  sync_status?: 'ok' | 'failing' | 'never'
  error_code?: string
  error_message?: string
  error_hint?: string
  consecutive_failures?: number
  next_attempt_at?: string
  /** Web (console) channel: the worse of the account and bill syncs. */
  web_sync_health?: DashboardPlatform
  web_accounts_health?: DashboardPlatform
  web_bills_health?: DashboardPlatform
  remote_counts_by_status?: Record<string, number>
  near_limit_accounts?: number
  today_model_cost_fen?: number
  week_model_cost_fen?: number
  /** All-time bill total for the caller's ownership scope (admin = all). */
  total_model_cost_fen?: number
  cost_by_day?: SpendDay[]
  cost_by_model?: { model_name: string; cost_fen: number }[]
  cost_by_account?: { account_id: string; account: string; cost_fen: number }[]
}

/** One account on the dashboard's quota board. Percentages are 0–100; null when the platform reports no usage. */
export interface QuotaRow {
  account_id: string
  account: string
  alias: string
  /** Same display id as /accounts cards (aid, else delivery id). */
  platform_account_id: string
  created_by_user_id: string
  created_by_nickname: string
  created_by_username: string
  status: string
  five_hour_percent: number | null
  five_hour_reset_at: string
  week_percent: number | null
  week_reset_at: string
  cost_fen: number | null
}

export interface IdleRow {
  account_id: string
  account: string
  alias: string
  platform_account_id: string
  created_by_user_id: string
  created_by_nickname: string
  created_by_username: string
  week_percent: number | null
  cost_fen: number | null
  plan_end: string
  /** null when the plan end date is unknown. */
  days_left: number | null
}

export interface HourlyCost {
  /** Start of the hour, RFC 3339 UTC. */
  hour: string
  cost_fen: number
}

export interface RecentJob {
  id: string
  account_id: string
  account: string
  connection: string
  state: string
  code: string
  updated_at: string
}

/** GET /dashboard/insights: the fleet panels below the headline numbers. */
export interface DashboardInsights {
  /** The platform rate when every enabled connection shares one; null otherwise. */
  usd_cny_rate: number | null
  fleet: {
    accounts: number
    cost_fen_total: number
    input_tokens: number
    output_tokens: number
    avg_five_hour_percent: number
    avg_week_percent: number
  }
  quota: QuotaRow[]
  hourly: HourlyCost[]
  composition: {
    by_delivery: Record<string, number>
    expiry: Record<string, number>
    /** Earliest plan end date not yet passed; "" when none is known. */
    next_expiry?: string
  }
  /** In-use accounts with both usage windows under 10%, most plan days left first. */
  idle?: IdleRow[]
  /** null when the user may not see the task centre. */
  recent_jobs: RecentJob[] | null
}

export interface SpendDay {
  date: string
  cost_fen: number
  final: boolean
}

export type AttentionKind = 'near_limit' | 'banned' | 'expiring' | 'missing' | 'profile_diff'

/** One row of GET /dashboard/attention. */
export interface AttentionItem {
  kind: AttentionKind
  severity: 'danger' | 'warn' | 'info'
  account_id: string
  account: string
  alias: string
  label: string
  detail: string
  upstream_id: string
  reset_at: string
  expires_at: string
  value: number
}

export interface AttentionResponse {
  items: AttentionItem[]
  counts: Record<AttentionKind, number>
}

export interface PreflightCheck {
  code: string
  status: string
  message?: string
  hint?: string
  field?: string
}

export interface Preflight {
  account_id?: string
  can_submit: boolean
  checks: PreflightCheck[]
  versions?: { account: number }
  checked_at?: string
}

export interface ImportRow {
  row_index: number
  masked_identity: string
  action: string
  password_configured: boolean
  auth_configured: boolean
  issues: ImportIssue[]
}

export interface ImportIssue {
  code: string
  message: string
  field?: string
}

export interface ImportPreview {
  import_id: string
  expires_at: string
  rows: ImportRow[]
  valid_count: number
}

export interface ImportResult {
  import_id: string
  created: number
  skipped: number
}

export interface OnboardResult {
  account_id: string
  job_id: string
  state: string
}

/** Result of POST /accounts/{id}/reauthorize. */
export interface ReauthorizeResult {
  account_id: string
  platform_pushed: boolean
  upstream_id: string
  credential?: { source: 'saved' | 'replaced' }
}

export type ListRow = Record<string, unknown> & { id?: string }
