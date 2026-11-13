<template>
  <div v-if="view" class="error-notice" :class="toneClass" role="alert">
    <span class="error-notice-icon">
      <CircleSlash v-if="tone === 'muted'" :size="16" /><AlertTriangle v-else :size="16" />
    </span>
    <div class="error-notice-body">
      <p class="error-notice-title">{{ view.title }}</p>
      <p v-if="view.detail" class="error-notice-detail">{{ view.detail }}</p>
      <ul v-if="!compact && view.fields.length" class="error-notice-fields">
        <li v-for="f in view.fields" :key="f.field + f.code" class="error-notice-field">
          {{ label(f.field) }}：{{ f.message }}
        </li>
      </ul>
      <p v-if="view.requestId" class="error-notice-meta">
        请求编号 {{ view.requestId }}
        <button type="button" class="error-notice-copy" @click="copy">{{ copied ? '已复制' : '复制错误信息' }}</button>
        <button type="button" class="error-notice-copy error-notice-report" @click="report">反馈这个问题</button>
      </p>
    </div>
    <button v-if="closable" type="button" class="error-notice-close" aria-label="关闭" @click="emit('close')">
      <X :size="14" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { openFeedback } from '../composables/useFeedback'
import { computed, ref } from 'vue'
import { AlertTriangle, CircleSlash, X } from 'lucide-vue-next'
import { describeError, errorClipboardText } from '../errors'
import { FieldLabels } from '../generated/error-codes'

const props = defineProps<{
  error: unknown | null
  fallback?: string
  /** hide the field list (the form shows them inline instead) */
  compact?: boolean
  closable?: boolean
}>()
const emit = defineEmits<{ close: [] }>()
const copied = ref(false)

const view = computed(() => (props.error ? describeError(props.error, props.fallback ?? '操作失败') : null))
/** pending = the user can fix it; danger = needs intervention; muted = external or wait. */
const tone = computed(() => {
  switch (view.value?.category) {
    case 'validation':
    case 'conflict':
      return 'pending'
    case 'upstream':
    case 'rate_limit':
      return 'muted'
    default:
      return 'danger'
  }
})
const toneClass = computed(() => `tone-${tone.value}`)
const label = (field: string) => FieldLabels[field] ?? field

// Hands the error to 用户之声 so the report carries its code and request id.
function report() {
  if (!view.value) return
  openFeedback({ title: view.value.title, errorCode: view.value.code, requestId: view.value.requestId })
}
async function copy() {
  if (!view.value) return
  if (await copyText(errorClipboardText(view.value))) {
    copied.value = true
    window.setTimeout(() => (copied.value = false), 1600)
  } else {
    copied.value = false
  }
}
</script>

<style scoped>
.error-notice {
  display: flex;
  gap: 10px;
  padding: 10px 14px;
  border-radius: 10px;
  border: 1px solid transparent;
  font-size: 13px;
  margin-bottom: 12px;
}
.tone-danger {
  background: #fdf1f0;
  border-color: #f2cfcc;
  color: var(--danger);
}
.tone-pending {
  background: var(--pending-soft);
  border-color: #f0ddb8;
  color: var(--pending);
}
.tone-muted {
  background: var(--surface-soft);
  border-color: var(--line-tint);
  color: var(--muted-strong);
}
.error-notice-icon {
  display: grid;
  place-items: center;
  flex-shrink: 0;
  padding-top: 2px;
}
.error-notice-body {
  flex: 1;
  min-width: 0;
}
.error-notice-title {
  margin: 0;
  font-weight: 600;
  color: var(--ink-soft);
}
.tone-danger .error-notice-title {
  color: var(--danger);
}
.error-notice-detail {
  margin: 3px 0 0;
  color: var(--muted-strong);
}
.error-notice-fields {
  margin: 6px 0 0;
  padding-left: 18px;
  color: var(--ink-mid);
}
.error-notice-field {
  margin: 2px 0;
}
.error-notice-meta {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--muted);
}
.error-notice-copy {
  margin-left: 8px;
  border: 0;
  background: none;
  padding: 0;
  font-size: 12px;
  color: var(--accent-dark);
  cursor: pointer;
}
.error-notice-copy:hover {
  color: var(--accent-strong);
  text-decoration: underline;
}
.error-notice-close {
  border: 0;
  background: none;
  padding: 2px;
  color: var(--muted-soft);
  cursor: pointer;
  align-self: flex-start;
}
</style>
