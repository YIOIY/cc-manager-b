<template>
  <PageHead
    eyebrow="SYSTEM"
    title="系统设置"
    description="全局上号限额与可上号时段。对所有用户生效；用户管理里还可单独设每日上限。"
  >
    <el-button :loading="loading" :disabled="saving" @click="load"><RefreshCw :size="15" />刷新</el-button>
    <el-button v-if="canEdit" type="primary" :loading="saving" :disabled="!dirty || loading || !draft" @click="save"
      >保存</el-button
    >
  </PageHead>
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />

  <section v-loading="loading" class="sys-panel panel" aria-label="上号限制">
    <div class="sys-heading">
      <span class="heading-icon small"><SlidersHorizontal :size="18" /></span>
      <div class="sys-title">
        <h2>上号限制</h2>
        <p>按北京时间自然日生效；时段外与超限时，新建、授权、上号与导入都会被拒绝。</p>
      </div>
      <span v-if="dirty" class="sys-dirty">有未保存修改</span>
    </div>

    <div v-if="saved" class="sys-summary">
      <div class="sys-summary-item">
        <span>每日上限</span>
        <strong>{{ saved.daily_onboard_max > 0 ? saved.daily_onboard_max + ' 个' : '不限制' }}</strong>
      </div>
      <div class="sys-summary-item">
        <span>可上号时段</span>
        <strong>{{
          saved.onboard_window_start && saved.onboard_window_end
            ? saved.onboard_window_start + ' – ' + saved.onboard_window_end
            : '全天'
        }}</strong>
      </div>
      <div class="sys-summary-item">
        <span>时区</span>
        <strong>Asia/Shanghai</strong>
      </div>
    </div>

    <el-form v-if="draft" class="sys-form" label-position="top" :disabled="!canEdit || saving">
      <div class="sys-blocks">
        <fieldset class="sys-block">
          <legend>每日最大可上号个数</legend>
          <div class="sys-block-body">
            <el-input-number
              v-model="draft.daily_onboard_max"
              :min="0"
              :max="100000"
              :step="1"
              controls-position="right"
            />
            <p class="sys-hint">0 表示不限制。统计全站「提交上号」次数（`account.submit`）。</p>
          </div>
        </fieldset>

        <fieldset class="sys-block">
          <legend>每日可上号时间</legend>
          <div class="sys-block-body">
            <div class="sys-window-mode" role="group" aria-label="时段模式">
              <button
                type="button"
                class="sys-mode"
                :class="{ 'is-active': !windowEnabled }"
                :disabled="!canEdit || saving"
                @click="windowEnabled = false"
              >
                <strong>全天</strong>
                <small>不限制时段</small>
              </button>
              <button
                type="button"
                class="sys-mode"
                :class="{ 'is-active': windowEnabled }"
                :disabled="!canEdit || saving"
                @click="windowEnabled = true"
              >
                <strong>限制时段</strong>
                <small>仅允许窗口内上号</small>
              </button>
            </div>

            <div v-if="windowEnabled" class="sys-window-pickers">
              <el-form-item label="开始" class="sys-time-item">
                <el-time-select
                  v-model="draft.onboard_window_start"
                  start="00:00"
                  step="00:30"
                  end="23:30"
                  placeholder="开始"
                />
              </el-form-item>
              <span class="sys-sep" aria-hidden="true">至</span>
              <el-form-item label="结束" class="sys-time-item">
                <el-time-select
                  v-model="draft.onboard_window_end"
                  start="00:00"
                  step="00:30"
                  end="23:30"
                  placeholder="结束"
                />
              </el-form-item>
            </div>
            <p class="sys-hint">开始晚于结束时视为跨夜（例如 22:00–06:00）。修改后须点右上角「保存」才生效。</p>
          </div>
        </fieldset>
      </div>
    </el-form>

    <p v-if="!canEdit" class="sys-readonly">当前账号只能查看，修改需要「系统设置」管理权限。</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RefreshCw, SlidersHorizontal } from 'lucide-vue-next'
import PageHead from '../components/PageHead.vue'
import { api, errorMessage } from '../api'
import { useSession } from '../composables/useSession'
import type { SystemSettings } from '../types/api'

const { can } = useSession()
const canEdit = computed(() => can('settings.manage'))

const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref<SystemSettings | null>(null)
const draft = ref<SystemSettings | null>(null)
const windowEnabled = ref(false)

