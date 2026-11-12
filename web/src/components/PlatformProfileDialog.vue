<template>
  <el-dialog
    :model-value="modelValue"
    :title="'修改平台资料 · ' + (account?.platform?.alias || account?.account || '')"
    width="640px"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <p class="profile-dialog-lead">
      改动会直接写入供应商平台，密码、凭证、标签与代理不受影响；提交后本地资料同步跟平台保持一致。
    </p>
    <template v-if="step === 1">
      <el-form label-width="110px" @submit.prevent>
        <el-form-item label="套餐">
          <el-select v-model="form.plan" placeholder="选择套餐">
            <el-option v-for="p in planOptions" :key="p.value" :label="p.label" :value="p.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="注册日期"
          ><el-date-picker v-model="form.register_time" value-format="YYYY-MM-DD" placeholder="YYYY-MM-DD"
        /></el-form-item>
        <el-form-item label="套餐开始日期"
          ><el-date-picker v-model="form.plan_start_time" value-format="YYYY-MM-DD" placeholder="YYYY-MM-DD"
        /></el-form-item>
        <el-form-item label="套餐结束日期"
          ><el-date-picker v-model="form.plan_end_time" value-format="YYYY-MM-DD" placeholder="YYYY-MM-DD"
        /></el-form-item>
        <el-form-item label="注册地区"><el-input v-model="form.register_location" placeholder="如 US" /></el-form-item>
        <el-form-item label="余量"
          ><el-input-number v-model="balance" :min="0" :max="100" :precision="0" controls-position="right"
        /></el-form-item>
        <el-form-item label="支付方式"
          ><el-input v-model="form.payment_method" placeholder="如 google_pay"
        /></el-form-item>
        <el-form-item label="注册设备"><el-input v-model="form.register_device" placeholder="如 Mac" /></el-form-item>
        <el-form-item label="充值日期"
          ><el-date-picker v-model="form.recharge_time" value-format="YYYY-MM-DD" placeholder="可留空" clearable
        /></el-form-item>
        <el-form-item label="质保期"
          ><el-input v-model="form.warranty_period" placeholder="可留空，如 1"
        /></el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <div class="profile-dialog-tools">
        <el-button text type="primary" @click="fillFromLocal">用本地资料填充</el-button>
        <span v-if="localFilled" class="profile-dialog-hint">已用本地账号的套餐、日期与注册地区覆盖表单</span>
      </div>
    </template>
    <template v-else>
      <table v-if="changes.length" class="profile-compare">
        <thead>
          <tr>
            <th>字段</th>
            <th>平台当前</th>
            <th>新值</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in changes" :key="c.key" :data-compare="c.key">
            <td>{{ c.label }}</td>
            <td>{{ c.before || '（空）' }}</td>
            <td>{{ c.after || '（空）' }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="profile-dialog-hint">没有改动，无需提交。</div>
      <p v-if="changes.length" class="profile-dialog-hint">提交后无法撤销；平台会整条覆盖资料字段。</p>
    </template>
    <el-alert v-if="conflict" :title="conflict" type="warning" :closable="false" show-icon />
    <ErrorNotice v-else-if="notice" :error="notice" fallback="提交失败" closable @close="notice = null" />
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button v-if="step === 2" @click="step = 1">上一步</el-button>
      <el-button v-if="step === 1" type="primary" @click="review">下一步</el-button>
      <el-button v-else type="primary" :loading="saving" :disabled="!changes.length || !!conflict" @click="submit"
        >提交到平台</el-button
      >
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, ApiError } from '../api'
import ErrorNotice from './ErrorNotice.vue'
import { plans } from '../list-model'
import type { Account, PlatformProfileResult } from '../types/api'

const props = defineProps<{ modelValue: boolean; account: Account | null }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: [result: PlatformProfileResult]
  /** The platform record changed under us; the owner should reload so a reopen sees the fresh snapshot. */
  conflict: []
}>()

type ProfileForm = Record<(typeof fieldOrder)[number], string>
const fieldOrder = [
  'plan',
  'register_time',
  'plan_start_time',
  'plan_end_time',
  'register_location',
  'balance',
  'payment_method',
  'register_device',
  'recharge_time',
  'warranty_period',
  'remark',
] as const
const labels: Record<(typeof fieldOrder)[number], string> = {
  plan: '套餐',
  register_time: '注册日期',
  plan_start_time: '套餐开始日期',
  plan_end_time: '套餐结束日期',
  register_location: '注册地区',
  balance: '余量',
  payment_method: '支付方式',
  register_device: '注册设备',
  recharge_time: '充值日期',
  warranty_period: '质保期',
  remark: '备注',
}

