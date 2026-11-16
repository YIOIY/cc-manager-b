<template>
  <el-dialog
    :model-value="modelValue"
    :title="user ? '编辑用户 · ' + user.username : '新建用户'"
    width="560px"
    class="access-dialog"
    :close-on-click-modal="!saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form label-position="top" :disabled="saving" @submit.prevent="save">
      <div class="form-grid two">
        <el-form-item label="账号" required>
          <el-input v-model="form.username" :disabled="!!user" placeholder="字母、数字或 . _ @ -" autocomplete="off" />
        </el-form-item>
        <el-form-item label="昵称">
          <el-input
            v-model="form.nickname"
            maxlength="32"
            show-word-limit
            placeholder="用于列表显示所属用户"
            autocomplete="off"
          />
        </el-form-item>
        <el-form-item v-if="!user" label="初始密码" required>
          <el-input
            v-model="form.password"
            type="password"
            show-password
            autocomplete="new-password"
            placeholder="至少 12 位"
          />
          <small class="access-field-hint" :class="{ 'is-ok': form.password.length >= 12 }">{{ passwordHint }}</small>
        </el-form-item>
      </div>
      <el-form-item required>
        <template #label>角色<span class="access-label-hint">可多选，有效权限为并集</span></template>
        <div class="access-role-options">
          <button
            v-for="role in roles"
            :key="role.id"
            type="button"
            class="access-role-option"
            :class="{ 'is-selected': form.role_ids.includes(role.id), 'is-admin': role.id === ADMIN_ROLE }"
            :disabled="role.id === ADMIN_ROLE && (!me?.is_admin || user?.id === me?.id)"
            @click="toggleRoleId(role.id)"
          >
            <span class="access-role-option-check"><Check :size="12" /></span>
            <span class="access-role-option-body">
              <strong>{{ role.name }}</strong>
              <small>{{
                role.id === ADMIN_ROLE ? '全部权限' : role.description || role.permissions.length + ' 项权限'
              }}</small>
            </span>
          </button>
        </div>
        <p v-if="effectiveCount !== null" class="field-hint">合并后共 {{ effectiveCount }} 项权限。</p>
      </el-form-item>
      <el-alert v-if="formError" :title="formError" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="!ready" @click="save">{{
        user ? '保存' : '创建用户'
      }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Check } from 'lucide-vue-next'
import { api, errorMessage } from '../../api'
import { useSession } from '../../composables/useSession'
import type { PermissionModule, RoleRecord, User } from '../../types/api'
import { ADMIN_ROLE, effectivePermissionCount } from './access'

const props = defineProps<{
  modelValue: boolean
  /** user being edited; null creates a new one */
  user: User | null
  roles: RoleRecord[]
  catalogue: PermissionModule[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const { user: me } = useSession()

const form = reactive<{ username: string; nickname: string; password: string; role_ids: string[] }>({
  username: '',
  nickname: '',
  password: '',
  role_ids: [],
})
const saving = ref(false)
const formError = ref('')
const ready = computed(
  () =>
    /^[a-zA-Z0-9._@-]{2,64}$/.test(form.username) &&
    form.role_ids.length > 0 &&
    [...form.nickname].length <= 32 &&
    (props.user ? true : form.password.length >= 12),
)
const passwordHint = computed(() => {
  const missing = 12 - form.password.length
  if (missing <= 0) return '长度满足要求'
  return form.password ? `至少 12 位，还差 ${missing} 位` : '至少 12 位，建议用密码管理器生成'
})
const effectiveCount = computed(() => effectivePermissionCount(form.role_ids, props.roles, props.catalogue))

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    Object.assign(form, {
      username: props.user?.username || '',
      nickname: props.user?.nickname || '',
      password: '',
      role_ids: props.user ? props.user.roles.map((r) => r.id) : [],
    })
    formError.value = ''
  },
  { immediate: true },
)

function toggleRoleId(id: string) {
  const i = form.role_ids.indexOf(id)
  if (i >= 0) form.role_ids.splice(i, 1)
  else form.role_ids.push(id)
}

async function save() {
  if (!ready.value || saving.value) return
  saving.value = true
  formError.value = ''
  try {
    if (props.user) {
      await api('/users/' + props.user.id, {
        method: 'PATCH',
        body: JSON.stringify({
          role_ids: form.role_ids,
          nickname: form.nickname.trim(),
          expected_version: props.user.version,
        }),
      })
    } else {
      await api('/users', {
        method: 'POST',
        body: JSON.stringify({
          username: form.username.trim(),
          nickname: form.nickname.trim(),
          password: form.password,
          role_ids: form.role_ids,
        }),
      })
    }
    form.password = ''
    emit('update:modelValue', false)
    emit('saved')
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}
</script>

<style>
.access-label-hint {
  margin-left: 8px;
  font-weight: 400;
  color: var(--muted-cool);
  font-size: 12px;
}
.access-role-options {
  display: grid;
  gap: 8px;
  width: 100%;
}
.access-role-option {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 12px;
  text-align: left;
  background: #fafbfc;
  border: 1px solid var(--line);
  border-radius: 10px;
  font: inherit;
  cursor: pointer;
  transition: border-color 0.15s;
}
.access-field-hint {
  display: block;
  width: 100%;
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--muted);
}
.access-field-hint.is-ok {
  color: var(--accent-deep);
}
.access-role-option:hover:not(:disabled) {
  border-color: #b9c3d2;
}
.access-role-option:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.access-role-option.is-selected {
  background: var(--tint-50);
  border-color: var(--accent);
}
.access-role-option-check {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  flex: none;
  border-radius: 5px;
  border: 1px solid #c8d1da;
  background: var(--surface);
  color: transparent;
}
.access-role-option.is-selected .access-role-option-check {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--surface);
}
.access-role-option-body {
  display: grid;
  gap: 2px;
  min-width: 0;
}
.access-role-option strong {
  font-size: 13px;
  color: var(--ink-navy);
}
.access-role-option small {
  font-size: 11.5px;
  color: var(--muted);
}
</style>
