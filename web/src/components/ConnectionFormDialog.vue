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
})
const form = ref(blank())
const saving = ref(false)
const error = ref('')
const notice = ref<unknown | null>(null)
const fieldErrors = ref<Record<string, string>>({})

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    error.value = ''
    const c = props.connection
    Object.assign(
      form,
      blank(),
      c
        ? {
            id: c.id,
            version: c.version,
            name: c.name,
            base_url: c.base_url,
            timezone: c.timezone || 'Asia/Shanghai',
            enabled: c.enabled !== false,
          }
        : {},
    )
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
    if (form.value.id) body.expected_version = form.value.version
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
</script>
