<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ApiError, adminLdapLogin, adminLogin, adminLoginAvailable } from '../api'
import { setToken } from '../auth'

// Admin login (security design §1): once the password passes, accounts bound to 2FA add a 6-digit TOTP or
// recovery code — the backend's machine codes totp_required/totp_invalid drive this page's code input.
const route = useRoute()
const router = useRouter()
const form = reactive({ username: '', password: '', totp: '' })
const needTotp = ref(false)
const loading = ref(false)

// External logins (G7): both are off server-side unless configured; the probe only decides whether
// the entry points are shown at all, so it is best-effort.
const avail = reactive({ oidc: false, ldap: false })
const useLdap = ref(false)

onMounted(async () => {
  // OIDC return leg: the provider round-trip lands on /login#oidc=<jwt> (the token rides the
  // fragment, never a cookie or a query string).
  const frag = new URLSearchParams(window.location.hash.startsWith('#') ? window.location.hash.slice(1) : '')
  const oidc = frag.get('oidc')
  if (oidc) {
    setToken(oidc)
    history.replaceState(null, '', window.location.pathname)
    router.push('/nodes')
    return
  }
  try {
    const a = await adminLoginAvailable()
    avail.oidc = a.oidc
    avail.ldap = a.ldap
  } catch {
    // keep the password form as-is
  }
})

async function submitLdap() {
  if (!form.username.trim() || !form.password) {
    ElMessage.warning('请输入用户名与密码')
    return
  }
  loading.value = true
  try {
    const r = await adminLdapLogin({ username: form.username.trim(), password: form.password })
    setToken(r.token)
    router.push('/nodes')
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '登录失败，请稍后再试')
  } finally {
    loading.value = false
  }
}

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
        <el-form-item v-if="needTotp && !useLdap" label="两步验证码">
          <el-input
            v-model="form.totp" placeholder="6 位验证码或恢复码" autocomplete="one-time-code"
            @keyup.enter="submit"
          />
          <span class="hint">验证器 6 位数字，或绑定时保存的恢复码（xxxx-xxxx，用一个销一个）</span>
        </el-form-item>
        <el-button type="primary" :loading="loading" style="width: 100%; margin-top: 4px" @click="useLdap ? submitLdap() : submit()">
          {{ useLdap ? '目录登录' : needTotp ? '验证并登录' : '登录' }}
        </el-button>
        <div v-if="avail.oidc || avail.ldap" class="ext-login">
          <el-button v-if="avail.ldap" link type="primary" @click="useLdap = !useLdap">
            {{ useLdap ? '返回密码登录' : '使用目录账号（LDAP）' }}
          </el-button>
          <a v-if="avail.oidc" class="oidc-link" href="/admin/oidc/login?redirect=/login">使用 OIDC 登录</a>
        </div>
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
.ext-login {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 10px;
  font-size: 13px;
}
.oidc-link {
  color: var(--el-color-primary);
  text-decoration: none;
}
.oidc-link:hover {
  text-decoration: underline;
}
</style>
