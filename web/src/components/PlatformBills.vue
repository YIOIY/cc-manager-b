<template>
  <div class="bills-workspace">
    <section class="panel bill-query-panel">
      <div class="bill-query-heading">
        <div class="bill-query-title">
          <span class="bill-query-icon"><ReceiptText :size="22" /></span>
          <div>
            <h2>账号区间账单</h2>
            <p>按所选日期汇总，每个账号一条账单</p>
          </div>
        </div>
        <div class="bill-range-presets" role="group" aria-label="日期快捷选择">
          <button
            v-for="days in [1, 7, 30, 90]"
            :key="days"
            type="button"
            :class="{ 'is-active': rangeIs(days) }"
            :aria-pressed="rangeIs(days)"
            :disabled="loading"
            @click="quickRange(days)"
          >
            {{ days === 1 ? '今天' : '近 ' + days + ' 天' }}
          </button>
        </div>
      </div>
      <form class="bill-summary-form" @submit.prevent="runQuery">
        <div class="bill-main-filters">
          <div class="bill-field">
            <label for="bill-connection">平台连接 <span>*</span></label
            ><el-select
              id="bill-connection"
              v-model="connectionId"
              :disabled="loading || initializing"
              placeholder="选择启用的平台"
              filterable
              ><el-option
                v-for="c in connections"
                :key="c.id"
                :label="c.name + (c.enabled ? '' : '（已停用）')"
                :value="c.id"
                :disabled="!c.enabled"
            /></el-select>
          </div>
          <div class="bill-field">
            <label for="bill-dates">查询日期 <span>*</span><small>包含开始和结束日期</small></label
            ><el-date-picker
              id="bill-dates"
              v-model="dates"
              type="daterange"
              value-format="YYYY-MM-DD"
              :clearable="false"
              :disabled="loading"
              :disabled-date="disabledDate"
              start-placeholder="开始日期"
              end-placeholder="结束日期"
              range-separator="至"
            />
          </div>
        </div>
        <div class="bill-field bill-account-field">
          <div class="bill-account-label">
            <label for="bill-accounts"
              >查询账号 <span>*</span><small>已选 {{ accountIds.length }} / 100</small></label
            >
            <div>
              <button
                type="button"
                :disabled="loading || accountsLoading || !eligibleAccounts.length || eligibleAccounts.length > 100"
                @click="selectAll"
              >
                全选 {{ eligibleAccounts.length }} 个</button
              ><button type="button" :disabled="loading || !accountIds.length" @click="accountIds = []">清空</button>
            </div>
          </div>
          <el-select
            id="bill-accounts"
            v-model="accountIds"
            multiple
            :multiple-limit="100"
            collapse-tags
            collapse-tags-tooltip
            :max-collapse-tags="3"
            filterable
            clearable
            :loading="accountsLoading"
            :disabled="loading || !connectionId || accountsLoading"
            placeholder="选择已上号账号，可搜索邮箱、别名或交付 ID"
            ><el-option
              class="bill-account-choice"
              v-for="a in eligibleAccounts"
              :key="a.id"
              :label="accountLabel(a)"
              :value="a.supplier_account_id"
              ><div class="bill-account-option">
                <span>{{ shortPlatformAlias(a.alias) || a.alias || a.account }}</span
                ><small>{{ a.alias ? a.account + ' · ' : '' }}交付 ID {{ a.supplier_account_id }}</small>
              </div></el-option
            ></el-select
          >
          <p class="bill-field-hint">
            使用上号返回的交付 ID 查询，单次最多 100 个账号。<span v-if="eligibleAccounts.length > 100"
              >当前账号较多，请选择本次需要查询的账号。</span
            >
          </p>
        </div>
        <div v-if="!enabledConnections.length && !initializing" class="bill-setup-notice">
          <Info :size="17" /><span>请先配置并启用平台连接。</span
          ><el-button text type="primary" @click="$emit('configure')">前往设置 <ArrowUpRight :size="14" /></el-button>
        </div>
        <div v-else-if="connectionId && !accountsLoading && !eligibleAccounts.length" class="bill-setup-notice">
          <Info :size="17" /><span>该平台暂无可查询的已上号账号。</span
          ><el-button text type="primary" @click="$emit('manage-accounts')"
            >账号管理 <ArrowUpRight :size="14"
          /></el-button>
        </div>
        <el-alert v-if="error" class="bill-error" :title="error" type="error" show-icon :closable="false" />
        <div class="bill-query-footer">
          <div>
            <CalendarDays :size="15" /><span
              >开始日期最早 {{ earliestDay }}<small>业务时区 {{ currentConnection?.timezone || '待配置' }}</small></span
            >
          </div>
          <el-button type="primary" native-type="submit" :loading="loading" :disabled="!canQuery"
            ><Search v-if="!loading" :size="16" />{{ loading ? '正在查询…' : '查询平台账单' }}</el-button
          >
        </div>
      </form>
    </section>

    <div v-if="result" class="bill-result-context">
      <div>
        <span class="bill-result-dot" /><strong
          >{{ result.range.start_ds }}<span>—</span>{{ result.range.end_ds }}</strong
        ><span>平台实际查询范围</span>
      </div>
      <span>{{ displayTime(result.queried_at) }} 查询</span>
    </div>
    <el-alert
      v-if="result && dirty"
      class="bill-result-notice"
      title="查询条件已修改，当前仍展示上次查询结果。点击“查询平台账单”更新。"
      type="info"
      show-icon
      :closable="false"
    />
    <el-alert
      v-if="result?.range_adjusted"
      class="bill-result-notice"
      :title="`平台调整了查询窗口：请求 ${result.requested_range.start_ds} 至 ${result.requested_range.end_ds}；以下数据按实际返回范围汇总。`"
      type="warning"
      show-icon
      :closable="false"
    />
    <section class="bill-summary" aria-label="所选账号账单合计">
      <div class="panel bill-cost-summary">
        <div>
          <span class="bill-stat-icon"><Wallet :size="18" /></span
          ><small>{{ result?.totals.is_partial ? '已返回账号消费合计' : '平台消费合计' }}</small>
        </div>
        <strong>{{ formatBillUsd(result?.totals.cost_usd) }}</strong>
        ><span>{{
          result
            ? `已返回 ${result.returned_count} / ${result.account_count} 个账号`
            : error
              ? '查询未完成'
              : '选择账号与日期后查询'
        }}</span>
      </div>
      <div class="panel">
        <div>
          <span class="bill-stat-icon"><Activity :size="18" /></span><small>请求数合计</small>
        </div>
        <strong>{{ formatBillCount(result?.totals.request_count) }}</strong
        ><span>{{ result ? '所选区间累计请求数' : error ? '查询未完成' : '尚未查询' }}</span>
      </div>
      <div class="panel bill-token-summary">
        <div>
          <span class="bill-stat-icon"><Layers3 :size="18" /></span><small>Token 用量合计</small>
        </div>
        <div class="bill-token-pair">
          <span
            ><small>输入</small><b>{{ formatBillCount(result?.totals.prompt_tokens) }}</b></span
          ><i /><span
            ><small>输出</small><b>{{ formatBillCount(result?.totals.completion_tokens) }}</b></span
          >
        </div>
        <span
          >缓存读取 {{ formatBillCount(result?.totals.cache_read_input_tokens) }}<em>·</em>缓存写入
          {{ formatBillCount(result?.totals.cache_write_input_tokens) }}</span
        >
      </div>
    </section>
    <div v-if="result && !result.complete" class="bill-partial-notice">
      <Info :size="18" />
      <div>
        <strong>{{ result.returned_count ? '部分账号未返回有效账单' : '平台未返回有效账单' }}</strong>
        <p>
          已返回 {{ result.returned_count }} 个<span v-if="result.no_data_count"
            >，未返回 {{ result.no_data_count }} 个</span
          ><span v-if="result.invalid_count">，数据异常 {{ result.invalid_count }} 个</span>。{{
            result.returned_count ? '合计仅包含有效返回的账号。' : '暂无可计算的合计。'
          }}未返回不代表零消费。
        </p>
      </div>
    </div>

    <section class="panel bill-table-panel">
      <div class="bill-table-heading">
        <div>
          <span class="bill-table-icon"><ListFilter :size="18" /></span>
          <h2>账号账单汇总</h2>
          <span v-if="result" class="bill-result-count">{{ result.account_count }} 个账号</span>
        </div>
        <span>金额单位：美元</span>
      </div>
      <el-table :data="pagedRows" v-loading="loading" row-key="account_id" class="bill-summary-table">
        <el-table-column label="账号" min-width="245"
          ><template #default="{ row }"
            ><strong class="bill-account-name">{{ shortPlatformAlias(row.alias) || row.alias || row.account }}</strong>
            <div v-if="row.alias" class="bill-secondary">{{ row.account }}</div></template
          ></el-table-column
        >
        <el-table-column label="交付 ID" width="105"
          ><template #default="{ row }"
            ><span class="bill-delivery-id">{{ row.supplier_account_id }}</span></template
          ></el-table-column
        >
        <el-table-column
          v-for="column in tokenColumns"
          :key="column.key"
          :label="column.label"
          :min-width="column.width"
          align="right"
          class-name="bill-number-column"
          ><template #default="{ row }">{{ formatBillCount(row[column.key]) }}</template></el-table-column
        >
        <el-table-column label="消费（美元）" min-width="135" align="right"
          ><template #default="{ row }"
            ><span class="bill-cost-cell">{{ formatBillUsd(row.cost_usd) }}</span></template
          ></el-table-column
        >
        <el-table-column label="返回状态" width="108" fixed="right"
          ><template #default="{ row }"
            ><el-tag :type="summaryStates[row.state as keyof typeof summaryStates].tone" effect="light">{{
              summaryStates[row.state as keyof typeof summaryStates].label
            }}</el-tag></template
          ></el-table-column
        >
        <template #empty
          ><div class="bill-empty-state">
            <span><ReceiptText :size="29" :stroke-width="1.4" /></span
            ><strong>{{ loading ? '正在向平台查询账单' : error ? '查询暂未完成' : '选择范围，查看账号账单' }}</strong>
            <p>
              {{
                error
                  ? '请根据上方提示调整查询条件或平台设置后重试。'
                  : '指定账号和日期后，查看每个账号的请求数、Token 与消费汇总。'
              }}
            </p>
          </div></template
        >
      </el-table>
      <div class="bill-table-footer">
        <span><Info :size="13" />每行是整个查询区间的合计；“—”表示平台未返回有效数值。</span
        ><el-pagination
          v-if="result && result.items.length > 10"
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="result.items.length"
          layout="sizes, prev, pager, next"
          small
        />
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, errorMessage } from '../api'
import { billGet, billPost, businessDay, shiftBillDay, formatBillCount, type BillConnection } from '../bills-api'
import { formatUsd } from '../model/money'

