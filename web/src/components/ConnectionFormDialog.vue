<template>
  <el-dialog
    :model-value="modelValue"
    :title="form.id ? '修改平台连接' : '新建平台连接'"
    width="720px"
    class="create-dialog"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <el-alert
      v-if="form.id && props.connection?.web_suspended_at"
      type="warning"
      :closable="false"
      show-icon
      class="web-suspended"
      :title="'网页通道已暂停：' + (props.connection?.web_suspended_reason || '登录连续失败')"
      description="重新保存网页登录密码，或测试登录成功后会自动恢复。"
    />
    <el-form label-position="right" label-width="100px" class="account-form" @submit.prevent="save">
      <el-form-item label="名称"><el-input v-model="form.name" placeholder="平台连接名称" /></el-form-item>
      <el-form-item label="Base URL" :error="fieldErrors.base_url"
        ><el-input v-model="form.base_url" placeholder="https://..."
      /></el-form-item>
      <el-form-item label="OpenAPI Token" :error="fieldErrors.token">
        <el-input
          v-model="form.token"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="form.id ? '留空则保留当前 Token' : '粘贴平台 Token'"
        />
        <div class="field-hint">修改连接时留空会保留原 Token，不会回显敏感信息。</div>
      </el-form-item>
      <el-form-item label="时区" :error="fieldErrors.timezone"
        ><el-input v-model="form.timezone" placeholder="Asia/Shanghai"
      /></el-form-item>
      <el-form-item label="启用状态"
        ><el-switch v-model="form.enabled" active-text="启用" inactive-text="停用"
      /></el-form-item>
      <el-form-item label="美元汇率" :error="fieldErrors.usd_cny_rate">
        <el-input-number v-model="form.usd_cny_rate" :min="1" :max="20" :step="0.1" controls-position="right" />
        <div class="field-hint">
          平台按固定汇率把美元原价折算成人民币（目前 1 美元 = 7.2
          人民币）。账号卡片用它把花费换算回美元；平台调整汇率时在这里修改。
        </div>
      </el-form-item>
      <div class="form-section-title">网页后台登录（只读通道）</div>
      <el-form-item label="登录账号" :error="fieldErrors.web_username">
        <el-input
          v-model="form.web_username"
          :disabled="form.web_clear"
          placeholder="供应商后台的登录账号"
          autocomplete="off"
        />
      </el-form-item>
      <el-form-item label="登录密码" :error="fieldErrors.web_password">
        <el-input
          v-model="form.web_password"
          type="password"
          show-password
          autocomplete="new-password"
          :disabled="form.web_clear"
          :placeholder="props.connection?.web_configured ? '留空则保留当前密码' : '供应商后台的登录密码'"
        />
        <div class="field-hint">密码加密保存，只用于定时读取账号状态、用量和按模型账单；系统不会对平台做任何修改。</div>
      </el-form-item>
      <el-form-item v-if="form.id" label="网页登录">
        <div class="web-login-actions">
          <el-button
            :loading="testing"
            :disabled="!props.connection?.web_configured || form.web_clear"
            @click="testLogin"
          >
            测试登录
          </el-button>
          <span v-if="testResult" class="web-login-result" :class="testResult.ok ? 'ok' : 'bad'">{{
            testResult.text
          }}</span>
          <el-checkbox v-if="props.connection?.web_configured" v-model="form.web_clear">清除网页登录</el-checkbox>
        </div>
      </el-form-item>
      <div v-else class="field-hint web-login-hint">保存连接后可以在这里测试登录。</div>
    </el-form>
    <template #footer>
      <div class="create-dialog-actions">
        <el-button :disabled="saving" @click="emit('update:modelValue', false)">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ form.id ? '保存修改' : '保存连接' }}</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, ApiError } from '../api'
import ErrorNotice from './ErrorNotice.vue'
import { applyFieldErrors } from '../errors'
import type { PlatformConnection } from '../types/api'

