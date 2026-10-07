<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getRecoveries, getRecoveryActions, type Recovery, type RecoveryAction } from '../api'

// 封禁恢复流水线（BR-1）与动作回放（BR-5）：入口被判封自动入线，
// L1 域名切 DNS → L2 预备机补位 → L3 开新机 分级推进，探测恢复随时收尾；
// 每级尝试落痕（recovery_actions），终态 failed 经通知通道升级人工。

const recoveries = ref<Recovery[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    recoveries.value = await getRecoveries()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 回放 ----
const replayVisible = ref(false)
const replayRec = ref<Recovery | null>(null)
const actions = ref<RecoveryAction[]>([])
const replayLoading = ref(false)

async function replay(row: Recovery) {
  replayRec.value = row
  replayVisible.value = true
  replayLoading.value = true
  try {
    actions.value = await getRecoveryActions(row.id)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    replayLoading.value = false
  }
}

const REC_STATE: Record<string, { text: string; type: 'success' | 'warning' | 'danger' | 'info' }> = {
  running: { text: '进行中', type: 'warning' },
  done: { text: '已恢复', type: 'success' },
  failed: { text: '失败', type: 'danger' },
}
const ACT_STATE: Record<string, { text: string; type: 'success' | 'warning' | 'danger' | 'info' }> = {
  running: { text: '执行中', type: 'warning' },
  ok: { text: '成功', type: 'success' },
  failed: { text: '失败', type: 'danger' },
  skipped: { text: '跳过', type: 'info' },
  timeout: { text: '超时', type: 'danger' },
}
const ACT_TAG: Record<string, string> = {
  dns_switch: 'L1 切备用 IP',
  standby_promote: 'L2 预备补位',
  new_instance: 'L3 开新机',
}

function fmtDate(v: string | null): string {
  if (!v) return '—'
  return new Date(v).toLocaleString()
}
function fmtClock(v: string): string {
  return new Date(v).toLocaleTimeString()
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <h2>恢复流水线</h2>
      <el-button size="small" @click="load">刷新</el-button>
    </div>
    <p class="page-desc">
      入口被探测判封后自动进入恢复流水线：L1 域名前置切备用 IP → L2 预备机补位 →
      L3 按供给模板开新机，每级超时进下一级，探测恢复随时收尾回切。
      每级尝试留痕可回放；全级耗尽记失败并经通知通道升级人工。
    </p>

    <el-table :data="recoveries" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="node_name" label="节点" min-width="130" />
      <el-table-column label="级别" width="70">
        <template #default="{ row }">L{{ row.level }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="REC_STATE[row.state]?.type ?? 'info'" size="small" effect="plain">
            {{ REC_STATE[row.state]?.text ?? row.state }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="当前动作" min-width="150">
        <template #default="{ row }">
          <span v-if="row.action">{{ row.action }}（{{ row.action_state || '待执行' }}）</span>
          <span v-else class="dim">—</span>
        </template>
      </el-table-column>
      <el-table-column label="原因" min-width="170">
        <template #default="{ row }">
          <el-tooltip :disabled="!row.last_error" :content="row.last_error" placement="top">
            <span :class="{ dim: !row.last_error }">{{ row.last_error || '—' }}</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="开始" min-width="150">
        <template #default="{ row }">{{ fmtDate(row.started_at) }}</template>
      </el-table-column>
      <el-table-column label="结束" min-width="150">
        <template #default="{ row }">{{ fmtDate(row.finished_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="replay(row)">回放</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="replayVisible" :title="`恢复回放：${replayRec?.node_name ?? ''}`" width="560px">
      <el-table
        :data="actions" v-loading="replayLoading" size="small"
        :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
      >
        <el-table-column label="级别" width="60">
          <template #default="{ row }">L{{ row.level }}</template>
        </el-table-column>
        <el-table-column label="动作" min-width="120">
          <template #default="{ row }">{{ ACT_TAG[row.action] ?? (row.action || '未注册') }}</template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <el-tag :type="ACT_STATE[row.state]?.type ?? 'info'" size="small" effect="plain">
              {{ ACT_STATE[row.state]?.text ?? row.state }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="说明" min-width="180">
          <template #default="{ row }">
            <span :class="{ dim: !row.detail }">{{ row.detail || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="开始" min-width="90">
          <template #default="{ row }">{{ fmtClock(row.started_at) }}</template>
        </el-table-column>
        <el-table-column label="结束" min-width="90">
          <template #default="{ row }">{{ row.finished_at ? fmtClock(row.finished_at) : '—' }}</template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<style scoped>
.head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.page-desc {
  color: var(--ferry-text-dim);
  font-size: 13px;
  margin-bottom: 16px;
}
.dim {
  color: var(--ferry-text-muted);
}
</style>
