<template>
  <div class="login">
    <div class="login-orbit login-orbit-one" aria-hidden="true"></div>
    <div class="login-orbit login-orbit-two" aria-hidden="true"></div>
    <div class="login-card">
      <div class="brand">
        <span class="brand-mark"><KeyRound :size="15" /></span><span class="brand-word">CCMAX <b>MANAGER</b></span>
      </div>
      <div class="login-kicker">CONTROL CENTER <span></span> SECURE ACCESS</div>
      <h1>欢迎回来</h1>
      <p class="login-subtitle">登录管理工作台，查看账号库存与平台运行状态。</p>
      <el-form class="login-form" label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input v-model="form.username" autocomplete="username" placeholder="输入管理员用户名">
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
          <span>{{ busy ? '正在验证…' : '进入工作台' }}</span
          ><ArrowUpRight :size="16" />
        </el-button>
      </el-form>
      <div class="login-footer">
        <span class="login-status-dot"></span><span>受保护的管理员会话</span><span class="login-footer-sep">·</span
        ><span>CCMAX MANAGER</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { KeyRound, UsersRound, ArrowUpRight } from 'lucide-vue-next'
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
  background: radial-gradient(circle at 20% 15%, #1c4c4b 0, transparent 30%), linear-gradient(135deg, #091621, #102d3a);
  position: relative;
  overflow: hidden;
}

.login:after {
  content: '';
  position: absolute;
  inset: 12% -8% auto auto;
  width: 460px;
  height: 460px;
  border: 1px solid #75dfc22b;
  border-radius: 50%;
  box-shadow:
    0 0 0 42px #75dfc20b,
    0 0 0 84px #75dfc205;
}

.login-card {
  width: 412px;
  background: rgba(255, 255, 255, 0.96);
  border: 1px solid #ffffff66;
  border-radius: 22px;
  padding: 38px 38px 34px;
  box-shadow: 0 30px 80px #0006;
  position: relative;
  z-index: 1;
}

.login-card h1 {
  margin: 0;
  font-size: 26px;
  letter-spacing: -0.04em;
}

.login-card .el-form {
  margin-top: 24px;
}

.login-card .el-form-item__label {
  font-weight: 700;
  color: #526370;
}

.wfull {
  width: 100%;
  margin-top: 12px;
  height: 42px;
}

@media (max-width: 620px) {
  .login-card {
    width: calc(100vw - 28px);
    padding: 30px 24px;
  }
}

