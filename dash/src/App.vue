<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get } from './api'
import { logout, logoutDist, role } from './auth'

// P0-14: side navigation + top bar + content area. The top bar always carries the panel health light;
// since the security batch (§1) the login page owns the viewport on its own (side bar hidden) and the
// top bar's right side signs out. DS-3: the distributor role's side bar renders only the distributor
// workbench entry, and signing out clears the distributor session.
const route = useRoute()
const router = useRouter()
const healthOk = ref(false)

// The login page owns the viewport; the distributor login page works the same way.
const bareView = computed(() => route.path === '/login' || route.path === '/dist-login')
const isDistributor = computed(() => role.value === 'distributor')

onMounted(async () => {
  try {
    await get('/api/health')
    healthOk.value = true
  } catch {
    healthOk.value = false
  }
})

function doLogout() {
  if (isDistributor.value) {
    logoutDist()
    router.push('/dist-login')
    return
  }
  logout()
  router.push('/login')
}
</script>

<template>
  <el-container class="layout">
    <el-aside v-if="!bareView" width="220px" class="layout-aside">
      <div class="logo">ferry</div>
      <div class="logo-sub">轻量级代理管理面板</div>
      <el-menu v-if="isDistributor" router :default-active="route.path" class="nav">
        <el-menu-item index="/dist">代理工作台</el-menu-item>
      </el-menu>
      <el-menu v-else router :default-active="route.path" class="nav">
        <el-menu-item index="/nodes">节点</el-menu-item>
        <el-menu-item index="/users">用户</el-menu-item>
        <el-menu-item index="/admins">管理员</el-menu-item>
        <el-menu-item index="/cards">卡密</el-menu-item>
        <el-menu-item index="/payments">对账</el-menu-item>
        <el-menu-item index="/promos">促销</el-menu-item>
        <el-menu-item index="/cost">成本</el-menu-item>
        <el-menu-item index="/bills">账单同步</el-menu-item>
        <el-menu-item index="/provision">供给</el-menu-item>
        <el-menu-item index="/dns">域名</el-menu-item>
        <el-menu-item index="/recoveries">恢复</el-menu-item>
        <el-menu-item index="/notifications">通知</el-menu-item>
        <el-menu-item index="/landings">调配</el-menu-item>
        <el-menu-item index="/paths">线路</el-menu-item>
        <el-menu-item index="/audit">审计</el-menu-item>
        <el-menu-item index="/cloud">云资源</el-menu-item>
        <el-menu-item index="/settings">设置</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="topbar" height="48px">
        <span class="health">
          <span class="dot" :class="{ ok: healthOk }"></span>
          {{ healthOk ? '面板在线' : '面板离线' }}
        </span>
        <el-button v-if="!bareView" link @click="doLogout">退出登录</el-button>
      </el-header>
      <el-main>
        <RouterView />
      </el-main>
    </el-container>
  </el-container>
</template>
