<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, patch, del, type CardBatch, type CardCode } from '../api'
import { GB, formatBytes, formatDate } from '../utils/format'

// PAY-6：卡密批次管理 + 卡密明细（复制/禁用）+ CSV 导出。

const batches = ref<CardBatch[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    batches.value = await get<CardBatch[]>('/api/card-batches')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 建批次 ----
const createVisible = ref(false)
const saving = ref(false)
const form = reactive({
  name: '',
  grantType: 'add_quota' as CardBatch['grant_type'],
  grantValue: 10,
  total: 10,
  expiredAt: null as Date | null,
})

function openCreate() {
  Object.assign(form, { name: '', grantType: 'add_quota', grantValue: 10, total: 10, expiredAt: null })
  createVisible.value = true
}

async function save() {
  const name = form.name.trim()
  if (!name) {
    ElMessage.warning('请输入批次名称')
    return
  }
  if (form.grantValue <= 0) {
    ElMessage.warning('权益值需大于 0')
    return
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      name,
      grant_type: form.grantType,
      // 加配额按 GiB 录入，后端口径为字节。
      grant_value: form.grantType === 'add_quota' ? Math.round(form.grantValue * GB) : form.grantValue,
      total: form.total,
    }
    if (form.expiredAt) body.expired_at = form.expiredAt.toISOString()
    const res = await post<{ batch: CardBatch; codes: string[] }>('/api/card-batches', body)
    createVisible.value = false
    ElMessage.success(`已生成 ${res.codes.length} 张卡密`)
    await load()
    await openCodes(res.batch)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

// ---- 卡密明细 ----
const codesVisible = ref(false)
const codes = ref<CardCode[]>([])
const activeBatch = ref<CardBatch | null>(null)

async function openCodes(b: CardBatch) {
  activeBatch.value = b
  codesVisible.value = true
  try {
    codes.value = await get<CardCode[]>(`/api/card-batches/${b.id}/codes`)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function copyCode(code: string) {
  await navigator.clipboard.writeText(code)
  ElMessage.success('已复制')
}

async function disableCode(row: CardCode) {
  await ElMessageBox.confirm(`禁用卡密 ${row.code} 后不可再兑换，确认？`, '禁用卡密', { type: 'warning' })
  try {
    await patch(`/api/card-codes/${row.id}/disable`)
    ElMessage.success('已禁用')
    if (activeBatch.value) await openCodes(activeBatch.value)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 导出 / 删除 ----
async function exportBatch(b: CardBatch) {
  try {
    const res = await fetch(`/api/card-batches/${b.id}/export.csv`)
    if (!res.ok) throw new Error(`导出失败（${res.status}）`)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `card-batch-${b.id}.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function remove(b: CardBatch) {
  await ElMessageBox.confirm(`删除批次「${b.name}」将连带删除其全部卡密，确认？`, '删除批次', { type: 'warning' })
  try {
    await del(`/api/card-batches/${b.id}`)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 展示辅助 ----
function grantText(b: CardBatch): string {
  return b.grant_type === 'add_quota' ? `+${formatBytes(b.grant_value)}` : `+${b.grant_value} 天`
}

function batchState(b: CardBatch): { text: string; type: 'success' | 'info' | 'danger' | 'warning' } {
  if (b.expired_at && new Date(b.expired_at).getTime() < Date.now()) return { text: '已过期', type: 'danger' }
  if (b.remaining === 0) return { text: '已售罄', type: 'info' }
  return { text: '可用', type: 'success' }
}

const codeState: Record<CardCode['status'], { text: string; type: 'success' | 'info' | 'danger' }> = {
  unused: { text: '未使用', type: 'info' },
  used: { text: '已使用', type: 'success' },
  disabled: { text: '已禁用', type: 'danger' },
}
</script>

<template>
  <div class="page">
    <h2>卡密</h2>
    <p class="page-desc">批次生成、导出与回收；兑换记为 provider=card 的订单。</p>

    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新建批次</el-button>
    </div>

    <el-table :data="batches" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="150" />
      <el-table-column label="权益" width="130">
        <template #default="{ row }">{{ grantText(row) }}</template>
      </el-table-column>
      <el-table-column label="剩余 / 总数" width="120">
        <template #default="{ row }">{{ row.remaining }} / {{ row.total }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="batchState(row).type" size="small" effect="dark">{{ batchState(row).text }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="有效期至" width="150">
        <template #default="{ row }">{{ formatDate(row.expired_at) }}</template>
      </el-table-column>
      <el-table-column prop="created_by" label="创建人" width="100" />
      <el-table-column label="创建时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openCodes(row)">卡密</el-button>
          <el-button link @click="exportBatch(row)">导出</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createVisible" title="新建批次" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="128" placeholder="如：50GB 月卡" />
        </el-form-item>
        <el-form-item label="权益类型">
          <el-radio-group v-model="form.grantType">
            <el-radio-button value="add_quota">加配额</el-radio-button>
            <el-radio-button value="extend_days">延长时长</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="form.grantType === 'add_quota' ? '配额 (GiB)' : '时长 (天)'">
          <el-input-number v-model="form.grantValue" :min="1" :step="form.grantType === 'add_quota' ? 10 : 30" />
        </el-form-item>
        <el-form-item label="生成数量">
          <el-input-number v-model="form.total" :min="1" :max="10000" :step="10" />
        </el-form-item>
        <el-form-item label="有效期至">
          <el-date-picker v-model="form.expiredAt" type="datetime" placeholder="留空表示永久" style="width: 100%" />
          <span class="form-hint">过期后卡密不可兑换</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="codesVisible" :title="activeBatch ? `卡密：${activeBatch.name}` : '卡密'" width="720px">
      <el-table :data="codes" max-height="420">
        <el-table-column prop="code" label="卡密" min-width="180">
          <template #default="{ row }">
            <span class="code">{{ row.code }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="codeState[row.status as CardCode['status']].type" size="small" effect="dark">
              {{ codeState[row.status as CardCode['status']].text }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="fail_count" label="失败次数" width="90" />
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button link type="primary" @click="copyCode(row.code)">复制</el-button>
            <el-button v-if="row.status === 'unused'" link type="danger" @click="disableCode(row)">禁用</el-button>
          </template>
        </el-table-column>
      </el-table>
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
.code {
  font-family: 'SFMono-Regular', 'JetBrains Mono', Menlo, Consolas, monospace;
  letter-spacing: 0.02em;
}
</style>
