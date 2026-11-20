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

export interface ListPageOptions {
  /** extra requests issued with the list, e.g. lookups for a form */
  extra?: () => Promise<void>
  /** poll while this returns true (checked each tick) */
  pollWhile?: () => boolean
  pollIntervalMs?: number
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
  const abnormalOnly = ref(false)
  const currentPage = ref(1)
  const pageSize = ref(20)
  const serverTotal = ref(0)
  let sequence = 0
  const serverPaged = computed(() => !!config.value?.serverPaged)
  const abnormalToggle = computed(() => !!config.value?.abnormalToggle)

  const activeFields = computed(() => config.value?.filters.filter((f) => hasValue(conditions.value[f.key])) || [])
  const activeFilterCount = computed(() => activeFields.value.length)
  const hasConditions = computed(
    () => !!query.value || !!activeFilterCount.value || (abnormalToggle.value && abnormalOnly.value),
  )
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

  function serverQuery(): string {
    const params = new URLSearchParams({ page: String(currentPage.value), page_size: String(pageSize.value) })
    if (query.value) params.set('q', query.value)
    for (const [key, value] of Object.entries(conditions.value)) if (hasValue(value)) params.set(key, String(value))
    if (abnormalToggle.value) params.set('abnormal', abnormalOnly.value ? '1' : '0')
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
    conditions.value = parsed
    draftConditions.value = { ...parsed }
    abnormalOnly.value = abnormalToggle.value && (q.abnormal === '1' || q.abnormal === 'true')
    filtersOpen.value = activeFilterCount.value > 0
  }

  function writeRouteQuery() {
    const next: LocationQueryRaw = {}
    if (query.value) next.q = query.value
    for (const [key, value] of Object.entries(conditions.value)) if (hasValue(value)) next['f_' + key] = String(value)
    if (abnormalToggle.value && abnormalOnly.value) next.abnormal = '1'
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
      const pagination = (list.data as { pagination?: { total?: number } })?.pagination
      serverTotal.value = pagination?.total ?? items.value.length
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
    abnormalOnly.value = false
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
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }
  function toggleAbnormal() {
    if (!abnormalToggle.value) return
    abnormalOnly.value = !abnormalOnly.value
    currentPage.value = 1
    writeRouteQuery()
    if (serverPaged.value) void load()
  }
  function clearAbnormal() {
    if (!abnormalOnly.value) return
    abnormalOnly.value = false
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
    abnormalOnly,
    abnormalToggle,
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
    toggleAbnormal,
    clearAbnormal,
    polling,
    invalidate: () => sequence++,
  }
}
