<template>
  <PageHead
    eyebrow="ACCESS CONTROL"
    title="用户与权限"
    description="角色由权限组合而成，用户可绑定多个角色，有效权限取并集。"
  >
    <el-button :loading="loading" @click="load"><RefreshCw :size="15" />刷新</el-button>
    <el-button v-if="tab === 'users' && can('users.create')" type="primary" @click="openCreateUser"
      ><Plus :size="16" />新建用户</el-button
    >
    <el-button v-if="tab === 'roles' && can('roles.manage')" type="primary" @click="openCreateRole()"
      ><Plus :size="16" />新建角色</el-button
    >
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon @close="error = ''" />

  <div class="access-tabs" role="tablist">
    <button
      type="button"
      role="tab"
      :class="{ 'is-active': tab === 'users' }"
      :aria-selected="tab === 'users'"
      @click="tab = 'users'"
    >
      <UsersRound :size="15" />用户<span class="count-badge">{{ users.length }}</span>
    </button>
    <button
      v-if="can('roles.view')"
      type="button"
      role="tab"
      :class="{ 'is-active': tab === 'roles' }"
      :aria-selected="tab === 'roles'"
      @click="tab = 'roles'"
    >
      <ShieldCheck :size="15" />角色<span class="count-badge">{{ roles.length }}</span>
    </button>
  </div>

  <!-- ===== users ===== -->
  <section v-if="tab === 'users'" class="list-workspace panel access-table">
    <el-table v-loading="loading" :data="users" row-key="id">
      <el-table-column label="账号" min-width="200">
        <template #default="{ row }">
          <div class="primary-cell">
            <span class="access-avatar" :class="{ 'is-disabled': !row.enabled, 'is-admin': row.is_admin }">{{
              (row.nickname || row.username).slice(0, 1).toUpperCase()
            }}</span>
            <div>
              <strong>{{ row.username }}<em v-if="row.id === me?.id" class="access-me">当前</em></strong>
              <small>{{ row.last_login_at ? '最近登录 ' + formatDateTime(row.last_login_at) : '尚未登录' }}</small>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="昵称" min-width="120">
        <template #default="{ row }">
          <span v-if="row.nickname">{{ row.nickname }}</span>
          <span v-else class="muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="角色" min-width="220">
        <template #default="{ row }">
          <div class="access-chips">
            <el-tag
              v-for="r in row.roles"
              :key="r.id"
              :type="r.id === ADMIN_ROLE ? 'danger' : r.builtin ? 'warning' : 'info'"
              effect="light"
              round
              >{{ r.name }}</el-tag
            >
            <span v-if="!row.roles.length" class="muted">未分配</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="权限" width="110">
        <template #default="{ row }">
          <span class="access-count">{{ row.is_admin ? '全部' : row.permissions.length + ' 项' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <span class="access-state" :class="row.enabled ? 'is-on' : 'is-off'">{{
            row.enabled ? '启用' : '已停用'
          }}</span>
        </template>
      </el-table-column>
      <el-table-column label="可上号" width="100">
        <template #default="{ row }">
          <el-switch
            v-if="can('users.update')"
            :model-value="row.can_onboard !== false"
            :disabled="togglingOnboardId === row.id"
            size="small"
            @change="(v: string | number | boolean) => toggleCanOnboard(row as User, !!v)"
          />
          <span v-else class="access-state" :class="row.can_onboard !== false ? 'is-on' : 'is-off'">{{
            row.can_onboard !== false ? '允许' : '禁止'
          }}</span>
        </template>
      </el-table-column>
      <el-table-column v-if="can('users.update') || can('users.delete')" label="操作" width="120" fixed="right">
        <template #default="{ row }">
          <div class="access-row-actions">
            <el-button v-if="can('users.update')" text type="primary" @click="openEditUser(row as User)"
              >编辑</el-button
            >
            <el-dropdown
              v-if="rowMenu(row as User).length"
              trigger="click"
              placement="bottom-end"
              @command="(cmd: string) => userCommand(cmd, row as User)"
            >
              <el-button text class="access-row-more" title="更多操作"><MoreHorizontal :size="16" /></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item
                    v-for="item in rowMenu(row as User)"
                    :key="item.command"
                    :command="item.command"
                    :divided="item.divided"
                    :class="{ 'access-danger': item.danger }"
                    >{{ item.label }}</el-dropdown-item
                  >
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </template>
      </el-table-column>
      <template #empty
        ><div class="list-empty"><strong>暂无用户</strong></div></template
      >
    </el-table>
  </section>

  <!-- ===== roles ===== -->
  <div v-else class="access-roles">
    <RoleCard
      v-for="role in roles"
      :key="role.id"
      :role="role"
      :catalogue="catalogue"
      @edit="openEditRole"
      @copy="openCreateRole"
      @remove="removeRole"
    />
  </div>

  <UserFormDialog v-model="userFormOpen" :user="editingUser" :roles="roles" :catalogue="catalogue" @saved="load" />

  <!-- ===== password dialog ===== -->
  <el-dialog
    v-model="passwordOpen"
    :title="'重置密码 · ' + (passwordTarget?.username || '')"
    width="440px"
    class="access-dialog"
    :close-on-click-modal="!saving"
  >
    <el-form label-position="top" :disabled="saving" @submit.prevent="savePassword">
      <el-form-item label="新密码" required>
        <el-input
          v-model="newPassword"
          type="password"
          show-password
          autocomplete="new-password"
          placeholder="至少 12 位"
        />
      </el-form-item>
      <p class="field-hint">重置后该用户现有登录全部失效。</p>
      <el-alert v-if="formError" :title="formError" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="passwordOpen = false">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="newPassword.length < 12" @click="savePassword"
        >重置密码</el-button
      >
    </template>
  </el-dialog>
  <RoleEditorDialog
    v-model="roleFormOpen"
    :role="editingRole"
    :copy-from="copySource"
    :catalogue="catalogue"
    :roles="roles"
    @saved="load"
  />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { MoreHorizontal, Plus, RefreshCw, ShieldCheck, UsersRound } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import RoleCard from '../components/users/RoleCard.vue'
import RoleEditorDialog from '../components/users/RoleEditorDialog.vue'
import UserFormDialog from '../components/users/UserFormDialog.vue'
import { ADMIN_ROLE } from '../components/users/access'
import { api, errorMessage } from '../api'
import { useSession } from '../composables/useSession'
import { formatDateTime } from '../utils/format'
import type { PermissionModule, RoleRecord, User } from '../types/api'

const { user: me, can } = useSession()
const tab = ref<'users' | 'roles'>('users')
const users = ref<User[]>([])
const roles = ref<RoleRecord[]>([])
const catalogue = ref<PermissionModule[]>([])
const loading = ref(false)
const error = ref('')
const saving = ref(false)
const formError = ref('')
const togglingOnboardId = ref('')

function rowMenu(row: User) {
  const self = row.id === me.value?.id
  const items: { command: string; label: string; divided?: boolean; danger?: boolean }[] = []
  if (can('users.update') && can('users.reset_password')) items.push({ command: 'password', label: '重置密码' })
  if (!self && can('users.update')) items.push({ command: 'toggle', label: row.enabled ? '停用账号' : '启用账号' })
  if (!self && can('users.delete')) items.push({ command: 'delete', label: '删除用户', divided: true, danger: true })
  return items
}

// ---- users ----
const userFormOpen = ref(false)
const editingUser = ref<User | null>(null)
const passwordOpen = ref(false)
const passwordTarget = ref<User | null>(null)
const newPassword = ref('')

// ---- roles ----
const roleFormOpen = ref(false)
const editingRole = ref<RoleRecord | null>(null)
const copySource = ref<RoleRecord | null>(null)

async function load() {
  loading.value = true
  try {
    const [u, r, c] = await Promise.all([
      can('users.view') ? api<User[]>('/users') : Promise.resolve({ data: [] as User[] }),
      can('roles.view') ? api<RoleRecord[]>('/roles') : Promise.resolve({ data: [] as RoleRecord[] }),
      can('roles.view') ? api<PermissionModule[]>('/permissions') : Promise.resolve({ data: [] as PermissionModule[] }),
    ])
    users.value = u.data
    roles.value = r.data
    catalogue.value = c.data
    error.value = ''
    if (!can('users.view') && can('roles.view')) tab.value = 'roles'
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

function openCreateUser() {
  editingUser.value = null
  userFormOpen.value = true
}
function openEditUser(row: User) {
  editingUser.value = row
  userFormOpen.value = true
}
async function toggleCanOnboard(row: User, next: boolean) {
  if (togglingOnboardId.value || (row.can_onboard !== false) === next) return
  togglingOnboardId.value = row.id
  error.value = ''
  try {
    await api('/users/' + row.id, {
      method: 'PATCH',
      body: JSON.stringify({ can_onboard: next, expected_version: row.version }),
    })
    await load()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    togglingOnboardId.value = ''
  }
}
async function userCommand(cmd: string, row: User) {
  if (cmd === 'password') return openPassword(row)
  if (cmd === 'toggle') {
    try {
      await api('/users/' + row.id, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !row.enabled, expected_version: row.version }),
      })
      await load()
    } catch (e) {
      error.value = errorMessage(e)
    }
  }
  if (cmd === 'delete') {
    try {
      await ElMessageBox.confirm(`确定删除用户 ${row.username}？该用户会立即失去访问权限。`, '删除用户', {
        type: 'warning',
        confirmButtonText: '删除',
        cancelButtonText: '取消',
      })
    } catch {
      return
    }
    try {
      await api('/users/' + row.id, { method: 'DELETE' })
      await load()
    } catch (e) {
      error.value = errorMessage(e)
    }
  }
}
function openPassword(row: User) {
  passwordTarget.value = row
  newPassword.value = ''
  formError.value = ''
  passwordOpen.value = true
}
async function savePassword() {
  if (!passwordTarget.value || newPassword.value.length < 12 || saving.value) return
  saving.value = true
  formError.value = ''
  try {
    await api('/users/' + passwordTarget.value.id, {
      method: 'PATCH',
      body: JSON.stringify({ password: newPassword.value, expected_version: passwordTarget.value.version }),
    })
    newPassword.value = ''
    passwordOpen.value = false
    await load()
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

function openCreateRole(from?: RoleRecord) {
  editingRole.value = null
  copySource.value = from || null
  roleFormOpen.value = true
}
function openEditRole(role: RoleRecord) {
  editingRole.value = role
  copySource.value = null
  roleFormOpen.value = true
}
async function removeRole(role: RoleRecord) {
  try {
    await ElMessageBox.confirm(`确定删除角色 ${role.name}？`, '删除角色', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api('/roles/' + role.id, { method: 'DELETE' })
    await load()
  } catch (e) {
    error.value = errorMessage(e)
  }
}
onMounted(load)
</script>

<style scoped>
.access-tabs {
  display: inline-flex;
  gap: 4px;
  padding: 4px;
  margin-bottom: 16px;
  background: #e9eef0;
  border-radius: 10px;
}
.access-tabs button {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 7px 14px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: #5d6c7b;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}
.access-tabs button.is-active {
  background: var(--surface);
  color: var(--ink);
  box-shadow: 0 2px 8px rgba(20, 35, 40, 0.08);
}
.access-table {
  padding: 6px 0;
}
.access-avatar {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: none;
  border-radius: 9px;
  background: var(--accent);
  color: var(--surface);
  font-weight: 800;
  font-size: 13px;
}
.access-avatar.is-admin {
  background: #d9534f;
}
.access-avatar.is-disabled {
  background: var(--line-strong);
}
.access-me {
  margin-left: 8px;
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--tint-100);
  color: var(--accent-deep);
  font-size: 10px;
  font-style: normal;
}
.access-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.access-count {
  font-family: 'ui-monospace', monospace;
  font-size: 12px;
  color: var(--ink-mid);
}
.access-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--ink-mid);
}
.access-state::before {
  content: '';
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--line-strong);
}
.access-state.is-on::before {
  background: var(--accent);
  box-shadow: 0 0 0 3px #d6eaff;
}
:global(.access-danger) {
  color: #d9534f;
}

/* role cards */
.access-roles {
  display: grid;
  gap: 12px;
}

/* dialogs */
:global(.access-dialog .el-dialog__body) {
  padding-top: 8px;
}
.access-row-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  white-space: nowrap;
}
.access-row-more {
  padding: 6px;
  color: var(--muted-strong);
}
</style>
