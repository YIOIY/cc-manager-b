<!-- OAuthSettingsPanel: named in tests because Vitest does not load the unplugin resolver. -->
<template>
  <section class="oauth-settings-panel panel" aria-label="上号调度设置">
    <div class="oauth-settings-heading">
      <span class="heading-icon small"><Gauge :size="18" /></span>
      <div class="oauth-settings-title">
        <h2>上号调度设置</h2>
        <p>控制 OAuth 上号的并发与首批请求错峰；429 与 Retry-After 仍按上游指令执行。</p>
      </div>
      <el-button v-if="can('oauth.settings')" :loading="loading" :disabled="saving" @click="openSettings">
        <Settings2 :size="15" />修改设置
      </el-button>
    </div>
    <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" show-icon />
    <div v-else-if="settings" class="oauth-settings-grid">
      <div class="oauth-settings-item">
        <span>全局并发</span><strong>{{ settings.global_concurrency }}</strong>
      </div>
      <div class="oauth-settings-item">
        <span>单出口并发</span><strong>{{ settings.exit_concurrency }}</strong>
      </div>
      <div class="oauth-settings-item">
        <span>出口间隔</span><strong>{{ settings.exchange_interval_seconds }}s</strong>
      </div>
      <div class="oauth-settings-item">
        <span>探测缓存</span><strong>{{ settings.probe_cache_seconds }}s</strong>
      </div>
      <div class="oauth-settings-item">
        <span>手动预热</span
        ><strong>{{ preheatLabel(settings.manual_preheat_seconds_min, settings.manual_preheat_seconds_max) }}</strong>
      </div>
      <div class="oauth-settings-item">
        <span>批量预热</span
        ><strong>{{ preheatLabel(settings.batch_preheat_seconds_min, settings.batch_preheat_seconds_max) }}</strong>
      </div>
      <div class="oauth-settings-item ua-pool-item">
        <span>UA 池</span><strong>{{ poolSize }} 个版本</strong>
      </div>
    </div>
  </section>
  <el-dialog
    v-if="draft"
    v-model="settingsOpen"
    title="修改上号调度设置"
    width="620px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @closed="saveError = ''"
  >
    <el-form label-position="top" :disabled="saving" @submit.prevent="save">
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" show-icon />
      <div class="oauth-settings-form-grid">
        <el-form-item label="全局并发（1–10）" required>
          <el-input-number v-model="draft!.global_concurrency" :min="1" :max="10" />
        </el-form-item>
        <el-form-item label="单出口并发（1–5）" required>
          <el-input-number v-model="draft!.exit_concurrency" :min="1" :max="5" />
        </el-form-item>
        <el-form-item label="出口请求间隔（0–60 秒）" required>
          <el-input-number v-model="draft!.exchange_interval_seconds" :min="0" :max="60" />
        </el-form-item>
        <el-form-item label="探测缓存（0–30 秒）" required>
          <el-input-number v-model="draft!.probe_cache_seconds" :min="0" :max="30" />
        </el-form-item>
      </div>
      <fieldset class="oauth-settings-group">
        <legend>手动导入预热（0–3 秒，上限 0 关闭）</legend>
        <el-form-item label="下限秒数" required>
          <el-input-number v-model="draft!.manual_preheat_seconds_min" :min="0" :max="3" />
        </el-form-item>
        <el-form-item label="上限秒数" required>
          <el-input-number v-model="draft!.manual_preheat_seconds_max" :min="0" :max="3" />
        </el-form-item>
      </fieldset>
      <fieldset class="oauth-settings-group">
        <legend>批量导入预热（0–15 秒，上限 0 关闭）</legend>
        <el-form-item label="下限秒数" required>
          <el-input-number v-model="draft!.batch_preheat_seconds_min" :min="0" :max="15" />
        </el-form-item>
        <el-form-item label="上限秒数" required>
          <el-input-number v-model="draft!.batch_preheat_seconds_max" :min="0" :max="15" />
        </el-form-item>
      </fieldset>
      <el-form-item label="UA 池（JSON 数组，最多 8 条）" required>
        <el-input v-model="draft!.ua_pool_json_text" type="textarea" :rows="5" spellcheck="false" />
      </el-form-item>
      <p class="oauth-settings-hint">
        每条一个固定 User-Agent；已有账号保留原指纹，429 与 Retry-After 逻辑不可由页面覆盖。
      </p>
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="settingsOpen = false">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="!draftDirty" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
defineOptions({ name: 'OAuthSettingsPanel' })
import { computed, onMounted, ref } from 'vue'
import { Gauge, Settings2 } from 'lucide-vue-next'
import { api, errorMessage } from '../api'
import { useSession } from '../composables/useSession'

