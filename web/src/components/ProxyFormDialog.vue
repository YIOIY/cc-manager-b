<template>
  <el-dialog
    :model-value="modelValue"
    :title="proxy ? '编辑代理' : '新增代理'"
    width="720px"
    class="create-dialog"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <el-form label-position="right" label-width="100px" class="account-form" @submit.prevent="save">
      <el-form-item label="代理名称"><el-input v-model="form.name" placeholder="例如：美国出口 01" /></el-form-item>
      <div class="form-grid two">
        <el-form-item label="协议" :error="fieldErrors.scheme">
          <el-select v-model="form.scheme">
            <el-option v-for="scheme in proxySchemes" :key="scheme" :label="scheme" :value="scheme" />
          </el-select>
        </el-form-item>
        <el-form-item label="端口" :error="fieldErrors.port"
          ><el-input-number v-model="form.port" :min="1" :max="65535"
        /></el-form-item>
      </div>
      <el-form-item label="主机地址" :error="fieldErrors.host">
        <el-input
          v-model="form.host"
          placeholder="IP 地址或域名，也可直接粘贴 http://用户:密码@主机:端口"
          @update:model-value="recognizeAddress"
        />
        <small v-if="recognized" class="muted proxy-recognized">{{ recognized }}</small>
      </el-form-item>
      <el-form-item label="用户名" :error="fieldErrors.username"
        ><el-input v-model="form.username" placeholder="无认证可留空"
      /></el-form-item>
      <el-form-item label="密码">
        <el-input
          v-model="form.password"
          type="password"
          show-password
          autocomplete="new-password"
          :disabled="form.clear_password"
          :placeholder="proxy?.password_configured ? '留空保留已保存的密码' : '无认证可留空'"
        />
        <el-checkbox v-if="proxy?.password_configured" v-model="form.clear_password" class="proxy-clear-password"
          >清除已保存的密码</el-checkbox
        >
      </el-form-item>
      <el-form-item v-if="proxy" label="启用状态">
        <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
        <small class="muted proxy-enabled-hint">停用后不再参与检测与新账号绑定，已绑定账号不受影响。</small>
      </el-form-item>
      <p v-else class="muted proxy-create-hint">新增代理会先做本机检测，通过后才会保存到代理池。</p>
    </el-form>
    <template #footer>
      <div class="create-dialog-actions">
        <el-button :disabled="saving" @click="emit('update:modelValue', false)">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{
          saving ? (proxy ? '正在保存…' : '正在检测并保存…') : proxy ? '保存代理' : '检测并保存'
        }}</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '../api'
import type { Proxy } from '../types/api'
import { parseProxyAddress } from '../model/proxy-address'
import ErrorNotice from './ErrorNotice.vue'
import { applyFieldErrors } from '../errors'
import { ApiError } from '../api'

const props = defineProps<{ modelValue: boolean; proxy?: Proxy | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [updated: boolean] }>()
const proxySchemes = ['http', 'https', 'socks5', 'socks5h']
const blank = () => ({
  name: '',
  scheme: 'http',
  host: '',
  port: 8080,
  username: '',
  password: '',
  enabled: true,
  clear_password: false,
})
const fromProxy = (p: Proxy) => ({
  ...blank(),
  name: p.name,
  scheme: p.scheme,
  host: p.host,
  port: p.port,
  username: p.username,
  enabled: p.enabled,
})
const form = ref(blank())
const saving = ref(false)
const error = ref('')
const notice = ref<unknown | null>(null)
const fieldErrors = ref<Record<string, string>>({})
const recognized = ref('')

/** Splits a pasted full address into the separate fields. */
function recognizeAddress(value: string) {
  const parsed = parseProxyAddress(value)
  if (!parsed) {
    recognized.value = ''
    return
  }
  const filled: string[] = []
  form.value.host = parsed.host
  if (parsed.scheme) {
    form.value.scheme = parsed.scheme
    filled.push('协议')
  }
  if (parsed.port !== undefined) {
    form.value.port = parsed.port
    filled.push('端口')
  }
  if (parsed.username !== undefined) {
    form.value.username = parsed.username
    form.value.password = parsed.password ?? ''
    form.value.clear_password = false
    filled.push('用户名和密码')
  }
  recognized.value = filled.length ? `已识别并填入${filled.join('、')}，主机地址只保留主机部分。` : ''
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      form.value = props.proxy ? fromProxy(props.proxy) : blank()
      error.value = ''
      notice.value = null
      fieldErrors.value = {}
      recognized.value = ''
    }
  },
)
async function save() {
  if (saving.value) return
  if (!form.value.host.trim()) {
    notice.value = new Error('请填写主机地址')
    return
  }
  saving.value = true
  notice.value = null
  fieldErrors.value = {}
  try {
    const { clear_password, enabled, password, ...rest } = form.value
    const body = { ...rest, host: rest.host.trim() }
    if (props.proxy) {
      await api('/proxies/' + props.proxy.id, {
        method: 'PATCH',
        body: JSON.stringify({
          ...body,
          enabled,
          clear_password,
          ...(password ? { password } : {}),
          expected_version: props.proxy.version,
        }),
      })
    } else {
      await api('/proxies', { method: 'POST', body: JSON.stringify({ ...body, ...(password ? { password } : {}) }) })
    }
    form.value = blank()
    emit('update:modelValue', false)
    emit('saved', !!props.proxy)
  } catch (e) {
    notice.value = e
    if (e instanceof ApiError && e.fields.length) fieldErrors.value = applyFieldErrors(e.fields)
  } finally {
    saving.value = false
  }
}
</script>

<style>
.proxy-clear-password {
  margin-top: 6px;
}
.proxy-enabled-hint {
  display: block;
  width: 100%;
  margin-top: 4px;
}
.proxy-recognized {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: var(--accent-dark);
}
.proxy-create-hint {
  margin: 0 0 4px 100px;
  font-size: 12px;
  line-height: 1.4;
}
</style>
