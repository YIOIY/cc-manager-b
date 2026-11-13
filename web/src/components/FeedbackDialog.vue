<template>
  <el-dialog v-model="open" title="反馈问题或建议" width="560px" destroy-on-close append-to-body>
    <template v-if="!sent">
      <ErrorNotice :error="notice" compact closable @close="notice = null" />
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="类型">
          <div class="feedback-kinds">
            <el-radio-group v-model="form.kind">
              <el-radio-button v-for="k in feedbackKinds" :key="k.value" :value="k.value">{{
                k.label
              }}</el-radio-button>
            </el-radio-group>
          </div>
        </el-form-item>
        <el-form-item label="描述" required :error="fieldErrors.body">
          <el-input
            v-model="form.body"
            type="textarea"
            :rows="5"
            maxlength="2000"
            show-word-limit
            placeholder="你在做什么、看到了什么、原本希望怎样。写得越具体，越容易处理。"
          />
        </el-form-item>
        <el-form-item label="标题" :error="fieldErrors.title">
          <el-input v-model="form.title" maxlength="80" placeholder="可不填，默认取描述的开头" />
        </el-form-item>
      </el-form>
      <div class="feedback-context">
        <span>会一起附上，方便定位：</span>
        <ul>
          <li>
            当前页面 <code>{{ page }}</code>
          </li>
          <li v-if="prefill?.errorCode">
            错误码 <code>{{ prefill.errorCode }}</code>
          </li>
          <li v-if="prefill?.requestId">
            请求编号 <code>{{ prefill.requestId }}</code>
          </li>
          <li>浏览器信息</li>
        </ul>
        <small>不会附带页面上的表单内容、搜索词、密码或凭证。</small>
      </div>
    </template>
    <div v-else class="feedback-sent" role="status">
      <CircleCheck :size="30" />
      <strong>已收到，谢谢</strong>
      <p>处理进度和回复可以在「用户之声」里看到。</p>
    </div>
    <template #footer>
      <template v-if="!sent">
        <el-button :disabled="saving" @click="open = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">提交反馈</el-button>
      </template>
      <template v-else>
        <el-button @click="open = false">关闭</el-button>
        <el-button type="primary" @click="goToList">查看我的反馈</el-button>
      </template>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CircleCheck } from 'lucide-vue-next'
import { api, ApiError } from '../api'
import { applyFieldErrors } from '../errors'
import ErrorNotice from './ErrorNotice.vue'
import { feedbackChanged, refreshFeedbackSummary, useFeedbackDialog } from '../composables/useFeedback'
import { feedbackKinds } from '../model/feedback'
import type { FeedbackKind } from '../types/api'

const route = useRoute()
const router = useRouter()
const { open, prefill } = useFeedbackDialog()

const blank = () => ({ kind: 'problem' as FeedbackKind, title: '', body: '' })
const form = ref(blank())
const saving = ref(false)
const sent = ref(false)
const notice = ref<unknown | null>(null)
const fieldErrors = ref<Record<string, string>>({})

// Only the path: a query string can hold search terms such as an account email.
const page = computed(() => route.path || '/')

watch(open, (isOpen) => {
  if (!isOpen) return
  form.value = { ...blank(), title: prefill.value?.title ?? '' }
  sent.value = false
  notice.value = null
  fieldErrors.value = {}
})

async function submit() {
  if (saving.value) return
  fieldErrors.value = {}
  notice.value = null
  const body = form.value.body.trim()
  if (!body) {
    fieldErrors.value = { body: '请描述遇到的问题或建议' }
    return
  }
  const context: Record<string, string> = {}
  if (prefill.value?.errorCode) context.error_code = prefill.value.errorCode
  if (prefill.value?.requestId) context.request_id = prefill.value.requestId
  if (typeof navigator !== 'undefined' && navigator.userAgent) context.user_agent = navigator.userAgent
  if (typeof window !== 'undefined') context.viewport = `${window.innerWidth}x${window.innerHeight}`
  saving.value = true
  try {
    await api('/feedback', {
      method: 'POST',
      body: JSON.stringify({ kind: form.value.kind, title: form.value.title.trim(), body, page: page.value, context }),
    })
    sent.value = true
    feedbackChanged.value++
    void refreshFeedbackSummary()
  } catch (e) {
    notice.value = e
    if (e instanceof ApiError && e.fields.length) fieldErrors.value = applyFieldErrors(e.fields)
  } finally {
    saving.value = false
  }
}
function goToList() {
  open.value = false
  router.push({ name: 'feedback' })
}
</script>

<style scoped>
/* The chosen kind uses the app's own accent instead of the library's blue. */
.feedback-kinds :deep(.el-radio-button__original-radio:checked + .el-radio-button__inner) {
  background-color: var(--accent, #18a889);
  border-color: var(--accent, #18a889);
  box-shadow: -1px 0 0 0 var(--accent, #18a889);
}
.feedback-kinds :deep(.el-radio-button__inner:hover) {
  color: var(--accent-deep, #0f8a70);
}
.feedback-kinds :deep(.el-radio-button__original-radio:checked + .el-radio-button__inner:hover) {
  color: #fff;
}
.feedback-context {
  padding: 11px 14px;
  border-radius: 10px;
  background: var(--surface-soft, #fbfcfc);
  border: 1px solid var(--line-soft, #edf1f2);
  color: var(--muted-strong, #6a7788);
  font-size: 12.5px;
  line-height: 1.7;
}
.feedback-context ul {
  margin: 4px 0 6px;
  padding-left: 18px;
}
.feedback-context code {
  padding: 0 5px;
  border-radius: 4px;
  background: #eef4f3;
  color: var(--ink-soft, #263b47);
  font:
    12px 'DM Mono',
    ui-monospace,
    monospace;
}
.feedback-context small {
  color: var(--muted-soft, #9aa8ae);
}
.feedback-sent {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 18px 0 8px;
  color: var(--accent-deep, #0f8a70);
  text-align: center;
}
.feedback-sent strong {
  color: var(--ink-soft, #263b47);
  font-size: 17px;
}
.feedback-sent p {
  margin: 0;
  color: var(--muted-strong, #6a7788);
  font-size: 13px;
}
</style>
