<template>
  <div class="completion-card" :class="{ 'is-ready': ready, 'is-compact': compact }">
    <template v-if="compact">
      <div class="completion-compact-head" aria-live="polite">
        <strong>补充上号资料</strong
        ><span>{{ blocker || (remaining ? `待补充 ${remaining} 组资料` : '必填资料已齐全') }}</span>
      </div>
      <el-alert
        v-for="warning in warnings"
        :key="warning"
        :title="warning"
        type="warning"
        :closable="false"
        show-icon
        class="completion-warning"
      />
      <details v-if="remaining" class="completion-gaps">
        <summary>查看待补充项</summary>
        <div class="completion-gap-links">
          <el-button
            v-for="item in items.filter((i) => !i.done)"
            :key="item.key"
            text
            type="primary"
            @click="emit('jump', item.anchor)"
            >{{ item.label }}</el-button
          >
        </div>
      </details>
    </template>
    <template v-else>
      <div class="completion-head">
        <span class="completion-head-icon">
          <CheckCircle2 v-if="ready" :size="18" /><AlertTriangle v-else-if="blocker" :size="18" /><ListChecks
            v-else
            :size="18"
          />
        </span>
        <strong v-if="ready">资料已齐全，可以上号</strong>
        <strong v-else-if="remaining === 0">日期顺序有误：{{ blocker }}</strong>
        <strong v-else>还差 {{ remaining }} 项才能上号</strong>
        <span class="completion-head-sub">{{
          ready ? '可以创建并上号' : remaining === 0 ? '请调整日期后再上号' : '授权不提供，需手动补充'
        }}</span>
      </div>
      <el-alert
        v-for="warning in warnings"
        :key="warning"
        :title="warning"
        type="warning"
        :closable="false"
        show-icon
        class="completion-warning"
      />
      <ul class="completion-list">
        <li v-for="item in items" :key="item.key" class="completion-item" :class="{ 'is-done': item.done }">
          <div class="completion-row">
            <span class="completion-state">
              <CheckCircle2 v-if="item.done" :size="16" /><Circle v-else :size="16" />
            </span>
            <span class="completion-text">
              <span class="completion-label">{{ item.label }}</span>
              <span class="completion-hint">{{ item.hint }}</span>
            </span>
            <el-button
              v-if="!item.done && item.key !== 'device_ids'"
              text
              type="primary"
              @click="emit('jump', item.anchor)"
              >去填写</el-button
            >
          </div>
          <div v-if="item.key === 'device_ids'" class="completion-inline"><slot name="device" /></div>
        </li>
      </ul>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { AlertTriangle, CheckCircle2, Circle, ListChecks } from 'lucide-vue-next'
import type { ChecklistItem } from '../model/account-form'

const props = defineProps<{
  items: ChecklistItem[]
  warnings: string[]
  /** A problem that is not a missing field (date ordering); shown instead of "ready". */
  blocker?: string
  compact?: boolean
}>()
const emit = defineEmits<{ jump: [anchor: string] }>()
const remaining = computed(() => props.items.filter((item) => !item.done).length)
const ready = computed(() => remaining.value === 0 && !props.blocker)
</script>

<style scoped>
.completion-card.is-compact {
  margin: 0 0 20px;
  padding: 0;
  border: 0;
  background: transparent;
}
.completion-compact-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.completion-compact-head strong {
  font-size: 14px;
  color: var(--ink-soft);
}
.completion-compact-head span,
.completion-gaps summary {
  font-size: 12px;
  color: var(--muted);
}
.completion-gaps {
  margin-top: 8px;
}
.completion-gaps summary {
  cursor: pointer;
}
.completion-gap-links {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.completion-card {
  margin-top: 14px;
  padding: 14px 18px 6px;
  border: 1px solid var(--line-tint);
  border-radius: 14px;
  background: #fff;
}
.completion-card.is-ready {
  background: var(--tint-50);
  border-color: var(--line-tint);
}
.completion-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--line-soft);
}
.completion-head-icon {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--pending-soft);
  color: var(--pending);
  flex-shrink: 0;
}
.is-ready .completion-head-icon {
  background: var(--tint-100);
  color: var(--accent-dark);
}
.completion-head strong {
  font-size: 14px;
  font-weight: 600;
  color: var(--ink-soft);
}
.completion-head-sub {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted-soft);
  text-align: right;
}
.completion-warning {
  margin-top: 10px;
}
.completion-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.completion-item {
  padding: 11px 0;
  border-bottom: 1px solid var(--line-soft);
}
.completion-item:last-child {
  border-bottom: 0;
}
.completion-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 24px;
}
.completion-state {
  display: grid;
  place-items: center;
  color: var(--pending);
  flex-shrink: 0;
}
.is-done .completion-state {
  color: var(--accent-dark);
}
.completion-text {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}
.completion-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-soft);
}
.completion-hint {
  font-size: 12px;
  color: var(--muted);
}
.is-done .completion-label {
  color: var(--muted);
  font-weight: 500;
}
.is-done .completion-hint {
  color: var(--muted-soft);
}
.completion-row :deep(.el-button) {
  --el-button-text-color: var(--accent-dark);
  --el-button-hover-text-color: var(--accent-strong);
  --el-button-hover-bg-color: var(--tint-50);
  padding: 4px 8px;
  height: auto;
  font-size: 12px;
}
.completion-inline {
  margin: 8px 0 2px 26px;
}
.completion-inline :deep(.el-form-item) {
  margin-bottom: 0;
}
.completion-inline :deep(.el-form-item__label) {
  display: none;
}
</style>
