<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, resolveAlert, upgradeNode, type Node, type DimensionStatus, type NodeShare, type BatchProcResult, type BatchConfigResult, type Alert, type NodeLoad } from '../api'
import QRCode from 'qrcode'
import RuleLibDialog from '../components/RuleLibDialog.vue'

// E-24：节点视图——节点列表（入口/落地筛选 + 方向/线路列）、
// 区域/运营商视角（状态灯 + 节点计数，两者同构）。协议/配置编辑见对话框。

const PROTOCOLS = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'shadowsocks', label: 'Shadowsocks' },
]

const ROLES = [
  { value: 'entry', label: '入口' },
  { value: 'landing', label: '落地' },
  { value: 'both', label: '入口+落地' },
]

const DIRECTIONS = [
  { value: 'out', label: '出海' },
  { value: 'in', label: '回国' },
  { value: 'both', label: '双向' },
]

const TRANSPORTS = ['tls', 'quic', 'ws-tls', 'ssh']

const STATUS_TYPE: Record<string, 'success' | 'danger' | 'info' | 'warning'> = {
  online: 'success',
  offline: 'info',
  unknown: 'warning',
  provisioning: 'warning',
}

const ROLE_TEXT: Record<string, string> = { entry: '入口', landing: '落地', both: '入口+落地' }
const ROLE_TYPE: Record<string, 'primary' | 'warning' | 'info'> = { entry: 'primary', landing: 'warning', both: 'info' }
const DIR_TEXT: Record<string, string> = { out: '出海', in: '回国', both: '双向' }

// ---- 数据 ----
const nodes = ref<Node[]>([])
const dims = ref<DimensionStatus[]>([])
const alerts = ref<Alert[]>([])
const loading = ref(false)
const view = ref<'nodes' | 'region' | 'isp'>('nodes')
const roleFilter = ref<'all' | 'entry' | 'landing' | 'both'>('all')

const filtered = computed(() =>
  roleFilter.value === 'all' ? nodes.value : nodes.value.filter((n) => n.role === roleFilter.value || n.role === 'both'),
)

