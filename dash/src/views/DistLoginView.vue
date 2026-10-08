<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { distLogin } from '../api'
import { setDistToken } from '../auth'

// 代理登录（DS-3 §3.2）：POST /distributor/login 独立端点，令牌落
// ferry.dist.token 独立键；无 2FA（代理非管理员），防爆破在后端限速。
const route = useRoute()
const router = useRouter()
const form = reactive({ username: '', password: '' })
const loading = ref(false)

async function submit() {
  if (!form.username.trim() || !form.password) {
    ElMessage.warning('请输入用户名与密码')
    return
  }
  loading.value = true
  try {
    const r = await distLogin({ username: form.username.trim(), password: form.password })
    setDistToken(r.token)
    const redirect = String(route.query.redirect ?? '')
    router.push(redirect.startsWith('/dist') ? redirect : '/dist')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '登录失败，请稍后再试')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand">ferry</div>
      <div class="sub">代理工作台 · 分销</div>
      <el-form label-position="top">
        <el-form-item label="代理用户名">
          <el-input v-model="form.username" placeholder="代理用户名" autocomplete="username" @keyup.enter="submit" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password" type="password" show-password placeholder="密码"
            autocomplete="current-password" @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" :loading="loading" style="width: 100%; margin-top: 4px" @click="submit">
          登录
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
</style>
