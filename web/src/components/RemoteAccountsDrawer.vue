<template>
  <el-drawer
    :model-value="modelValue"
    title="平台账号对账"
    size="72%"
    class="remote-drawer"
    @update:model-value="emit('update:modelValue', $event)"
    @open="load"
  >
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <div class="remote-toolbar">
      <el-select v-model="status" placeholder="全部状态" clearable style="width: 160px" @change="reload">
        <el-option
          v-for="(n, key) in data?.counts_by_status || {}"
          :key="key"
          :label="runtimeLabel(key) + '（' + n + '）'"
          :value="key"
        />
      </el-select>
      <el-input
        v-model="keyword"
        placeholder="邮箱、别名或平台 ID"
        clearable
        style="width: 240px"
        @keyup.enter="reload"
        @clear="reload"
      />
      <el-button :loading="loading" @click="reload"><RefreshCw :size="14" />刷新</el-button>
    </div>
    <div v-if="data" class="remote-summary">
      <span
        >平台共 <b>{{ data.summary.total }}</b></span
      >
      <span
        >已关联 <b>{{ data.summary.linked }}</b></span
      >
      <span
        >自动建号 <b>{{ data.summary.auto_created }}</b></span
      >
      <span
        >历史交付 <b>{{ data.summary.superseded }}</b></span
      >
      <span :class="{ attention: data.summary.missing }"
        >已消失 <b>{{ data.summary.missing }}</b></span
      >
      <span :class="{ attention: data.summary.unmatched }"
        >无法匹配 <b>{{ data.summary.unmatched }}</b></span
      >
    </div>
    <el-table :data="data?.list || []" v-loading="loading" size="small" row-key="upstream_id">
      <el-table-column label="交付 ID" width="92" prop="upstream_id" />
      <el-table-column label="平台账号 ID" width="104" prop="aid" />
      <el-table-column label="账号" min-width="200">
        <template #default="{ row }">
          <div class="remote-account">
            <span :title="row.email">{{ row.email || '（平台未返回邮箱）' }}</span>
            <small :title="row.alias">{{ shortPlatformAlias(row.alias) || row.alias }}</small>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="平台状态" width="110">
        <template #default="{ row }">
          <span class="mini-badge" :class="statusTone(row.status)">{{ runtimeLabel(row.status) }}</span>
          <Lock v-if="row.locked" :size="12" class="locked-icon" />
        </template>
      </el-table-column>
      <el-table-column label="本地关联" min-width="150">
        <template #default="{ row }">
          <template v-if="row.account_id">
            <span class="remote-local">{{ row.account || row.account_id }}</span>
            <span v-if="row.auto_created" class="mini-badge teal">自动建号</span>
          </template>
          <span v-else class="remote-muted">未关联</span>
        </template>
      </el-table-column>
      <el-table-column label="标记" min-width="170">
        <template #default="{ row }">
          <span v-if="row.superseded_by" class="mini-badge" :title="'当前交付 ' + row.superseded_by"
            >历史交付 → {{ row.superseded_by }}</span
          >
          <span v-if="row.missing_since" class="mini-badge attention" :title="row.missing_since">已从平台消失</span>
          <span v-if="row.profile_diff?.length" class="mini-badge yellow" :title="row.profile_diff.join('、')"
            >资料不一致</span
          >
          <span v-if="row.profile_incomplete" class="mini-badge" :title="row.profile_missing_fields.join('、')"
            >资料不完整</span
          >
        </template>
      </el-table-column>
      <el-table-column label="原因" min-width="220">
        <template #default="{ row }">
          <span class="remote-reason" :title="row.reason_text">{{ row.reason_text || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="平台创建" width="140" prop="remote_created_at" />
    </el-table>
    <div class="remote-pager">
      <el-pagination
        layout="total, prev, pager, next"
        :total="data?.total || 0"
        :page-size="pageSize"
        :current-page="page"
        @current-change="onPage"
      />
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Lock, RefreshCw } from 'lucide-vue-next'
import { api } from '../api'
import ErrorNotice from './ErrorNotice.vue'
import type { RemoteAccountsResponse } from '../types/api'
import { runtimeLabel } from '../account-workflow'
import { shortPlatformAlias } from '../model/platform-card'

const props = defineProps<{ modelValue: boolean; connectionId: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const data = ref<RemoteAccountsResponse | null>(null)
const loading = ref(false)
const notice = ref<unknown | null>(null)
const status = ref('')
const keyword = ref('')
const page = ref(1)
const pageSize = 20

const failing = new Set([
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
function statusTone(s: string) {
  if (s === 'offline') return 'danger'
  return failing.has(s) ? 'attention' : s === 'using' || s === 'online' ? 'teal' : ''
}
async function load() {
  if (!props.connectionId) return
  loading.value = true
  notice.value = null
  try {
    const params = new URLSearchParams({ page: String(page.value), page_size: String(pageSize) })
    if (status.value) params.set('status', status.value)
    if (keyword.value.trim()) params.set('q', keyword.value.trim())
    data.value = (
      await api<RemoteAccountsResponse>('/platform-connections/' + props.connectionId + '/remote-accounts?' + params)
    ).data
  } catch (e) {
    notice.value = e
  } finally {
    loading.value = false
  }
}
function reload() {
  page.value = 1
  void load()
}
function onPage(p: number) {
  page.value = p
  void load()
}
</script>

<style scoped>
.mini-badge {
  display: inline-block;
  padding: 3px 7px;
  border-radius: 5px;
  font: 700 10px/1 monospace;
  background: var(--surface-muted, #f0f2f5);
  color: var(--text-secondary, #606266);
}
.mini-badge.teal {
  background: #d6eaff;
  color: #0062d6;
}
.mini-badge.yellow {
  background: #fdf6d8;
  color: #8a6d00;
}
.mini-badge.danger {
  background: rgba(220, 38, 38, 0.14);
  color: #c62828;
  font-weight: 700;
}
.mini-badge.attention {
  background: var(--el-color-danger-light-9);
  color: var(--el-color-danger);
}
.mini-badge + .mini-badge {
  margin-left: 4px;
}
.remote-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.remote-summary {
  display: flex;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 12px;
  font-size: 13px;
  color: var(--text-secondary, #606266);
}
.remote-summary b {
  color: var(--text-primary, #303133);
  margin-left: 2px;
}
.remote-summary .attention b {
  color: var(--el-color-danger);
}
.remote-account {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.remote-account small,
.remote-muted {
  color: var(--text-tertiary, #909399);
}
.remote-account span,
.remote-reason,
.remote-local {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: block;
}
.locked-icon {
  margin-left: 4px;
  vertical-align: middle;
}
.remote-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
