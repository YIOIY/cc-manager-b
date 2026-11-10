<template>
  <el-config-provider :locale="zhCn">
    <LoginView v-if="checked && !authenticated" />
    <AppShell v-else-if="authenticated" />
    <div v-else class="app-booting" aria-busy="true"></div>
  </el-config-provider>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import LoginView from './views/LoginView.vue'
import AppShell from './layouts/AppShell.vue'
import { useSession } from './composables/useSession'
import { onRateLimited } from './api'
import { ElMessage } from 'element-plus'

const { authenticated, checked, check } = useSession()
onMounted(() => check().catch(() => undefined))
// One global notice for every 429: the user learns how long to wait without
// each view handling it.
onRateLimited((seconds) => {
  ElMessage.warning(seconds ? `操作过于频繁，请在 ${seconds} 秒后重试` : '操作过于频繁，请稍后再试')
})
</script>
