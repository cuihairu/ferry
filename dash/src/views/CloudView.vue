<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  bindCloudResource, cancelCloudOperation, checkCloudNodeBoot, createCloudOperation,
  createProvider, deleteProvider, drainCloudNode, getCloudOperation, getCloudOperations,
  getCloudPathHealth, getCloudResources, getCloudResource, getCloudShutdownCheck,
  getCloudSyncLogs, get, getProviders, linkCloudResource, post, testProvider,
  unbindCloudResource, updateProvider, syncProvider,
  type CloudOperation, type CloudOperationLog, type CloudProvider, type CloudResource,
  type CloudSyncLog, type Node,
} from '../api'

// Cloud resource management (F-0411~F-0432): provider accounts, resources and
// operation tasks. Secrets are never echoed back; a blank key on update keeps
// the stored credential. Vendor success alone never marks a Ferry Node healthy
// (F-0397), so the resource page exposes the controlled refresh and the Agent
// checks separately. Last-sync timestamps make a stale vendor view visible
// (F-0432).

const route = useRoute()
const providers = ref<CloudProvider[]>([])
const resources = ref<CloudResource[]>([])
const nodes = ref<Node[]>([])
const syncLogs = ref<CloudSyncLog[]>([])
const operations = ref<CloudOperation[]>([])
const loading = ref(false)
const tab = ref<'overview' | 'operations'>('overview')

// Resource filters (F-0416~F-0419): the server owns provider/region/state
// filtering; account and keyword filtering are applied on the fetched rows.
const providerFilter = ref<number | null>(null)
const accountFilter = ref('')
const regionFilter = ref('')
const stateFilter = ref('')
const keyword = ref('')

async function load() {
  loading.value = true
  try {
    const [ps, ns, ls, ops] = await Promise.all([
      getProviders(), get<Node[]>('/api/nodes'), getCloudSyncLogs(), getCloudOperations(),
    ])
    providers.value = ps
    nodes.value = ns
    syncLogs.value = ls
    operations.value = ops
    await loadResources()
    // F-0431: a Node detail deep link lands on its bound resource.
    const nodeId = Number(route.query.node_id)
    if (nodeId) {
      const bound = resources.value.find((r) => r.node_id === nodeId)
      if (bound) await openResource(bound)
    }
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}

async function loadResources() {
  const params: { provider_id?: number; region?: string; state?: string } = {}
  if (providerFilter.value) params.provider_id = providerFilter.value
  if (regionFilter.value) params.region = regionFilter.value
  if (stateFilter.value) params.state = stateFilter.value
  resources.value = await getCloudResources(params)
}
onMounted(load)

const filteredResources = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return resources.value.filter((r) => {
    const provider = providers.value.find((p) => p.id === r.provider_id)
    if (accountFilter.value && provider?.name !== accountFilter.value) return false
    if (kw && ![r.name, r.external_id, r.public_ip].some((v) => v.toLowerCase().includes(kw))) return false
    return true
  })
})

function providerName(id: number): string {
  return providers.value.find((p) => p.id === id)?.name ?? String(id)
}
function nodeName(id: number | null | undefined): string {
  if (!id) return '—'
  return nodes.value.find((n) => n.id === id)?.name ?? String(id)
}
function lastSync(row: CloudResource): string {
  return row.last_sync_at ?? row.last_seen_at ?? ''
}
function stale(row: CloudResource): boolean {
  const at = new Date(lastSync(row)).getTime()
  return !at || Date.now() - at > 10 * 60 * 1000
}

// ---- Provider accounts (F-0412~F-0414) ----
const provDialog = ref(false)
const provEditing = ref<CloudProvider | null>(null)
const provForm = ref({ name: '', type: 'vultr', access_key: '', note: '', region: '' })

