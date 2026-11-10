<template>
  <el-dialog
    :model-value="open"
    title="同步平台账单"
    width="min(580px, calc(100vw - 24px))"
    destroy-on-close
    @update:model-value="$emit('update:open', $event)"
  >
    <el-alert
      type="info"
      :closable="false"
      show-icon
      :title="`按 ${timezone} 业务日期补采；平台账单约延迟 2 小时，数据可修订。`"
    />
    <el-form label-position="top" class="sync-form" @submit.prevent="submit">
      <el-form-item label="同步账号范围"
        ><el-radio-group v-model="scope"
          ><el-radio-button value="selected">指定账号</el-radio-button
          ><el-radio-button value="all_linked">全部关联账号</el-radio-button></el-radio-group
        ></el-form-item
      >
      <el-form-item v-if="scope === 'selected'" label="账号"
        ><el-select v-model="ids" multiple filterable clearable placeholder="选择要同步的账号"
          ><el-option
            v-for="a in linkedAccounts"
            :key="a.id"
            :label="a.account + ' · ' + a.supplier_account_id"
            :value="a.id" /></el-select
      ></el-form-item>
      <el-form-item label="账单日期"
        ><el-date-picker
          v-model="dates"
          type="daterange"
          value-format="YYYY-MM-DD"
          :clearable="false"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          :disabled-date="disabledDate"
      /></el-form-item>
      <p class="muted">可补采 {{ earliest }} 至 {{ today }}；本次 {{ count }} 个账号。历史记录查询不受此窗口限制。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer
      ><el-button @click="$emit('update:open', false)">取消</el-button
      ><el-button type="primary" :loading="busy" :disabled="!count" @click="submit">开始同步</el-button></template
    >
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorMessage } from '../api'
import { randomId } from '../utils/id'
import { billPost, businessDay, shiftBillDay, type BillAccount, type BillSelection } from '../bills-api'
const props = defineProps<{ open: boolean; accounts: BillAccount[]; selection: BillSelection; timezone: string }>()
const emit = defineEmits<{ (event: 'update:open', value: boolean): void; (event: 'submitted', value: unknown): void }>()
let submissionKey = '',
  submissionPayload = ''
const scope = ref('selected'),
  ids = ref<string[]>([]),
  dates = ref<string[]>([]),
  busy = ref(false),
  error = ref('')
const linkedAccounts = computed(() => props.accounts.filter((a) => a.linked)),
  today = computed(() => businessDay(props.timezone)),
  earliest = computed(() => shiftBillDay(today.value, -90)),
  count = computed(() => (scope.value === 'all_linked' ? linkedAccounts.value.length : ids.value.length))
watch(
  () => props.open,
  (open) => {
    if (open) {
      submissionKey = ''
      submissionPayload = ''
      ids.value = props.selection.account_ids.filter((id) => linkedAccounts.value.some((a) => a.id === id))
      scope.value = ids.value.length ? 'selected' : 'all_linked'
      dates.value = [props.selection.from, props.selection.to]
      error.value = ''
    }
  },
)
function disabledDate(d: Date) {
  const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  return day < earliest.value || day > today.value
}
async function submit() {
  error.value = ''
  if (
    dates.value.length !== 2 ||
    dates.value[0]! < earliest.value ||
    dates.value[1]! > today.value ||
    dates.value[0]! > dates.value[1]!
  ) {
    error.value = '请选择可补采窗口内的有效日期'
    return
  }
  busy.value = true
  try {
    const payload = {
      ...props.selection,
      scope: scope.value,
      account_ids: scope.value === 'selected' ? [...ids.value].sort() : [],
      from: dates.value[0],
      to: dates.value[1],
    }
    const serialized = JSON.stringify(payload)
    if (serialized !== submissionPayload) {
      submissionKey = randomId()
      submissionPayload = serialized
    }
    const result = await billPost('/bills/sync', payload, submissionKey)
    emit('submitted', result)
    emit('update:open', false)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>
<style scoped>
.sync-form {
  margin-top: 20px;
}
.sync-form .el-select,
.sync-form .el-date-editor {
  width: 100%;
}
.sync-form p {
  line-height: 1.7;
}
</style>
