<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getAuditLogs, type AuditLogRow } from '../api'
import { formatDate } from '../utils/format'

// 操作审计（AU-2，P2-6 转正）：管理员写操作流水查询——谁/何时/动作/对象/
// 请求体快照（脱敏）/成败/IP；过滤=操作者/方法/时间范围，分页加载。

const rows = ref<AuditLogRow[]>([])
const loading = ref(false)
const filters = reactive({ actor: '', method: '', from: '', to: '' })
const page = ref(1)
const pageSize = 50

async function load() {
  loading.value = true
  try {
    const r = await getAuditLogs({
      actor: filters.actor || undefined,
      method: filters.method || undefined,
      from: filters.from || undefined,
      to: filters.to || undefined,
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    })
    rows.value = r.audit_logs
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  load()
}

function fmtTime(s: string): string {
  return formatDate(s)
}

onMounted(load)
</script>

<template>
  <div class="page">
    <h2>操作审计</h2>
    <p class="page-desc">管理员写操作流水（audit_logs）：谁对哪个对象做了什么，请求体快照已脱敏。</p>

    <el-form :inline="true" @submit.prevent="applyFilters" style="margin-bottom: 12px">
      <el-form-item label="操作者">
        <el-input v-model="filters.actor" placeholder="用户名" style="width: 140px" clearable @clear="applyFilters" />
      </el-form-item>
      <el-form-item label="方法">
        <el-input v-model="filters.method" placeholder="如 PUT" style="width: 100px" clearable @clear="applyFilters" />
      </el-form-item>
      <el-form-item label="起">
        <el-date-picker v-model="filters.from" type="datetime" placeholder="开始时间" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width: 190px" />
      </el-form-item>
      <el-form-item label="止">
        <el-date-picker v-model="filters.to" type="datetime" placeholder="结束时间" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width: 190px" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="applyFilters">查询</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="rows" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column type="expand">
        <template #default="{ row }">
          <div style="padding: 4px 24px">
            <p><b>请求体快照（脱敏）</b></p>
            <pre style="white-space: pre-wrap; word-break: break-all; max-height: 240px; overflow: auto">{{ row.body || '（无）' }}</pre>
            <p><b>IP</b>：{{ row.ip }}</p>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column label="时间" width="170">
        <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="actor" label="操作者" width="130" />
      <el-table-column prop="actor_kind" label="类型" width="80" />
      <el-table-column prop="action" label="动作" min-width="220" />
      <el-table-column prop="target" label="对象" width="90" />
      <el-table-column label="结果" width="90">
        <template #default="{ row }">
          <el-tag :type="row.success ? 'success' : 'danger'" size="small">{{ row.success ? '成功' : '失败' }}</el-tag>
        </template>
      </el-table-column>
    </el-table>

    <div style="margin-top: 12px; display: flex; gap: 8px; align-items: center">
      <el-button :disabled="page <= 1" @click="page--; load()">上一页</el-button>
      <span class="form-hint">第 {{ page }} 页（每页 {{ pageSize }} 条，最多回溯 200 条/页）</span>
      <el-button :disabled="rows.length < pageSize" @click="page++; load()">下一页</el-button>
    </div>
  </div>
</template>