.login {
  isolation: isolate;
  background:
    radial-gradient(circle at 12% 14%, #164b4b 0, transparent 28%),
    radial-gradient(circle at 86% 82%, #12374a 0, transparent 30%), #071923;
}

.login:before {
  content: '';
  position: absolute;
  inset: 0;
  opacity: 0.24;
  background-image:
    linear-gradient(rgba(111, 218, 194, 0.07) 1px, transparent 1px),
    linear-gradient(90deg, rgba(111, 218, 194, 0.07) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: linear-gradient(135deg, black, transparent 70%);
}

.login:after {
  inset: auto -150px -230px auto;
  width: 620px;
  height: 620px;
  border-color: #67d8bd30;
  box-shadow:
    0 0 0 52px #67d8bd0b,
    0 0 0 105px #67d8bd06;
}

.login-orbit {
  position: absolute;
  border: 1px solid rgba(106, 218, 194, 0.18);
  border-radius: 50%;
  pointer-events: none;
  z-index: 0;
}

.login-orbit-one {
  width: 360px;
  height: 360px;
  left: -160px;
  bottom: -130px;
  box-shadow:
    0 0 0 20px rgba(106, 218, 194, 0.025),
    0 0 0 52px rgba(106, 218, 194, 0.018);
}

.login-orbit-two {
  width: 150px;
  height: 150px;
  right: 14%;
  top: 12%;
  border-color: rgba(255, 255, 255, 0.1);
}

.login-card {
  width: min(500px, calc(100vw - 32px));
  padding: 42px 46px 30px;
  border-radius: 26px;
  background: rgba(250, 252, 253, 0.97);
  border: 1px solid rgba(255, 255, 255, 0.8);
  box-shadow:
    0 34px 90px rgba(0, 0, 0, 0.34),
    0 0 0 8px rgba(143, 231, 209, 0.035);
  animation: login-card-in 0.55s cubic-bezier(0.2, 0.8, 0.2, 1) both;
}

.login-kicker {
  display: flex;
  align-items: center;
  gap: 9px;
  color: #178b77;
  font:
    700 10px/1 'DM Mono',
    monospace;
  letter-spacing: 0.16em;
}

.login-kicker span {
  width: 22px;
  height: 1px;
  background: #76cdbb;
}

.login-card h1 {
  margin: 15px 0 8px;
  font-size: 34px;
  line-height: 1.12;
  letter-spacing: -0.055em;
  color: #10283a;
}

.login-subtitle {
  margin: 0;
  color: #73818d;
  font-size: 13px;
  line-height: 1.7;
}

.login-card .el-form {
  margin-top: 29px;
}

.login-card .el-form-item {
  display: block;
  margin-bottom: 19px;
}

.login-card .el-form-item__label {
  display: block;
  width: auto !important;
  height: auto;
  line-height: 1.2;
  margin: 0 0 9px;
  padding: 0;
  color: #385363;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.02em;
}

.login-card .el-input__wrapper {
  min-height: 51px;
  padding: 1px 15px;
  border-radius: 12px !important;
  background: var(--surface) !important;
  box-shadow:
    0 0 0 1px #d7e2e6 inset,
    0 5px 16px rgba(22, 48, 58, 0.035) !important;
  transition:
    box-shadow 0.2s ease,
    transform 0.2s ease;
}

.login-card .el-input__wrapper:hover {
  box-shadow:
    0 0 0 1px #b9d6d3 inset,
    0 5px 16px rgba(22, 48, 58, 0.06) !important;
}

.login-card .el-input__wrapper.is-focus {
  box-shadow:
    0 0 0 2px #8ed7c7 inset,
    0 7px 18px rgba(24, 168, 137, 0.1) !important;
  transform: translateY(-1px);
}

.login-card .el-input__prefix {
  color: #71909a;
  margin-right: 8px;
}

.login-card .el-input__inner {
  color: #193548;
  font-size: 14px;
}

.login-card .el-input__inner::placeholder {
  color: #a2afb5;
}

.login-card .el-alert {
  margin: 2px 0 15px;
  padding: 12px 14px;
  border: 1px solid #f4d8d5;
  background: #fff4f2;
  color: #bf5a57;
}

.login-card .wfull {
  height: 51px;
  margin-top: 5px;
  border-radius: 12px;
  font-size: 14px;
  letter-spacing: 0.02em;
  box-shadow: 0 9px 18px rgba(18, 154, 127, 0.22);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.login-card .wfull svg {
  transition: transform 0.2s ease;
}

.login-card .wfull:hover svg {
  transform: translate(2px, -2px);
}

.login-footer {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  margin-top: 24px;
  color: #93a0a6;
  font:
    10px/1 'DM Mono',
    monospace;
  letter-spacing: 0.04em;
}

.login-footer-sep {
  color: #c1cace;
}

.login-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #20ae8e;
  box-shadow: 0 0 0 4px #dff5ee;
}

@media (max-width: 620px) {
  .login-card {
    padding: 31px 24px 25px;
    border-radius: 22px;
  }
  .login-card h1 {
    font-size: 30px;
  }
  .login-orbit-two {
    right: -55px;
    top: 8%;
  }
  .login-footer {
    font-size: 9px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .login-card {
    animation: none;
  }
}

.login-card .login-form {
  display: grid;
  gap: 0;
  margin-top: 29px;
}

.login-card .login-form .el-form-item {
  display: block;
  margin: 0 0 18px;
}

.login-card .login-form .el-form-item__label {
  display: block !important;
  width: 100% !important;
  height: auto !important;
  margin: 0 0 8px;
  padding: 0 !important;
  text-align: left !important;
  line-height: 1.25 !important;
  color: #385363;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.02em;
}

.login-card .login-form .el-form-item__content {
  display: block;
  margin-left: 0 !important;
  line-height: 1.25;
}

.login-card .login-form .el-input {
  width: 100%;
}

.login-card .login-form .el-alert {
  margin: 1px 0 15px;
}

.login-card .login-form .wfull {
  margin-top: 0;
}

@media (max-width: 620px) {
  .login-card .login-form {
    margin-top: 24px;
  }
  .login-card .login-form .el-form-item {
    margin-bottom: 15px;
  }
}
</style>
