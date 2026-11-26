<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, resolveAlert, upgradeNode, getNodeProcs, getNodeStatus, rotateNodeToken, revokeNodeToken, type Node, type DimensionStatus, type NodeShare, type BatchProcResult, type BatchConfigResult, type Alert, type NodeLoad, type NodeProcRow, type NodeStatusAggregate } from '../api'
import QRCode from 'qrcode'
import RuleLibDialog from '../components/RuleLibDialog.vue'

// E-24: the node view — node list (entry/landing filter + direction/line columns) and the region/ISP
// perspective (status lights + node counts, structurally identical). Protocol/config editing lives in dialogs.

const router = useRouter()

const PROTOCOLS = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'shadowsocks', label: 'Shadowsocks' },
  { value: 'hysteria2', label: 'Hysteria2 (hy2)' },
]

// Config template examples (per protocol): one template mechanism (node.Config JSON) throughout, with hy2
// adding the password/sni/obfs/up/down fields; for kernel config templates see the deployment doc's procs[].config_path.
const CONFIG_EXAMPLES: Record<string, string> = {
  vless: '{"uuid":"...","tls":true,"sni":"cdn.example.com","net":"ws","host":"cdn.example.com","path":"/ws"}',
  vmess: '{"uuid":"...","scy":"aes-128-gcm","net":"ws","path":"/ws","host":"cdn.example.com","tls":true,"sni":"cdn.example.com"}',
  trojan: '{"password":"...","sni":"cdn.example.com","net":"grpc"}',
  shadowsocks: '{"method":"aes-256-gcm","password":"..."}',
  hysteria2: '{"password":"...","sni":"cdn.example.com","obfs":"salamander","obfs_password":"...","up":100,"down":100}',
}

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

// ---- Data ----
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

// ---- Load dashboard (E-25): connection count / measured throughput / bandwidth utilization; over 80% flagged red ----
const loads = ref<NodeLoad[]>([])

function loadOf(id: number): NodeLoad | undefined {
  return loads.value.find((l) => l.node_id === id)
}

