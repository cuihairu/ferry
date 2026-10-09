<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, get, getUnreadCount } from './api'
import type { Me } from './api'
import { auth, logout, setUser } from './auth'
import { initSupport } from './support'

// 壳层：顶栏品牌 + 一级导航 + 用户身份（P0-14 同款 Linear 暗色）。
const route = useRoute()
const router = useRouter()

/** refreshUnread 拉站内信未读数（NT-1）：进面板与每次换页时刷新（轻量轮询口径）。 */
async function refreshUnread() {
  if (!auth.token) return
  try {
    auth.unread = await getUnreadCount()
  } catch {
    /* 未读数拉取失败不打扰主流程 */
  }
}

onMounted(async () => {
  if (!auth.token) return
  try {
    setUser(await get<Me>('/api/panel/me'))
    void initSupport() // 客服组件按需挂载，失败只 warn（support.ts）
  } catch (e) {
    // 404 = 令牌失效/用户停用；网络类错误保留令牌下次再试。
    if (e instanceof ApiError && e.status === 404) doLogout()
  }
  refreshUnread()
})

watch(() => route.path, refreshUnread)

function doLogout() {
  logout()
  router.push('/login')
}
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <div class="brand">ferry<span class="brand-sub">用户面板</span></div>
      <nav v-if="auth.token" class="nav">
        <RouterLink to="/" class="nav-link" :class="{ active: route.path === '/' }">概览</RouterLink>
        <RouterLink to="/redeem" class="nav-link" :class="{ active: route.path === '/redeem' }">兑换</RouterLink>
        <RouterLink to="/orders" class="nav-link" :class="{ active: route.path === '/orders' }">订单</RouterLink>
        <RouterLink to="/notifications" class="nav-link" :class="{ active: route.path === '/notifications' }">
          通知<span v-if="auth.unread > 0" class="badge">{{ auth.unread > 99 ? '99+' : auth.unread }}</span>
        </RouterLink>
      </nav>
      <div v-if="auth.token" class="topbar-right">
        <span class="uname">{{ auth.user?.username ?? '…' }}</span>
        <button class="btn btn-ghost" @click="doLogout">退出</button>
      </div>
    </header>
    <main class="main">
      <RouterView />
    </main>
  </div>
</template>
