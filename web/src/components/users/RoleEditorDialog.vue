<template>
  <el-dialog
    :model-value="modelValue"
    :title="role ? '编辑角色 · ' + role.name : '新建角色'"
    width="860px"
    class="access-dialog access-role-dialog"
    :close-on-click-modal="!saving"
    top="6vh"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form label-position="top" :disabled="saving" @submit.prevent="save">
      <div class="form-grid two">
        <el-form-item label="角色名称" required>
          <el-input
            v-model="form.name"
            :disabled="!!role?.builtin"
            maxlength="40"
            show-word-limit
            placeholder="例如：网络巡检"
          />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" maxlength="120" placeholder="这个角色负责什么" />
        </el-form-item>
      </div>

      <div class="access-pages">
        <div class="access-pages-head">
          <strong><Eye :size="14" />可见页面</strong>
          <span class="muted">关闭后该页面从导航中消失，页内的其他动作权限也会一并移除</span>
        </div>
        <div class="access-pages-grid">
          <label
            v-for="mod in pages"
            :key="mod.key"
            class="access-page-toggle"
            :class="{ 'is-on': hasView(mod), 'is-locked': !canGrant(viewKey(mod)) }"
          >
            <el-switch
              :model-value="hasView(mod)"
              :disabled="!canGrant(viewKey(mod))"
              size="small"
              @change="setPermission(viewKey(mod), $event as boolean)"
            />
            <span>{{ mod.page }}</span>
          </label>
        </div>
      </div>

      <div class="access-editor">
        <aside class="access-editor-nav">
          <div class="access-editor-nav-head">
            <span>模块</span>
            <el-dropdown trigger="click" @command="applyPreset">
              <el-button text size="small">预设<ChevronDown :size="13" /></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="readonly">只读（所有查看权限）</el-dropdown-item>
                  <el-dropdown-item v-for="r in presetRoles" :key="r.id" :command="'role:' + r.id"
                    >同「{{ r.name }}」</el-dropdown-item
                  >
                  <el-dropdown-item command="all" divided>全选可授予</el-dropdown-item>
                  <el-dropdown-item command="none">清空</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
          <button
            v-for="mod in catalogue"
            :key="mod.key"
            type="button"
            class="access-editor-module"
            :class="{ 'is-active': activeModule === mod.key, 'is-empty': moduleCount(mod) === 0 }"
            @click="activeModule = mod.key"
          >
            <span
              ><EyeOff v-if="mod.page && !hasView(mod)" :size="12" class="access-module-hidden" />{{ mod.label }}</span
            >
            <b :class="{ 'is-full': moduleCount(mod) === grantableIn(mod).length && moduleCount(mod) > 0 }"
              >{{ moduleCount(mod) }}/{{ grantableIn(mod).length }}</b
            >
          </button>
          <div class="access-editor-total">
            已选 <b>{{ form.permissions.length }}</b> / {{ grantable.length }}
          </div>
        </aside>

        <section v-if="currentModule" class="access-editor-body">
          <header class="access-editor-body-head">
            <div>
              <h4>{{ currentModule.label }}</h4>
              <p>{{ moduleCount(currentModule) }} / {{ grantableIn(currentModule).length }} 项已授予</p>
            </div>
            <div class="access-editor-body-actions">
              <el-button
                size="small"
                :disabled="
                  !grantableIn(currentModule).length || moduleCount(currentModule) === grantableIn(currentModule).length
                "
                @click="setModule(currentModule, true)"
                >全选</el-button
              >
              <el-button
                size="small"
                :disabled="moduleCount(currentModule) === 0"
                @click="setModule(currentModule, false)"
                >清空</el-button
              >
            </div>
          </header>
          <ul class="access-perm-list">
            <li
              v-for="p in currentModule.permissions"
              :key="p.key"
              :class="{
                'is-on': form.permissions.includes(p.key),
                'is-locked': !canGrant(p.key) || needsView(currentModule, p.key),
                'is-page': p.key === viewKey(currentModule) && !!currentModule.page,
              }"
            >
              <label>
                <span class="access-perm-text">
                  <strong
                    >{{ p.label }}<code>{{ p.key }}</code
                    ><em v-if="p.key === viewKey(currentModule) && currentModule.page" class="access-page-tag"
                      ><Eye :size="11" />页面可见</em
                    ></strong
                  >
                  <small v-if="!canGrant(p.key)">你自己没有这项权限，不能授予</small>
                  <small v-else-if="needsView(currentModule, p.key)">先开启「{{ currentModule.page }}」页面可见</small>
                  <small v-else-if="p.hint">{{ p.hint }}</small>
                </span>
                <el-switch
                  :model-value="form.permissions.includes(p.key)"
                  :disabled="!canGrant(p.key) || needsView(currentModule, p.key)"
                  size="small"
                  @change="setPermission(p.key, $event as boolean)"
                />
              </label>
            </li>
          </ul>
        </section>
      </div>
      <el-alert v-if="formError" :title="formError" type="error" :closable="false" show-icon />
    </el-form>
    <template #footer>
      <div class="access-dialog-footer">
        <span class="muted">{{ dirty ? '保存后对已绑定用户立即生效' : '没有改动' }}</span>
        <div>
          <el-button :disabled="saving" @click="emit('update:modelValue', false)">取消</el-button>
          <el-button type="primary" :loading="saving" :disabled="!form.name.trim() || !dirty" @click="save">{{
            role ? '保存' : '创建角色'
          }}</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ChevronDown, Eye, EyeOff } from 'lucide-vue-next'
