<template>
  <el-dialog
    :model-value="modelValue"
    title="JSON 批量导入账号"
    width="920px"
    class="import-dialog"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="import-dialog-intro">
      <div class="heading-icon"><FileJson :size="20" /></div>
      <div>
        <strong>批量导入账号与鉴权信息</strong>
        <p>单批最多 100 条，支持 password 和 auth_json；敏感字段会加密保存，预览只显示是否已配置。</p>
      </div>
    </div>
    <div class="import-layout">
      <div class="import-editor">
        <div class="import-editor-head">
          <label for="import-json">账号 JSON</label><span class="import-shortcut">Ctrl / ⌘ + Enter 校验</span>
        </div>
        <div class="json-toolbar" role="toolbar" aria-label="JSON 编辑工具">
          <el-button text size="small" :disabled="!text.trim()" @click="format">格式化</el-button>
          <el-button text size="small" :disabled="!text.trim()" @click="minify">压缩</el-button>
          <span class="toolbar-divider"></span>
          <el-button text size="small" :disabled="!text.trim()" @click="validate">校验</el-button>
          <el-button text size="small" :disabled="!text.trim()" @click="copy"
            ><Copy :size="14" />{{ copied ? '已复制' : '复制' }}</el-button
          >
          <el-button text size="small" class="danger-action" :disabled="!text.trim()" @click="clear"
            ><Trash2 :size="14" />清空</el-button
          >
        </div>
        <el-input
          id="import-json"
          v-model="text"
          class="json-input"
          :class="{ 'is-invalid': !!parseError }"
          type="textarea"
          :rows="16"
          spellcheck="false"
          autocomplete="off"
          placeholder="粘贴 JSON 对象或数组，需包含 account、password、auth_json"
          @keydown.ctrl.enter.prevent="preview"
          @keydown.meta.enter.prevent="preview"
        />
        <div class="json-status" :class="{ 'is-error': parseError, 'is-success': jsonValid && !parseError }">
          <span v-if="parseError">{{ parseError }}</span>
          <span v-else-if="jsonValid"><CheckCircle2 :size="14" />格式正确 · {{ jsonCount }} 条记录</span>
          <span v-else>等待输入 JSON · 支持对象或数组</span>
          <span v-if="text.trim()" class="json-stats">{{ lineCount }} 行 · {{ text.length }} 字符</span>
        </div>
        <div class="card-actions">
          <el-button type="primary" :loading="busy" :disabled="!text.trim() || !!parseError" @click="preview"
            >校验并预览</el-button
          >
          <el-button v-if="importId" :loading="busy" @click="commit">确认导入 {{ validCount }} 条</el-button>
        </div>
      </div>
      <div class="import-reference">
        <div class="reference-title">
          <span>格式示例</span>
          <div class="reference-actions">
            <el-button text size="small" @click="fillExample">填入示例</el-button>
            <el-button text size="small" :disabled="!text.trim()" @click="clear">清空输入</el-button>
          </div>
        </div>
        <pre>{{ importExample }}</pre>
        <p>auth_json 需包含 Claude 所需字段；id_token 可为空字符串，其余字段必须有效。真实 token 不会在预览中回显。</p>
      </div>
    </div>
    <div v-if="rows.length" class="import-results">
      <strong>校验结果 · {{ validCount }} 条可导入</strong>
      <div v-for="row in rows" :key="row.row_index">
        <span>
          第 {{ row.row_index }} 行 · {{ row.masked_identity }}
          <em v-if="row.password_configured" class="import-mark">密码已配置</em
          ><em v-if="row.auth_configured" class="import-mark auth">鉴权已配置</em>
        </span>
        <el-tag
          :type="row.issues.length ? 'danger' : 'success'"
          :title="row.issues.map((issue) => issue.code).join(' ')"
          >{{ row.issues.length ? row.issues.map((issue) => issue.message).join('、') : '通过' }}</el-tag
        >
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, ref, watch } from 'vue'
import { FileJson, Copy, Trash2, CheckCircle2 } from 'lucide-vue-next'
import { api, errorMessage } from '../api'
import type { ImportPreview, ImportResult, ImportRow } from '../types/api'

defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  feedback: [message: string, type: 'success' | 'info' | 'warning' | 'error']
  imported: [result: ImportResult]
}>()

const text = ref('')
const importId = ref('')
const rows = ref<ImportRow[]>([])
const validCount = ref(0)
const busy = ref(false)
const parseError = ref('')
const jsonValid = ref(false)
const jsonCount = ref(0)
const copied = ref(false)
const lineCount = computed(() => (text.value ? text.value.split('\n').length : 0))

const importExample = JSON.stringify(
  [
    {
      account: 'name@example.com',
      password: 'your-password',
      plan: 'claude/cc-max-20x',
      auth_json: {
        id_token: '',
        access_token: 'access-token',
        refresh_token: 'refresh-token',
        last_refresh: '2026-11-04T11:12:55.123Z',
        email: 'name@example.com',
        account_uuid: 'account-uuid',
        organization_uuid: 'organization-uuid',
        organization_name: 'Example Organization',
        claude_device_ids: ['device-id'],
        type: 'claude',
        expired: '2026-12-04T11:12:55.123Z',
        proxy_url: 'http://user:password@127.0.0.1:8080',
        verification_status: 'not_required',
      },
      register_location: 'US',
      register_time: '2026-11-04',
      plan_start_time: '2026-11-04',
      plan_end_time: '2026-12-04',
      payment_method: 'virtual_credit_card',
      register_device: 'Windows',
      balance: 86,
      recharge_time: '2026-11-04',
      warranty_period: 30,
      tags: ['速刷coding'],
      remark: '示例备注',
    },
  ],
  null,
  2,
)

function parse(): unknown | null {
  if (!text.value.trim()) {
    parseError.value = '请输入 JSON 内容'
    jsonValid.value = false
    jsonCount.value = 0
    return null
  }
  try {
    const value: unknown = JSON.parse(text.value)
    if (!value || typeof value !== 'object') throw new Error('顶层必须是对象或数组')
    if (Array.isArray(value) && !value.length) throw new Error('数组不能为空')
    parseError.value = ''
    jsonValid.value = true
    jsonCount.value = Array.isArray(value) ? value.length : 1
    return value
  } catch (error) {
    const message = error instanceof Error ? error.message : 'JSON 格式错误'
    const match = message.match(/position (\d+)/i)
    let location = ''
    if (match) {
      const position = Number(match[1])
      const before = text.value.slice(0, position)
      location = `（第 ${before.split('\n').length} 行，第 ${position - before.lastIndexOf('\n')} 列）`
    }
    parseError.value = `JSON 格式错误${location}：${message.replace(/ in JSON at position \d+/i, '')}`
    jsonValid.value = false
    jsonCount.value = 0
    return null
  }
}
function format() {
  const value = parse()
  if (value !== null) text.value = JSON.stringify(value, null, 2)
}
function minify() {
  const value = parse()
  if (value !== null) text.value = JSON.stringify(value)
}
function validate() {
  parse()
  if (!parseError.value) emit('feedback', `JSON 格式正确，共 ${jsonCount.value} 条记录`, 'success')
}
async function copy() {
  if (!text.value.trim()) return
  if (await copyText(text.value)) {
    copied.value = true
    window.setTimeout(() => (copied.value = false), 1600)
    emit('feedback', 'JSON 已复制到剪贴板', 'success')
  } else {
    emit('feedback', '复制失败，请选中文本手动复制', 'warning')
  }
}
function clear() {
  text.value = ''
  parseError.value = ''
  jsonValid.value = false
  jsonCount.value = 0
  importId.value = ''
  rows.value = []
}
function fillExample() {
  if (text.value.trim()) {
    emit('feedback', '编辑区已有内容，请先清空后再填入示例', 'warning')
    return
  }
  text.value = importExample
}
async function preview() {
  if (!parse()) return
  busy.value = true
  try {
    const d = (
      await api<ImportPreview>('/imports/preview', {
        method: 'POST',
        body: JSON.stringify({ format: 'accounts', content: text.value }),
      })
    ).data
    importId.value = d.valid_count ? d.import_id : ''
    validCount.value = d.valid_count
    rows.value = d.rows || []
  } catch (e) {
    emit('feedback', errorMessage(e), 'error')
  } finally {
    busy.value = false
  }
}
async function commit() {
  busy.value = true
  try {
    const d = (
      await api<ImportResult>('/imports/' + importId.value + '/commit', {
        method: 'POST',
        body: JSON.stringify({ strategy: 'skip_existing' }),
      })
    ).data
    clear()
    emit('update:modelValue', false)
    emit('imported', d)
  } catch (e) {
    emit('feedback', errorMessage(e), 'error')
  } finally {
    busy.value = false
  }
}
watch(text, () => {
  importId.value = ''
  rows.value = []
  if (text.value.trim()) parse()
  else {
    parseError.value = ''
    jsonValid.value = false
    jsonCount.value = 0
  }
})
</script>

