<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ApiError, adminLogin } from '../api'
import { setToken } from '../auth'

// 管理员登录（安全设计 §1）：密码通过后，绑定两步验证者补 6 位 TOTP 或恢复码
// ——后端回机器码 totp_required/totp_invalid 驱动本页出验证码输入位。
const route = useRoute()
const router = useRouter()
const form = reactive({ username: '', password: '', totp: '' })
const needTotp = ref(false)
const loading = ref(false)

async function submit() {
  if (!form.username.trim() || !form.password) {
    ElMessage.warning('请输入用户名与密码')
    return
  }
  if (needTotp.value && !form.totp.trim()) {
    ElMessage.warning('请输入两步验证码')
    return
  }
  loading.value = true
  try {
    const r = await adminLogin({
      username: form.username.trim(),
      password: form.password,
      ...(needTotp.value ? { totp: form.totp.trim() } : {}),
    })
    setToken(r.token)
    const redirect = String(route.query.redirect ?? '')
    router.push(redirect.startsWith('/') ? redirect : '/nodes')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'totp_required') {
      needTotp.value = true
      ElMessage.warning('该账号已开启两步验证，请输入验证码')
    } else if (e instanceof ApiError && e.code === 'totp_invalid') {
      ElMessage.error('验证码错误（可用绑定时保存的恢复码代替）')
    } else {
      ElMessage.error(e instanceof ApiError ? e.message : '登录失败，请稍后再试')
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand">ferry</div>
      <div class="sub">轻量级代理管理面板 · 管理端</div>
      <el-form label-position="top">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="用户名" autocomplete="username" @keyup.enter="submit" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password" type="password" show-password placeholder="密码"
            autocomplete="current-password" @keyup.enter="submit"
          />
        </el-form-item>
        <el-form-item v-if="needTotp" label="两步验证码">
          <el-input
            v-model="form.totp" placeholder="6 位验证码或恢复码" autocomplete="one-time-code"
            @keyup.enter="submit"
          />
          <span class="hint">验证器 6 位数字，或绑定时保存的恢复码（xxxx-xxxx，用一个销一个）</span>
        </el-form-item>
        <el-button type="primary" :loading="loading" style="width: 100%; margin-top: 4px" @click="submit">
          {{ needTotp ? '验证并登录' : '登录' }}
        </el-button>
      </el-form>
    </div>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--ferry-bg);
}
.login-card {
  width: 360px;
  padding: 32px 28px;
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 12px;
}
.brand {
  font-size: 26px;
  font-weight: 700;
  letter-spacing: 0.5px;
}
.sub {
  margin: 4px 0 20px;
  color: var(--ferry-text-dim);
  font-size: 13px;
}
.hint {
  display: block;
  margin-top: 4px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
</style>
