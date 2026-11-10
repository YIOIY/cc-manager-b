<template>
  <form class="search-row" @submit.prevent="emit('apply')">
    <el-input
      :model-value="draftQuery"
      :placeholder="config.placeholder"
      clearable
      aria-label="关键词搜索"
      @update:model-value="emit('update:draftQuery', $event)"
      @clear="emit('apply')"
    >
      <template #prefix><Search :size="17" /></template>
    </el-input>
    <el-button
      :class="{ 'is-filtered': activeFilterCount }"
      :aria-expanded="filtersOpen"
      @click="emit('update:filtersOpen', !filtersOpen)"
    >
      <SlidersHorizontal :size="15" />条件筛选<span v-if="activeFilterCount" class="filter-count">{{
        activeFilterCount
      }}</span>
      <ChevronDown :size="14" :class="{ rotated: filtersOpen }" />
    </el-button>
    <el-button native-type="submit" type="primary">查询</el-button>
    <el-button
      :disabled="!hasConditions && !draftQuery && !Object.values(draftConditions).some(hasValue)"
      @click="emit('reset')"
      >重置</el-button
    >
  </form>
  <div v-show="filtersOpen" class="filter-panel">
    <div class="filter-grid">
      <div v-for="field in config.filters" :key="field.key" class="filter-field">
        <label :for="'filter-' + field.key">{{ field.label }}</label>
        <el-select
          v-if="field.type === 'select'"
          :id="'filter-' + field.key"
          :model-value="draftConditions[field.key] ?? ''"
          clearable
          :placeholder="'全部' + field.label"
          @update:model-value="set(field.key, $event)"
        >
          <el-option v-for="option in field.options" :key="option.value" :label="option.label" :value="option.value" />
        </el-select>
        <el-date-picker
          v-else-if="field.type === 'date'"
          :id="'filter-' + field.key"
          :model-value="draftConditions[field.key] ?? ''"
          value-format="YYYY-MM-DD"
          placeholder="选择日期"
          clearable
          @update:model-value="set(field.key, $event)"
        />
        <el-input-number
          v-else-if="field.type === 'number'"
          :id="'filter-' + field.key"
          :model-value="numberValue(field.key)"
          :min="0"
          :controls="false"
          placeholder="不限"
          @update:model-value="set(field.key, $event)"
          @keyup.enter="emit('apply')"
        />
        <el-input
          v-else
          :id="'filter-' + field.key"
          :model-value="String(draftConditions[field.key] ?? '')"
          clearable
          placeholder="输入筛选条件"
          @update:model-value="set(field.key, $event)"
          @keyup.enter="emit('apply')"
        />
      </div>
    </div>
    <p class="filter-caption">不同条件同时满足；日期范围包含起止日期。设置后点击「查询」生效。</p>
  </div>
  <div v-if="hasConditions" class="active-filters">
    <span>当前筛选</span>
    <el-tag v-if="query" closable effect="plain" @close="emit('removeQuery')">关键词：{{ query }}</el-tag>
    <el-tag
      v-for="field in activeFields"
      :key="field.key"
      closable
      effect="plain"
      @close="emit('removeCondition', field.key)"
      >{{ field.label }}：{{ conditionLabel(field) }}</el-tag
    >
    <el-button text size="small" @click="emit('reset')">清除全部</el-button>
  </div>
</template>

<script setup lang="ts">
import { Search, SlidersHorizontal, ChevronDown } from 'lucide-vue-next'
import { hasValue, type Conditions, type FilterField, type ListConfig } from '../list-model'

const props = defineProps<{
  config: ListConfig
  draftQuery: string
  query: string
  draftConditions: Conditions
  conditions: Conditions
  filtersOpen: boolean
  activeFields: FilterField[]
  activeFilterCount: number
  hasConditions: boolean
}>()
const emit = defineEmits<{
  'update:draftQuery': [value: string]
  'update:draftConditions': [value: Conditions]
  'update:filtersOpen': [value: boolean]
  apply: []
  reset: []
  removeQuery: []
  removeCondition: [key: string]
}>()

function set(key: string, value: unknown) {
  emit('update:draftConditions', { ...props.draftConditions, [key]: value as Conditions[string] })
}
function numberValue(key: string): number | undefined {
  const value = props.draftConditions[key]
  return hasValue(value) ? Number(value) : undefined
}
function conditionLabel(field: FilterField) {
  const value = props.conditions[field.key]
  return field.options?.find((o) => o.value === value)?.label || value
}
</script>

<style scoped>
.search-row {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 13px 15px;
}

:global(.search-row > .el-input) {
  flex: 1;
  min-width: 180px;
}

:global(.search-row > .el-button) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

:global(.search-row .el-input__wrapper) {
  min-height: 38px;
  background: #f8fafb !important;
}

.filter-count {
  display: inline-grid;
  place-items: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 99px;
  background: #d8eee8;
  color: var(--accent-dark);
  font-size: 10px;
}

.filter-panel {
  padding: 14px 15px;
  border-top: 1px solid var(--line-soft);
  background: var(--surface-soft);
}

.filter-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.filter-field {
  min-width: 0;
}

:global(.filter-field > label) {
  display: block;
  color: var(--muted-strong);
  font-size: 11px;
  font-weight: 700;
  margin-bottom: 6px;
}

:global(.filter-field .el-input) {
  width: 100%;
}

:global(.filter-field .el-select) {
  width: 100%;
}

:global(.filter-field .el-date-editor) {
  width: 100%;
}

:global(.filter-field .el-input-number) {
  width: 100%;
}

.filter-caption {
  margin: 12px 0 0;
  color: #91a0a8;
  font-size: 11px;
}

.active-filters {
  display: flex;
  gap: 7px;
  flex-wrap: wrap;
  align-items: center;
  padding: 0 14px 12px;
  color: #89969d;
  font-size: 11px;
}

:global(.active-filters .el-tag) {
  border-radius: 5px;
}

:global(.active-filters .el-button) {
  margin-left: auto;
}

.platform-monitor .search-row {
  padding: 13px 20px;
}

:global(.platform-monitor .search-row .el-input__wrapper) {
  background: #0d1a2b !important;
  box-shadow: 0 0 0 1px #21324a inset !important;
}

:global(.platform-monitor .search-row .el-input__inner) {
  color: #dbe9f6;
}

.platform-monitor .filter-panel {
  background: #0a1727;
  border-color: #20334c;
}

:global(.platform-monitor .filter-field > label) {
  color: #6e849d;
}

.platform-monitor .filter-caption {
  color: #6e849d;
}

.platform-monitor .active-filters {
  color: #7890a9;
}

.platform-monitor .search-row {
  padding: 13px 15px;
}

:global(.platform-monitor .search-row .el-input__wrapper) {
  background: #f8fafb !important;
  box-shadow: 0 0 0 1px #dfe8ea inset !important;
}

:global(.platform-monitor .search-row .el-input__inner) {
  color: var(--ink-soft);
}

.platform-monitor .filter-panel {
  background: var(--surface-soft);
  border-color: var(--line-soft);
}

:global(.platform-monitor .filter-field > label) {
  color: var(--muted-strong);
}

.platform-monitor .filter-caption {
  color: var(--muted-strong);
}

.platform-monitor .active-filters {
  color: #89969d;
}

@media (max-width: 980px) {
  .filter-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 620px) {
  .search-row {
    flex-wrap: wrap;
  }
  :global(.search-row > .el-input) {
    flex-basis: 100%;
  }
  .filter-grid {
    grid-template-columns: 1fr;
  }
}
</style>