import { api, errorMessage } from '../../api'
import { useSession } from '../../composables/useSession'
import type { Permission, PermissionModule, RoleRecord } from '../../types/api'
import {
  ADMIN_ROLE,
  canGrantWith,
  pageModules,
  presetPermissions,
  roleSnapshot,
  togglePermission,
  viewKey,
} from './access'

const props = defineProps<{
  modelValue: boolean
  /** role being edited; null creates a new one */
  role: RoleRecord | null
  /** when creating, start from this role's name/description/permissions */
  copyFrom?: RoleRecord | null
  catalogue: PermissionModule[]
  roles: RoleRecord[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const { user: me } = useSession()

const form = reactive<{ name: string; description: string; permissions: Permission[] }>({
  name: '',
  description: '',
  permissions: [],
})
const saving = ref(false)
const formError = ref('')
const baseline = ref('')
const activeModule = ref('')

const canGrant = (p: Permission) => canGrantWith(me.value)(p)
const pages = computed(() => pageModules(props.catalogue))
const currentModule = computed(() => props.catalogue.find((m) => m.key === activeModule.value) || props.catalogue[0])
const presetRoles = computed(() => props.roles.filter((r) => r.id !== ADMIN_ROLE && r.id !== props.role?.id))
const grantable = computed(() => props.catalogue.flatMap((m) => m.permissions.map((p) => p.key)).filter(canGrant))
const dirty = computed(() => roleSnapshot(form.name, form.description, form.permissions) !== baseline.value)

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    if (props.role) {
      Object.assign(form, {
        name: props.role.name,
        description: props.role.description,
        permissions: [...props.role.permissions],
      })
      baseline.value = roleSnapshot(form.name, form.description, form.permissions)
    } else {
      const from = props.copyFrom
      Object.assign(form, {
        name: from ? from.name + ' 副本' : '',
        description: from?.description || '',
        permissions: from ? from.permissions.filter(canGrant) : [],
      })
      baseline.value = ''
    }
    activeModule.value = props.catalogue[0]?.key || ''
    formError.value = ''
  },
  { immediate: true },
)

const hasView = (mod: PermissionModule) => form.permissions.includes(viewKey(mod))
/** Actions inside a page need the page itself to be visible first. */
function needsView(mod: PermissionModule, key: Permission) {
  return !!mod.page && key !== viewKey(mod) && !hasView(mod)
}
function grantableIn(mod: PermissionModule) {
  return mod.permissions.map((p) => p.key).filter(canGrant)
}
function moduleCount(mod: PermissionModule) {
  return mod.permissions.filter((p) => form.permissions.includes(p.key)).length
}
function setPermission(key: Permission, on: boolean) {
  form.permissions = togglePermission(form.permissions, key, on, props.catalogue, canGrant)
}
function setModule(mod: PermissionModule, on: boolean) {
  for (const key of grantableIn(mod)) setPermission(key, on)
}
function applyPreset(cmd: string) {
  const next = presetPermissions(cmd, grantable.value, props.roles, canGrant)
  if (next) form.permissions = next
}