function formatBillUsd(value: string | null | undefined) {
  if (value == null || value === '') return '—'
  return formatUsd(value)
}
import {
  eligibleBillAccount,
  summaryValidation,
  summaryStates,
  type SummaryAccount,
  type SummaryResult,
} from '../bill-summary'
import { shortPlatformAlias } from '../model/platform-card'
import {
  Search,
  ReceiptText,
  Info,
  ArrowUpRight,
  CalendarDays,
  Wallet,
  Activity,
  Layers3,
  ListFilter,
} from 'lucide-vue-next'
defineEmits<{ (event: 'configure'): void; (event: 'manage-accounts'): void }>()

const connections = ref<BillConnection[]>([]),
  connectionId = ref(''),
  accounts = ref<SummaryAccount[]>([]),
  accountIds = ref<string[]>([]),
  dates = ref<string[]>([]),
  result = ref<SummaryResult>(),
  initializing = ref(true),
  accountsLoading = ref(false),
  loading = ref(false),
  error = ref(''),
  page = ref(1),
  pageSize = ref(20),
  lastSelection = ref('')
let sequence = 0,
  stopped = false
const currentConnection = computed(() => connections.value.find((c) => c.id === connectionId.value)),
  enabledConnections = computed(() => connections.value.filter((c) => c.enabled)),
  eligibleAccounts = computed(() => accounts.value.filter(eligibleBillAccount))
