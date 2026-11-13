<template>
  <PageHead
    eyebrow="USER VOICE"
    title="用户之声"
    :description="
      canManage
        ? '大家在使用中遇到的问题和建议。在这里查看、回复并标记处理进度。'
        : '使用中遇到的问题和想法都可以告诉我们。这里能看到你提交过的反馈、处理进度和回复。'
    "
  >
    <el-button type="primary" @click="openFeedback()"><MessageSquarePlus :size="15" />提交反馈</el-button>
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />

  <section class="voice-panel">
    <div class="voice-toolbar">
      <div class="voice-status" role="tablist" aria-label="按状态筛选">
        <button
          type="button"
          role="tab"
          :aria-selected="status === ''"
          :class="{ active: status === '' }"
          data-status=""
          @click="setStatus('')"
        >
          全部 <b>{{ totalAll }}</b>
        </button>
        <button
          v-for="s in feedbackStatuses"
          :key="s.value"
          type="button"
          role="tab"
          :aria-selected="status === s.value"
          :class="{ active: status === s.value }"
          :data-status="s.value"
          @click="setStatus(s.value)"
        >
          {{ s.label }} <b>{{ counts[s.value] ?? 0 }}</b>
        </button>
      </div>
      <div class="voice-filters">
        <el-select v-model="kind" placeholder="全部类型" clearable aria-label="按类型筛选" @change="reload">
          <el-option label="全部类型" value="" />
          <el-option v-for="k in feedbackKinds" :key="k.value" :label="k.label" :value="k.value" />
        </el-select>
        <el-input
          v-model="keyword"
          clearable
          placeholder="搜索标题、内容或提交人"
          aria-label="搜索反馈"
          @keyup.enter="reload"
          @clear="reload"
        />
      </div>
    </div>

    <ul v-if="items.length" class="voice-list">
      <li v-for="item in items" :key="item.id" :data-feedback="item.id" :class="'kind-' + item.kind">
        <div class="voice-head">
          <span class="voice-kind">{{ feedbackKindLabel(item.kind) }}</span>
          <strong>{{ item.title }}</strong>
          <el-tag size="small" :type="feedbackStatusOf(item.status).tone">{{
            feedbackStatusOf(item.status).label
          }}</el-tag>
        </div>
        <p class="voice-body">{{ item.body }}</p>
        <div class="voice-meta">
          <span v-if="canManage || !item.mine">{{ item.username }}</span>
          <time :datetime="item.created_at">{{ formatDateTime(item.created_at) }}</time>
          <span v-if="item.page"
            >页面 <code>{{ item.page }}</code></span
          >
          <span v-if="item.context.error_code"
            >错误码 <code>{{ item.context.error_code }}</code></span
          >
          <span v-if="item.context.request_id"
            >请求编号 <code>{{ item.context.request_id }}</code></span
          >
          <span v-if="canManage && item.app_version"
            >版本 <code>{{ item.app_version }}</code></span
          >
        </div>
        <div v-if="item.reply" class="voice-reply">
          <span
            >{{ item.handled_by || '管理员' }} 的回复<template v-if="item.handled_at"
              >，{{ formatDateTime(item.handled_at) }}</template
            ></span
          >
          <p>{{ item.reply }}</p>
        </div>

        <template v-if="canManage">
          <div v-if="editing === item.id" class="voice-handle">
            <label>
              <span>状态</span>
              <el-select v-model="draft.status" aria-label="处理状态">
                <el-option v-for="s in feedbackStatuses" :key="s.value" :label="s.label" :value="s.value" />
              </el-select>
            </label>
            <label class="grow">
              <span>回复（提交人可以看到）</span>
              <el-input
                v-model="draft.reply"
                type="textarea"
                :rows="3"
                maxlength="1000"
                show-word-limit
                placeholder="说明原因、进展或解决办法"
              />
            </label>
            <p v-if="saveError" class="voice-save-error" role="alert">{{ saveError }}</p>
            <div class="voice-handle-actions">
              <el-button :disabled="saving" @click="editing = ''">取消</el-button>
              <el-button type="primary" :loading="saving" @click="save(item)">保存处理结果</el-button>
            </div>
          </div>
          <el-button v-else text type="primary" class="voice-handle-open" data-handle @click="startHandling(item)">
            {{ item.reply || item.status !== 'open' ? '修改处理结果' : '处理' }}
          </el-button>
        </template>
      </li>
    </ul>
    <div v-else-if="!error" class="voice-empty">
      <template v-if="loading">正在读取…</template>
      <template v-else-if="filtered">没有符合条件的反馈。</template>
      <template v-else>
        <strong>还没有反馈</strong>
        <span>使用中遇到问题或有想法，点右上角「提交反馈」，或者点页面顶部的「反馈」按钮。</span>
      </template>
    </div>

    <el-pagination
      v-if="total > pageSize"
      class="voice-pager"
      layout="prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="goToPage"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { MessageSquarePlus } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import { api, errorMessage } from '../api'
import type { Feedback, FeedbackList, FeedbackStatus } from '../types/api'
import { feedbackChanged, openFeedback, refreshFeedbackSummary } from '../composables/useFeedback'
import { feedbackKindLabel, feedbackKinds, feedbackStatusOf, feedbackStatuses } from '../model/feedback'
import { formatDateTime } from '../utils/format'

const pageSize = 20
const items = ref<Feedback[]>([])
const counts = ref<Partial<Record<FeedbackStatus, number>>>({})
const total = ref(0)
const canManage = ref(false)
const loading = ref(false)
const error = ref('')
const status = ref<FeedbackStatus | ''>('')
const kind = ref('')
const keyword = ref('')
const page = ref(1)