async function save() {
  if (!form.name.trim() || saving.value) return
  saving.value = true
  formError.value = ''
  try {
    const body: Record<string, unknown> = { description: form.description.trim(), permissions: form.permissions }
    if (!props.role?.builtin) body.name = form.name.trim()
    if (props.role) {
      body.expected_version = props.role.version
      await api('/roles/' + props.role.id, { method: 'PATCH', body: JSON.stringify(body) })
    } else {
      await api('/roles', { method: 'POST', body: JSON.stringify(body) })
    }
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
.access-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.access-dialog-footer .muted {
  font-size: 12px;
}
.access-pages {
  margin-bottom: 14px;
  padding: 14px 16px;
  border: 1px solid #d9efe8;
  border-radius: 12px;
  background: #f4fbf8;
}
.access-pages-head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 10px;
}
.access-pages-head strong {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #0b6f5a;
}
.access-pages-head .muted {
  font-size: 12px;
}
.access-pages-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.access-page-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px 6px 8px;
  border: 1px solid #e0e8e5;
  border-radius: 99px;
  background: var(--surface);
  font-size: 12.5px;
  color: var(--muted);
  cursor: pointer;
}
.access-page-toggle.is-on {
  border-color: var(--accent);
  color: var(--ink-navy);
}
.access-page-toggle.is-locked {
  opacity: 0.55;
  cursor: not-allowed;
}
.access-module-hidden {
  margin-right: 5px;
  color: #b4bdc8;
  vertical-align: -2px;
}
.access-perm-list li.is-page label {
  border: 1px dashed #c5e6dc;
}
.access-page-tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--tint-100);
  color: var(--accent-deep);
  font-size: 10.5px;
  font-style: normal;
  font-weight: 600;
}

/* role editor */
.access-editor {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr);
  min-height: 380px;
  border: 1px solid var(--line);
  border-radius: 12px;
  overflow: hidden;
}
.access-editor-nav {
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
  padding: 8px;
  background: #f6f8f9;
  border-right: 1px solid var(--line);
}
.access-editor-nav-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 6px 8px 10px;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: var(--muted-cool);
}
.access-editor-nav-head .el-button {
  flex: none;
  padding: 2px 6px;
  gap: 2px;
  letter-spacing: 0;
}
.access-editor-module {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: #3b4a5c;
  font: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}
.access-editor-module > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.access-editor-module:hover {
  background: #eceff2;
}
.access-editor-module.is-active {
  background: var(--surface);
  color: var(--accent-deep);
  font-weight: 700;
  box-shadow: 0 1px 4px rgba(20, 35, 40, 0.08);
}
.access-editor-module b {
  font-family: 'DM Mono', monospace;
  font-size: 11px;
  font-weight: 500;
  color: #9aa6b3;
}
.access-editor-module.is-empty b {
  color: var(--line-strong);
}
.access-editor-module b.is-full {
  color: var(--accent-deep);
}
.access-editor-total {
  margin-top: auto;
  padding: 12px 10px 4px;
  border-top: 1px solid var(--line);
  font-size: 12px;
  color: var(--muted-strong);
}
.access-editor-total b {
  color: var(--accent-deep);
}
.access-editor-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
  max-height: 44vh;
}
.access-editor-body-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--line-soft);
}
.access-editor-body-head h4 {
  margin: 0 0 2px;
  font-size: 14px;
}
.access-editor-body-head p {
  margin: 0;
  font-size: 12px;
  color: var(--muted-cool);
}
.access-editor-body-actions {
  display: flex;
  gap: 4px;
}
.access-perm-list {
  margin: 0;
  padding: 6px 8px;
  list-style: none;
  overflow: auto;
}
.access-perm-list li label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 10px;
  border-radius: 9px;
  cursor: pointer;
}
.access-perm-list li label:hover {
  background: #f6f8f9;
}
.access-perm-list li.is-on label {
  background: var(--tint-50);
}
.access-perm-list li.is-locked label {
  cursor: not-allowed;
  opacity: 0.6;
}
.access-perm-text {
  display: grid;
  gap: 2px;
  min-width: 0;
}
.access-perm-text strong {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-navy);
}
.access-perm-text code {
  padding: 1px 6px;
  border-radius: 5px;
  background: #eef2f4;
  color: var(--muted);
  font-size: 10.5px;
  font-weight: 400;
}
.access-perm-text small {
  font-size: 11.5px;
  color: var(--muted-cool);
}
@media (max-width: 760px) {
  .access-editor {
    grid-template-columns: 1fr;
  }
  .access-editor-nav {
    flex-direction: row;
    flex-wrap: wrap;
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }
  .access-editor-nav-head,
  .access-editor-total {
    width: 100%;
  }
}
</style>