const props = defineProps<{ modelValue: boolean; connection: PlatformConnection | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [updated: boolean] }>()
const blank = () => ({
  id: '',
  version: 0,
  name: '',
  base_url: '',
  token: '',
  timezone: 'Asia/Shanghai',
  enabled: true,
  usd_cny_rate: 7.2 as number | null,
  web_username: '',
  web_password: '',
  web_clear: false,
})
const form = ref(blank())
const saving = ref(false)
const error = ref('')
const notice = ref<unknown | null>(null)
const fieldErrors = ref<Record<string, string>>({})
const testing = ref(false)
const testResult = ref<{ ok: boolean; text: string } | null>(null)

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    error.value = ''
    testResult.value = null
    const c = props.connection
    // Replace form.value: assigning onto the ref itself left the form blank,
    // so 修改 opened as 新建 and saving created a duplicate connection.
    form.value = {
      ...blank(),
      ...(c
        ? {
            id: c.id,
            version: c.version,
            name: c.name,
            base_url: c.base_url,
            timezone: c.timezone || 'Asia/Shanghai',
            enabled: c.enabled !== false,
            usd_cny_rate: c.usd_cny_rate ?? 7.2,
            web_username: c.web_username || '',
          }
        : {}),
    }
  },
)
async function save() {
  if (saving.value) return
  saving.value = true
  error.value = ''
  notice.value = null
  fieldErrors.value = {}
  try {
    const body: Record<string, unknown> = {
      name: form.value.name.trim(),
      base_url: form.value.base_url.trim(),
      timezone: form.value.timezone.trim(),
      enabled: form.value.enabled,
    }
    if (form.value.token) body.token = form.value.token
    // A cleared field keeps the saved rate (or the default for a new connection).
    if (typeof form.value.usd_cny_rate === 'number') body.usd_cny_rate = form.value.usd_cny_rate
    if (form.value.id) body.expected_version = form.value.version
    if (form.value.web_clear) {
      body.web_clear = true
    } else {
      const username = form.value.web_username.trim()
      const configured = !!props.connection?.web_configured
      if (form.value.web_password) {
        body.web_username = username
        body.web_password = form.value.web_password
      } else if (username && username !== (props.connection?.web_username || '') && configured) {
        body.web_username = username
      } else if (username && !configured) {
        body.web_username = username
      }
    }
    await api(form.value.id ? '/platform-connections/' + form.value.id : '/platform-connections', {
      method: form.value.id ? 'PUT' : 'POST',
      body: JSON.stringify(body),
    })
    const updated = !!form.value.id
    // Never keep a token in memory after it has been sent.
    form.value = blank()
    emit('update:modelValue', false)
    emit('saved', updated)
  } catch (e) {
    notice.value = e
    if (e instanceof ApiError && e.fields.length) fieldErrors.value = applyFieldErrors(e.fields)
  } finally {
    saving.value = false
  }
}
async function testLogin() {
  if (testing.value || !form.value.id) return
  testing.value = true
  testResult.value = null
  notice.value = null
  try {
    const res = await api<{ ok: boolean; supplier_name: string; expires_in: number }>(
      '/platform-connections/' + form.value.id + '/web-login-test',
      { method: 'POST' },
    )
    testResult.value = { ok: true, text: '登录成功：' + (res.data.supplier_name || '供应商') }
    emit('saved', true)
  } catch (e) {
    testResult.value = { ok: false, text: '登录失败' }
    notice.value = e
  } finally {
    testing.value = false
  }
}
</script>

<style scoped>
.form-section-title {
  margin: 6px 0 10px 100px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary, #606266);
}
.web-login-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.web-login-result.ok {
  color: var(--el-color-success);
}
.web-login-result.bad {
  color: var(--el-color-danger);
}
.web-login-hint {
  margin-left: 100px;
}
.web-suspended {
  margin-bottom: 12px;
}
</style>
