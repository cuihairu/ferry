<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, type User, type UserTemplate } from '../api'
import { GB, quotaText, formatDate } from '../utils/format'

// P0-15：用户表格 + 新建/编辑对话框 + 订阅链接复制与重置。

const users = ref<User[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    users.value = await get<User[]>('/api/users')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const dialogVisible = ref(false)
const editing = ref<User | null>(null)
const saving = ref(false)
const form = reactive({
  username: '',
  quotaGb: 0,
  resetCycle: 'none',
  expires: null as Date | null,
  enabled: true,
  hadExpiry: false,
  bwUp: 0,
  bwDown: 0,
})

// 重置周期选项（P1-4）：none 不限、day/week/month 按日历窗口重算用量。
const cycleOptions = [
  { value: 'none', label: '不限' },
  { value: 'day', label: '每日' },
  { value: 'week', label: '每周' },
  { value: 'month', label: '每月' },
]
function cycleLabel(c: string): string {
  return cycleOptions.find((o) => o.value === c)?.label ?? c
}

function openCreate() {
  editing.value = null
  Object.assign(form, { username: '', quotaGb: 0, resetCycle: 'none', expires: null, enabled: true, hadExpiry: false, bwUp: 0, bwDown: 0 })
  dialogVisible.value = true
}

function openEdit(u: User) {
  editing.value = u
  Object.assign(form, {
    username: u.username,
    quotaGb: u.quota_bytes / GB,
    resetCycle: u.reset_cycle || 'none',
    expires: u.expires_at ? new Date(u.expires_at) : null,
    enabled: u.enabled,
    hadExpiry: !!u.expires_at,
    bwUp: u.bw_up_mbps,
    bwDown: u.bw_down_mbps,
  })
  dialogVisible.value = true
}

// 套用默认模板（P1-5）：把设置里的默认配额/时长/周期/带宽限额填进表单，可再改。
async function applyTemplate() {
  try {
    const t = await get<UserTemplate>('/api/user-template')
    form.quotaGb = t.quota_bytes / GB
    form.resetCycle = t.reset_cycle || 'none'
    form.expires = t.expire_days > 0 ? new Date(Date.now() + t.expire_days * 86400000) : null
    form.bwUp = t.bw_up_mbps
    form.bwDown = t.bw_down_mbps
    ElMessage.success('已套用默认模板')
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function save() {
  const username = form.username.trim()
  if (!username) {
    ElMessage.warning('请输入用户名')
    return
  }
  saving.value = true
  try {
    // 后端对 expires_at 采用 PATCH 语义：传 null 不清除，清空需显式 clear_expire。
    const body: Record<string, unknown> = {
      username,
      quota_bytes: Math.round(form.quotaGb * GB),
      reset_cycle: form.resetCycle,
      enabled: form.enabled,
      bw_up_mbps: form.bwUp,
      bw_down_mbps: form.bwDown,
    }
    if (form.expires) {
      body.expires_at = form.expires.toISOString()
    } else if (editing.value && form.hadExpiry) {
      body.clear_expire = true
    }
    if (editing.value) {
      await put(`/api/users/${editing.value.id}`, body)
    } else {
      await post('/api/users', body)
    }
    dialogVisible.value = false
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

function subUrl(u: User): string {
  return `${location.origin}/sub/${u.sub_token}`
}

// 带宽限额展示：0=不限（P2-3）。
function bwText(u: User): string {
  if (!u.bw_up_mbps && !u.bw_down_mbps) return '不限'
  const parts: string[] = []
  parts.push(u.bw_up_mbps ? `↑${u.bw_up_mbps}` : '↑—')
  parts.push(u.bw_down_mbps ? `↓${u.bw_down_mbps}` : '↓—')
  return parts.join(' ')
}

async function copyLink(u: User) {
  await navigator.clipboard.writeText(subUrl(u))
  ElMessage.success('订阅链接已复制')
}

async function resetToken(u: User) {
  await ElMessageBox.confirm('旧订阅链接将立即失效，确认重置？', '重置订阅令牌', { type: 'warning' })
  await post(`/api/users/${u.id}/sub-token`)
  ElMessage.success('已重置')
  await load()
}

async function toggleEnabled(u: User, enabled: boolean) {
  try {
    await put(`/api/users/${u.id}`, { enabled })
    u.enabled = enabled
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function remove(u: User) {
  await ElMessageBox.confirm(`删除用户 ${u.username} 将连带删除其流量记录，确认？`, '删除用户', { type: 'warning' })
  await del(`/api/users/${u.id}`)
  ElMessage.success('已删除')
  await load()
}
</script>

<template>
  <div class="page">
    <h2>用户</h2>
    <p class="page-desc">订阅用户与配额管理。</p>

    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新建用户</el-button>
    </div>

    <el-table :data="users" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="username" label="用户名" min-width="140" />
      <el-table-column label="配额" width="110">
        <template #default="{ row }">{{ quotaText(row.quota_bytes) }}</template>
      </el-table-column>
      <el-table-column label="带宽" width="110">
        <template #default="{ row }">{{ bwText(row) }}</template>
      </el-table-column>
      <el-table-column label="重置" width="80">
        <template #default="{ row }">{{ cycleLabel(row.reset_cycle) }}</template>
      </el-table-column>
      <el-table-column label="到期" width="150">
        <template #default="{ row }">{{ formatDate(row.expires_at) }}</template>
      </el-table-column>
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: boolean) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="订阅链接" min-width="160">
        <template #default="{ row }">
          <el-button link type="primary" @click="copyLink(row)">复制链接</el-button>
        </template>
      </el-table-column>
      <el-table-column label="创建时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link @click="resetToken(row)">重置令牌</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑用户' : '新建用户'" width="480px">
      <el-form label-width="90px">
        <el-form-item v-if="!editing">
          <el-button size="small" @click="applyTemplate">套用默认模板</el-button>
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="form.username" maxlength="64" placeholder="1-64 字符" />
        </el-form-item>
        <el-form-item label="配额 (GiB)">
          <el-input-number v-model="form.quotaGb" :min="0" :step="10" />
          <span class="form-hint">0 表示不限</span>
        </el-form-item>
        <el-form-item label="带宽限额">
          <div class="bw-row">
            <el-input-number v-model="form.bwUp" :min="0" :max="100000" :step="10" />
            <span class="bw-sep">↑ Mbps</span>
            <el-input-number v-model="form.bwDown" :min="0" :max="100000" :step="10" />
            <span class="bw-sep">↓ Mbps</span>
          </div>
          <span class="form-hint">0 表示不限（P2-3，节点侧执行）</span>
        </el-form-item>
        <el-form-item label="重置周期">
          <el-select v-model="form.resetCycle" style="width: 160px">
            <el-option v-for="o in cycleOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <span class="form-hint">用量按该周期重新计算</span>
        </el-form-item>
        <el-form-item label="到期时间">
          <el-date-picker v-model="form.expires" type="datetime" placeholder="留空表示不限期" style="width: 100%" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 16px;
}
.form-hint {
  margin-left: 10px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.bw-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bw-sep {
  color: var(--ferry-text-muted);
  font-size: 12px;
}
</style>
