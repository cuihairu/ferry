<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, get, getUnreadCount } from './api'
import type { Me } from './api'
import { auth, logout, setUser } from './auth'
import { initSupport } from './support'

// Shell: top bar brand + primary navigation + user identity (the same Linear dark style as P0-14).
const route = useRoute()
const router = useRouter()

/** refreshUnread fetches the in-app notification unread count (NT-1): refreshed on panel entry and on every route change (lightweight polling). */
async function refreshUnread() {
  if (!auth.token) return
  try {
    auth.unread = await getUnreadCount()
  } catch {
    /* a failed unread-count fetch does not disturb the main flow */
  }
}

onMounted(async () => {
  if (!auth.token) return
  try {
    setUser(await get<Me>('/api/panel/me'))
    void initSupport() // the support widget mounts on demand; failures only warn (support.ts)
  } catch (e) {
    // 404 = invalid token / user disabled; network-class errors keep the token and retry next time.
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
        <RouterLink to="/shop" class="nav-link" :class="{ active: route.path === '/shop' }">购买</RouterLink>
        <RouterLink to="/orders" class="nav-link" :class="{ active: route.path === '/orders' }">订单</RouterLink>
        <RouterLink to="/subscriptions" class="nav-link" :class="{ active: route.path === '/subscriptions' }">订阅</RouterLink>
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
