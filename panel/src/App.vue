<script setup lang="ts">
// P0-13 骨架：占位导航，P0-14 落 Linear 风格布局与主题。
</script>

<template>
  <nav>
    <RouterLink to="/nodes">节点</RouterLink>
    <RouterLink to="/users">用户</RouterLink>
    <RouterLink to="/settings">设置</RouterLink>
  </nav>
  <RouterView />
</template>