<style>
.card-actions {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}

.import-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(250px, 0.8fr);
  gap: 16px;
}

.import-editor > label {
  display: block;
  font-size: 11px;
  font-weight: 700;
  color: #566b76;
  margin-bottom: 7px;
}

.import-editor textarea {
  font:
    12px/1.7 'ui-monospace',
    monospace;
  background: var(--surface-soft);
}

.import-reference {
  padding: 13px 14px;
  border: 1px solid #d1e8e1;
  border-radius: 7px;
  background: #f5f6f8;
}

.reference-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: #0062d6;
  font-size: 11px;
  font-weight: 700;
}

.reference-title .el-button {
  padding: 0;
  height: auto;
}

.import-reference pre {
  margin: 10px 0;
  white-space: pre-wrap;
  word-break: break-word;
  color: #315563;
  font:
    10px/1.55 'ui-monospace',
    monospace;
}

.import-reference p {
  margin: 0;
  color: #85949a;
  font-size: 10px;
  line-height: 1.6;
}

.import-results {
  border-top: 1px solid #f0f1f2;
  padding-top: 13px;
  margin-top: 16px;
}

.import-results > strong {
  display: block;
  font-size: 11px;
  margin-bottom: 8px;
}

.import-results > div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 0;
  border-bottom: 1px solid #f0f1f2;
  font-size: 11px;
  color: #5a6d77;
}

@media (max-width: 980px) {
  .import-layout {
    grid-template-columns: 1fr;
  }
}

.import-mark {
  display: inline-block;
  margin-left: 7px;
  padding: 3px 6px;
  border-radius: 5px;
  background: #d6eaff;
  color: var(--accent-deep);
  font: 10px/1 monospace;
  font-style: normal;
}

.import-mark.auth {
  background: #edf3fb;
  color: #3d72a2;
}

.import-dialog .el-dialog__header {
  padding: 20px 24px 15px;
  background: linear-gradient(135deg, var(--tint-50), var(--surface));
}

.import-dialog .el-dialog__body {
  padding: 20px 24px 24px;
  background: var(--surface-soft);
}

.import-dialog .el-dialog__footer {
  padding: 0;
}

.import-dialog-intro {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 18px;
}

.import-dialog-intro strong {
  color: #1c1f23;
  font-size: 15px;
}

.import-dialog-intro p {
  margin: 5px 0 0;
  color: #84949b;
  font-size: 11px;
}

.import-dialog .import-layout {
  grid-template-columns: minmax(0, 1.2fr) minmax(280px, 0.8fr);
}

.import-dialog .import-editor textarea {
  min-height: 300px;
  border-radius: 10px;
}

.import-dialog .import-reference {
  background: var(--tint-50);
}

