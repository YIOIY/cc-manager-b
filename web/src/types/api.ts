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
  | 'proxies.view'
  | 'proxies.create'
  | 'proxies.update'
  | 'proxies.archive'
  | 'proxies.delete'
  | 'proxies.test'
  | 'proxies.settings'
  | 'jobs.view'
  | 'bills.view'
  | 'bills.query'
  | 'bills.sync'
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

export interface RoleRef {
  id: string
  name: string
  builtin: boolean
}

export interface SessionUser {
  id: string
  username: string
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
  enabled: boolean
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
  onboarding_state: OnboardingState
  credentials_configured?: boolean
  password_configured?: boolean
  submission_job?: SubmissionJob
  balance?: number | string
  tags?: string[]
  remark?: string
  payment_method?: string
  register_device?: string
  recharge_time?: string
  warranty_period?: number | string
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
}

/** Picker option from GET /lookups/platform-connections; never carries the token. */
export interface ConnectionOption {
  id: string
  name: string
  base_url: string
  timezone: string
  enabled: boolean
}

export interface PlatformConnection extends ConnectionOption {
  version: number
  token_configured: boolean
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

export type ListRow = Record<string, unknown> & { id?: string }