type Settings = {
  global_concurrency: number
  exit_concurrency: number
  exchange_interval_seconds: number
  probe_cache_seconds: number
  manual_preheat_seconds_min: number
  manual_preheat_seconds_max: number
  batch_preheat_seconds_min: number
  batch_preheat_seconds_max: number
  /** The server stores and returns the pool as a JSON string (DB column mirror). */
  ua_pool_json: string
  version: number
}
type Draft = Settings & { ua_pool_json_text: string }

const emit = defineEmits<{ changed: [] }>()
const { can } = useSession()
const settings = ref<Settings>()
const draft = ref<Draft>()
const loading = ref(false)
const loadError = ref('')
const settingsOpen = ref(false)
const saving = ref(false)
const saveError = ref('')

const preheatLabel = (min: number, max: number) => (max > min ? `${min}–${max}s` : `${min}s`)
/** Decode the server's JSON-string pool; anything unparsable counts as empty. */
function parsePool(raw: unknown): string[] {
  if (Array.isArray(raw)) return raw.filter((ua): ua is string => typeof ua === 'string')
  if (typeof raw !== 'string' || !raw.trim()) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((ua): ua is string => typeof ua === 'string') : []
  } catch {
    return []
  }
}
const uaPoolIsValid = (value: string) => {
  try {
    const parsed = JSON.parse(value)
    return (
      Array.isArray(parsed) &&
      parsed.length <= 8 &&
      parsed.every((ua) => typeof ua === 'string' && ua.trim().length > 0 && ua.length <= 120)
    )
  } catch {
    return false
  }
}
const poolSize = computed(() => parsePool(settings.value?.ua_pool_json).length)
const draftDirty = computed(() => {
  const s = settings.value
  const d = draft.value
  if (!s || !d) return false
  const fields = [
    'global_concurrency',
    'exit_concurrency',
    'exchange_interval_seconds',
    'probe_cache_seconds',
    'manual_preheat_seconds_min',
    'manual_preheat_seconds_max',
    'batch_preheat_seconds_min',
    'batch_preheat_seconds_max',
  ] as const
  const poolChanged = JSON.stringify(draftPool()) !== JSON.stringify(parsePool(s.ua_pool_json))
  return poolChanged || fields.some((field) => d[field] !== s[field])
})
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    settings.value = (await api<Settings>('/oauth-settings')).data
  } catch (e) {
    loadError.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
function draftPool(): string[] {
  const raw = draft.value?.ua_pool_json_text
  if (raw == null) return parsePool(draft.value?.ua_pool_json)
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}
function openSettings() {
  if (!settings.value) return
  draft.value = {
    ...settings.value,
    ua_pool_json_text: JSON.stringify(parsePool(settings.value.ua_pool_json), null, 2),
  }
  saveError.value = ''
  settingsOpen.value = true
}
async function save() {
  if (!draft.value || saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    const body: Record<string, unknown> = { ...draft.value }
    delete body.ua_pool_json_text
    const uaPool = draftPool()
    if (!uaPoolIsValid(JSON.stringify(uaPool))) {
      saveError.value = 'UA 池必须是不超过 8 条、每条不超过 120 字符的非空字符串'
      return
    }
    // Contract of PUT /oauth-settings: the pool travels as a JSON string and the
    // optimistic-lock field is `version`, mirroring what GET returned.
    settings.value = (
      await api<Settings>('/oauth-settings', {
        method: 'PUT',
        body: JSON.stringify({ ...body, ua_pool_json: JSON.stringify(uaPool) }),
      })
    ).data
    settingsOpen.value = false
    emit('changed')
  } catch (e) {
    saveError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}
defineExpose({ load })
onMounted(load)
</script>

<style scoped>
.oauth-settings-panel {
  padding: 16px 20px 14px;
  margin-bottom: 18px;
}
.oauth-settings-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.oauth-settings-title {
  flex: 1;
  min-width: 0;
}
.oauth-settings-title h2 {
  margin: 0;
  font-size: 15px;
}
.oauth-settings-title p {
  margin: 2px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}
.oauth-settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 10px;
  margin-top: 12px;
}
.oauth-settings-item {
  padding: 10px 12px;
  border: 1px solid var(--line-1);
  border-radius: 10px;
  background: var(--card);
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.oauth-settings-item span {
  color: var(--muted);
  font-size: 11px;
}
.oauth-settings-item strong {
  font-size: 16px;
}
.oauth-settings-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.oauth-settings-group {
  margin: 8px 0 12px;
  padding: 10px 12px;
  border: 1px solid var(--line-1);
  border-radius: 10px;
}
.oauth-settings-hint {
  color: var(--muted);
  font-size: 12px;
  margin: 0;
}
.ua-pool-item {
  grid-column: 1 / -1;
}
</style>