async function load() {
  loading.value = true
  try {
    const [ns, ds, as, ls] = await Promise.all([
      get<Node[]>('/api/nodes'),
      get<DimensionStatus[]>('/api/dimension-status'),
      get<Alert[]>('/api/alerts?limit=200'),
      get<NodeLoad[]>('/api/load'),
    ])
    nodes.value = ns
    dims.value = ds
    alerts.value = as
    loads.value = ls
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 负载看板（E-25）：连接数/实测吞吐/带宽利用率，超 80% 红标 ----
const loads = ref<NodeLoad[]>([])

function loadOf(id: number): NodeLoad | undefined {
  return loads.value.find((l) => l.node_id === id)
}

// loadCell 负载列视图模型：无采样 has=false 展示「—」。
function loadCell(id: number): { has: boolean; pct: number; over: boolean; status: 'success' | 'warning' | 'exception'; text: string } {
  const l = loadOf(id)
  if (!l || l.util_pct < 0) {
    return { has: false, pct: 0, over: false, status: 'success', text: '' }
  }
  return {
    has: true,
    pct: Math.min(l.util_pct, 100),
    over: l.util_pct > 80,
    status: l.util_pct > 80 ? 'exception' : l.util_pct > 60 ? 'warning' : 'success',
    text: `${l.conns} 连接 · ${Math.round(l.mbps)} Mbps / ${l.capacity_mbps} Mbps`,
  }
}

async function togglePool(row: Node) {
  const suspending = row.pool_state !== 'suspended'
  if (suspending) {
    await ElMessageBox.confirm(
      `摘除后 ${row.name} 立即退出订阅入口池（agent 连接保持），确认？`,
      '手动摘除',
      { type: 'warning' },
    )
  }
  try {
    await post(`/api/pool/${row.id}/${suspending ? 'suspend' : 'resume'}`)
    ElMessage.success(suspending ? '已摘除' : '已复位')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 告警（A-22）：活跃告警计数与节点映射 ----
const activeAlerts = computed(() => alerts.value.filter((a) => a.state === 'active'))
const alertCountByNode = computed(() => {
  const m = new Map<number, number>()
  for (const a of activeAlerts.value) m.set(a.node_id, (m.get(a.node_id) ?? 0) + 1)
  return m
})
const nodeName = computed(() => new Map(nodes.value.map((n) => [n.id, n.name])))

const ALERT_KIND_TEXT: Record<string, string> = {
  proc_crash: '进程崩溃',
  cert_expiry: '证书临期',
  high_load: '高负载',
  config_error: '配置错误',
}
const ALERT_SEV_TYPE: Record<string, 'warning' | 'danger' | 'info'> = {
  critical: 'danger',
  warning: 'warning',
}

// 告警对话框：活跃在前，可过滤状态、手动处理。
const alertVisible = ref(false)
const alertFilter = ref<'active' | 'resolved' | 'all'>('active')
const alertBusy = ref(false)

const alertRows = computed(() => {
  const rows = alertFilter.value === 'all' ? alerts.value : alerts.value.filter((a) => a.state === alertFilter.value)
  return rows.map((a) => ({
    ...a,
    node_name: nodeName.value.get(a.node_id) ?? String(a.node_id),
    kind_text: ALERT_KIND_TEXT[a.kind] ?? a.kind,
  }))
})

async function resolveOne(a: Alert) {
  alertBusy.value = true
  try {
    await resolveAlert(a.id)
    a.state = 'resolved'
    a.resolved_at = new Date().toISOString()
    ElMessage.success('已处理')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    alertBusy.value = false
  }
}

// ---- 视角聚合（区域/运营商同构）----
interface DimCard {
  key: string
  state: string
  reason: string
  total: number
  online: number
}

function dimCards(scope: 'region' | 'isp'): DimCard[] {
  const keys = new Set<string>()
  for (const n of nodes.value) {
    const k = scope === 'region' ? n.region : n.isp
    if (k) keys.add(k)
  }
  const cards: DimCard[] = []
  for (const key of keys) {
    const st = dims.value.find((d) => d.scope === scope && d.key === key)
    const ns = nodes.value.filter((n) => (scope === 'region' ? n.region : n.isp) === key)
    cards.push({
      key,
      state: st?.state ?? 'unknown',
      reason: st?.reason ?? '',
      total: ns.length,
      online: ns.filter((n) => n.status === 'online' && n.enabled).length,
    })
  }
  return cards.sort((a, b) => a.key.localeCompare(b.key, 'zh'))
}

const STATE_DOT: Record<string, string> = {
  healthy: 'dot ok',
  degraded: 'dot warn',
  failed: 'dot bad',
  unknown: 'dot',
}
const STATE_TEXT: Record<string, string> = {
  healthy: '正常',
  degraded: '劣化',
  failed: '故障',
  unknown: '无数据',
}

// ---- 编辑 ----
const dialogVisible = ref(false)
const ruleLibFor = ref<Node | null>(null)
const ruleLibVisible = ref(false)
const editing = ref<Node | null>(null)
const saving = ref(false)
const form = reactive({
  name: '',
  address: '',
  port: 443,
  protocol: 'vless',
  config: '{}',
  enabled: true,
  role: 'landing',
  direction: 'out',
  region: '',
  city: '',
  datacenter: '',
  isp: '',
  line_type: '',
  transport: 'tls',
})

function openRuleLib(n: Node) {
  ruleLibFor.value = n
  ruleLibVisible.value = true
}

// 分享对话框（P1-9）：单节点分享链接 + 二维码（服务端出链接，本地渲染二维码）。
const shareVisible = ref(false)
const shareName = ref('')
const shareLink = ref('')
const shareQr = ref('')

async function openShare(n: Node) {
  try {
    const s = await get<NodeShare>(`/api/nodes/${n.id}/share`)
    shareName.value = s.name
    shareLink.value = s.link
    shareQr.value = await QRCode.toDataURL(s.link, { width: 220, margin: 1 })
    shareVisible.value = true
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function copyShareLink() {
  await navigator.clipboard.writeText(shareLink.value)
  ElMessage.success('分享链接已复制')
}

// ---- 批量操作（A-15）：选中节点统一下发 proc 操作与配置 ----
const tableRef = ref()
const selected = ref<Node[]>([])
const procBusy = ref(false)

const PROC_TEXT: Record<string, string> = { start: '启动', stop: '停止', reload: '重载' }

function clearSelection() {
  tableRef.value?.clearSelection()
}

// summarizeProc 汇总批量回执：全成功给成功提示，有失败列出节点名与原因。
function summarizeProc(results: BatchProcResult[], names: Map<number, string>, action: string) {
  const fail = results.filter((r) => !r.ok)
  if (!fail.length) {
    ElMessage.success(`${PROC_TEXT[action] ?? action}指令已下发 ${results.length} 个节点`)
    return
  }
  const detail = fail
    .slice(0, 5)
    .map((r) => `${names.get(r.node_id) ?? r.node_id}：${r.error ?? '失败'}`)
    .join('；')
  ElMessage.warning(`成功 ${results.length - fail.length} / 失败 ${fail.length}——${detail}`)
}

async function runProc(action: 'start' | 'stop' | 'reload') {
  if (!selected.value.length || procBusy.value) return
  procBusy.value = true
  try {
    const ids = selected.value.map((n) => n.id)
    const names = new Map(selected.value.map((n) => [n.id, n.name]))
    const res = await post<BatchProcResult[]>('/api/nodes/batch/proc', { ids, action })
    summarizeProc(res, names, action)
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    procBusy.value = false
  }
}

// 批量下发配置对话框：同一份 payload 推到所有选中节点，逐节点回执。
const cfgVisible = ref(false)
const cfgBusy = ref(false)
const cfgForm = reactive({ proc: 'xray', kind: 'xray', payload: '' })

async function sendCfg() {
  if (!cfgForm.proc.trim() || !cfgForm.payload.trim()) {
    ElMessage.warning('进程与配置内容必填')
    return
  }
  try {
    JSON.parse(cfgForm.payload)
  } catch {
    ElMessage.warning('配置内容必须是合法 JSON')
    return
  }
  cfgBusy.value = true
  try {
    const ids = selected.value.map((n) => n.id)
    const names = new Map(selected.value.map((n) => [n.id, n.name]))
    const res = await post<BatchConfigResult[]>('/api/nodes/batch/config', { ids, ...cfgForm })
    const fail = res.filter((r) => !r.ok || r.status !== 'applied')
    if (!fail.length) {
      ElMessage.success(`配置已下发 ${res.length} 个节点`)
    } else {
      const detail = fail
        .slice(0, 5)
        .map((r) => `${names.get(r.node_id) ?? r.node_id}：${r.error ?? r.status ?? '失败'}`)
        .join('；')
      ElMessage.warning(`成功 ${res.length - fail.length} / 失败 ${fail.length}——${detail}`)
    }
    cfgVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    cfgBusy.value = false
  }
}

// 自升级对话框（A-23）：面板下发 upgrade 指令，agent 下载替换重启，
// 结果经 Agent 版本列体现（hello 上报新版本）。
const upgradeVisible = ref(false)
const upgradeBusy = ref(false)
const upgradeFor = ref<Node | null>(null)
const upgradeForm = reactive({ version: '', url: '', sha256: '' })
const upgradeMsg = ref('')

function openUpgrade(n: Node) {
  upgradeFor.value = n
  upgradeForm.version = ''
  upgradeForm.url = ''
  upgradeForm.sha256 = ''
  upgradeMsg.value = ''
  upgradeVisible.value = true
}

async function sendUpgrade() {
  if (!upgradeFor.value || upgradeBusy.value) return
  if (!upgradeForm.version.trim() || !upgradeForm.url.trim()) {
    ElMessage.warning('版本号与下载地址必填')
    return
  }
  upgradeBusy.value = true
  upgradeMsg.value = ''
  try {
    const res = await upgradeNode(upgradeFor.value.id, {
      version: upgradeForm.version.trim(),
      url: upgradeForm.url.trim(),
      sha256: upgradeForm.sha256.trim() || undefined,
    })
    upgradeMsg.value = `已受理：agent 将升级到 ${res.version}（当前 ${res.current_version || '未知'}），完成后版本列自动刷新`
    ElMessage.success('升级指令已受理')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    upgradeBusy.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, {
    name: '', address: '', port: 443, protocol: 'vless', config: '{}', enabled: true,
    role: 'landing', direction: 'out', region: '', city: '', datacenter: '', isp: '', line_type: '', transport: 'tls',
  })
  dialogVisible.value = true
}

function openEdit(n: Node) {
  editing.value = n
  Object.assign(form, {
    name: n.name,
    address: n.address,
    port: n.port,
    protocol: n.protocol,
    config: n.config || '{}',
    enabled: n.enabled,
    role: n.role,
    direction: n.direction || 'out',
    region: n.region || '',
    city: n.city || '',
    datacenter: n.datacenter || '',
    isp: n.isp || '',
    line_type: n.line_type || '',
    transport: n.transport || 'tls',
  })
  dialogVisible.value = true
}

async function save() {
  if (!form.name.trim() || !form.address.trim()) {
    ElMessage.warning('名称与地址必填')
    return
  }
  try {
    JSON.parse(form.config)
  } catch {
    ElMessage.warning('配置模板必须是合法 JSON')
    return
  }
  saving.value = true
  try {
    const body = {
      name: form.name.trim(),
      address: form.address.trim(),
      port: form.port,
      protocol: form.protocol,
      config: form.config,
      enabled: form.enabled,
      role: form.role,
      direction: form.direction,
      region: form.region.trim(),
      city: form.city.trim(),
      datacenter: form.datacenter.trim(),
      isp: form.isp.trim(),
      line_type: form.line_type.trim(),
      transport: form.transport,
    }
    if (editing.value) {
      await put(`/api/nodes/${editing.value.id}`, body)
    } else {
      await post('/api/nodes', body)
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

async function toggleEnabled(n: Node, enabled: boolean) {
  try {
    await put(`/api/nodes/${n.id}`, { enabled })
    n.enabled = enabled
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function remove(n: Node) {
  await ElMessageBox.confirm(`删除节点 ${n.name}，确认？`, '删除节点', { type: 'warning' })
  await del(`/api/nodes/${n.id}`)
  ElMessage.success('已删除')
  await load()
}
</script>

<template>
  <div class="page">
    <h2>节点</h2>
    <p class="page-desc">节点、注册元数据与区域/运营商健康聚合。</p>

    <div class="toolbar">
      <el-radio-group v-model="view">
        <el-radio-button value="nodes">节点</el-radio-button>
        <el-radio-button value="region">区域</el-radio-button>
        <el-radio-button value="isp">运营商</el-radio-button>
      </el-radio-group>
      <el-select v-if="view === 'nodes'" v-model="roleFilter" style="width: 140px">
        <el-option value="all" label="全部角色" />
        <el-option v-for="r in ROLES" :key="r.value" :value="r.value" :label="r.label" />
      </el-select>
      <el-badge :value="activeAlerts.length" :hidden="!activeAlerts.length" :max="99">
        <el-button @click="alertVisible = true">告警</el-button>
      </el-badge>
      <el-button type="primary" @click="openCreate">新建节点</el-button>
    </div>

    <!-- 批量操作条（A-15）：选中节点统一 proc/配置下发 -->
    <div v-if="view === 'nodes' && selected.length" class="batch-bar">
      <span class="batch-count">已选 {{ selected.length }}</span>
      <el-button :loading="procBusy" @click="runProc('start')">启动</el-button>
      <el-button :loading="procBusy" @click="runProc('stop')">停止</el-button>
      <el-button :loading="procBusy" @click="runProc('reload')">重载</el-button>
      <el-button type="primary" @click="cfgVisible = true">下发配置</el-button>
      <el-button @click="clearSelection">取消选择</el-button>
    </div>

    <!-- 节点视角 -->
    <el-table
      v-if="view === 'nodes'"
      ref="tableRef"
      :data="filtered"
      v-loading="loading"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
      @selection-change="(rows: Node[]) => (selected = rows)"
    >
      <el-table-column type="selection" width="40" />
      <el-table-column prop="name" label="名称" min-width="120">
        <template #default="{ row }">
          <span>{{ row.name }}</span>
          <el-tag v-if="alertCountByNode.get(row.id)" size="small" type="warning" disable-transitions style="margin-left: 6px">
            告警 {{ alertCountByNode.get(row.id) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="角色" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="ROLE_TYPE[row.role] ?? 'info'" disable-transitions>
            {{ ROLE_TEXT[row.role] ?? row.role }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column label="协议" width="90">
        <template #default="{ row }">
          <el-tag size="small" disable-transitions>{{ row.protocol }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="地址" min-width="160">
        <template #default="{ row }">{{ row.address }}:{{ row.port }}</template>
      </el-table-column>
      <el-table-column label="状态" min-width="110">
        <template #default="{ row }">
          <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'" disable-transitions>{{ row.status }}</el-tag>
          <el-tooltip v-if="row.provision_note" :content="row.provision_note" placement="top" :show-after="200">
            <span class="provision-note">ⓘ</span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="Agent 版本" width="110">
        <template #default="{ row }">
          <span class="version-text">{{ row.agent_version || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="region" label="区域" width="90" />
      <el-table-column prop="isp" label="运营商" width="90" />
      <el-table-column prop="line_type" label="线路" width="90" />
      <el-table-column label="负载" width="150">
        <template #default="{ row }">
          <el-tooltip v-if="loadCell(row.id).has" :content="loadCell(row.id).text">
            <div class="load-cell">
              <el-progress
                :percentage="loadCell(row.id).pct"
                :stroke-width="6"
                :show-text="false"
                :status="loadCell(row.id).status"
              />
              <span class="load-text" :class="{ over: loadCell(row.id).over }">{{ loadCell(row.id).pct }}%</span>
            </div>
          </el-tooltip>
          <span v-else class="load-na">—</span>
        </template>
      </el-table-column>
      <el-table-column label="入口池" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.pool_state === 'suspended'" type="warning" size="small" effect="dark" disable-transitions>已摘除</el-tag>
          <span v-else-if="row.role === 'entry' || row.role === 'both'" class="pool-ok">在池</span>
          <span v-else>—</span>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: boolean) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="320" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="primary" @click="openShare(row)">分享</el-button>
          <el-button link type="primary" @click="openRuleLib(row)">分流</el-button>
          <el-button link type="primary" @click="openUpgrade(row)">升级</el-button>
          <el-button
            v-if="(row.role === 'entry' || row.role === 'both') && row.enabled"
            link :type="row.pool_state === 'suspended' ? 'success' : 'warning'"
            @click="togglePool(row)"
          >{{ row.pool_state === 'suspended' ? '复位' : '摘除' }}</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 区域 / 运营商视角（同构） -->
    <template v-else>
      <div v-loading="loading" class="dim-grid">
        <div v-for="card in dimCards(view === 'region' ? 'region' : 'isp')" :key="card.key" class="dim-card">
          <div class="dim-head">
            <span :class="STATE_DOT[card.state]"></span>
            <span class="dim-key">{{ card.key }}</span>
            <el-tag size="small" effect="plain" disable-transitions>{{ STATE_TEXT[card.state] }}</el-tag>
          </div>
          <div class="dim-stat">在线 {{ card.online }} / {{ card.total }}</div>
          <div v-if="card.reason" class="dim-reason">{{ card.reason }}</div>
        </div>
        <div v-if="!dimCards(view === 'region' ? 'region' : 'isp').length" class="dim-empty">
          暂无节点，先在「节点」视角新建。
        </div>
      </div>
    </template>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑节点' : '新建节点'" width="620px">
      <el-form label-width="90px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="64" placeholder="节点名称" />
        </el-form-item>
        <el-form-item label="协议">
          <el-select v-model="form.protocol" style="width: 100%">
            <el-option v-for="p in PROTOCOLS" :key="p.value" :value="p.value" :label="p.label" />
          </el-select>
        </el-form-item>
        <el-form-item label="地址">
          <el-input v-model="form.address" maxlength="255" placeholder="域名或 IP" />
        </el-form-item>
        <el-form-item label="端口">
          <el-input-number v-model="form.port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="配置模板">
          <el-input
            v-model="form.config"
            type="textarea"
            :rows="6"
            class="config-input"
            placeholder='{"uuid": "...", "tls": true, "net": "ws"}'
          />
          <span class="form-hint">协议配置 JSON，按协议约定字段</span>
        </el-form-item>
        <el-form-item label="角色">
          <el-radio-group v-model="form.role">
            <el-radio-button v-for="r in ROLES" :key="r.value" :value="r.value">{{ r.label }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="方向">
          <el-radio-group v-model="form.direction">
            <el-radio-button v-for="d in DIRECTIONS" :key="d.value" :value="d.value">{{ d.label }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="区域">
          <el-input v-model="form.region" maxlength="64" placeholder="如 香港 / 圣何塞" />
        </el-form-item>
        <el-form-item label="运营商">
          <el-input v-model="form.isp" maxlength="32" placeholder="如 HKT / 电信" />
        </el-form-item>
        <el-form-item label="线路">
          <el-input v-model="form.line_type" maxlength="16" placeholder="如 cn2_gia / 163 / IPLC" />
        </el-form-item>
        <el-form-item label="传输">
          <el-select v-model="form.transport" style="width: 200px">
            <el-option v-for="t in TRANSPORTS" :key="t" :value="t" :label="t" />
          </el-select>
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

    <!-- 分享（P1-9）：链接 + 二维码 -->
    <el-dialog v-model="shareVisible" :title="`分享 ${shareName}`" width="320px">
      <div class="share-body">
        <img v-if="shareQr" :src="shareQr" alt="分享二维码" class="share-qr" />
        <el-input :model-value="shareLink" readonly />
        <el-button type="primary" @click="copyShareLink">复制链接</el-button>
      </div>
    </el-dialog>

    <!-- 批量下发配置（A-15）：同一份配置推到选中节点 -->
    <el-dialog v-model="cfgVisible" :title="`下发配置到 ${selected.length} 个节点`" width="560px">
      <el-form label-width="80px">
        <el-form-item label="进程">
          <el-input v-model="cfgForm.proc" maxlength="32" placeholder="如 xray" />
        </el-form-item>
        <el-form-item label="类型">
          <el-input v-model="cfgForm.kind" maxlength="32" placeholder="如 xray" />
        </el-form-item>
        <el-form-item label="配置内容">
          <el-input
            v-model="cfgForm.payload"
            type="textarea"
            :rows="10"
            class="config-input"
            placeholder='{"inbounds": [...]}'
          />
          <span class="form-hint">JSON 配置，下发前校验、失败自动回滚</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cfgVisible = false">取消</el-button>
        <el-button type="primary" :loading="cfgBusy" @click="sendCfg">下发</el-button>
      </template>
    </el-dialog>

    <!-- 自升级（A-23）：下发 upgrade 指令 -->
    <el-dialog v-model="upgradeVisible" :title="`升级 ${upgradeFor?.name ?? ''}`" width="480px">
      <el-form label-width="90px">
        <el-form-item label="目标版本">
          <el-input v-model="upgradeForm.version" maxlength="32" placeholder="如 v0.2.0" />
        </el-form-item>
        <el-form-item label="下载地址">
          <el-input v-model="upgradeForm.url" maxlength="255" placeholder="https://.../ferry-agent" />
        </el-form-item>
        <el-form-item label="SHA-256">
          <el-input v-model="upgradeForm.sha256" maxlength="64" placeholder="可选，十六进制校验和" />
        </el-form-item>
      </el-form>
      <p v-if="upgradeMsg" class="upgrade-msg">{{ upgradeMsg }}</p>
      <span class="form-hint">agent 下载校验后替换自身并重启；启动验证超时自动回滚。</span>
      <template #footer>
        <el-button @click="upgradeVisible = false">取消</el-button>
        <el-button type="primary" :loading="upgradeBusy" @click="sendUpgrade">下发</el-button>
      </template>
    </el-dialog>

    <!-- 告警（A-22）：活跃告警列表，可手动处理 -->
    <el-dialog v-model="alertVisible" title="异常告警" width="720px">
      <div class="alert-bar">
        <el-radio-group v-model="alertFilter">
          <el-radio-button value="active">活跃（{{ activeAlerts.length }}）</el-radio-button>
          <el-radio-button value="resolved">已处理</el-radio-button>
          <el-radio-button value="all">全部</el-radio-button>
        </el-radio-group>
      </div>
      <div v-loading="loading" class="alert-list">
        <div v-for="a in alertRows" :key="a.id" class="alert-row">
          <div class="alert-head">
            <el-tag size="small" :type="ALERT_SEV_TYPE[a.severity] ?? 'info'" disable-transitions>{{ a.severity }}</el-tag>
            <span class="alert-kind">{{ a.kind_text }}</span>
            <span class="alert-node">{{ a.node_name }}</span>
            <span v-if="a.proc" class="alert-proc">{{ a.proc }}</span>
            <span class="alert-time">{{ new Date(a.created_at).toLocaleString() }}</span>
          </div>
          <div class="alert-msg">{{ a.message }}</div>
          <div class="alert-foot">
            <el-tag v-if="a.state === 'resolved'" size="small" type="success" disable-transitions>
              已处理{{ a.resolved_at ? ` · ${new Date(a.resolved_at).toLocaleString()}` : '' }}
            </el-tag>
            <el-button v-else link type="primary" :loading="alertBusy" @click="resolveOne(a)">处理</el-button>
          </div>
        </div>
        <div v-if="!alertRows.length" class="alert-empty">暂无告警</div>
      </div>
    </el-dialog>

    <RuleLibDialog v-model="ruleLibVisible" :node="ruleLibFor" />
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
.batch-bar {
  margin-bottom: 12px;
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 12px;
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
}
.batch-count {
  color: var(--ferry-text-dim);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  margin-right: 4px;
}
.form-hint {
  margin-left: 10px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.config-input :deep(textarea) {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}

/* ---- 分享对话框 ---- */
.share-body {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}
.share-qr {
  width: 220px;
  height: 220px;
  border-radius: 8px;
  background: #fff;
  padding: 6px;
}

/* ---- 区域/运营商视角 ---- */
.dim-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
  min-height: 80px;
}
.dim-card {
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  padding: 14px 16px;
}
.dim-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.dim-key {
  font-weight: 600;
  margin-right: auto;
}
.dim-stat {
  margin-top: 10px;
  font-size: 13px;
  color: var(--ferry-text-dim);
  font-variant-numeric: tabular-nums;
}
.dim-reason {
  margin-top: 8px;
  font-size: 12px;
  color: var(--ferry-warn);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--ferry-text-muted);
  flex: none;
}
.dot.ok {
  background: var(--ferry-ok);
}
.dot.warn {
  background: var(--ferry-warn);
}
.dot.bad {
  background: var(--ferry-danger);
}
.dim-empty {
  grid-column: 1 / -1;
  color: var(--ferry-text-muted);
  font-size: 13px;
  padding: 24px 0;
  text-align: center;
}

/* ---- 升级对话框（A-23） ---- */
.upgrade-msg {
  margin: 0 0 8px;
  font-size: 13px;
  color: var(--ferry-ok);
}
.version-text {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
  color: var(--ferry-text-dim);
}

/* ---- 告警对话框（A-22） ---- */
.alert-bar {
  margin-bottom: 12px;
}
.alert-list {
  max-height: 50vh;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.alert-row {
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  padding: 10px 12px;
}
.alert-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.alert-kind {
  font-weight: 600;
}
.alert-node {
  color: var(--ferry-text-dim);
  font-size: 13px;
}
.alert-proc {
  color: var(--ferry-text-muted);
  font-size: 12px;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
}
.alert-time {
  margin-left: auto;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.alert-msg {
  margin-top: 6px;
  font-size: 13px;
  color: var(--ferry-text-dim);
  word-break: break-all;
}
.alert-foot {
  margin-top: 8px;
  display: flex;
  justify-content: flex-end;
}
.alert-empty {
  color: var(--ferry-text-muted);
  font-size: 13px;
  padding: 24px 0;
  text-align: center;
}
.load-cell {
  display: flex;
  gap: 8px;
  align-items: center;
}
.load-cell .el-progress {
  flex: 1;
}
.load-text {
  font-size: 12px;
  color: var(--ferry-text-muted);
  min-width: 32px;
  text-align: right;
}
.load-text.over {
  color: var(--ferry-danger);
  font-weight: 650;
}
.load-na {
  color: var(--ferry-text-muted);
}
.pool-ok {
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.provision-note {
  margin-left: 6px;
  color: var(--ferry-text-muted);
  cursor: help;
}
</style>
