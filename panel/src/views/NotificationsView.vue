<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ApiError, getNotifications, markAllRead, markRead } from '../api'
import type { NotificationRow } from '../api'
import { auth } from '../auth'
import { formatDate } from '../utils/format'

// 通知中心（NT-1）：四类站内信（公告/到期/流量预警/系统），点卡片即已读。
const loading = ref(false)
const error = ref('')
const rows = ref<NotificationRow[]>([])

async function load() {
  loading.value = true
  try {
    rows.value = await getNotifications()
    auth.unread = rows.value.filter((n) => !n.read_at).length
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    loading.value = false
  }
}
onMounted(load)

/** readOne 点卡片已读：已读的幂等跳过，失败不打扰（下次再点即可）。 */
async function readOne(n: NotificationRow) {
  if (n.read_at) return
  try {
    await markRead(n.id)
    n.read_at = new Date().toISOString()
    auth.unread = Math.max(0, auth.unread - 1)
  } catch {
    /* 单条已读失败静默，列表不动 */
  }
}

const markingAll = ref(false)

async function readAll() {
  markingAll.value = true
  try {
    await markAllRead()
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    markingAll.value = false
  }
}

const unreadCount = () => rows.value.filter((n) => !n.read_at).length

const typeText: Record<string, string> = {
  announcement: '公告',
  expiry: '到期',
  traffic: '流量',
  system: '系统',
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <p v-if="loading" class="muted">加载中…</p>
      <template v-else-if="!error">
        <p v-if="rows.length === 0" class="muted">暂无通知，公告与到期提醒会显示在这里。</p>
        <button v-else-if="unreadCount() > 0" class="btn btn-ghost" :disabled="markingAll" @click="readAll">
          全部已读（{{ unreadCount() }}）
        </button>
      </template>
    </div>
    <p v-if="error" class="error-text">{{ error }}</p>

    <section
      v-for="n in rows"
      :key="n.id"
      class="card notif"
      :class="{ unread: !n.read_at }"
      @click="readOne(n)"
    >
      <div class="notif-head">
        <span class="tag" :class="n.type === 'announcement' ? 'tag-ok' : 'tag-dim'">{{ typeText[n.type] ?? n.type }}</span>
        <span class="notif-title">{{ n.title }}</span>
        <span v-if="!n.read_at" class="dot" title="未读"></span>
      </div>
      <p v-if="n.body" class="notif-body">{{ n.body }}</p>
      <p class="notif-time">{{ formatDate(n.created_at) }}</p>
    </section>
  </div>
</template>

<style scoped>
.head-row {
  min-height: 24px;
  display: flex;
  justify-content: flex-end;
}
.notif {
  cursor: pointer;
}
.notif.unread {
  border-color: var(--ferry-accent);
}
.notif-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.notif-title {
  font-weight: 600;
  font-size: 14px;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ferry-accent);
  margin-left: auto;
  flex: none;
}
.notif-body {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--ferry-text-dim);
  white-space: pre-wrap;
}
.notif-time {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--ferry-text-muted);
}
</style>