const totalAll = computed(() => Object.values(counts.value).reduce((n, c) => n + (c || 0), 0))
const filtered = computed(() => !!status.value || !!kind.value || !!keyword.value.trim())

async function load() {
  loading.value = true
  const params = new URLSearchParams({ page: String(page.value), page_size: String(pageSize) })
  if (status.value) params.set('status', status.value)
  if (kind.value) params.set('kind', kind.value)
  if (keyword.value.trim()) params.set('q', keyword.value.trim())
  try {
    const data = (await api<FeedbackList>('/feedback?' + params.toString())).data
    items.value = data.items
    counts.value = data.counts
    total.value = data.pagination.total
    canManage.value = data.can_manage
    error.value = ''
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
function reload() {
  page.value = 1
  void load()
}
function setStatus(next: FeedbackStatus | '') {
  status.value = next
  reload()
}
function goToPage(next: number) {
  page.value = next
  void load()
}

// Handling one item at a time, inline.
const editing = ref('')
const draft = ref<{ status: FeedbackStatus; reply: string }>({ status: 'open', reply: '' })
const saving = ref(false)
const saveError = ref('')
function startHandling(item: Feedback) {
  editing.value = item.id
  draft.value = { status: item.status, reply: item.reply }
  saveError.value = ''
}
async function save(item: Feedback) {
  if (saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    await api('/feedback/' + item.id, {
      method: 'PATCH',
      body: JSON.stringify({
        status: draft.value.status,
        reply: draft.value.reply.trim(),
        expected_version: item.version,
      }),
    })
    editing.value = ''
    await load()
    void refreshFeedbackSummary()
  } catch (e) {
    saveError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

watch(feedbackChanged, reload)
onMounted(load)
</script>

<style scoped>
.voice-panel {
  margin-top: 8px;
  padding: 18px 22px 22px;
  background: var(--surface);
  border: 1px solid var(--line-tint);
  border-radius: 14px;
}
.voice-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line-soft);
}
.voice-status {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.voice-status button {
  padding: 6px 12px;
  border: 1px solid transparent;
  border-radius: 99px;
  background: transparent;
  color: var(--muted-strong);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.voice-status button b {
  margin-left: 3px;
  color: var(--ink-mid);
  font-family: 'DM Mono', ui-monospace, monospace;
}
.voice-status button:hover,
.voice-status button:focus-visible {
  background: #f4faf8;
  outline: none;
}
.voice-status button.active {
  border-color: #cfe6df;
  background: #e9f6f2;
  color: var(--accent-deep);
  font-weight: 600;
}
.voice-filters {
  display: flex;
  gap: 8px;
}
.voice-filters :deep(.el-select) {
  width: 130px;
}
.voice-filters :deep(.el-input) {
  width: 230px;
}
.voice-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.voice-list li {
  padding: 16px 4px 16px 16px;
  border-bottom: 1px solid var(--line-soft);
  border-left: 3px solid transparent;
}
.voice-list li:last-child {
  border-bottom: 0;
}
/* The left rule tells the kinds apart without reading the label. */
.voice-list li.kind-problem {
  border-left-color: #e0a030;
}
.voice-list li.kind-suggestion {
  border-left-color: var(--accent);
}
.voice-list li.kind-other {
  border-left-color: #c9d3d6;
}
.voice-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.voice-kind {
  flex: none;
  color: var(--muted-strong);
  font-size: 12px;
}
.voice-head strong {
  flex: 1;
  min-width: 0;
  color: var(--ink-soft);
  font-size: 15px;
  overflow-wrap: anywhere;
}
.voice-body {
  max-width: 820px;
  margin: 8px 0 10px;
  color: #33474f;
  font-size: 14px;
  line-height: 1.75;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.voice-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  color: var(--muted-soft);
  font-size: 12px;
}
.voice-meta code {
  color: var(--muted-strong);
  font:
    11.5px 'DM Mono',
    ui-monospace,
    monospace;
}
.voice-reply {
  max-width: 820px;
  margin-top: 12px;
  padding: 11px 14px;
  border-radius: 10px;
  background: #f1f8f6;
  border: 1px solid #d5ebe5;
}
.voice-reply span {
  color: #4d7a70;
  font-size: 12px;
}
.voice-reply p {
  margin: 4px 0 0;
  color: #24453d;
  font-size: 14px;
  line-height: 1.7;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.voice-handle-open {
  margin-top: 6px;
  padding-left: 0;
}
.voice-handle {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 16px;
  max-width: 820px;
  margin-top: 12px;
  padding: 14px;
  border: 1px dashed var(--line-tint);
  border-radius: 10px;
}
.voice-handle label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: var(--muted-strong);
  font-size: 12px;
}
.voice-handle label :deep(.el-select) {
  width: 140px;
}
.voice-handle label.grow {
  flex: 1;
  min-width: 260px;
}
.voice-save-error {
  flex-basis: 100%;
  margin: 0;
  color: var(--danger);
  font-size: 12.5px;
}
.voice-handle-actions {
  flex-basis: 100%;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.voice-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 46px 0 34px;
  color: var(--muted-strong);
  font-size: 13px;
  text-align: center;
}
.voice-empty strong {
  color: var(--ink-soft);
  font-size: 16px;
}
.voice-pager {
  justify-content: center;
  margin-top: 16px;
}
@media (max-width: 760px) {
  .voice-filters {
    width: 100%;
  }
  .voice-filters :deep(.el-input) {
    flex: 1;
    width: auto;
  }
  .voice-head {
    flex-wrap: wrap;
  }
}
</style>