function newProvider() {
  provEditing.value = null
  provForm.value = { name: '', type: 'vultr', access_key: '', note: '', region: '' }
  provDialog.value = true
}
function editProvider(row: CloudProvider) {
  provEditing.value = row
  provForm.value = { name: row.name, type: row.type, access_key: '', note: row.note ?? '', region: row.region ?? '' }
  provDialog.value = true
}
async function saveProvider() {
  try {
    if (provEditing.value) {
      const body: Record<string, string | boolean> = {
        name: provForm.value.name, type: provForm.value.type,
        note: provForm.value.note, region: provForm.value.region,
      }
      if (provForm.value.access_key) body.access_key = provForm.value.access_key
      await updateProvider(provEditing.value.id, body)
      ElMessage.success('账号已更新')
    } else {
      await createProvider({ name: provForm.value.name, type: provForm.value.type,
        access_key: provForm.value.access_key, note: provForm.value.note, region: provForm.value.region })
      ElMessage.success('账号已创建')
    }
    provDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function testConnection(row: CloudProvider) {
  try {
    const out = await testProvider(row.id)
    if (out.ok) ElMessage.success(out.message || `连接正常（${out.mode}）`)
    else ElMessage.error(out.error || '连接失败')
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function syncResources(row: CloudProvider) {
  try {
    const out = await syncProvider(row.id)
    if (!out.ok) {
      ElMessage.error('同步失败')
    } else {
      ElMessage.success(`新增 ${out.added} · 更新 ${out.updated} · 丢失 ${out.missing}`)
    }
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function removeProvider(row: CloudProvider) {
  try {
    await ElMessageBox.confirm(`删除账号「${row.name}」？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteProvider(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Resource detail and binding (F-0420~F-0422/F-0430) ----
const detailVisible = ref(false)
const detail = ref<CloudResource | null>(null)
async function openResource(row: CloudResource) {
  detail.value = await getCloudResource(row.id)
  detailVisible.value = true
}
const bindVisible = ref(false)
const bindRow = ref<CloudResource | null>(null)
const bindNodeId = ref<number | null>(null)
function openBind(row: CloudResource) {
  bindRow.value = row
  bindNodeId.value = row.node_id ?? null
  bindVisible.value = true
}
async function saveBind() {
  if (!bindRow.value || !bindNodeId.value) {
    ElMessage.warning('请选择节点')
    return
  }
  try {
    await bindCloudResource(bindRow.value.id, bindNodeId.value)
    ElMessage.success('已绑定')
    bindVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function unbind(row: CloudResource) {
  try {
    await ElMessageBox.confirm(`解除「${row.name}」与节点的绑定？`, '解绑确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await unbindCloudResource(row.id)
    ElMessage.success('已解绑')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function refreshLink(row: CloudResource) {
  try {
    await linkCloudResource(row.id)
    ElMessage.success('已按厂商真实状态刷新')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// F-0430: create a Ferry Node from a resource, then bind them. The address
// comes from the resource public IP; other registration fields are set on the
// Nodes page after the node registers.
const createVisible = ref(false)
const createRow = ref<CloudResource | null>(null)
const createForm = ref({ name: '', address: '', region: '' })
function openCreateNode(row: CloudResource) {
  createRow.value = row
  createForm.value = { name: row.name || row.external_id, address: row.public_ip, region: row.region }
  createVisible.value = true
}
async function createNodeFromResource() {
  if (!createRow.value || !createForm.value.name.trim() || !createForm.value.address.trim()) {
    ElMessage.warning('名称与公网 IP 必填')
    return
  }
  try {
    const created = await post<Node>('/api/nodes', {
      name: createForm.value.name.trim(), address: createForm.value.address.trim(),
      port: 443, protocol: 'vless', config: '{}', enabled: true,
      region: createForm.value.region, provider_id: createRow.value.provider_id,
    })
    await bindCloudResource(createRow.value.id, created.id)
    ElMessage.success('节点已创建并绑定')
    createVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Operations (F-0423~F-0429) ----
const opVisible = ref(false)
const opRows = ref<CloudResource[]>([])
const opForm = ref({ op: 'power_on', confirmed: false })
const opBusy = ref(false)
const HIGH_RISK = ['power_off', 'force_stop', 'rebuild']
const OP_TEXT: Record<string, string> = {
  power_on: '开机', power_off: '关机', reboot: '重启', force_stop: '强制停止', rebuild: '重装',
}
const STATE_TYPE: Record<string, 'success' | 'warning' | 'danger' | 'info'> = {
  succeeded: 'success', running: 'warning', pending: 'info', cancelled: 'info',
  failed: 'danger', unknown: 'warning',
}

function openOperation(row: CloudResource, op: string) {
  opRows.value = [row]
  opForm.value = { op, confirmed: false }
  opVisible.value = true
}
function openBatchOperation(op: string) {
  const picked = filteredResources.value.filter((r) => r.state !== 'missing')
  if (!picked.length) {
    ElMessage.warning('没有可操作的资源')
    return
  }
  opRows.value = picked
  opForm.value = { op, confirmed: false }
  opVisible.value = true
}
async function submitOperation() {
  if (opBusy.value || !opRows.value.length) return
  const risky = HIGH_RISK.includes(opForm.value.op)
  if (risky && !opForm.value.confirmed) {
    ElMessage.warning('高风险操作需要二次确认')
    return
  }
  if (risky) {
    try {
      await ElMessageBox.confirm(
        `即将对 ${opRows.value.length} 个资源执行「${OP_TEXT[opForm.value.op] ?? opForm.value.op}」，此操作有风险，确认继续？`,
        '高风险确认', { type: 'warning' },
      )
    } catch {
      return
    }
  }
  opBusy.value = true
  try {
    const out = await createCloudOperation({
      provider_id: opRows.value[0].provider_id, op: opForm.value.op,
      specs: opRows.value.map((r) => ({ resource_id: r.id })), confirmed: opForm.value.confirmed,
    })
    ElMessage.success(`已提交 ${out.count} 个任务`)
    opVisible.value = false
    tab.value = 'operations'
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    opBusy.value = false
  }
}

const opDetailVisible = ref(false)
const opDetail = ref<CloudOperation | null>(null)
const opLogs = ref<CloudOperationLog[]>([])
async function openOperationDetail(id: number) {
  const out = await getCloudOperation(id)
  opDetail.value = out.operation
  opLogs.value = out.logs
  opDetailVisible.value = true
}
async function cancelOperation(row: CloudOperation) {
  try {
    await cancelCloudOperation(row.id)
    ElMessage.success('已取消')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Cloud ↔ Node linkage checks (F-0399~F-0403) ----
async function shutdownCheck(row: CloudResource) {
  if (!row.node_id) {
    ElMessage.warning('该资源未绑定节点')
    return
  }
  try {
    const out = await getCloudShutdownCheck(row.node_id)
    if (out.risk) {
      await ElMessageBox.confirm(
        `节点「${nodeName(row.node_id)}」近期有用户流量：${out.message}。请先摘流再关机。`,
        '关机风险提示', { type: 'warning', confirmButtonText: '仍要继续' },
      )
    } else {
      ElMessage.success('近期无用户流量，可以关机')
    }
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function drain(row: CloudResource) {
  if (!row.node_id) {
    ElMessage.warning('该资源未绑定节点')
    return
  }
  try {
    await drainCloudNode(row.node_id, 'cloud maintenance window')
    ElMessage.success('节点已摘流并进入维护窗口')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function bootCheck(row: CloudResource) {
  if (!row.node_id) {
    ElMessage.warning('该资源未绑定节点')
    return
  }
  try {
    const out = await checkCloudNodeBoot(row.node_id)
    const paths = await getCloudPathHealth(row.node_id)
    const count = Object.keys(paths.paths ?? {}).length
    if (out.healthy) ElMessage.success(`Agent 正常，${count} 条线路已复核`)
    else ElMessage.warning(`Agent 未恢复（online=${out.agent_online} heartbeat=${out.heartbeat_fresh} config=${out.config_valid} crash=${out.crash_alert}）`)
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
</script>

<template>
  <div class="page">
    <h2>云资源</h2>
    <p class="page-desc">云账号、实例、操作任务与 Ferry Node 联动；厂商返回成功不等于节点可用，需 Agent 检查后恢复调度。</p>

    <el-tabs v-model="tab">
      <el-tab-pane label="账号与资源" name="overview">
        <div class="head-row">
          <h3 class="section">云账号</h3>
          <el-button type="primary" size="small" @click="newProvider">录入账号</el-button>
        </div>
        <el-table :data="providers" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="name" label="名称" min-width="130" />
          <el-table-column prop="type" label="类型" width="120" />
          <el-table-column prop="region" label="区域范围" width="110" />
          <el-table-column label="凭据" width="100">
            <template #default="{ row }">
              <el-tag :type="row.has_access_key ? 'success' : 'info'" size="small" effect="plain">
                {{ row.has_access_key ? '已录入' : '未录入' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="启用" width="80">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="plain">{{ row.enabled ? '启用' : '停用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="260" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="testConnection(row)">测试连接</el-button>
              <el-button link type="primary" size="small" :loading="loading" @click="syncResources(row)">同步</el-button>
              <el-button link type="primary" size="small" @click="editProvider(row)">编辑</el-button>
              <el-button link type="danger" size="small" @click="removeProvider(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>

        <div class="head-row">
          <h3 class="section">云资源</h3>
          <div class="row2">
            <el-button size="small" :disabled="!filteredResources.length" @click="openBatchOperation('power_on')">批量开机</el-button>
            <el-button size="small" :disabled="!filteredResources.length" @click="openBatchOperation('reboot')">批量重启</el-button>
            <el-button size="small" :disabled="!filteredResources.length" @click="openBatchOperation('power_off')">批量关机</el-button>
          </div>
        </div>
        <div class="filters">
          <el-select v-model="providerFilter" placeholder="Provider 类型" clearable style="width: 150px" @change="loadResources">
            <el-option v-for="p in providers" :key="p.id" :value="p.id" :label="`${p.name}（${p.type}）`" />
          </el-select>
          <el-select v-model="accountFilter" placeholder="账号" clearable style="width: 140px">
            <el-option v-for="p in providers" :key="p.id" :value="p.name" :label="p.name" />
          </el-select>
          <el-input v-model="regionFilter" placeholder="区域" clearable style="width: 120px" @keyup.enter="loadResources" />
          <el-select v-model="stateFilter" placeholder="实例状态" clearable style="width: 120px" @change="loadResources">
            <el-option v-for="s in ['running', 'stopped', 'starting', 'stopping', 'rebuilding', 'error']" :key="s" :value="s" :label="s" />
          </el-select>
          <el-input v-model="keyword" placeholder="名称 / ID / IP" clearable style="width: 180px" />
          <el-button size="small" @click="loadResources">刷新</el-button>
        </div>
        <el-table :data="filteredResources" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="name" label="实例" min-width="140">
            <template #default="{ row }">
              <el-button link type="primary" @click="openResource(row)">{{ row.name || row.external_id }}</el-button>
              <el-tag v-if="row.missing" type="danger" size="small" effect="dark" disable-transitions>资源丢失</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="账号" width="120">
            <template #default="{ row }">{{ providerName(row.provider_id) }}</template>
          </el-table-column>
          <el-table-column prop="region" label="区域" width="90" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.state === 'running' ? 'success' : row.state === 'error' ? 'danger' : 'info'" effect="plain">
                {{ row.state || 'unknown' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="public_ip" label="公网 IP" width="130" />
          <el-table-column prop="spec" label="规格" width="100" />
          <el-table-column label="带宽" width="90">
            <template #default="{ row }">{{ row.bw_mbps ? `${row.bw_mbps} Mbps` : '—' }}</template>
          </el-table-column>
          <el-table-column label="绑定节点" width="120">
            <template #default="{ row }">
              <el-button v-if="row.node_id" link type="primary" @click="openResource(row)">{{ nodeName(row.node_id) }}</el-button>
              <span v-else class="dim">未绑定</span>
            </template>
          </el-table-column>
          <el-table-column label="最后同步" width="150">
            <template #default="{ row }">
              <span :class="{ dim: stale(row) }">{{ lastSync(row) || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="330" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openOperation(row, 'power_on')">开机</el-button>
              <el-button link type="warning" size="small" @click="openOperation(row, 'power_off')">关机</el-button>
              <el-button link type="primary" size="small" @click="openOperation(row, 'reboot')">重启</el-button>
              <el-button link type="info" size="small" @click="refreshLink(row)">刷新状态</el-button>
              <el-button v-if="row.node_id" link type="warning" size="small" @click="shutdownCheck(row)">关机检查</el-button>
              <el-button v-if="row.node_id" link type="warning" size="small" @click="drain(row)">摘流</el-button>
              <el-button v-if="row.node_id" link type="success" size="small" @click="bootCheck(row)">开机检查</el-button>
              <el-button v-if="row.node_id" link type="primary" size="small" @click="unbind(row)">解绑</el-button>
              <el-button v-else link type="primary" size="small" @click="openBind(row)">绑定</el-button>
              <el-button v-if="!row.node_id" link type="primary" size="small" @click="openCreateNode(row)">创建节点</el-button>
            </template>
          </el-table-column>
        </el-table>

        <div class="head-row">
          <h3 class="section">同步历史</h3>
        </div>
        <el-table :data="syncLogs.slice(0, 30)" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="id" label="#" width="60" />
          <el-table-column label="账号" width="120">
            <template #default="{ row }">{{ providerName(row.provider_id) }}</template>
          </el-table-column>
          <el-table-column prop="trigger" label="触发" width="90" />
          <el-table-column label="结果" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.ok ? 'success' : 'danger'" effect="plain">{{ row.ok ? '成功' : '失败' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="新增 / 更新 / 丢失" width="150">
            <template #default="{ row }">{{ row.added }} / {{ row.updated }} / {{ row.missing }}</template>
          </el-table-column>
          <el-table-column prop="error" label="错误" min-width="180" />
          <el-table-column prop="finished_at" label="完成时间" min-width="170" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="操作任务" name="operations">
        <div class="head-row">
          <h3 class="section">任务列表</h3>
          <el-button size="small" @click="load">刷新</el-button>
        </div>
        <el-table :data="operations" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="id" label="#" width="60" />
          <el-table-column label="资源" min-width="140">
            <template #default="{ row }">
              <el-button link type="primary" @click="openOperationDetail(row.id)">
                {{ resources.find((r) => r.id === row.resource_id)?.name ?? row.resource_id }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column prop="op" label="操作" width="110">
            <template #default="{ row }">{{ OP_TEXT[row.op] ?? row.op }}</template>
          </el-table-column>
          <el-table-column label="批次" min-width="130">
            <template #default="{ row }">{{ row.batch_id || '—' }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="STATE_TYPE[row.state] ?? 'info'" effect="plain">{{ row.state }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="actor" label="操作者" width="110" />
          <el-table-column label="结果" min-width="150">
            <template #default="{ row }">{{ row.result || row.error || row.provider_code || '—' }}</template>
          </el-table-column>
          <el-table-column prop="created_at" label="提交时间" min-width="170" />
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openOperationDetail(row.id)">日志</el-button>
              <el-button v-if="row.allow_cancel" link type="warning" size="small" @click="cancelOperation(row)">取消</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="provDialog" :title="provEditing ? '编辑云账号' : '录入云账号'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="provForm.name" /></el-form-item>
        <el-form-item label="类型"><el-input v-model="provForm.type" /></el-form-item>
        <el-form-item label="区域"><el-input v-model="provForm.region" placeholder="留空表示整个账号" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="provForm.note" /></el-form-item>
        <el-form-item :label="provEditing ? '新凭据' : '凭据'">
          <el-input v-model="provForm.access_key" type="password" show-password :placeholder="provEditing ? '留空保持原凭据' : '必填'" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="provDialog = false">取消</el-button>
        <el-button type="primary" @click="saveProvider">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailVisible" :title="detail?.name || '云资源详情'" width="640px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="外部 ID">{{ detail.external_id }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.state }}</el-descriptions-item>
        <el-descriptions-item label="区域">{{ detail.region }}</el-descriptions-item>
        <el-descriptions-item label="规格">{{ detail.spec || '—' }}</el-descriptions-item>
        <el-descriptions-item label="公网 IP">{{ detail.public_ip || '—' }}</el-descriptions-item>
        <el-descriptions-item label="带宽">{{ detail.bw_mbps ? `${detail.bw_mbps} Mbps` : '—' }}</el-descriptions-item>
        <el-descriptions-item label="计费信息">{{ detail.tags || '—' }}</el-descriptions-item>
        <el-descriptions-item label="绑定节点">{{ nodeName(detail.node_id) }}</el-descriptions-item>
        <el-descriptions-item label="最后同步">{{ lastSync(detail) || '—' }}</el-descriptions-item>
        <el-descriptions-item label="资源丢失">{{ detail.missing ? '是' : '否' }}</el-descriptions-item>
      </el-descriptions>
      <template #footer><el-button @click="detailVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="bindVisible" title="绑定到 Ferry Node" width="420px">
      <el-form label-width="80px">
        <el-form-item label="节点">
          <el-select v-model="bindNodeId" filterable placeholder="选择节点" style="width: 100%">
            <el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="`${n.name}（${n.address}）`" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="bindVisible = false">取消</el-button>
        <el-button type="primary" @click="saveBind">绑定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="createVisible" title="从云资源创建节点" width="460px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="createForm.name" /></el-form-item>
        <el-form-item label="公网 IP"><el-input v-model="createForm.address" /></el-form-item>
        <el-form-item label="区域"><el-input v-model="createForm.region" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" @click="createNodeFromResource">创建并绑定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="opVisible" :title="`提交${OP_TEXT[opForm.op] ?? opForm.op}`" width="520px">
      <p class="op-hint">目标资源 {{ opRows.length }} 个，提交后由任务系统异步执行，结果在「操作任务」页查看。</p>
      <el-table :data="opRows" max-height="260">
        <el-table-column prop="name" label="实例" min-width="140" />
        <el-table-column prop="external_id" label="外部 ID" min-width="140" />
        <el-table-column prop="state" label="当前状态" width="100" />
      </el-table>
      <el-checkbox v-model="opForm.confirmed" style="margin-top: 12px">
        我已确认风险，同意执行{{ HIGH_RISK.includes(opForm.op) ? '（高风险操作，需二次确认）' : '' }}
      </el-checkbox>
      <template #footer>
        <el-button @click="opVisible = false">取消</el-button>
        <el-button type="primary" :loading="opBusy" @click="submitOperation">提交</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="opDetailVisible" title="任务详情" width="620px">
      <el-descriptions v-if="opDetail" :column="2" border>
        <el-descriptions-item label="任务 ID">{{ opDetail.id }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ opDetail.state }}</el-descriptions-item>
        <el-descriptions-item label="操作">{{ OP_TEXT[opDetail.op] ?? opDetail.op }}</el-descriptions-item>
        <el-descriptions-item label="批次">{{ opDetail.batch_id || '—' }}</el-descriptions-item>
        <el-descriptions-item label="结果">{{ opDetail.result || '—' }}</el-descriptions-item>
        <el-descriptions-item label="错误">{{ opDetail.error || opDetail.provider_code || '—' }}</el-descriptions-item>
        <el-descriptions-item label="操作者">{{ opDetail.actor }}</el-descriptions-item>
        <el-descriptions-item label="完成时间">{{ opDetail.finished_at || '—' }}</el-descriptions-item>
      </el-descriptions>
      <h4 class="section">执行日志</h4>
      <el-table :data="opLogs" max-height="240">
        <el-table-column prop="created_at" label="时间" width="170" />
        <el-table-column prop="state" label="状态" width="100" />
        <el-table-column prop="message" label="说明" min-width="240" />
      </el-table>
      <template #footer><el-button @click="opDetailVisible = false">关闭</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-desc {
  color: var(--ferry-text-dim);
  font-size: 13px;
  margin-bottom: 16px;
}
.head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 18px 0 10px;
}
.head-row .section,
.section {
  font-size: 15px;
  font-weight: 650;
}
.row2 {
  display: flex;
  gap: 8px;
}
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 10px 0;
}
.dim {
  color: var(--ferry-text-muted);
}
.op-hint {
  color: var(--ferry-text-dim);
  font-size: 13px;
  margin: 0 0 10px;
}
</style>
