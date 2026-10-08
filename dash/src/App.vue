<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get } from './api'
import { logout, logoutDist, role } from './auth'

// P0-14：侧边导航 + 顶栏 + 内容区。顶栏常驻面板健康灯；安全批（§1）起
// 登录页独占视口（藏侧栏），顶栏右侧退出登录。DS-3：代理角色侧栏只渲染
// 代理工作台一项，退出清代理会话。
const route = useRoute()
const router = useRouter()
const healthOk = ref(false)

// 登录页独占视口；代理登录页同理。
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
        <el-menu-item index="/cards">卡密</el-menu-item>
        <el-menu-item index="/payments">对账</el-menu-item>
        <el-menu-item index="/cost">成本</el-menu-item>
        <el-menu-item index="/provision">供给</el-menu-item>
        <el-menu-item index="/dns">域名</el-menu-item>
        <el-menu-item index="/recoveries">恢复</el-menu-item>
        <el-menu-item index="/notifications">通知</el-menu-item>
        <el-menu-item index="/landings">调配</el-menu-item>
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
