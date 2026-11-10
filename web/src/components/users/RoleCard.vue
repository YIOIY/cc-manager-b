<template>
  <article class="panel access-role" :class="{ 'is-admin': role.id === ADMIN_ROLE }">
    <div class="access-role-main">
      <span class="access-role-icon"
        ><component :is="role.id === ADMIN_ROLE ? Crown : role.builtin ? Shield : ShieldCheck" :size="18"
      /></span>
      <div class="access-role-text">
        <h3>
          {{ role.name }}
          <span v-if="role.id === ADMIN_ROLE" class="access-pill is-admin">超级角色</span>
          <span v-else-if="role.builtin" class="access-pill">内置</span>
        </h3>
        <p>{{ role.description || '无描述' }}</p>
        <div class="access-role-summary">
          <template v-if="role.id === ADMIN_ROLE"
            ><span class="access-summary-all">全部 {{ total }} 项权限，含以后新增</span></template
          >
          <template v-else>
            <span class="access-summary-pages">
              <Eye :size="12" />
              <template v-if="pages.length">{{ pages.join('、') }}</template>
              <template v-else>没有可见页面</template>
            </span>
            <span
              v-for="mod in summary"
              :key="mod.key"
              class="access-summary-chip"
              :class="{ 'is-full': mod.granted === mod.total }"
            >
              {{ mod.label }}<b>{{ mod.granted }}/{{ mod.total }}</b>
            </span>
            <span v-if="!summary.length" class="muted">未授予任何权限</span>
          </template>
        </div>
      </div>
    </div>
    <div class="access-role-side">
      <span class="access-role-users"><UsersRound :size="13" />{{ role.user_count }} 个用户</span>
      <div class="access-role-actions">
        <el-button v-if="can('roles.manage') && role.id !== ADMIN_ROLE" size="small" @click="emit('edit', role)"
          >编辑权限</el-button
        >
        <el-button v-if="can('roles.manage') && role.id !== ADMIN_ROLE" size="small" text @click="emit('copy', role)"
          >复制</el-button
        >
        <el-button
          v-if="can('roles.manage') && !role.builtin"
          size="small"
          text
          type="danger"
          :disabled="role.user_count > 0"
          :title="role.user_count > 0 ? '仍有用户绑定' : ''"
          @click="emit('remove', role)"
          >删除</el-button
        >
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Crown, Eye, Shield, ShieldCheck, UsersRound } from 'lucide-vue-next'
import { useSession } from '../../composables/useSession'
import type { PermissionModule, RoleRecord } from '../../types/api'
import { ADMIN_ROLE, moduleSummary, totalPermissions, visiblePages } from './access'

const props = defineProps<{ role: RoleRecord; catalogue: PermissionModule[] }>()
const emit = defineEmits<{ edit: [role: RoleRecord]; copy: [role: RoleRecord]; remove: [role: RoleRecord] }>()
const { can } = useSession()

const total = computed(() => totalPermissions(props.catalogue))
const pages = computed(() => visiblePages(props.role, props.catalogue))
const summary = computed(() => moduleSummary(props.role, props.catalogue))
</script>

<style scoped>
.access-role {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  padding: 18px 20px;
}
.access-role.is-admin {
  background: linear-gradient(90deg, #fff7f6 0, var(--surface) 40%);
  border-color: #f3d6d5;
}
.access-role-main {
  display: flex;
  gap: 14px;
  min-width: 0;
}
.access-role-icon {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  flex: none;
  border-radius: 11px;
  background: var(--tint-100);
  color: var(--accent-deep);
}
.access-role.is-admin .access-role-icon {
  background: #fde8e7;
  color: var(--danger);
}
.access-role-text {
  min-width: 0;
}
.access-role h3 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 3px;
  font-size: 15px;
}
.access-pill {
  padding: 1px 7px;
  border-radius: 99px;
  background: #eef2f4;
  color: var(--muted-strong);
  font-size: 10.5px;
  font-weight: 600;
}
.access-pill.is-admin {
  background: #fde8e7;
  color: var(--danger);
}
.access-role p {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--muted);
}
.access-role-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.access-summary-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  border-radius: 7px;
  background: #f3f6f7;
  color: var(--ink-mid);
  font-size: 11.5px;
}
.access-summary-chip b {
  font-family: 'DM Mono', monospace;
  font-weight: 500;
  color: var(--muted-cool);
}
.access-summary-chip.is-full b {
  color: var(--accent-deep);
}
.access-summary-all {
  font-size: 12px;
  color: var(--danger);
}
.access-role-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
  flex: none;
}
.access-role-users {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--muted-cool);
}
.access-role-actions {
  display: flex;
  gap: 2px;
}
.access-summary-pages {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  width: 100%;
  margin-bottom: 2px;
  font-size: 12px;
  color: #3b4a5c;
}
.access-summary-pages svg {
  color: var(--accent-deep);
}
@media (max-width: 760px) {
  .access-role {
    flex-direction: column;
  }
  .access-role-side {
    align-items: flex-start;
  }
}
</style>
