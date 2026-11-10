<template>
  <details class="account-optional-section">
    <summary>
      <span class="account-optional-icon"><Plus :size="15" /></span><strong>补充信息</strong
      ><span>余额、标签、支付与备注 · 选填</span><ChevronDown :size="16" />
    </summary>
    <div class="account-optional-body">
      <div class="form-grid two">
        <el-form-item label="余额百分比"
          ><el-input-number v-model="edit.balance" :min="0" :max="100" placeholder="0–100"
        /></el-form-item>
        <el-form-item label="标签">
          <el-select v-model="edit.tags" multiple :multiple-limit="1" clearable placeholder="速刷或慢蒸">
            <el-option label="速刷" value="速刷" /><el-option label="慢蒸" value="慢蒸" />
          </el-select>
          <div class="field-hint">二者互斥，上号后不支持修改。</div>
        </el-form-item>
        <el-form-item label="支付方式">
          <el-select v-model="edit.payment_method" clearable placeholder="选填">
            <el-option v-for="p in paymentMethods" :key="p.value" :label="p.label" :value="p.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="注册设备">
          <el-select v-model="edit.register_device" clearable placeholder="选填">
            <el-option v-for="device in ['Mac', 'Windows', 'Linux']" :key="device" :label="device" :value="device" />
          </el-select>
        </el-form-item>
        <el-form-item label="充值时间"
          ><el-date-picker v-model="edit.recharge_time" value-format="YYYY-MM-DD" placeholder="选填日期"
        /></el-form-item>
        <el-form-item label="质保期">
          <el-select v-model="edit.warranty_period" clearable placeholder="选填">
            <el-option v-for="days in [1, 7, 30]" :key="days" :label="days + ' 天'" :value="days" />
          </el-select>
        </el-form-item>
      </div>
      <el-form-item label="备注"
        ><el-input v-model="edit.remark" type="textarea" :rows="2" placeholder="记录账号补充说明"
      /></el-form-item>
    </div>
  </details>
</template>
<script setup lang="ts">
import { Plus, ChevronDown } from 'lucide-vue-next'
import type { AccountForm } from '../model/account-form'
const edit = defineModel<AccountForm>({ required: true })
const paymentMethods = [
  { label: '实体信用卡', value: 'physical_credit_card' },
  { label: '虚拟信用卡', value: 'virtual_credit_card' },
  { label: 'PayPal', value: 'paypal' },
  { label: 'Apple Pay', value: 'apple_pay' },
  { label: 'Google Pay', value: 'google_pay' },
  { label: '第三方代付', value: 'third_party_payment' },
]
</script>
