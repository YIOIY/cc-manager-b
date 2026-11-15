<template>
  <el-dialog
    :model-value="modelValue"
    title="分配用户"
    width="440px"
    destroy-on-close
    :close-on-click-modal="!busy"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <p v-if="resourceLabel" class="assign-target">
      将 <strong>{{ resourceLabel }}</strong> 分配给用户后，对方即可查看与管理。
      <template v-if="endpoint === '/accounts'"
        >独占代理会一并转移；若代理仍被其他账号使用，则只解绑本账号。</template
      >
    </p>
    <el-form label-position="top" :disabled="busy" @submit.prevent="submit">
      <el-form-item label="所属用户">
        <el-select
          v-model="userId"
          filterable
          clearable
          placeholder="选择用户；清空表示未分配（仅管理员可见）"
          style="width: 100%"
          :loading="loading"
        >
          <el-option
            v-for="u in enabledUsers"
            :key="u.id"
            :label="optionLabel(u)"
            :value="u.id"
          />
        </el-select>
      </el-form-item>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer>
      <el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="busy" :disabled="loading || !listReady" @click="submit"
        >保存分配</el-button
      >
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, errorMessage } from '../api'
import type { User } from '../types/api'

const props = defineProps<{
  modelValue: boolean
  /** API path prefix without trailing slash, e.g. /accounts or /proxies */
  endpoint: string
  resourceId: string
  resourceLabel?: string
  currentUserId?: string
}>()
export type AssignResult = {
  userId: string
  message?: string
  proxy_action?: string
}

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  assigned: [result: AssignResult]
}>()

const users = ref<User[]>([])
const userId = ref(props.currentUserId || '')
const loading = ref(false)
const listReady = ref(false)
const busy = ref(false)
const error = ref('')
const enabledUsers = computed(() => users.value.filter((u) => u.enabled))

function optionLabel(u: User) {
  const nick = (u.nickname || '').trim()
  return nick && nick !== u.username ? `${nick}（${u.username}）` : u.username
}

function resetForm() {
  userId.value = props.currentUserId || ''
  error.value = ''
  listReady.value = false
}

async function loadUsers() {
  loading.value = true
  error.value = ''
  try {
    const res = await api<User[]>('/users')
    users.value = res.data
    listReady.value = true
  } catch (e) {
    error.value = errorMessage(e, '加载用户列表失败')
    users.value = []
    listReady.value = false
  } finally {
    loading.value = false
  }
}

// immediate: callers often mount with v-if when opening; without it the open
// transition never runs and the form stays empty (save would clear ownership).
watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    resetForm()
    void loadUsers()
  },
  { immediate: true },
)

async function submit() {
  if (!props.resourceId || busy.value || loading.value || !listReady.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await api<{
      message?: string
      proxy_action?: string
      created_by_user_id?: string
    }>(props.endpoint + '/' + props.resourceId + '/assign', {
      method: 'POST',
      body: JSON.stringify({ user_id: userId.value }),
    })
    emit('update:modelValue', false)
    emit('assigned', {
      userId: userId.value,
      message: res.data.message,
      proxy_action: res.data.proxy_action,
    })
  } catch (e) {
    error.value = errorMessage(e, '分配失败')
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.assign-target {
  margin: 0 0 14px;
  color: var(--text-secondary, #606266);
  font-size: 13px;
  line-height: 1.6;
}
.assign-target strong {
  color: var(--ink-soft, #303133);
}
</style>
