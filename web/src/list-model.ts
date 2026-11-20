export interface Choice {
  label: string
  value: string
}
export interface FilterField {
  key: string
  label: string
  field: string
  type: 'select' | 'text' | 'date' | 'number'
  operator?: 'min' | 'max'
  options?: Choice[]
}
export interface Column {
  field: string
  label: string
  width?: number
  minWidth?: number
  format?: 'date' | 'boolean' | 'status' | 'plan' | 'money'
}
export interface ListConfig {
  name: string
  endpoint: string
  /** paginate and filter on the server; filter keys map 1:1 to query params */
  serverPaged?: boolean
  /** accounts list: toggle beside search to show only abnormal rows (default hides them) */
  abnormalToggle?: boolean
  description: string
  search: string[]
  placeholder: string
  filters: FilterField[]
  columns: Column[]
}
const choices = (values: string[]): Choice[] => values.map((value) => ({ label: value, value }))
const enabled: FilterField = {
  key: 'enabled',
  label: '启用状态',
  field: 'enabled',
  type: 'select',
  options: [
    { label: '启用', value: 'true' },
    { label: '停用', value: 'false' },
  ],
}
const created: FilterField[] = [
  { key: 'created_from', label: '创建日期起', field: 'created_at', type: 'date', operator: 'min' },
  { key: 'created_to', label: '创建日期止', field: 'created_at', type: 'date', operator: 'max' },
]
export const plans = [
  { label: 'CC Max 20x', value: 'claude/cc-max-20x' },
  { label: 'CC Max 5x', value: 'claude/cc-max-5x' },
  { label: 'CC Pro', value: 'claude/cc-pro' },
]
export const states: Record<string, string> = {
  queued: '排队中',
  running: '执行中',
  succeeded: '已成功',
  failed: '已失败',
  unknown: '结果待确认',
}
export const listConfigs: Record<string, ListConfig> = {
  accounts: {
    name: '账号库存',
    endpoint: '/accounts',
    serverPaged: true,
    abnormalToggle: true,
    description: '管理账号资料、代理绑定与平台交付；消耗字段来自供应商观测快照。',
    search: [
      'account',
      'id',
      'plan',
      'register_location',
      'proxy_id',
      'platform_account_id',
      'platform_alias',
      'platform_status',
    ],
    placeholder: '搜索账号、平台账号 ID、别名、状态或套餐',
    filters: [
      {
        key: 'onboarding_state',
        label: '上号状态',
        field: 'onboarding_state',
        type: 'select',
        options: [
          { label: '待上号', value: 'pending' },
          { label: '上号中', value: 'submitting' },
          { label: '已上号', value: 'linked' },
          { label: '上号失败', value: 'failed' },
          { label: '结果待确认', value: 'unknown' },
        ],
      },
      { key: 'plan', label: '账号套餐', field: 'plan', type: 'select', options: plans },
      {
        key: 'five_hour_min',
        label: '5 小时窗口',
        field: 'five_hour_min',
        type: 'select',
        options: [
          { label: '≥ 80%（接近限额）', value: '80' },
          { label: '≥ 95%', value: '95' },
        ],
      },
      {
        key: 'archived',
        label: '库存状态',
        field: 'archived',
        type: 'select',
        options: [
          { label: '正常库存', value: 'false' },
          { label: '已归档', value: 'true' },
        ],
      },
      {
        key: 'proxy',
        label: '代理绑定',
        field: 'proxy_id',
        type: 'select',
        options: [
          { label: '已绑定', value: 'present' },
          { label: '未绑定', value: 'absent' },
        ],
      },
      {
        key: 'platform_status',
        label: '平台状态',
        field: 'platform_status',
        type: 'select',
        options: [
          { label: '在线', value: 'online' },
          { label: '使用中', value: 'using' },
          { label: '待登录', value: 'ready' },
          { label: '已下线', value: 'offline' },
          { label: '已封禁', value: 'ban' },
          { label: '已封禁(banned)', value: 'banned' },
          { label: '初始化', value: 'init' },
          { label: '已绑定', value: 'bound' },
          { label: '养号中', value: 'cooking' },
          { label: '养号失败', value: 'cooking_failed' },
          { label: '登录失败', value: 'login_failed' },
          { label: '待重新登录', value: 're_login' },
          { label: '重新登录失败', value: 're_login_failed' },
          { label: '待激活', value: 'pending_activation' },
          { label: '绑定失败', value: 'bound_failed' },
          { label: '同步失败', value: 'sync_failed' },
          { label: '待验证', value: 'wait_check' },
          { label: '验证失败', value: 'check_fail' },
          { label: '故障', value: 'fault' },
          { label: '已暂停', value: 'suspended' },
          { label: '欠费', value: 'arrearage' },
        ],
      },
      { key: 'end_from', label: '套餐到期日起', field: 'plan_end_time', type: 'date', operator: 'min' },
      { key: 'end_to', label: '套餐到期日止', field: 'plan_end_time', type: 'date', operator: 'max' },
      ...created,
    ],
    columns: [
      { field: 'plan', label: '套餐', minWidth: 135, format: 'plan' },
      { field: 'register_location', label: '注册地区', width: 100 },
      { field: 'platform_account_id', label: '平台账号 ID', minWidth: 135 },
      { field: 'platform_alias', label: '平台别名', minWidth: 145 },
      { field: 'platform_status', label: '平台状态', width: 110, format: 'status' },
      { field: 'platform_requests', label: '今日请求', width: 100 },
      { field: 'platform_prompt_tokens', label: '输入 Token', minWidth: 115 },
      { field: 'platform_completion_tokens', label: '输出 Token', minWidth: 115 },
      { field: 'platform_success_rate_percent', label: '成功率', width: 95 },
      { field: 'platform_cost_fen', label: '平台消费（元）', minWidth: 130, format: 'money' },
      { field: 'platform_observed_at', label: '最后观测', minWidth: 170, format: 'date' },
      { field: 'plan_end_time', label: '套餐到期', width: 120 },
      { field: 'proxy_id', label: '代理绑定', minWidth: 130 },
      { field: 'archived', label: '库存状态', width: 110, format: 'status' },
    ],
  },
  proxies: {
    name: '代理列表',
    endpoint: '/proxies',
    description: '管理账号代理，分别查看本机检测与供应商上号结果。',
    search: ['name', 'id', 'host', 'port', 'scheme'],
    placeholder: '搜索名称、主机、端口或代理 ID',
    filters: [
      {
        key: 'scheme',
        label: '代理协议',
        field: 'scheme',
        type: 'select',
        options: choices(['http', 'https', 'socks5', 'socks5h']),
      },
      enabled,
      { key: 'host', label: '主机包含', field: 'host', type: 'text' },
      { key: 'port', label: '端口', field: 'port', type: 'number' },
    ],
    columns: [
      { field: 'scheme', label: '协议', width: 110 },
      { field: 'host', label: '主机地址', minWidth: 160 },
      { field: 'port', label: '端口', width: 85 },
      { field: 'enabled', label: '启用状态', width: 110, format: 'status' },
    ],
  },
  jobs: {
    name: '任务记录',
    endpoint: '/jobs',
    serverPaged: true,
    description: '跟踪上传任务与执行结果，每 3 秒自动刷新。',
    search: ['id', 'account_id', 'kind', 'state'],
    placeholder: '搜索任务 ID、账号 ID 或任务类型',
    filters: [
      {
        key: 'state',
        label: '任务状态',
        field: 'state',
        type: 'select',
        options: Object.entries(states).map(([value, label]) => ({ value, label })),
      },
      {
        key: 'kind',
        label: '任务类型',
        field: 'kind',
        type: 'select',
        options: [{ label: '账号上传', value: 'account.submit' }],
      },
      { key: 'account_id', label: '账号 ID 包含', field: 'account_id', type: 'text' },
      ...created,
    ],
    columns: [
      { field: 'kind', label: '任务类型', minWidth: 120 },
      { field: 'account_id', label: '账号 ID', minWidth: 180 },
      { field: 'state', label: '任务状态', width: 120, format: 'status' },
      { field: 'created_at', label: '创建时间', minWidth: 165, format: 'date' },
      { field: 'updated_at', label: '更新时间', minWidth: 165, format: 'date' },
    ],
  },
  platformBills: {
    name: '平台离线账单',
    endpoint: '/platform-usage/bills/summary',
    description: '指定日期范围，查询每个账号的请求数、Token 与平台消费汇总。',
    search: [],
    placeholder: '',
    filters: [],
    columns: [],
  },
  connections: {
    name: '平台连接',
    endpoint: '/platform-connections',
    description: '维护供应商连接与访问配置，Token 只写入、不回显。',
    search: ['id', 'name', 'base_url', 'timezone'],
    placeholder: '搜索连接名称、地址、ID 或时区',
    filters: [
      enabled,
      {
        key: 'token',
        label: 'Token 配置',
        field: 'token_configured',
        type: 'select',
        options: [
          { label: '已配置', value: 'true' },
          { label: '未配置', value: 'false' },
        ],
      },
      { key: 'timezone', label: '时区包含', field: 'timezone', type: 'text' },
    ],
    columns: [
      { field: 'base_url', label: '平台地址', minWidth: 240 },
      { field: 'timezone', label: '时区', minWidth: 135 },
      { field: 'enabled', label: '启用状态', width: 105, format: 'status' },
      { field: 'token_configured', label: 'Token', width: 105, format: 'boolean' },
    ],
  },
  audit: {
    name: '审计事件',
    endpoint: '/audit-events',
    serverPaged: true,
    description: '按操作、资源与时间查看已记录的审计事件。',
    search: ['id', 'action', 'resource_type', 'resource_id'],
    placeholder: '搜索操作、资源类型或资源 ID',
    filters: [
      { key: 'action', label: '操作包含', field: 'action', type: 'text' },
      { key: 'resource_type', label: '资源类型包含', field: 'resource_type', type: 'text' },
      { key: 'resource_id', label: '资源 ID 包含', field: 'resource_id', type: 'text' },
      ...created,
    ],
    columns: [
      { field: 'action', label: '操作', minWidth: 140 },
      { field: 'resource_type', label: '资源类型', minWidth: 130 },
      { field: 'resource_id', label: '资源 ID', minWidth: 180 },
      { field: 'created_at', label: '发生时间', minWidth: 165, format: 'date' },
    ],
  },
}
export type Conditions = Record<string, string | number | null | undefined>
export const hasValue = (value: unknown) => value !== '' && value !== null && value !== undefined
export function filterRows<T extends Record<string, unknown>>(
  rows: T[],
  config: ListConfig,
  query: string,
  conditions: Conditions,
): T[] {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  return rows.filter((row) => {
    const text = config.search
      .map((key) => String(row[key] ?? ''))
      .join(' ')
      .toLocaleLowerCase()
    if (!words.every((word) => text.includes(word))) return false
    return config.filters.every((filter) => {
      const expected = conditions[filter.key]
      if (!hasValue(expected)) return true
      const actual = row[filter.field]
      if (filter.key === 'proxy') return expected === 'present' ? !!actual : !actual
      if (!hasValue(actual)) return false
      if (filter.type === 'date') {
        const day = String(actual).slice(0, 10)
        return filter.operator === 'min' ? day >= String(expected) : day <= String(expected)
      }
      if (filter.type === 'number') {
        if (!Number.isFinite(Number(actual))) return false
        return filter.operator === 'min'
          ? Number(actual) >= Number(expected)
          : filter.operator === 'max'
            ? Number(actual) <= Number(expected)
            : Number(actual) === Number(expected)
      }
      if (filter.type === 'select') return String(actual) === String(expected)
      return String(actual).toLocaleLowerCase().includes(String(expected).trim().toLocaleLowerCase())
    })
  })
}