const today = computed(() => {
    try {
      return businessDay(currentConnection.value?.timezone || 'Asia/Shanghai')
    } catch {
      return businessDay('Asia/Shanghai')
    }
  }),
  earliestDay = computed(() => shiftBillDay(today.value, -90))
const validTimezone = computed(() => {
  try {
    return (
      !!currentConnection.value?.timezone &&
      currentConnection.value.timezone !== 'Local' &&
      !!businessDay(currentConnection.value.timezone)
    )
  } catch {
    return false
  }
})
const canQuery = computed(
  () =>
    !!currentConnection.value?.enabled &&
    validTimezone.value &&
    !!accountIds.value.length &&
    !accountsLoading.value &&
    !initializing.value,
)
const selectionKey = computed(() => JSON.stringify([connectionId.value, [...accountIds.value].sort(), dates.value])),
  dirty = computed(() => !!result.value && selectionKey.value !== lastSelection.value)
const pagedRows = computed(
  () => result.value?.items.slice((page.value - 1) * pageSize.value, page.value * pageSize.value) || [],
)
const tokenColumns = [
  { key: 'request_count', label: '请求数', width: 110 },
  { key: 'prompt_tokens', label: '输入 Token', width: 135 },
  { key: 'completion_tokens', label: '输出 Token', width: 135 },
  { key: 'cache_read_input_tokens', label: '缓存读取 Token', width: 150 },
  { key: 'cache_write_input_tokens', label: '缓存写入 Token', width: 150 },
]
function accountLabel(a: SummaryAccount) {
  const alias = shortPlatformAlias(a.alias) || a.alias
  return (alias ? alias + ' · ' : '') + a.account + ' · ' + a.supplier_account_id
}
function displayTime(value: string) {
  return new Date(value).toLocaleString('zh-CN', { timeZone: result.value?.timezone || 'Asia/Shanghai', hour12: false })
}
function disabledDate(value: Date) {
  const day = [
    value.getFullYear(),
    String(value.getMonth() + 1).padStart(2, '0'),
    String(value.getDate()).padStart(2, '0'),
  ].join('-')
  return day < earliestDay.value
}
function quickRange(days: number) {
  dates.value = [shiftBillDay(today.value, -days + 1), today.value]
  error.value = ''
}
function rangeIs(days: number) {
  return dates.value?.[0] === shiftBillDay(today.value, -days + 1) && dates.value?.[1] === today.value
}
function selectAll() {
  if (eligibleAccounts.value.length <= 100) accountIds.value = eligibleAccounts.value.map((a) => a.supplier_account_id)
}
async function runQuery() {
  if (loading.value) return
  error.value = summaryValidation(connectionId.value, accountIds.value, dates.value || [], today.value)
  if (error.value) return
  if (!validTimezone.value) {
    error.value = '请先在平台设置中配置有效的业务时区'
    return
  }
  const request = ++sequence,
    key = selectionKey.value
  loading.value = true
  result.value = undefined
  page.value = 1
  try {
    const value = await billPost<SummaryResult>('/bills/summary', {
      connection_id: connectionId.value,
      account_ids: accountIds.value.map(Number),
      start_ds: dates.value[0],
      end_ds: dates.value[1],
    })
    if (!stopped && request === sequence) {
      result.value = value
      lastSelection.value = key
    }
  } catch (e) {
    if (!stopped && request === sequence) error.value = errorMessage(e)
  } finally {
    if (request === sequence) loading.value = false
  }
}
watch(connectionId, async (conn) => {
  const request = ++sequence
  result.value = undefined
  accounts.value = []
  accountIds.value = []
  error.value = ''
  loading.value = false
  page.value = 1
  quickRange(30)
  if (!conn) return
  accountsLoading.value = true
  try {
    const value = await billGet<SummaryAccount[]>('/bills/accounts?connection_id=' + encodeURIComponent(conn))
    if (!stopped && request === sequence) {
      accounts.value = value
      if (eligibleAccounts.value.length <= 100) selectAll()
      if (!validTimezone.value) error.value = '平台业务时区无效，请先在平台设置中修正'
    }
  } catch (e) {
    if (!stopped && request === sequence) error.value = errorMessage(e)
  } finally {
    if (request === sequence) accountsLoading.value = false
  }
})
watch(pageSize, () => {
  page.value = 1
})
onMounted(async () => {
  quickRange(30)
  try {
    const value = (await api<BillConnection[]>('/lookups/platform-connections')).data
    if (!stopped) {
      connections.value = value
      connectionId.value = value.find((c) => c.enabled)?.id || ''
    }
  } catch (e) {
    if (!stopped) error.value = errorMessage(e)
  } finally {
    initializing.value = false
  }
})
onUnmounted(() => {
  stopped = true
  sequence++
})
</script>

<style scoped src="../styles/bill-summary.css"></style>
