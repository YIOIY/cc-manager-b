<template>
  <div class="onboarding-proxy-picker">
    <div class="proxy-source" role="radiogroup" aria-label="代理来源">
      <label><input type="radio" :name="radioName" value="auto" disabled />自动分配 <small>待接入</small></label>
      <label
        ><input
          type="radio"
          :name="radioName"
          value="existing"
          :checked="mode === 'existing'"
          :disabled="disabled || saving"
          @change="changeMode('existing')"
        />选择已有代理</label
      >
      <label
        ><input
          type="radio"
          :name="radioName"
          value="new"
          :checked="mode === 'new'"
          :disabled="disabled || saving || !canCreate"
          @change="changeMode('new')"
        />填写新代理</label
      >
    </div>
    <template v-if="mode === 'existing'">
      <el-select
        :model-value="modelValue"
        filterable
        clearable
        :disabled="disabled || saving"
        placeholder="选择本次授权与上号使用的代理"
        aria-label="选择已有代理"
        @update:model-value="emit('update:modelValue', $event || '')"
      >
        <el-option
          v-for="p in proxies"
          :key="p.id"
          :value="p.id"
          :label="`${p.name || p.host} · ${p.scheme.toUpperCase()} · ${p.host}:${p.port}`"
          :disabled="!p.enabled"
        />
      </el-select>
      <p class="field-hint">
        {{
          proxies.some((p) => p.enabled)
            ? '已保存的代理认证信息会自动使用，无需重复填写。'
            : '暂无启用的代理，可在此添加新代理或由管理员配置。'
        }}
      </p>
    </template>
    <div v-else class="proxy-inline-create">
      <label for="onboarding-proxy-address">代理地址</label>
      <el-input
        id="onboarding-proxy-address"
        v-model="address"
        data-proxy-address
        type="password"
        show-password
        autocomplete="new-password"
        :disabled="saving || disabled"
        placeholder="http://user:pass@host:port 或 host:port:user:pass"
      />
      <p v-if="parsed" class="field-hint">
        已识别 {{ parsed.scheme || 'http' }} · {{ parsed.host }}:{{ parsed.port
        }}{{ parsed.username ? ' · 含认证信息' : '' }}
      </p>
      <details>
        <summary>代理名称（选填）</summary>
        <el-input
          v-model="name"
          aria-label="代理名称"
          :disabled="saving || disabled"
          placeholder="便于之后识别这条代理"
        />
      </details>
      <div class="proxy-save-row">
        <el-button data-save-proxy :loading="saving" :disabled="disabled || !canCreate || !address.trim()" @click="save"
          >保存并使用</el-button
        ><span class="field-hint">保存到代理池，取消上号后仍可复用。</span>
      </div>
    </div>
    <p v-if="!canCreate" class="field-hint">当前账号可选择已有代理，新建代理需要相应权限。</p>
    <p v-if="created" class="field-hint">新代理已保存并选中。</p>
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
  </div>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { api } from '../api'
import { can } from '../composables/useSession'
import { parseProxyAddress } from '../model/proxy-address'
import type { ProxyOption } from '../types/api'
import ErrorNotice from './ErrorNotice.vue'
const props = defineProps<{ modelValue?: string; proxies: ProxyOption[]; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [id: string]
  created: [proxy: ProxyOption]
  busy: [busy: boolean]
  dirty: [dirty: boolean]
  pending: [pending: boolean]
}>()
const radioName = useId()
const canCreate = computed(() => can('proxies.create'))
const mode = ref<'existing' | 'new'>('existing')
const address = ref('')
const name = ref('')
const saving = ref(false)
const created = ref(false)
const notice = ref<unknown>(null)
let disposed = false
const parsed = computed(() => {
  const text = address.value.trim()
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(text) && !/^(https?|socks5h?|socks):\/\//i.test(text)) return null
  const value = parseProxyAddress(text)
  return value?.host && value.port ? value : null
})
watch([address, name], () => emit('dirty', !!address.value || !!name.value))
function changeMode(value: 'existing' | 'new') {
  if (props.disabled || saving.value || (value === 'new' && !canCreate.value)) return
  mode.value = value
  emit('pending', value === 'new')
  notice.value = null
  emit('update:modelValue', '')
}
async function save() {
  if (saving.value || props.disabled || !canCreate.value) return
  if (!parsed.value) {
    notice.value = new Error('请填写有效的 HTTP / HTTPS / SOCKS5 代理地址，包含主机和端口。')
    return
  }
  const value = {
    ...parsed.value,
    scheme: parsed.value.scheme || 'http',
    name: name.value.trim(),
    port: parsed.value.port!,
  }
  saving.value = true
  emit('busy', true)
  notice.value = null
  try {
    const { data } = await api<{ id: string }>('/proxies', { method: 'POST', body: JSON.stringify(value) })
    if (disposed) return
    const proxy: ProxyOption = {
      id: data.id,
      name: value.name,
      host: value.host,
      port: value.port,
      scheme: value.scheme,
      enabled: true,
    }
    emit('created', proxy)
    emit('update:modelValue', data.id)
    address.value = ''
    name.value = ''
    mode.value = 'existing'
    emit('pending', false)
    created.value = true
  } catch (e) {
    if (!disposed) notice.value = e
  } finally {
    if (!disposed) {
      saving.value = false
      emit('busy', false)
    }
  }
}
onBeforeUnmount(() => {
  disposed = true
})
</script>
<style scoped>
.proxy-source {
  display: flex;
  gap: 10px 18px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.proxy-source label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.proxy-source input {
  accent-color: var(--accent);
}
.proxy-source small {
  color: var(--muted);
}
.proxy-inline-create {
  display: grid;
  gap: 8px;
}
.proxy-inline-create label {
  font-size: 12px;
  color: var(--ink-soft);
}
.proxy-inline-create summary {
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
}
.proxy-inline-create details[open] summary {
  margin-bottom: 8px;
}
.proxy-save-row {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.onboarding-proxy-picker {
  width: 100%;
}
@media (max-width: 600px) {
  .proxy-source label {
    min-height: 36px;
  }
}
</style>
