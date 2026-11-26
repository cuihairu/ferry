<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  getCostSources, createCostSource, updateCostSource, getBillImports,
  importBillsAPI, importBillsFile,
  type CostSourceRow, type BillImportRow, type CostSourceInput, type ImportAPIInput,
} from '../api'
import { formatDate } from '../utils/format'

// §4.7 F-0632: bill sync admin — cost sources, one-shot sync (API window or
// uploaded vendor file) and the import run trail with failure reasons. The
// records themselves land on the 成本 page (实际账单).

const sources = ref<CostSourceRow[]>([])
const sourcesLoading = ref(false)
const imports = ref<BillImportRow[]>([])
const importsLoading = ref(false)

const SOURCE_LABEL: Record<string, string> = { api: '厂商 API', file: '文件导入', manual: '手工录入' }
const STATE_TAG: Record<string, string> = { running: 'warning', succeeded: 'success', partial: 'warning', failed: 'danger' }

function sourceLabel(s: string): string {
  return SOURCE_LABEL[s] ?? s
}

async function loadSources() {
  sourcesLoading.value = true
  try {
    const data = await getCostSources()
    sources.value = data.sources
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    sourcesLoading.value = false
  }
}

async function loadImports() {
  importsLoading.value = true
  try {
    const data = await getBillImports()
    imports.value = data.imports
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    importsLoading.value = false
  }
}

// ---- Source creation ----
const addVisible = ref(false)
const addSaving = ref(false)
const addForm = reactive<CostSourceInput & { provider_id_text: string }>({
  name: '', provider_id: null, source: 'api', account: '', region: '', currency: 'CNY', note: '', provider_id_text: '',
})

function openAdd() {
  addForm.name = ''
  addForm.provider_id = null
  addForm.provider_id_text = ''
  addForm.source = 'api'
  addForm.account = ''
  addForm.region = ''
  addForm.currency = 'CNY'
  addForm.note = ''
  addVisible.value = true
}

async function saveAdd() {
  addSaving.value = true
  try {
    const pid = Number(addForm.provider_id_text)
    const input: CostSourceInput = { ...addForm }
    delete (input as Partial<typeof addForm>).provider_id_text
    input.provider_id = addForm.provider_id_text && pid > 0 ? pid : null
    await createCostSource(input)
    ElMessage.success('成本源已创建')
    addVisible.value = false
    await loadSources()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    addSaving.value = false
  }
}

