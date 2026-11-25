import { computed, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter, type LocationQuery, type LocationQueryRaw } from 'vue-router'
import { api, errorMessage } from '../api'
import { filterRows, hasValue, type Conditions, type ListConfig } from '../list-model'
import type { ListRow } from '../types/api'
import { usePolling } from './usePolling'

const lastQueries = new Map<string, LocationQueryRaw>()

/** Query string a nav link should restore for a list page (its last filters). */
export function rememberedQuery(routeName: string): LocationQueryRaw | undefined {
  return lastQueries.get(routeName)
}

function arrayData<T>(data: unknown): T[] {
  if (Array.isArray(data)) return data as T[]
  const items = (data as { items?: unknown })?.items
  return Array.isArray(items) ? (items as T[]) : []
}

export type AccountStatusTab = 'all' | 'using' | 'offline' | 'ban' | 'abnormal'

export type AccountTabCounts = Record<AccountStatusTab, number>

const emptyTabCounts = (): AccountTabCounts => ({
  all: 0,
  using: 0,
  offline: 0,
  ban: 0,
  abnormal: 0,
})

export interface ListPageOptions {
  /** extra requests issued with the list, e.g. lookups for a form */
  extra?: () => Promise<void>
  /** poll while this returns true (checked each tick) */
  pollWhile?: () => boolean
  pollIntervalMs?: number
}

function parseStatusTab(q: LocationQuery): AccountStatusTab {
  const tab = typeof q.tab === 'string' ? q.tab : ''
  if (tab === 'using' || tab === 'offline' || tab === 'ban' || tab === 'abnormal') return tab
  if (q.abnormal === '1' || q.abnormal === 'true') return 'abnormal'
  return tabForPlatformStatus(typeof q.f_platform_status === 'string' ? q.f_platform_status : '') || 'all'
}

/** Map a 平台状态 filter value onto a status tab, if it has one. */
function tabForPlatformStatus(status: string): AccountStatusTab | null {
  if (status === 'using' || status === 'offline') return status
  if (status === 'ban' || status === 'banned' || status === 'ban,banned' || status === 'banned,ban') return 'ban'
  return null
}

function platformStatusForTab(tab: AccountStatusTab): string | undefined {
  if (tab === 'using' || tab === 'offline') return tab
  if (tab === 'ban') return 'ban'
  return undefined
}

function tabOwnsPlatformStatus(tab: AccountStatusTab): boolean {
  return tab === 'using' || tab === 'offline' || tab === 'ban'
}

