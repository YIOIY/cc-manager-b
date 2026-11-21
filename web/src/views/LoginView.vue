<template>
  <div class="login">
    <div class="login-panel">
      <div class="login-card">
        <div class="brand">
          <span class="brand-mark"><KeyRound :size="15" /></span><span class="brand-word">CCMAX <b>MANAGER</b></span>
        </div>
        <h1>登录</h1>
        <p class="login-subtitle">使用管理员账号进入控制台</p>
        <el-form class="login-form" label-position="top" @submit.prevent="submit">
          <el-form-item label="账号">
            <el-input v-model="form.username" autocomplete="username" placeholder="输入管理员账号">
              <template #prefix><UsersRound :size="16" /></template>
            </el-input>
          </el-form-item>
          <el-form-item label="密码">
            <el-input
              v-model="form.password"
              type="password"
              show-password
              autocomplete="current-password"
              placeholder="输入登录密码"
            >
              <template #prefix><KeyRound :size="16" /></template>
            </el-input>
          </el-form-item>
          <el-alert v-if="error" :title="error" type="error" :closable="false" />
          <el-button native-type="submit" type="primary" class="wfull" :loading="busy">
            <span>{{ busy ? '正在验证…' : '登录' }}</span>
          </el-button>
        </el-form>
        <div class="login-footer">CCMAX MANAGER</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { KeyRound, UsersRound } from 'lucide-vue-next'
import { useRoute, useRouter } from 'vue-router'
import { useSession } from '../composables/useSession'
import { firstAllowedRoute } from '../router'
import { errorMessage } from '../api'

const { login, can } = useSession()
const route = useRoute()
const router = useRouter()
const form = reactive({ username: '', password: '' })
const busy = ref(false)
const error = ref('')

async function submit() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await login(form.username.trim(), form.password)
    form.password = ''
    const target = firstAllowedRoute()
    if (route.meta.permission && !can(route.meta.permission)) await router.replace({ name: target })
  } catch (e) {
    error.value = errorMessage(e, '登录失败')
  } finally {
    busy.value = false
  }
}
</script>

<style>
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  background: #f5f6f8;
  position: relative;
}

.login-panel {
  width: min(420px, calc(100vw - 32px));
  position: relative;
  z-index: 1;
}

.login-card {
  background: #fff;
  border: 1px solid #e5e6e8;
  border-radius: 12px;
  padding: 36px 32px 28px;
  box-shadow: 0 4px 14px rgba(28, 31, 35, 0.06);
  animation: login-card-in 0.35s ease both;
}

.login-card h1 {
  margin: 8px 0 6px;
  font-size: 24px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: #1c1f23;
}

.login-subtitle {
  margin: 0 0 24px;
  color: #6b7075;
  font-size: 13px;
  line-height: 1.6;
}

.login-card .login-form {
  display: grid;
  gap: 0;
}

.login-card .login-form .el-form-item {
  display: block;
  margin: 0 0 16px;
}

.login-card .login-form .el-form-item__label {
  display: block !important;
  width: 100% !important;
  height: auto !important;
  margin: 0 0 6px;
  padding: 0 !important;
  text-align: left !important;
  line-height: 1.25 !important;
  color: #1c1f23;
  font-size: 13px;
  font-weight: 600;
}

.login-card .login-form .el-form-item__content {
  display: block;
  margin-left: 0 !important;
}

.login-card .login-form .el-input {
  width: 100%;
}

.login-card .el-input__wrapper {
  min-height: 40px;
  padding: 1px 12px;
  border-radius: 6px !important;
  background: #fff !important;
  box-shadow: 0 0 0 1px #e5e6e8 inset !important;
}

.login-card .el-input__wrapper:hover {
  box-shadow: 0 0 0 1px #c8c9cc inset !important;
}

.login-card .el-input__wrapper.is-focus {
  box-shadow: 0 0 0 1px #0077fa inset !important;
}

.login-card .el-input__prefix {
  color: #888d92;
  margin-right: 6px;
}

.login-card .el-input__inner {
  color: #1c1f23;
  font-size: 14px;
}

.login-card .el-alert {
  margin: 0 0 12px;
}

.login-card .wfull {
  width: 100%;
  height: 40px;
  margin-top: 4px;
  border-radius: 6px;
  font-size: 14px;
  font-weight: 600;
}

.login-footer {
  margin-top: 20px;
  text-align: center;
  color: #888d92;
  font-size: 12px;
}

@media (max-width: 620px) {
  .login-card {
    padding: 28px 20px 22px;
  }
  .login-card h1 {
    font-size: 22px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .login-card {
    animation: none;
  }
}
</style>