async function toggleEnabled(row: CostSourceRow) {
  try {
    await updateCostSource(row.id, { enabled: !row.enabled })
    row.enabled = !row.enabled
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- One-shot API sync per source ----
const syncVisible = ref(false)
const syncSaving = ref(false)
const syncTarget = ref<CostSourceRow | null>(null)
const syncForm = reactive<ImportAPIInput & { range: [string, string] | null }>({
  from: '', to: '', account: '', region: '', incremental: false, range: null,
})

function openSync(row: CostSourceRow) {
  syncTarget.value = row
  const now = new Date()
  const start = new Date(now.getFullYear(), now.getMonth(), 1)
  const pad = (x: number) => String(x).padStart(2, '0')
  const iso = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T00:00:00Z`
  syncForm.range = [iso(start), iso(now)]
  syncForm.account = row.account
  syncForm.region = row.region
  syncForm.incremental = false
  syncVisible.value = true
}

async function doSync() {
  if (!syncTarget.value) return
  if (!syncForm.range || !syncForm.range[0]) {
    ElMessage.error('请选择同步窗口')
    return
  }
  syncSaving.value = true
  try {
    const out = await importBillsAPI(syncTarget.value.id, {
      from: syncForm.range[0], to: syncForm.range[1] ?? '',
      account: syncForm.account, region: syncForm.region, incremental: syncForm.incremental,
    })
    if (out.ok) {
      ElMessage.success(`同步完成：新增 ${out.run?.added ?? 0} 条`)
    } else {
      ElMessage.error(`同步失败：${out.error ?? '未知原因'}`)
    }
    syncVisible.value = false
    await Promise.all([loadSources(), loadImports()])
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    syncSaving.value = false
  }
}

// ---- File import per source ----
async function onFileChange(row: CostSourceRow, file: File) {
  try {
    const out = await importBillsFile(row.id, file)
    if (out.ok) {
      ElMessage.success(`导入完成：新增 ${out.run?.added ?? 0} 条`)
    } else {
      ElMessage.error(`导入失败：${out.error ?? '未知原因'}`)
    }
    await Promise.all([loadSources(), loadImports()])
  } catch (e) {
    ElMessage.error(String(e))
  }
  return false // keep el-upload from auto-posting itself
}

onMounted(loadSources)
onMounted(loadImports)
</script>

<template>
  <div class="page">
    <h2>账单同步</h2>
    <p class="page-desc">
      云厂商账单接入：成本源注册、按窗口同步（厂商 API 或上传账单文件）、同步运行记录与失败原因；导入的账单在「成本」页展示为实际账单。
    </p>

    <h3 class="section">成本源</h3>
    <div class="toolbar">
      <el-button type="primary" @click="openAdd">新建成本源</el-button>
      <el-button @click="loadSources" :loading="sourcesLoading">刷新</el-button>
    </div>
    <el-table :data="sources" v-loading="sourcesLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="130" />
      <el-table-column label="接入方式" width="100">
        <template #default="{ row }">{{ sourceLabel(row.source) }}</template>
      </el-table-column>
      <el-table-column prop="account" label="账号" min-width="120" />
      <el-table-column prop="currency" label="币种" width="70" />
      <el-table-column prop="region" label="区域" width="90" />
      <el-table-column label="关联云账号" width="100">
        <template #default="{ row }">{{ row.provider_id ? `#${row.provider_id}` : '—' }}</template>
      </el-table-column>
      <el-table-column label="最近同步" width="140">
        <template #default="{ row }">{{ formatDate(row.last_sync_at) }}</template>
      </el-table-column>
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="toggleEnabled(row)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200">
        <template #default="{ row }">
          <el-button size="small" type="primary" :disabled="row.source !== 'api'" @click="openSync(row)">同步窗口</el-button>
          <el-upload :show-file-list="false" :auto-upload="false" accept=".json"
            @change="(up: { file: { raw: File } }) => onFileChange(row, up.file.raw)">
            <el-button size="small" :disabled="row.source === 'manual'">上传账单</el-button>
          </el-upload>
        </template>
      </el-table-column>
    </el-table>

    <h3 class="section">同步记录</h3>
    <div class="toolbar">
      <el-button @click="loadImports" :loading="importsLoading">刷新记录</el-button>
      <span class="hint">running/succeeded/failed/partial：失败与部分成功都会保留原因，可重新发起同步补齐。</span>
    </div>
    <el-table :data="imports" v-loading="importsLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="id" label="#" width="60" />
      <el-table-column label="成本源" width="110">
        <template #default="{ row }">#{{ row.cost_provider_id }}</template>
      </el-table-column>
      <el-table-column label="方式" width="90">
        <template #default="{ row }">{{ sourceLabel(row.source) }}</template>
      </el-table-column>
      <el-table-column label="触发" width="90">
        <template #default="{ row }">{{ row.trigger === 'manual' ? '手动' : row.trigger === 'scheduled' ? '计划' : '补录' }}</template>
      </el-table-column>
      <el-table-column label="窗口" min-width="180">
        <template #default="{ row }">{{ formatDate(row.period_start) }} ~ {{ formatDate(row.period_end) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="(STATE_TAG[row.state] ?? 'info') as 'success' | 'warning' | 'danger' | 'info'" size="small">{{ row.state }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="取到/新增/更新/跳过" width="150">
        <template #default="{ row }">{{ row.fetched }}/{{ row.added }}/{{ row.updated }}/{{ row.skipped }}</template>
      </el-table-column>
      <el-table-column label="开始时间" width="140">
        <template #default="{ row }">{{ formatDate(row.started_at) }}</template>
      </el-table-column>
      <el-table-column label="原因" min-width="200">
        <template #default="{ row }">{{ row.error || '—' }}</template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="addVisible" title="新建成本源" width="520px">
      <el-form label-width="90px">
        <el-form-item label="名称"><el-input v-model="addForm.name" /></el-form-item>
        <el-form-item label="接入方式">
          <el-select v-model="addForm.source" style="width: 180px">
            <el-option value="api" label="厂商 API" />
            <el-option value="file" label="文件导入" />
            <el-option value="manual" label="手工录入" />
          </el-select>
        </el-form-item>
        <el-form-item label="云账号 ID"><el-input v-model="addForm.provider_id_text" placeholder="关联 Provider 行号，留空为手工源" /></el-form-item>
        <el-form-item label="账号"><el-input v-model="addForm.account" /></el-form-item>
        <el-form-item label="币种"><el-input v-model="addForm.currency" style="width: 120px" /></el-form-item>
        <el-form-item label="区域"><el-input v-model="addForm.region" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="addForm.note" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="addVisible = false">取消</el-button>
        <el-button type="primary" :loading="addSaving" @click="saveAdd">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="syncVisible" :title="`同步窗口：${syncTarget?.name ?? ''}`" width="520px">
      <el-form label-width="90px">
        <el-form-item label="时间范围">
          <el-date-picker v-model="syncForm.range" type="datetimerange" value-format="YYYY-MM-DDTHH:mm:ssZ" />
        </el-form-item>
        <el-form-item label="账号"><el-input v-model="syncForm.account" placeholder="留空用成本源账号" /></el-form-item>
        <el-form-item label="区域"><el-input v-model="syncForm.region" placeholder="留空为全部区域" /></el-form-item>
        <el-form-item label="增量">
          <el-switch v-model="syncForm.incremental" />
          <span class="hint" style="margin-left: 8px">按厂商侧更新时间拉取变更，而不是整窗重拉</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="syncVisible = false">取消</el-button>
        <el-button type="primary" :loading="syncSaving" @click="doSync">开始同步</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 14px;
}
.section {
  margin: 20px 0 10px;
  font-size: 15px;
  font-weight: 650;
}
.hint {
  font-size: 12px;
  color: var(--ferry-text-muted);
}
</style>