.import-dialog .import-results {
  margin-top: 18px;
  padding: 14px;
  border: 1px solid #e1eeeb;
  border-radius: 10px;
  background: var(--surface);
}

.import-dialog .import-results > div {
  padding: 9px 0;
}

.import-dialog .card-actions {
  margin-top: 12px;
}

@media (max-width: 620px) {
  .import-dialog .el-dialog__body {
    padding: 16px;
  }
  .import-dialog .import-layout {
    grid-template-columns: 1fr;
  }
  .import-dialog .import-editor textarea {
    min-height: 220px;
  }
}

.import-editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 7px;
}

.import-editor-head label {
  margin: 0 !important;
}

.import-shortcut {
  color: #94a2a8;
  font:
    10px/1 'ui-monospace',
    monospace;
}

.json-toolbar {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 6px 8px;
  border: 1px solid var(--line-tint);
  border-bottom: 0;
  border-radius: 10px 10px 0 0;
  background: #f5f6f8;
}

.json-toolbar .el-button {
  height: 27px;
  padding: 0 8px;
  color: #56717a;
  font-size: 11px;
  font-weight: 700;
}

.json-toolbar .el-button:hover {
  color: var(--accent-deep);
  background: #d6eaff;
}

.json-toolbar .el-button.is-disabled {
  color: #b1bdc1;
}

.json-toolbar .el-button svg {
  margin-right: 4px;
}

.toolbar-divider {
  height: 18px;
  width: 1px;
  margin: 0 5px;
  background: var(--line-tint);
}

.json-toolbar .danger-action:hover {
  color: #c65d5d;
  background: #fff0ef;
}

.json-input .el-textarea__inner {
  min-height: 304px !important;
  border-radius: 0 !important;
  border-color: var(--line-tint) !important;
  background: #fafbfc !important;
  padding: 13px 14px !important;
  color: #1c1f23;
  resize: vertical;
}

.json-input.is-invalid .el-textarea__inner {
  border-color: #e59b9b !important;
  background: #fffafa !important;
  box-shadow: 0 0 0 1px #f0c8c8 inset !important;
}

.json-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 31px;
  padding: 7px 10px;
  border: 1px solid var(--line-tint);
  border-top: 0;
  border-radius: 0 0 10px 10px;
  background: #f5f6f8;
  color: #819197;
  font-size: 10px;
}

.json-status.is-error {
  color: #c45c5c;
  background: #fff4f3;
  border-color: #f0cccc;
}

.json-status.is-success {
  color: #0062d6;
  background: #eaf5ff;
}

.json-status > span:first-child {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.json-stats {
  margin-left: auto;
  color: var(--muted-soft);
  font:
    10px 'ui-monospace',
    monospace;
  white-space: nowrap;
}

.reference-actions {
  display: flex;
  gap: 2px;
}

.import-reference pre {
  max-height: 390px;
  overflow: auto;
  margin: 14px 0 12px;
  padding: 13px;
  border: 1px solid #e0eeea;
  border-radius: 9px;
  background: var(--surface);
  color: #45616b;
  font:
    11px/1.65 'ui-monospace',
    monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

.import-reference p {
  line-height: 1.65;
}

@media (max-width: 620px) {
  .import-editor-head {
    align-items: flex-start;
    flex-direction: column;
    gap: 5px;
  }
  .json-toolbar {
    flex-wrap: wrap;
  }
  .toolbar-divider {
    display: none;
  }
  .json-input .el-textarea__inner {
    min-height: 230px !important;
  }
  .json-status {
    align-items: flex-start;
    flex-direction: column;
    gap: 4px;
  }
  .json-stats {
    margin-left: 0;
  }
  .reference-actions {
    flex-wrap: wrap;
  }
  .import-dialog .card-actions {
    flex-wrap: wrap;
  }
  .import-dialog .card-actions .el-button {
    flex: 1;
    min-width: 140px;
  }
}
</style>
