<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, get } from '../api'
import type { Me } from '../api'
import { logout, setToken, setUser, tokenFromInput } from '../auth'

// 登录 = 校验订阅令牌（粘贴裸令牌或整条订阅链接都可）。
const router = useRouter()
const input = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  const token = tokenFromInput(input.value)
  if (!token) {
    error.value = '请输入订阅令牌或订阅链接'
    return
  }
  loading.value = true
  error.value = ''
  setToken(token)
  try {
    setUser(await get<Me>('/api/panel/me'))
    router.push('/')
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      error.value = '令牌无效或已失效'
      logout()
    } else if (e instanceof ApiError && e.status === 429) {
      error.value = '尝试过于频繁，请稍后再试'
    } else if (e instanceof ApiError) {
      error.value = e.message
      logout()
    } else {
      error.value = '网络异常，请稍后再试'
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <form class="login-card" @submit.prevent="submit">
      <h1 class="login-title">ferry 用户面板</h1>
      <p class="login-desc">粘贴订阅链接或订阅令牌进入面板。</p>
      <label class="label" for="token">订阅链接 / 令牌</label>
      <input
        id="token"
        v-model="input"
        class="input input-mono"
        placeholder="https://…/sub/xxxx 或令牌"
        autocomplete="off"
        autofocus
      />
      <p v-if="error" class="error-text">{{ error }}</p>
      <div style="margin-top: 18px">
        <button class="btn btn-primary" style="width: 100%" type="submit" :disabled="loading">
          {{ loading ? '校验中…' : '进入面板' }}
        </button>
      </div>
      <p class="hint">令牌由面板管理者发放；丢失请联系管理者重置。</p>
    </form>
  </div>
</template>
