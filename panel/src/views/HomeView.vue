<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError, get } from '../api'
import type { Me } from '../api'
import { auth, setUser } from '../auth'
import { formatDate, formatBytes, quotaText } from '../utils/format'

// 概览：用量进度、配额与到期、订阅链接复制。
const loading = ref(false)
const error = ref('')
const copied = ref('')

onMounted(async () => {
  if (!auth.token) return
  if (auth.user) return
  loading.value = true
  try {
    setUser(await get<Me>('/api/panel/me'))
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    loading.value = false
  }
})

const me = computed(() => auth.user)
const unlimited = computed(() => (me.value?.quota_bytes ?? 0) === 0)
const pct = computed(() => {
      const m = me.value
      if (!m || m.quota_bytes === 0) return 0
      return Math.min(100, Math.round((m.used_bytes / m.quota_bytes) * 100))
    })
const remaining = computed(() => {
      const m = me.value
      if (!m) return '—'
      if (m.quota_bytes === 0) return '不限'
      return formatBytes(Math.max(0, m.quota_bytes - m.used_bytes))
    })

function subLink(target: 'v2ray' | 'clash'): string {
  const base = `${window.location.origin}/sub/${auth.token}`
  return `${base}?target=${target}`
}

async function copy(text: string, key: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 剪贴板不可用时退回选中复制：建临时 input 走 execCommand。
    const el = document.createElement('input')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    el.remove()
  }
  copied.value = key
  setTimeout(() => (copied.value = ''), 1600)
}
</script>

<template>
  <div class="page">
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="error" class="error-text">{{ error }}</p>
    <template v-else-if="me">
      <div v-if="!me.active" class="alert">
        当前账号不可用（已停用 / 已到期 / 已超配额），订阅链接不会返回节点。如需继续使用请兑换卡密。
      </div>

      <section class="card">
        <h2 class="card-title">用量</h2>
        <div class="usage-row">
          <span class="usage-num">{{ formatBytes(me.used_bytes) }}</span>
          <span class="muted">已用 / {{ quotaText(me.quota_bytes) }}</span>
        </div>
        <div v-if="!unlimited" class="meter">
          <i :class="{ warn: pct >= 70, danger: pct >= 95 }" :style="{ width: pct + '%' }"></i>
        </div>
        <dl class="kv">
          <div class="kv-row"><dt>剩余</dt><dd>{{ remaining }}</dd></div>
          <div class="kv-row"><dt>到期</dt><dd>{{ formatDate(me.expires_at) }}</dd></div>
          <div class="kv-row"><dt>状态</dt><dd>{{ me.active ? '正常' : '不可用' }}</dd></div>
        </dl>
      </section>

      <section class="card">
        <h2 class="card-title">订阅链接</h2>
        <p class="muted">按客户端类型复制对应链接；多数客户端粘贴通用链接可自动识别。</p>
        <div class="link-block">
          <div class="link-label">通用（v2ray / base64）</div>
          <div class="link-row">
            <code class="link-text">{{ subLink('v2ray') }}</code>
            <button class="btn btn-ghost" @click="copy(subLink('v2ray'), 'v2ray')">
              {{ copied === 'v2ray' ? '已复制' : '复制' }}
            </button>
          </div>
        </div>
        <div class="link-block">
          <div class="link-label">clash / mihomo（YAML）</div>
          <div class="link-row">
            <code class="link-text">{{ subLink('clash') }}</code>
            <button class="btn btn-ghost" @click="copy(subLink('clash'), 'clash')">
              {{ copied === 'clash' ? '已复制' : '复制' }}
            </button>
          </div>
        </div>
      </section>
    </template>
  </div>
</template>