const dirty = computed(() => {
  if (!saved.value || !draft.value) return false
  const s = saved.value
  const d = draft.value
  return (
    s.daily_onboard_max !== d.daily_onboard_max ||
    s.onboard_window_start !== d.onboard_window_start ||
    s.onboard_window_end !== d.onboard_window_end
  )
})

watch(windowEnabled, (on) => {
  if (!draft.value) return
  if (!on) {
    draft.value.onboard_window_start = ''
    draft.value.onboard_window_end = ''
    return
  }
  if (!draft.value.onboard_window_start) draft.value.onboard_window_start = '09:00'
  if (!draft.value.onboard_window_end) draft.value.onboard_window_end = '18:00'
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api<SystemSettings>('/system-settings')
    saved.value = { ...res.data }
    draft.value = { ...res.data }
    windowEnabled.value = !!(res.data.onboard_window_start && res.data.onboard_window_end)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value || !canEdit.value || saving.value) return
  if (windowEnabled.value && (!draft.value.onboard_window_start || !draft.value.onboard_window_end)) {
    error.value = '请填写完整的可上号开始与结束时间，或改为全天'
    return
  }
  saving.value = true
  error.value = ''
  try {
    const payload: SystemSettings = {
      ...draft.value,
      onboard_window_start: windowEnabled.value ? draft.value.onboard_window_start : '',
      onboard_window_end: windowEnabled.value ? draft.value.onboard_window_end : '',
    }
    const res = await api<SystemSettings>('/system-settings', {
      method: 'PUT',
      body: JSON.stringify(payload),
    })
    saved.value = { ...res.data }
    draft.value = { ...res.data }
    windowEnabled.value = !!(res.data.onboard_window_start && res.data.onboard_window_end)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.sys-panel {
  padding: 18px 20px 20px;
  max-width: 720px;
}
.sys-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.sys-title {
  flex: 1;
  min-width: 0;
}
.sys-title h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--ink);
}
.sys-title p {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}
.sys-dirty {
  flex: none;
  padding: 3px 8px;
  border-radius: 6px;
  background: #fff6e5;
  color: #a56a12;
  font-size: 12px;
  font-weight: 600;
}
.sys-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-top: 14px;
}
.sys-summary-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: #fafbfc;
}
.sys-summary-item span {
  color: var(--muted);
  font-size: 11px;
}
.sys-summary-item strong {
  font-size: 16px;
  font-weight: 700;
  color: var(--ink);
  letter-spacing: 0.01em;
}
.sys-form {
  margin-top: 16px;
}
.sys-blocks {
  display: grid;
  gap: 12px;
}
.sys-block {
  margin: 0;
  padding: 0;
  border: 1px solid var(--line);
  border-radius: 10px;
  min-width: 0;
}
.sys-block legend {
  margin-left: 12px;
  padding: 0 6px;
  font-size: 13px;
  font-weight: 700;
  color: var(--ink);
}
.sys-block-body {
  padding: 12px 14px 14px;
}
.sys-hint {
  margin: 10px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}
.sys-window-mode {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}
.sys-mode {
  display: grid;
  gap: 2px;
  padding: 10px 12px;
  text-align: left;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: var(--surface);
  font: inherit;
  cursor: pointer;
  transition:
    border-color 0.15s,
    background 0.15s;
}
.sys-mode:hover:not(:disabled) {
  border-color: #b9c3d2;
}
.sys-mode:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.sys-mode.is-active {
  border-color: var(--accent);
  background: var(--tint-50, #eef6ff);
}
.sys-mode strong {
  font-size: 13px;
  color: var(--ink);
}
.sys-mode small {
  font-size: 11.5px;
  color: var(--muted);
}
.sys-window-pickers {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 10px 12px;
  margin-top: 12px;
}
.sys-time-item {
  margin-bottom: 0;
  flex: 1 1 140px;
  max-width: 200px;
}
.sys-sep {
  color: var(--muted);
  font-size: 13px;
  padding-bottom: 8px;
}
.sys-readonly {
  margin: 14px 0 0;
  color: var(--muted);
  font-size: 12px;
}
@media (max-width: 700px) {
  .sys-summary {
    grid-template-columns: 1fr;
  }
  .sys-window-mode {
    grid-template-columns: 1fr;
  }
}
</style>
