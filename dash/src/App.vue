<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { get } from './api'

// P0-14：侧边导航 + 顶栏 + 内容区。顶栏常驻面板健康灯。
const route = useRoute()
const healthOk = ref(false)

onMounted(async () => {
  try {
    await get('/api/health')
    healthOk.value = true
  } catch {
    healthOk.value = false
  }
})
</script>

<template>
  <el-container class="layout">
    <el-aside width="220px" class="layout-aside">
      <div class="logo">ferry</div>
      <div class="logo-sub">轻量级代理管理面板</div>
      <el-menu router :default-active="route.path" class="nav">
        <el-menu-item index="/nodes">节点</el-menu-item>
        <el-menu-item index="/users">用户</el-menu-item>
        <el-menu-item index="/settings">设置</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="topbar" height="48px">
        <span class="health">
          <span class="dot" :class="{ ok: healthOk }"></span>
          {{ healthOk ? '面板在线' : '面板离线' }}
        </span>
      </el-header>
      <el-main>
        <RouterView />
      </el-main>
    </el-container>
  </el-container>
</template>
