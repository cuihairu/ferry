<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, type Node, type LandingAssignment, type TransportRow } from '../api'
import { formatDate } from '../utils/format'

// E-20：手动落地分配——为入口节点或区域整体指定落地与权重。
// 分配落库留痕、释放不删行；经 config.push 下发落地列表的生效链路
// 随 agent relay 配置化（E-5）接入，当前以分配记录为准。
// E-17：区域传输判定面板——哪种活着用哪种的换线建议。

const rows = ref<LandingAssignment[]>([])
const nodes = ref<Node[]>([])
const txRows = ref<TransportRow[]>([])
const loading = ref(false)
const scope = ref<'active' | 'all'>('active')

async function load() {
  loading.value = true
  try {
    const [ls, ns, ts] = await Promise.all([
      get<LandingAssignment[]>(`/api/landings?scope=${scope.value}`),
      get<Node[]>('/api/nodes'),
      get<TransportRow[]>('/api/transport-status'),
    ])
    rows.value = ls
    nodes.value = ns
    txRows.value = ts
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

function txText(r: TransportRow): string {
  return r.transports.map((t) => `${t.transport} ${t.alive}/${t.total}`).join(' · ')
}

const nodeById = computed(() => new Map(nodes.value.map((n) => [n.id, n])))

function nodeName(id: number | null): string {
  if (!id) return '—'
  return nodeById.value.get(id)?.name ?? `#${id}`
}

const entries = computed(() => nodes.value.filter((n) => n.role === 'entry' || n.role === 'both'))
const landings = computed(() => nodes.value.filter((n) => n.role === 'landing' || n.role === 'both'))
const regions = computed(() => [...new Set(entries.value.map((n) => n.region).filter(Boolean))])

// ---- 新建 ----
const dialogVisible = ref(false)
const saving = ref(false)
const form = reactive({
  scopeMode: 'entry' as 'entry' | 'region',
  entryNodeId: null as number | null,
  region: '',
  landingNodeId: null as number | null,
  direction: 'out' as 'out' | 'in',
  weight: 0,
  reason: '',
})

function openCreate() {
  Object.assign(form, {
    scopeMode: 'entry', entryNodeId: null, region: '',
    landingNodeId: null, direction: 'out', weight: 0, reason: '',
  })
  dialogVisible.value = true
}

async function save() {
  if (form.scopeMode === 'entry' && !form.entryNodeId) {
    ElMessage.warning('请选择入口节点')
    return
  }
  if (form.scopeMode === 'region' && !form.region.trim()) {
    ElMessage.warning('请填写区域')
    return
  }
  if (!form.landingNodeId) {
    ElMessage.warning('请选择落地节点')
    return
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      landing_node_id: form.landingNodeId,
      direction: form.direction,
      weight: form.weight,
      reason: form.reason.trim() || '手动分配',
    }
    if (form.scopeMode === 'entry') body.entry_node_id = form.entryNodeId
    else body.region = form.region.trim()
    await post('/api/landings', body)
    dialogVisible.value = false
    ElMessage.success('已分配')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function tweakWeight(row: LandingAssignment) {
  const { value } = await ElMessageBox.prompt(
    `调整 ${nodeName(row.entry_node_id) !== '—' ? nodeName(row.entry_node_id) : row.region} → ${nodeName(row.landing_node_id)} 的权重`,
    '调整权重',
    { inputValue: String(row.weight), inputPattern: /^\d+$/, inputErrorMessage: '请输入非负整数' },
  ).catch(() => ({ value: null }))
  if (value === null) return
  try {
    await put(`/api/landings/${row.id}`, { weight: Number(value) })
    ElMessage.success('已调整')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function release(row: LandingAssignment) {
  await ElMessageBox.confirm(
    `释放该分配后落地恢复自动兜底，确认？`,
    '释放分配',
    { type: 'warning' },
  )
  try {
    await del(`/api/landings/${row.id}`)
    ElMessage.success('已释放')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

const DIR_TEXT: Record<string, string> = { out: '出海', in: '回国' }
</script>

<template>
  <div class="page">
    <h2>落地分配</h2>
    <p class="page-desc">
      手动为入口节点或区域指定落地与权重；释放保留分配史。生效链路（relay 配置推送）随 agent relay 配置化接入。
    </p>

    <div class="toolbar">
      <el-radio-group v-model="scope" @change="load">
        <el-radio-button value="active">生效中</el-radio-button>
        <el-radio-button value="all">含历史</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="openCreate">新建分配</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="入口 / 区域" min-width="140">
        <template #default="{ row }">
          <el-tag v-if="row.entry_node_id" size="small" effect="plain" disable-transitions>
            节点 · {{ nodeName(row.entry_node_id) }}
          </el-tag>
          <el-tag v-else size="small" type="warning" effect="plain" disable-transitions>
            区域 · {{ row.region }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="落地" min-width="130">
        <template #default="{ row }">{{ nodeName(row.landing_node_id) }}</template>
      </el-table-column>
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column prop="weight" label="权重" width="80" />
      <el-table-column prop="strategy" label="策略" width="90" />
      <el-table-column prop="reason" label="缘由" min-width="110" />
      <el-table-column label="分配时间" width="150">
        <template #default="{ row }">{{ formatDate(row.assigned_at) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag v-if="!row.released_at" size="small" type="success" disable-transitions>生效中</el-tag>
          <el-tooltip v-else :content="row.release_reason || '已释放'">
            <el-tag size="small" type="info" disable-transitions>已释放</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <template v-if="!row.released_at">
            <el-button link type="primary" @click="tweakWeight(row)">权重</el-button>
            <el-button link type="danger" @click="release(row)">释放</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>

    <h3 class="section">区域传输判定（E-17）</h3>
    <p class="section-desc">区域内各传输形态的探测存活聚合，「哪种活着用哪种」；建议与现行不一致时给出换线提示，切换 = 改节点传输标注并重推配置。</p>
    <el-table :data="txRows" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="region" label="区域" width="120" />
      <el-table-column label="各传输存活" min-width="200">
        <template #default="{ row }">{{ txText(row) }}</template>
      </el-table-column>
      <el-table-column prop="current" label="现行" width="100" />
      <el-table-column label="建议" width="100">
        <template #default="{ row }">{{ row.recommended }}</template>
      </el-table-column>
      <el-table-column label="换线" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.switchneeded" type="warning" size="small" effect="dark" disable-transitions>建议换线</el-tag>
          <span v-else class="tx-ok">—</span>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" title="新建落地分配" width="500px">
      <el-form label-width="90px">
        <el-form-item label="分配给">
          <el-radio-group v-model="form.scopeMode">
            <el-radio-button value="entry">入口节点</el-radio-button>
            <el-radio-button value="region">区域整体</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.scopeMode === 'entry'" label="入口">
          <el-select v-model="form.entryNodeId" style="width: 100%" placeholder="选择入口节点">
            <el-option v-for="n in entries" :key="n.id" :value="n.id" :label="`${n.name}（${n.region}）`" />
          </el-select>
        </el-form-item>
        <el-form-item v-else label="区域">
          <el-select v-model="form.region" style="width: 100%" placeholder="选择或输入区域" filterable allow-create>
            <el-option v-for="rg in regions" :key="rg" :value="rg" :label="rg" />
          </el-select>
        </el-form-item>
        <el-form-item label="落地">
          <el-select v-model="form.landingNodeId" style="width: 100%" placeholder="选择落地节点">
            <el-option v-for="n in landings" :key="n.id" :value="n.id" :label="`${n.name}（${n.region}）`" />
          </el-select>
        </el-form-item>
        <el-form-item label="方向">
          <el-radio-group v-model="form.direction">
            <el-radio-button value="out">出海</el-radio-button>
            <el-radio-button value="in">回国</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="权重">
          <el-input-number v-model="form.weight" :min="0" :max="1000" />
        </el-form-item>
        <el-form-item label="缘由">
          <el-input v-model="form.reason" maxlength="64" placeholder="留痕用，如：晚高峰切优质线" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">分配</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 16px;
  display: flex;
  gap: 12px;
  align-items: center;
}
.toolbar .el-button--primary {
  margin-left: auto;
}
.section {
  margin: 28px 0 4px;
  font-size: 15px;
  font-weight: 650;
}
.section-desc {
  margin: 0 0 12px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.tx-ok {
  color: var(--ferry-text-muted);
}
</style>