const blank = (): ProfileForm => ({
  plan: '',
  register_time: '',
  plan_start_time: '',
  plan_end_time: '',
  register_location: '',
  balance: '',
  payment_method: '',
  register_device: '',
  recharge_time: '',
  warranty_period: '',
  remark: '',
})

function snapshotOf(account: Account | null): ProfileForm {
  const p = account?.platform
  if (!p) return blank()
  return {
    plan: p.profile.plan,
    register_time: p.profile.register_time,
    plan_start_time: p.profile.plan_start_time,
    plan_end_time: p.profile.plan_end_time,
    register_location: p.profile.register_location,
    balance: p.profile.balance,
    payment_method: p.profile.payment_method,
    register_device: p.profile.register_device,
    recharge_time: p.profile.recharge_time,
    warranty_period: p.profile.warranty_period,
    remark: p.remark || '',
  }
}

const step = ref<1 | 2>(1)
const base = ref<ProfileForm>(blank())
const form = ref<ProfileForm>(blank())
const saving = ref(false)
const notice = ref<unknown | null>(null)
const conflict = ref('')
const localFilled = ref(false)

const balance = computed({
  get: () => (form.value.balance === '' ? undefined : Number(form.value.balance)),
  set: (v: number | undefined) => {
    form.value.balance = v === undefined || v === null || Number.isNaN(v) ? '' : String(v)
  },
})

// The platform's current plan is offered even when this system does not
// know it, so an unusual plan is never silently rewritten.
const planOptions = computed(() => {
  const current = base.value.plan
  if (!current || plans.some((p) => p.value === current)) return plans
  return [{ label: current, value: current }, ...plans]
})

// Element Plus date pickers emit undefined when cleared; treat that as empty.
const valueOf = (k: (typeof fieldOrder)[number]) => String(form.value[k] ?? '').trim()

const changes = computed(() =>
  fieldOrder
    .filter((k) => valueOf(k) !== base.value[k])
    .map((k) => ({ key: k, label: labels[k], before: base.value[k], after: valueOf(k) })),
)

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    step.value = 1
    notice.value = null
    conflict.value = ''
    localFilled.value = false
    base.value = snapshotOf(props.account)
    form.value = { ...base.value }
  },
  { immediate: true },
)

function fillFromLocal() {
  const a = props.account
  if (!a) return
  form.value.plan = a.plan || form.value.plan
  form.value.register_time = a.register_time || form.value.register_time
  form.value.plan_start_time = a.plan_start_time || form.value.plan_start_time
  form.value.plan_end_time = a.plan_end_time || form.value.plan_end_time
  form.value.register_location = a.register_location || form.value.register_location
  localFilled.value = true
}

function review() {
  notice.value = null
  step.value = 2
}

async function submit() {
  if (saving.value || !props.account || !changes.value.length) return
  saving.value = true
  notice.value = null
  conflict.value = ''
  const body = {
    changes: Object.fromEntries(changes.value.map((c) => [c.key, c.after])),
    base: { ...base.value },
  }
  try {
    const res = await api<PlatformProfileResult>('/accounts/' + props.account.id + '/platform-profile', {
      method: 'POST',
      body: JSON.stringify(body),
    })
    emit('update:modelValue', false)
    emit('saved', res.data)
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      conflict.value = '平台资料刚更新，请重新打开后再改'
      emit('conflict')
    } else {
      notice.value = e
    }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.profile-dialog-lead {
  margin: 0 0 14px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-secondary, #606266);
}
.profile-dialog-tools {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-left: 110px;
}
.profile-dialog-hint {
  font-size: 12px;
  color: var(--text-tertiary, #909399);
}
.profile-compare {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.profile-compare th,
.profile-compare td {
  padding: 8px 10px;
  text-align: left;
  border-bottom: 1px solid var(--line-tint, #eef2f2);
  vertical-align: top;
  word-break: break-all;
}
.profile-compare th {
  font-size: 12px;
  color: var(--text-tertiary, #909399);
  font-weight: 600;
}
.profile-compare td:nth-child(2) {
  color: var(--text-secondary, #606266);
}
.profile-compare td:nth-child(3) {
  color: var(--accent-deep, #167c69);
  font-weight: 600;
}
.profile-compare + .profile-dialog-hint {
  display: block;
  margin-top: 10px;
}
</style>