export function useListPage<T extends ListRow = ListRow>(
  config: Ref<ListConfig | undefined>,
  options: ListPageOptions = {},
) {
  const route = useRoute()
  const router = useRouter()
  const items = ref<T[]>([]) as Ref<T[]>
  const loading = ref(false)
  const error = ref('')
  const draftQuery = ref('')
  const query = ref('')
  const draftConditions = ref<Conditions>({})
  const conditions = ref<Conditions>({})
  const filtersOpen = ref(false)
  const statusTab = ref<AccountStatusTab>('all')
  const tabCounts = ref<AccountTabCounts>(emptyTabCounts())
  const currentPage = ref(1)
  const pageSize = ref(20)
  const serverTotal = ref(0)
  let sequence = 0
  const serverPaged = computed(() => !!config.value?.serverPaged)
  const statusTabs = computed(() => !!config.value?.statusTabs)

  // Tab-owned 平台状态 is shown in the filter panel, but not as a redundant chip.
  const activeFields = computed(
    () =>
      config.value?.filters.filter((f) => {
        if (!hasValue(conditions.value[f.key])) return false
        if (statusTabs.value && f.key === 'platform_status' && tabOwnsPlatformStatus(statusTab.value)) return false
        return true
      }) || [],
  )
  const activeFilterCount = computed(() => activeFields.value.length)
  const hasConditions = computed(() => !!query.value || !!activeFilterCount.value)
  const filteredItems = computed(() => {
    if (serverPaged.value) return items.value
    return config.value ? (filterRows(items.value, config.value, query.value, conditions.value) as T[]) : []
  })
  const pagedItems = computed(() =>
    serverPaged.value
      ? items.value
      : filteredItems.value.slice((currentPage.value - 1) * pageSize.value, currentPage.value * pageSize.value),
  )
  const total = computed(() => (serverPaged.value ? serverTotal.value : filteredItems.value.length))
  const abnormalOnly = computed(() => statusTabs.value && statusTab.value === 'abnormal')

  function mirrorTabIntoFilters(tab: AccountStatusTab, target: Conditions) {
    const status = platformStatusForTab(tab)
    if (status) target.platform_status = status
    else if (tab === 'all' || tab === 'abnormal') delete target.platform_status
  }

  function statusTabParams(params: URLSearchParams) {
    if (!statusTabs.value) return
    // 全部 / 使用中 / 已下线 hide abnormal rows; 已封禁 shows every ban/banned row;
    // 异常 is abnormal-only.
    if (statusTab.value === 'abnormal') params.set('abnormal', '1')
    else if (statusTab.value !== 'ban') params.set('abnormal', '0')
    if (statusTab.value === 'using') params.set('platform_status', 'using')
    else if (statusTab.value === 'offline') params.set('platform_status', 'offline')
    else if (statusTab.value === 'ban') params.set('platform_status', 'ban,banned')
  }

  function serverQuery(): string {
    const params = new URLSearchParams({ page: String(currentPage.value), page_size: String(pageSize.value) })
    if (query.value) params.set('q', query.value)
    for (const [key, value] of Object.entries(conditions.value)) {
      if (!hasValue(value)) continue
      // Tab owns using/offline/ban; other tabs can still use the platform_status filter.
      if (statusTabs.value && tabOwnsPlatformStatus(statusTab.value) && key === 'platform_status') continue
      params.set(key, String(value))
    }
    statusTabParams(params)
    return '?' + params.toString()
  }

  function readRouteQuery(q: LocationQuery) {
    query.value = typeof q.q === 'string' ? q.q : ''
    draftQuery.value = query.value
    const parsed: Conditions = {}
    for (const field of config.value?.filters || []) {
      const value = q['f_' + field.key]
      if (typeof value === 'string' && value !== '') parsed[field.key] = field.type === 'number' ? Number(value) : value
    }
    statusTab.value = statusTabs.value ? parseStatusTab(q) : 'all'
    if (statusTabs.value) mirrorTabIntoFilters(statusTab.value, parsed)
    conditions.value = parsed
    draftConditions.value = { ...parsed }
    filtersOpen.value = activeFilterCount.value > 0
  }

  function writeRouteQuery() {
    const next: LocationQueryRaw = {}
    if (query.value) next.q = query.value
    for (const [key, value] of Object.entries(conditions.value)) {
      if (!hasValue(value)) continue
      // Persist tab statuses via `tab=` only; keep other platform_status values as f_platform_status.
      if (statusTabs.value && key === 'platform_status' && tabOwnsPlatformStatus(statusTab.value)) continue
      next['f_' + key] = String(value)
    }
    if (statusTabs.value && statusTab.value !== 'all') next.tab = statusTab.value
    if (route.name) lastQueries.set(String(route.name), next)
    router.replace({ query: next }).catch(() => undefined)
  }

  async function load() {
    const endpoint = config.value?.endpoint
    if (!endpoint) return
    const seq = ++sequence
    loading.value = true
    try {
      const [list] = await Promise.all([
        api<unknown>(serverPaged.value ? endpoint + serverQuery() : endpoint),
        options.extra?.(),
      ])
      if (seq !== sequence) return
      items.value = arrayData<T>(list.data)
      const data = list.data as {
        pagination?: { total?: number }
        tab_counts?: Partial<AccountTabCounts>
      }
      serverTotal.value = data.pagination?.total ?? items.value.length
      if (statusTabs.value && data.tab_counts) {
        tabCounts.value = { ...emptyTabCounts(), ...data.tab_counts }
      }
      error.value = ''
    } catch (e) {
      if (seq === sequence) error.value = errorMessage(e)
    } finally {
      if (seq === sequence) loading.value = false
    }
  }

  function applySearch(): string {
    for (const field of config.value?.filters || []) {
      if (field.type !== 'date' || field.operator !== 'min') continue
      const upper = config.value?.filters.find((f) => f.field === field.field && f.operator === 'max')
      const lo = draftConditions.value[field.key]
      const hi = upper ? draftConditions.value[upper.key] : undefined
      if (upper && lo && hi && String(lo) > String(hi)) return field.label + '不能晚于结束日期'
    }
    query.value = draftQuery.value.trim()
    conditions.value = { ...draftConditions.value }
    // Filter → tab: keep them aligned when 平台状态 changes.
    if (statusTabs.value) {
      const status = String(conditions.value.platform_status || '')
      const matched = tabForPlatformStatus(status)
      if (matched) {
        statusTab.value = matched
        const mirrored = platformStatusForTab(matched)!
        conditions.value.platform_status = mirrored
        draftConditions.value.platform_status = mirrored
      } else if (status) {
        // Other statuses (在线 / 登录失败 …) stay on 异常 if already there; otherwise 全部.
        if (statusTab.value !== 'abnormal') statusTab.value = 'all'
      } else if (tabOwnsPlatformStatus(statusTab.value)) {
        statusTab.value = 'all'
      }
    }
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
    return ''
  }
  function resetFilters() {
    draftQuery.value = ''
    query.value = ''
    draftConditions.value = {}
    conditions.value = {}
    statusTab.value = 'all'
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }
  function removeQuery() {
    query.value = ''
    draftQuery.value = ''
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }
  function removeCondition(key: string) {
    delete conditions.value[key]
    delete draftConditions.value[key]
    if (key === 'platform_status' && tabOwnsPlatformStatus(statusTab.value)) statusTab.value = 'all'
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }
  function setStatusTab(tab: AccountStatusTab) {
    if (!statusTabs.value || statusTab.value === tab) return
    statusTab.value = tab
    // Tab → filter: mirror into 条件筛选 so the panel matches the active tab.
    mirrorTabIntoFilters(tab, conditions.value)
    mirrorTabIntoFilters(tab, draftConditions.value)
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }

  watch([total, pageSize], () => {
    const max = Math.max(1, Math.ceil(total.value / pageSize.value))
    if (currentPage.value > max) currentPage.value = max
  })
  watch([currentPage, pageSize], () => {
    if (serverPaged.value) void load()
  })

  const polling = usePolling(async () => {
    if (!loading.value && (options.pollWhile ? options.pollWhile() : true)) await load()
  }, options.pollIntervalMs ?? 3000)

  readRouteQuery(route.query)
  if (route.name) lastQueries.set(String(route.name), { ...route.query } as LocationQueryRaw)

  return {
    items,
    loading,
    error,
    draftQuery,
    query,
    draftConditions,
    conditions,
    filtersOpen,
    statusTab,
    statusTabs,
    tabCounts,
    abnormalOnly,
    currentPage,
    pageSize,
    activeFields,
    activeFilterCount,
    hasConditions,
    filteredItems,
    pagedItems,
    total,
    serverPaged,
    load,
    applySearch,
    resetFilters,
    removeQuery,
    removeCondition,
    setStatusTab,
    polling,
    invalidate: () => sequence++,
  }
}
