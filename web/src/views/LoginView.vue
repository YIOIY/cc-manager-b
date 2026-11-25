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
          <el-form-item label="验证码">
            <div class="login-captcha">
              <el-input
                v-model="form.captcha"
                autocomplete="off"
                maxlength="8"
                placeholder="输入图形验证码"
                @keyup.enter="submit"
              >
                <template #prefix><ShieldCheck :size="16" /></template>
              </el-input>
              <button
                type="button"
                class="login-captcha-image"
                :disabled="captchaLoading"
                title="点击刷新验证码"
                aria-label="点击刷新验证码"
                @click="refreshCaptcha"
              >
                <img v-if="captchaImage" :src="captchaImage" alt="图形验证码" />
                <span v-else>{{ captchaLoading ? '加载中…' : '点击获取' }}</span>
              </button>
            </div>
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
import { onMounted, reactive, ref } from 'vue'
import { KeyRound, ShieldCheck, UsersRound } from 'lucide-vue-next'
import { useRoute, useRouter } from 'vue-router'
import { api, errorMessage } from '../api'
import { useSession } from '../composables/useSession'
import { firstAllowedRoute } from '../router'

const { login, can } = useSession()
const route = useRoute()
const router = useRouter()
const form = reactive({ username: '', password: '', captcha: '' })
const busy = ref(false)
const error = ref('')
const captchaId = ref('')
const captchaImage = ref('')
const captchaLoading = ref(false)

async function refreshCaptcha() {
  if (captchaLoading.value) return
  captchaLoading.value = true
  try {
    const r = await api<{ captcha_id: string; image: string }>('/auth/captcha')
    captchaId.value = r.data.captcha_id
    captchaImage.value = r.data.image
    form.captcha = ''
  } catch (e) {
    error.value = errorMessage(e, '验证码加载失败')
  } finally {
    captchaLoading.value = false
  }
}

async function submit() {
  if (busy.value) return
  if (!form.captcha.trim() || !captchaId.value) {
    error.value = '请输入验证码'
    if (!captchaId.value) await refreshCaptcha()
    return
  }
  busy.value = true
  error.value = ''
  try {
    await login(form.username.trim(), form.password, captchaId.value, form.captcha.trim())
    form.password = ''
    form.captcha = ''
    const target = firstAllowedRoute()
    if (route.meta.permission && !can(route.meta.permission)) await router.replace({ name: target })
  } catch (e) {
    error.value = errorMessage(e, '登录失败')
    await refreshCaptcha()
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  void refreshCaptcha()
})
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

.login-captcha {
  display: grid;
  grid-template-columns: 1fr 140px;
  gap: 10px;
  align-items: stretch;
}

.login-captcha-image {
  height: 40px;
  margin: 0;
  padding: 0;
  border: 1px solid #e5e6e8;
  border-radius: 6px;
  background: #f5f6f8;
  cursor: pointer;
  overflow: hidden;
  display: grid;
  place-items: center;
  color: #6b7075;
  font-size: 12px;
}

.login-captcha-image:hover:not(:disabled) {
  border-color: #c8c9cc;
}

.login-captcha-image:disabled {
  cursor: wait;
  opacity: 0.75;
}

.login-captcha-image img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
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
  .login-captcha {
    grid-template-columns: 1fr 120px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .login-card {
    animation: none;
  }
}
</style>
