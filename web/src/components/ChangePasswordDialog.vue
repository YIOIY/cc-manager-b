<template>
  <el-dialog
    :model-value="modelValue"
    title="修改密码"
    width="440px"
    :close-on-click-modal="!busy"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form label-position="top" :disabled="busy" @submit.prevent="submit">
      <el-form-item label="当前密码" required>
        <el-input v-model="current" type="password" show-password autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="新密码" required :error="mismatch">
        <el-input v-model="next" type="password" show-password autocomplete="new-password" placeholder="至少 12 位" />
      </el-form-item>
      <el-form-item label="再次输入新密码" required>
        <el-input v-model="confirm" type="password" show-password autocomplete="new-password" />
      </el-form-item>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <p class="field-hint">修改后其他设备上的登录会失效，当前会话保持。</p>
    </el-form>
    <template #footer>
      <el-button :disabled="busy" @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="busy" :disabled="!ready" @click="submit">修改密码</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useSession } from '../composables/useSession'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; changed: [] }>()
const { changePassword, errorMessage } = useSession()
const current = ref('')
const next = ref('')
const confirm = ref('')
const busy = ref(false)
const error = ref('')
const mismatch = computed(() => (next.value && next.value.length < 12 ? '新密码至少 12 位' : ''))
const ready = computed(() => !!current.value && next.value.length >= 12 && next.value === confirm.value)

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      current.value = next.value = confirm.value = ''
      error.value = ''
    }
  },
)
async function submit() {
  if (!ready.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await changePassword(current.value, next.value)
    emit('update:modelValue', false)
    emit('changed')
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>