// loadCell is the load column view model: has=false when unsampled, rendered as "—".
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
  // Three-state toggle among remove / reset / stage-back-to-pool: suspended -> reset, standby -> back to pool, active -> remove.
  if (row.pool_state === 'suspended' || row.pool_state === 'standby') {
    try {
      await post(`/api/pool/${row.id}/resume`)
      ElMessage.success(row.pool_state === 'suspended' ? '已复位' : '已回池')
      await load()
    } catch (e) {
      ElMessage.error(String(e))
    }
    return
  }
  await ElMessageBox.confirm(
    `摘除后 ${row.name} 立即退出订阅入口池（agent 连接保持），确认？`,
    '手动摘除',
    { type: 'warning' },
  )
  try {
    await post(`/api/pool/${row.id}/suspend`)
    ElMessage.success('已摘除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function markStandby(row: Node) {
  await ElMessageBox.confirm(
    `预备后 ${row.name} 退出订阅入口池待命（agent 连接保持），被封恢复补位或手动回池时再入列，确认？`,
    '标记预备',
    { type: 'warning' },
  )
  try {
    await post(`/api/pool/${row.id}/standby`)
    ElMessage.success('已标记预备')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Alerts (A-22): active alert counts and the node mapping ----
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

// Alert dialog: active ones first, filterable by state, resolvable manually.
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

// ---- Perspective aggregation (region and ISP are structurally identical) ----
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

// ---- Edit ----
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
  maintenance: false,
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

// Share dialog (P1-9): single-node share link + QR code (the server provides the link, the QR renders locally).
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

async function rotateToken(n: Node) {
  await ElMessageBox.confirm(
    '旧令牌保留 10 分钟兼容窗口，请立即更新 agent 配置；可随后撤销旧令牌。',
    '轮换节点令牌',
    { type: 'warning' },
  )
  const rotated = await rotateNodeToken(n.id)
  await navigator.clipboard.writeText(rotated.token)
  Object.assign(n, rotated)
  ElMessage.success('新令牌已复制，请更新 agent 配置')
}

async function revokeToken(n: Node) {
  if (!n.old_token) return
  await ElMessageBox.confirm('旧令牌将立即失效，确认吊销？', '吊销旧令牌', { type: 'warning' })
  const revoked = await revokeNodeToken(n.id, n.old_token)
  Object.assign(n, revoked)
  ElMessage.success('旧令牌已吊销')
}

// Process status dialog (SAVE-3): snapshots of the managed processes reported by the agent heartbeat (including cache-hit statistics).
const procsVisible = ref(false)
const procsBusy = ref(false)
const procsFor = ref<Node | null>(null)
const procsRows = ref<NodeProcRow[]>([])

const PROC_STATE_TYPE: Record<string, string> = { running: 'success', stopped: 'info', crashed: 'danger' }

// Node detail tri-state dialog (F-0750): agent liveness, relay process and the latest probe verdict.
const statusVisible = ref(false)
const statusBusy = ref(false)
const statusFor = ref<Node | null>(null)
const statusData = ref<NodeStatusAggregate | null>(null)

const AGENT_STATE_TYPE: Record<string, string> = { online: 'success', offline: 'danger', unknown: 'info' }
const RELAY_STATE_TYPE: Record<string, string> = { running: 'success', stopped: 'info', crashed: 'danger' }
// F-0902: per-node derived health (same vocabulary as path health).
const NODE_HEALTH_TYPE: Record<string, string> = { healthy: 'success', unknown: 'info', degraded: 'warning', off: 'danger' }
function healthTagType(state: string) {
  return NODE_HEALTH_TYPE[state] ?? 'info'
}

// F-0804: capability flags from the agent's heartbeat declaration. UDP stays
// explicitly unsupported until landing-side UDP termination exists.
function relayCapTags(caps: NonNullable<NodeStatusAggregate['relay_caps']>) {
  return [
    { label: 'TCP', on: caps.tcp_forward },
    { label: 'UDP', on: caps.udp_forward },
    { label: '重连', on: caps.reconnect },
    { label: '空闲清理', on: caps.idle_cleanup },
    { label: '指标', on: caps.metrics },
    { label: '连接限流', on: caps.stream_limit },
    { label: '带宽限流', on: caps.bandwidth_limit },
  ]
}

async function openStatus(n: Node) {
  statusFor.value = n
  statusVisible.value = true
  statusBusy.value = true
  try {
    statusData.value = await getNodeStatus(n.id)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    statusBusy.value = false
  }
}

async function openProcs(n: Node) {
  procsFor.value = n
  procsVisible.value = true
  procsBusy.value = true
  try {
    procsRows.value = await getNodeProcs(n.id)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    procsBusy.value = false
  }
}

// ---- Batch actions (A-15): push proc operations and config to all selected nodes at once ----
const tableRef = ref()
const selected = ref<Node[]>([])
const procBusy = ref(false)

const PROC_TEXT: Record<string, string> = { start: '启动', stop: '停止', reload: '重载' }

function clearSelection() {
  tableRef.value?.clearSelection()
}

// summarizeProc aggregates the batch receipts: all-success shows a success toast, any failure lists node names and reasons.
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

// Batch config push dialog: one payload pushed to every selected node, with per-node receipts.
const cfgVisible = ref(false)
const cfgBusy = ref(false)
const cfgForm = reactive({ proc: 'xray', kind: 'xray', payload: '' })
const JSON_CONFIG_PLACEHOLDER = '{"inbounds": [...]}'

// hysteria2 kernel config example (YAML): accong obfuscation password + bandwidth cap + QUIC listener,
// with fields matching the hysteria2 server binary; up/down should match the node's plan.
const HY2_SERVER_CONFIG_EXAMPLE = `listen: :443

tls:
  cert: /etc/ferry/hy2/fullchain.pem
  key: /etc/ferry/hy2/privkey.pem

quic:
  initStreamReceiveWindow: 8388608
  maxIdleTimeout: 30s

auth:
  type: password
  password: <change-me>

bandwidth:
  up: 100 mbps
  down: 200 mbps

masquerade:
  type: proxy
  proxy:
    url: https://news.ycombinator.com/
    rewriteHost: true`

async function sendCfg() {
  if (!cfgForm.proc.trim() || !cfgForm.payload.trim()) {
    ElMessage.warning('进程与配置内容必填')
    return
  }
  // The hysteria2 kernel config is YAML (no syntax validation here; the agent-side validate/check command is the backstop);
  // the rest (xray) is still JSON, and the panel rejects syntax errors up front.
  if (cfgForm.proc.trim() !== 'hysteria2') {
    try {
      JSON.parse(cfgForm.payload)
    } catch {
      ElMessage.warning('配置内容必须是合法 JSON（hysteria2 走 YAML，选 hysteria2 进程时不校验）')
      return
    }
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

// Self-upgrade dialog (A-23): the panel sends the upgrade instruction, the agent downloads, replaces and restarts,
// and the result shows up in the agent version column (the new version is reported in hello).
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
    name: '', address: '', port: 443, protocol: 'vless', config: '{}', enabled: true, maintenance: false,
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
    maintenance: n.maintenance,
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

// Fill in the config template example for the current protocol (confirm before overwriting non-empty content).
async function fillConfigExample() {
  const example = CONFIG_EXAMPLES[form.protocol]
  if (!example) {
    ElMessage.warning('当前协议没有内置示例')
    return
  }
  if (form.config && form.config.trim() !== '{}') {
    try {
      await ElMessageBox.confirm('将覆盖当前配置模板，继续？', '填充示例', { type: 'warning' })
    } catch {
      return
    }
  }
  form.config = example
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
      maintenance: form.maintenance,
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

// Maintenance toggle (F-0746): the node stays listed but allocation and
// subscriptions skip it while the switch is on.
async function toggleMaintenance(n: Node, maintenance: boolean) {
  try {
    await put(`/api/nodes/${n.id}`, { maintenance })
    n.maintenance = maintenance
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

    <!-- Batch action bar (A-15): push proc/config to all selected nodes at once -->
    <div v-if="view === 'nodes' && selected.length" class="batch-bar">
      <span class="batch-count">已选 {{ selected.length }}</span>
      <el-button :loading="procBusy" @click="runProc('start')">启动</el-button>
      <el-button :loading="procBusy" @click="runProc('stop')">停止</el-button>
      <el-button :loading="procBusy" @click="runProc('reload')">重载</el-button>
      <el-button type="primary" @click="cfgVisible = true">下发配置</el-button>
      <el-button @click="clearSelection">取消选择</el-button>
    </div>

    <!-- Node perspective -->
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
          <el-tag v-else-if="row.pool_state === 'standby'" type="info" size="small" effect="plain" disable-transitions>预备</el-tag>
          <span v-else-if="row.role === 'entry' || row.role === 'both'" class="pool-ok">在池</span>
          <span v-else>—</span>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: boolean) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="维护" width="80">
        <template #default="{ row }">
          <el-tooltip content="维护中的节点不参与分配与订阅" placement="top">
            <el-switch :model-value="row.maintenance" @change="(v: boolean) => toggleMaintenance(row, v)" />
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="360" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openStatus(row)">状态</el-button>
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="primary" @click="openShare(row)">分享</el-button>
          <el-button link type="primary" @click="openRuleLib(row)">分流</el-button>
          <el-button link type="primary" @click="openProcs(row)">进程</el-button>
          <el-button link type="primary" @click="openUpgrade(row)">升级</el-button>
          <el-button link type="warning" @click="rotateToken(row)">换令牌</el-button>
          <el-button v-if="row.old_token" link type="danger" @click="revokeToken(row)">吊销旧令牌</el-button>
          <el-button
            v-if="(row.role === 'entry' || row.role === 'both') && row.enabled"
            link :type="row.pool_state === 'suspended' || row.pool_state === 'standby' ? 'success' : 'warning'"
            @click="togglePool(row)"
          >{{ row.pool_state === 'suspended' ? '复位' : row.pool_state === 'standby' ? '回池' : '摘除' }}</el-button>
          <el-button
            v-if="(row.role === 'entry' || row.role === 'both') && row.enabled && row.pool_state === 'active'"
            link type="info"
            @click="markStandby(row)"
          >预备</el-button>
          <el-button link type="info" @click="router.push({ path: '/cloud', query: { node_id: String(row.id) } })">关联云资源</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Region / ISP perspective (structurally identical) -->
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
            :placeholder="CONFIG_EXAMPLES[form.protocol]"
          />
          <span class="form-hint">
            协议配置 JSON，按协议约定字段；
            <el-button link type="primary" size="small" @click="fillConfigExample">填充当前协议示例</el-button>
            （hysteria2: password/sni/obfs 必填，up/down 单位 Mbps）
          </span>
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
            <el-option v-for="t in TRANSPORTS" :key="t" :value="t"
              :label="t === 'ssh' ? 'ssh（备选·特征独特）' : t" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item label="维护">
          <el-switch v-model="form.maintenance" />
          <span class="form-hint">维护中的节点不参与分配与订阅</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <!-- Share (P1-9): link + QR code -->
    <el-dialog v-model="shareVisible" :title="`分享 ${shareName}`" width="320px">
      <div class="share-body">
        <img v-if="shareQr" :src="shareQr" alt="分享二维码" class="share-qr" />
        <el-input :model-value="shareLink" readonly />
        <el-button type="primary" @click="copyShareLink">复制链接</el-button>
      </div>
    </el-dialog>

    <!-- Batch config push (A-15): the same config pushed to the selected nodes -->
    <el-dialog v-model="cfgVisible" :title="`下发配置到 ${selected.length} 个节点`" width="560px">
      <el-form label-width="80px">
        <el-form-item label="进程">
          <el-select v-model="cfgForm.proc" style="width: 100%" placeholder="选择被管进程">
            <el-option value="xray" label="xray" />
            <el-option value="hysteria2" label="hysteria2" />
            <el-option value="sing-box" label="sing-box" />
            <el-option value="relay" label="relay（落地覆盖）" />
          </el-select>
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
            :placeholder="cfgForm.proc === 'hysteria2' ? HY2_SERVER_CONFIG_EXAMPLE : JSON_CONFIG_PLACEHOLDER"
          />
          <span class="form-hint">
            {{ cfgForm.proc === 'hysteria2' ? 'YAML 配置，agent 校验命令（如配置）失败自动回滚' : 'JSON 配置，下发前校验、失败自动回滚' }}
          </span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cfgVisible = false">取消</el-button>
        <el-button type="primary" :loading="cfgBusy" @click="sendCfg">下发</el-button>
      </template>
    </el-dialog>

    <!-- Self-upgrade (A-23): send the upgrade instruction -->
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

    <!-- Alerts (A-22): the active alert list, manually resolvable -->
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

    <!-- Process status (SAVE-3): heartbeat snapshots read back, with metrics columns such as cache-hit statistics -->
    <el-dialog v-model="procsVisible" :title="`进程 · ${procsFor?.name ?? ''}`" width="680px">
      <el-table :data="procsRows" v-loading="procsBusy" size="small" empty-text="暂无上报（agent 心跳携带进程快照后出现）">
        <el-table-column prop="proc" label="进程" width="130" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="PROC_STATE_TYPE[row.state] ?? 'info'" disable-transitions>{{ row.state }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="PID" width="80">
          <template #default="{ row }">{{ row.pid || '—' }}</template>
        </el-table-column>
        <el-table-column prop="restarts" label="重启" width="64" />
        <el-table-column label="指标">
          <template #default="{ row }">
            <span v-if="row.metrics" class="proc-metrics">
              <span v-for="(v, k) in row.metrics" :key="k" class="proc-metric">{{ k }} {{ v.toLocaleString() }}</span>
            </span>
            <span v-else>—</span>
          </template>
        </el-table-column>
        <el-table-column label="更新" width="110">
          <template #default="{ row }">{{ new Date(row.updated_at).toLocaleTimeString() }}</template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button :disabled="!procsFor" :loading="procsBusy" @click="procsFor && openProcs(procsFor)">刷新</el-button>
        <el-button @click="procsVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- Node detail tri-state (F-0750): agent liveness, relay process, latest probe verdict -->
    <el-dialog v-model="statusVisible" :title="`状态 · ${statusFor?.name ?? ''}`" width="640px">
      <div v-loading="statusBusy">
        <el-descriptions v-if="statusData" :column="2" border size="small">
          <el-descriptions-item label="Agent 状态">
            <el-tag :type="AGENT_STATE_TYPE[statusData.agent.status] ?? 'info'" size="small" disable-transitions>
              {{ statusData.agent.status }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="最近在线">
            {{ statusData.agent.last_seen ? new Date(statusData.agent.last_seen).toLocaleString() : '从未上报' }}
          </el-descriptions-item>
          <el-descriptions-item label="启用">
            {{ statusData.agent.enabled ? '是' : '否' }}
          </el-descriptions-item>
          <el-descriptions-item label="维护">
            {{ statusData.agent.maintenance ? '维护中（不参与分配与订阅）' : '否' }}
          </el-descriptions-item>
          <el-descriptions-item label="Relay 进程">
            <template v-if="statusData.relay">
              <el-tag :type="RELAY_STATE_TYPE[statusData.relay.state] ?? 'info'" size="small" disable-transitions>
                {{ statusData.relay.state }}
              </el-tag>
              <span class="status-meta">PID {{ statusData.relay.pid ?? '—' }} · 重启 {{ statusData.relay.restarts }} 次</span>
              <span v-if="statusData.relay.version" class="status-meta">版本 {{ statusData.relay.version }}</span>
            </template>
            <span v-else>—</span>
          </el-descriptions-item>
          <el-descriptions-item label="角色 / 入口池">
            <template v-if="statusData.relay">
              {{ statusData.relay.role }} / {{ statusData.relay.pool_state }}
            </template>
            <span v-else>—</span>
          </el-descriptions-item>
          <el-descriptions-item label="进程快照">
            <template v-if="statusData.relay">{{ new Date(statusData.relay.updated_at).toLocaleString() }}</template>
            <span v-else>暂无上报</span>
          </el-descriptions-item>
          <el-descriptions-item label="节点健康">
            <el-tag :type="healthTagType(statusData.health)" size="small" disable-transitions>
              {{ statusData.health }}
            </el-tag>
            <span class="status-meta">节点自身状态（区别于单条线路健康）</span>
          </el-descriptions-item>
          <el-descriptions-item label="出口健康">
            <el-tag :type="healthTagType(statusData.egress)" size="small" disable-transitions>
              {{ statusData.egress }}
            </el-tag>
            <span class="status-meta">节点自身公网出口（区别于「别人能否访问本节点」）</span>
          </el-descriptions-item>
          <el-descriptions-item label="探测判定">
            <template v-if="statusData.probe">
              <el-tag :type="statusData.probe.verdict === 'healthy' ? 'success' : 'warning'" size="small" disable-transitions>
                {{ statusData.probe.verdict }}
              </el-tag>
              <span class="status-meta">{{ statusData.probe.direction }} · RTT {{ statusData.probe.rtt_ms }}ms · 抖动 {{ statusData.probe.jitter_ms }}ms · 丢包 {{ statusData.probe.loss_pct }}%<template v-if="statusData.probe.err_class"> · 失败类型 {{ statusData.probe.err_class }}</template></span>
            </template>
            <span v-else>暂无探测记录</span>
          </el-descriptions-item>
          <el-descriptions-item label="探测时间" :span="2">
            <template v-if="statusData.probe">{{ new Date(statusData.probe.probed_at).toLocaleString() }}</template>
            <span v-else>—</span>
          </el-descriptions-item>
          <el-descriptions-item label="Relay 能力" :span="2">
            <template v-if="statusData.relay_caps">
              <el-tag v-for="tag in relayCapTags(statusData.relay_caps)" :key="tag.label"
                :type="tag.on ? 'success' : 'info'" size="small" effect="plain" disable-transitions>
                {{ tag.label }}: {{ tag.on ? '支持' : '不支持' }}
              </el-tag>
              <span class="status-meta">max_conns {{ statusData.relay_caps.max_conns }}</span>
            </template>
            <span v-else>暂无声明（旧版 agent）</span>
          </el-descriptions-item>
        </el-descriptions>
      </div>
      <template #footer>
        <el-button :disabled="!statusFor" :loading="statusBusy" @click="statusFor && openStatus(statusFor)">刷新</el-button>
        <el-button @click="statusVisible = false">关闭</el-button>
      </template>
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
.status-meta {
  margin-left: 8px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.config-input :deep(textarea) {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}

/* ---- Share dialog ---- */
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

/* ---- Region/ISP perspective ---- */
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

/* ---- Upgrade dialog (A-23) ---- */
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

/* ---- Alert dialog (A-22) ---- */
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
.proc-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
}
.proc-metric {
  color: var(--ferry-text-muted);
  font-variant-numeric: tabular-nums;
}
</style>
