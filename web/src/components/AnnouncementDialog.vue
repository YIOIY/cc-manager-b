<template>
  <el-dialog v-model="open" title="发布公告" width="520px" destroy-on-close append-to-body @open="onOpen">
    <ErrorNotice :error="notice" compact closable @close="notice = null" />
    <p class="announcement-hint">所有登录账号会在页面顶部看到这条公告。清空文案并保存即可下线。</p>
    <el-form label-position="top" @submit.prevent="save">
      <el-form-item label="公告文案" :error="fieldErrors.body">
        <el-input
          v-model="draft"
          type="textarea"
          :rows="4"
          maxlength="500"
          show-word-limit
          placeholder="例如：今晚 22:00–22:30 短暂停机维护，请提前保存工作。"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="open = false">取消</el-button>
      <el-button :disabled="saving || !draft.trim()" @click="clear">清空下线</el-button>
      <el-button type="primary" :loading="saving" @click="save">发布</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, ApiError } from '../api'
import { applyFieldErrors } from '../errors'
import ErrorNotice from './ErrorNotice.vue'
import { announcementBody, loadAnnouncement, openAnnouncementEditor } from '../composables/useAnnouncement'

const open = openAnnouncementEditor
const draft = ref('')
const saving = ref(false)
const notice = ref<ApiError | Error | string | null>(null)
const fieldErrors = ref<Record<string, string>>({})

watch(open, (v) => {
  if (v) draft.value = announcementBody.value
})

async function onOpen() {
  notice.value = null
  fieldErrors.value = {}
  await loadAnnouncement()
  draft.value = announcementBody.value
}

async function put(body: string) {
  saving.value = true
  notice.value = null
  fieldErrors.value = {}
  try {
    const res = await api<{ body: string }>('/announcement', {
      method: 'PUT',
      body: JSON.stringify({ body }),
    })
    announcementBody.value = res.data.body
    open.value = false
  } catch (e) {
    if (e instanceof ApiError) {
      notice.value = e
      if (e.fields.length) fieldErrors.value = applyFieldErrors(e.fields)
    } else {
      notice.value = e instanceof Error ? e : String(e)
    }
  } finally {
    saving.value = false
  }
}

function save() {
  return put(draft.value.trim())
}

function clear() {
  draft.value = ''
  return put('')
}
</script>

<style scoped>
.announcement-hint {
  margin: 0 0 14px;
  color: var(--ink-soft);
  font-size: 13px;
  line-height: 1.5;
}
</style>
