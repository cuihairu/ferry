<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createAnnouncement, deleteNotification, getNotifications, type NotificationRow } from '../api'
import { formatDate } from '../utils/format'

// NT-1：站内信通知中心——公告扇出给全部启用用户，全量列表供排障与清理。
// 到期/流量预警由 NT-2 定时扫描落行，这里只管发布与查看。

const loading = ref(false)
const rows = ref<NotificationRow[]>([])
const typeFilter = ref('')

async function load() {
  loading.value = true
  try {
    rows.value = await getNotifications()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const filtered = computed(() =>
  typeFilter.value ? rows.value.filter((r) => r.type === typeFilter.value) : rows.value,
)

// 发布公告：标题必填，正文选填。
const publishing = ref(false)
const title = ref('')
const body = ref('')

async function publish() {
  if (!title.value.trim()) {
    ElMessage.warning('标题不能为空')
    return
  }
  publishing.value = true
  try {
    const { created } = await createAnnouncement(title.value.trim(), body.value.trim())
    ElMessage.success(`已发布，送达 ${created} 个用户`)
    title.value = ''
    body.value = ''
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    publishing.value = false
  }
}

async function remove(row: NotificationRow) {
  try {
    await ElMessageBox.confirm(`删除通知「${row.title}」？`, '删除', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteNotification(row.id)
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

const TYPE_TAG: Record<string, { text: string; type: 'success' | 'info' | 'danger' | 'warning' | 'primary' }> = {
  announcement: { text: '公告', type: 'primary' },
  expiry: { text: '到期', type: 'warning' },
  traffic: { text: '流量', type: 'danger' },
  system: { text: '系统', type: 'info' },
}
</script>

<template>
  <div class="page">
    <h2>通知</h2>
    <p class="page-desc">站内信通知中心（公告 / 到期 / 流量预警 / 系统四类），公告扇出给全部启用用户；自动触发（到期、流量阈值）由定时扫描落行。</p>

    <div class="panel-card">
      <div class="card-title">发布公告</div>
      <el-input v-model="title" placeholder="标题（必填，≤128 字）" maxlength="128" show-word-limit />
      <el-input
        v-model="body"
        type="textarea"
        :rows="3"
        placeholder="正文（选填，≤512 字）"
        maxlength="512"
        show-word-limit
        class="body-input"
      />
      <div class="publish-bar">
        <el-button type="primary" :loading="publishing" @click="publish">发布</el-button>
      </div>
    </div>

    <div class="toolbar">
      <el-select v-model="typeFilter" placeholder="全部类型" clearable style="width: 140px">
        <el-option label="公告" value="announcement" />
        <el-option label="到期" value="expiry" />
        <el-option label="流量" value="traffic" />
        <el-option label="系统" value="system" />
      </el-select>
      <el-button @click="load" :loading="loading">刷新</el-button>
    </div>

    <el-table :data="filtered" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="类型" width="90">
        <template #default="{ row }">
          <el-tag :type="TYPE_TAG[row.type]?.type ?? 'info'" size="small" effect="dark">
            {{ TYPE_TAG[row.type]?.text ?? row.type }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="title" label="标题" min-width="200" show-overflow-tooltip />
      <el-table-column prop="body" label="正文" min-width="260" show-overflow-tooltip>
        <template #default="{ row }">{{ row.body || '—' }}</template>
      </el-table-column>
      <el-table-column prop="user_id" label="用户" width="70" />
      <el-table-column label="已读" width="80">
        <template #default="{ row }">
          <span :class="row.read_at ? 'read' : 'unread'">{{ row.read_at ? '已读' : '未读' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="80">
        <template #default="{ row }">
          <el-button size="small" type="danger" plain @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<style scoped>
.panel-card {
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  padding: 16px;
  margin-bottom: 20px;
}
.card-title {
  font-size: 13px;
  font-weight: 650;
  margin-bottom: 12px;
}
.body-input {
  margin-top: 10px;
}
.publish-bar {
  margin-top: 12px;
}
.read {
  color: var(--ferry-text-muted);
}
.unread {
  color: var(--ferry-warn);
  font-weight: 600;
}
</style>
