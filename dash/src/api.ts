import { auth, logout, logoutDist } from './auth'

// API 客户端：统一解析后端 {error} 错误载荷；管理员 JWT 随身携带，管理面
// 401（令牌过期）清会话回登录页。code 是后端机器可读错误码（如登录的
// totp_required/totp_invalid，安全设计 §1）。
export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, message: string, code = '') {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { ...((init?.headers as Record<string, string>) ?? {}) }
  if (init?.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json'
  // DS-3：代理自面端点注入独立代理令牌，管理面沿用管理员令牌。
  const isDist = path.startsWith('/distributor/api') || path === '/distributor/login'
  const token = isDist ? auth.distToken : auth.token
  if (token) headers['Authorization'] = `Bearer ${token}`
  const res = await fetch(path, { ...init, headers })
  const text = await res.text()
  const body = text ? JSON.parse(text) : null
  if (!res.ok) {
    if (res.status === 401) {
      if (isDist && auth.distToken && path !== '/distributor/login') {
        // 代理令牌过期或被停用（停用即踢）——清代理会话回代理登录页。
        logoutDist()
        window.location.assign('/dist-login')
      } else if (!isDist && auth.token && path !== '/admin/login') {
        logout()
        window.location.assign('/login')
      }
    }
    throw new ApiError(res.status, body?.error ?? res.statusText, body?.code ?? '')
  }
  return body as T
}

export function get<T>(path: string): Promise<T> {
  return request<T>(path)
}

export function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
}

export function put<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'PUT', body: JSON.stringify(body) })
}

export function patch<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'PATCH', body: body === undefined ? undefined : JSON.stringify(body) })
}

export function del<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' })
}

// postRaw 以原始请求体 POST（规则库数据文件分发用，SAVE-1）。
export function postRaw<T>(path: string, body: Blob): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    body,
    headers: { 'Content-Type': 'application/octet-stream' },
  })
}

// ---- 资源类型（对齐 server 的 JSON 载荷）----

export interface Node {
  id: number
  name: string
  address: string
  port: number
  protocol: string
  config: string
  enabled: boolean
  token: string
  status: string
  role: string
  direction: string
  line_type: string
  region: string
  city: string
  datacenter: string
  isp: string
  transport: string
  last_seen: string | null
  pool_state: 'active' | 'suspended' | string
  pool_changed_at: string | null
  pool_reason: string
}

/** DimensionStatus 是区域/运营商聚合判定的状态灯（GET /api/dimension-status）。 */
export interface DimensionStatus {
  scope: 'region' | 'isp'
  key: string
  state: 'healthy' | 'degraded' | 'failed'
  reason: string
}

/** LandingAssignment 是一条落地分配（E-20）：入口或区域 → 落地与权重。 */
export interface LandingAssignment {
  id: number
  entry_node_id: number | null
  region: string
  landing_node_id: number
  direction: 'out' | 'in'
  strategy: string
  weight: number
  reason: string
  assigned_at: string
  released_at: string | null
  release_reason: string
}

/** TransportHealth 是一种传输在区域内的存活概要（E-17）。 */
export interface TransportHealth {
  transport: string
  total: number
  alive: number
}

/** TransportRow 是一个区域的传输判定：现行主流、推荐与换线建议。 */
export interface TransportRow {
  region: string
  transports: TransportHealth[]
  current: string
  recommended: string
  switchneeded: boolean
}

/** AllocPolicy 是落地自动分配的每方向档位与低峰再平衡窗口（E-21/E-22）。 */
export interface AllocPolicy {
  out: string
  in: string
  rebalance_start: number
  rebalance_end: number
  /** balanced 乘法 score 系数（dash 可配）；缺省 0.5/0.3/0.2 即现公式口径。 */
  w_load: number
  w_cost: number
  w_premium: number
}

/** AllocData 是策略与生效中的 auto 分配行。 */
export interface AllocData {
  policy: AllocPolicy
  rows: LandingAssignment[]
}

export function getAlloc(): Promise<AllocData> {
  return get<AllocData>('/api/alloc')
}

export function putAllocPolicy(policy: AllocPolicy): Promise<AllocPolicy> {
  return put<AllocPolicy>('/api/alloc/policy', policy)
}

/** QuotaLinkSetting 是配额联动配置（SAVE-6）：超阈值自动把该用户订阅降档到低成本档入口。 */
export interface QuotaLinkSetting {
  enabled: boolean
  /** 流量档阈值（1-100）：窗口用量达个人配额的 N% 触发；0=不按流量档触发 */
  traffic_percent: number
  /** 费用档阈值（分）：窗口折算费用达 N 分触发；0=不按费用档触发 */
  cost_cents: number
  /** 降档成本档线（分/GB）：包月或单价不超线的节点视为低成本档；0=只有包月 */
  max_price_cents: number
}

/** QuotaActionRow 是一条配额联动降档留痕（released_at 空=降档生效中）。 */
export interface QuotaActionRow {
  id: number
  user_id: number
  trigger: 'traffic' | 'cost'
  used_bytes: number
  quota_bytes: number
  cost_cents: number
  max_price_cents: number
  reason: string
  created_at: string
  released_at: string | null
  release_reason: string
}

/** QuotaLinkData 是联动配置、降档留痕（生效中在前）与用户名映射。 */
export interface QuotaLinkData {
  setting: QuotaLinkSetting
  rows: QuotaActionRow[]
  names: Record<string, string>
}

export function getQuotaLink(): Promise<QuotaLinkData> {
  return get<QuotaLinkData>('/api/quota-link')
}

export function putQuotaLink(setting: QuotaLinkSetting): Promise<QuotaLinkSetting> {
  return put<QuotaLinkSetting>('/api/quota-link', setting)
}

/** EventRow 是一条事件 outbox 行（HERALD-1，failed=重试超限死信标红）。 */
export interface EventRow {
  id: number
  kind: string
  severity: 'critical' | 'warning' | 'info'
  title: string
  body?: string
  target: string
  dedup_key?: string
  meta?: string
  status: 'pending' | 'sent' | 'failed'
  attempts: number
  next_attempt_at?: string | null
  occurred_at: string
  created_at: string
}

/** EventsData 是事件列表与各状态计数（failed>0 即通道故障未消化）。 */
export interface EventsData {
  rows: EventRow[]
  counts: { pending: number; sent: number; failed: number }
}

export function getEvents(status = ''): Promise<EventsData> {
  return get<EventsData>(`/api/events${status ? `?status=${encodeURIComponent(status)}` : ''}`)
}

export function retryEvent(id: number): Promise<{ ok: boolean }> {
  return post<{ ok: boolean }>(`/api/events/${id}/retry`)
}

/** NodeCost 是一个节点的月度成本视图（E-23）。 */
export interface NodeCost {
  id: number
  name: string
  region: string
  isp: string
  billing_type: string
  month_rx_bytes: number
  month_tx_bytes: number
  traffic_cost_cents: number
  fixed_cost_cents: number
  projected_cents: number
}

/** GroupCost 是区域/运营商维度的成本汇总（E-23）。 */
export interface GroupCost {
  key: string
  nodes: number
  traffic_cents: number
  fixed_cents: number
  projected_cents: number
}

/** NodeLoad 是节点负载快照：连接数/实测吞吐/带宽利用率（E-25，util_pct=-1 无采样）。 */
export interface NodeLoad {
  node_id: number
  conns: number
  mbps: number
  capacity_mbps: number
  util_pct: number
}

/** EveningHour 是一个钟点段的回程质量（E-29，availability_pct=-1 无样本）。 */
export interface EveningHour {
  hour: number
  samples: number
  avg_rtt_ms: number
  avg_loss_pct: number
  availability_pct: number
}

/** EveningWindow 是晚高峰/平峰的汇总对比块。 */
export interface EveningWindow {
  samples: number
  avg_rtt_ms: number
  avg_loss_pct: number
  availability_pct: number
}

/** EveningReport 是晚高峰回程报表：24 小时明细 + 晚高峰（19–23 时）/平峰汇总。 */
export interface EveningReport {
  days: number
  hours: EveningHour[]
  peak: EveningWindow
  offpeak: EveningWindow
}

export function getEvening(days?: number): Promise<EveningReport> {
  return get<EveningReport>(`/api/evening${days ? `?days=${days}` : ''}`)
}

export interface User {
  id: number
  username: string
  sub_token: string
  quota_bytes: number
  reset_cycle: 'none' | 'day' | 'week' | 'month'
  expires_at: string | null
  enabled: boolean
  created_at: string
}

/** NodeShare 是单节点分享链接（P1-9）。 */
export interface NodeShare {
  node_id: number
  name: string
  link: string
}

/** BatchProcResult 是批量进程操作的逐节点回执（A-15）。 */
export interface BatchProcResult {
  node_id: number
  proc: string
  action: string
  ok: boolean
  error?: string
}

/** BatchConfigResult 是批量配置下发的逐节点回执（A-15）。 */
export interface BatchConfigResult {
  node_id: number
  ok: boolean
  status?: string
  sha256?: string
  error?: string
  http_status?: number
}

/** Alert 是一条异常告警（A-22）：agent 上报的进程崩溃/证书临期/高负载/配置错误。 */
export interface Alert {
  id: number
  node_id: number
  kind: 'proc_crash' | 'cert_expiry' | 'high_load' | 'config_error' | string
  severity: 'warning' | 'critical' | string
  proc?: string
  message: string
  state: 'active' | 'resolved'
  created_at: string
  updated_at: string
  resolved_at: string | null
}

export function getAlerts(params?: { state?: string; node_id?: number; kind?: string; limit?: number }): Promise<Alert[]> {
  const qs = new URLSearchParams()
  if (params?.state) qs.set('state', params.state)
  if (params?.node_id) qs.set('node_id', String(params.node_id))
  if (params?.kind) qs.set('kind', params.kind)
  if (params?.limit) qs.set('limit', String(params.limit))
  const q = qs.toString()
  return get<Alert[]>(`/api/alerts${q ? `?${q}` : ''}`)
}

export function resolveAlert(id: number): Promise<Alert> {
  return post<Alert>(`/api/alerts/${id}/resolve`)
}

/** UpgradeResult 是自升级指令受理回执（A-23）。 */
export interface UpgradeResult {
  node_id: number
  version: string
  accepted: boolean
  current_version: string
}

export function upgradeNode(id: number, body: { version: string; url: string; sha256?: string }): Promise<UpgradeResult> {
  return post<UpgradeResult>(`/api/nodes/${id}/upgrade`, body)
}

/** UserTemplate 是默认用户模板（P1-5）：新建用户可套用的默认配额/时长/重置周期。 */
export interface UserTemplate {
  quota_bytes: number
  expire_days: number
  reset_cycle: 'none' | 'day' | 'week' | 'month'
}

export interface CardBatch {
  id: number
  name: string
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  price_cents: number
  total: number
  expired_at: string | null
  created_by: string
  created_at: string
  remaining: number
  distributor_id: number // DS-1：0=面板自营
}

export interface CardCode {
  id: number
  batch_id: number
  code: string
  status: 'unused' | 'used' | 'disabled'
  fail_count: number
  used_by: number | null
  used_at: string | null
}

// ---- 支付三账对账（PAY-9）----

export interface PaymentOrder {
  id: number
  order_no: string
  user_id: number
  provider: string
  amount_cents: number
  product: string
  status: 'pending' | 'paid' | 'failed' | 'expired' | 'refunded' | string
  grant_type?: string
  grant_value?: number
  created_at: string
  paid_at: string | null
  /** 退款留痕（OD-2）：钱款退回经渠道后台操作，面板只记状态流转。 */
  refund_at?: string | null
  refund_note?: string
}

export interface PaymentTransaction {
  id: number
  order_no: string
  provider: string
  external_id: string
  amount_cents: number
  direction: 'in' | 'out'
  raw: string
  occurred_at: string
  created_at: string
}

export interface Grant {
  id: number
  order_no: string
  user_id: number
  grant_type: 'add_quota' | 'extend_days' | string
  grant_value: number
  snapshot: string
  created_at: string
}

/** ReconcileRow 是一个订单的三账视图：订单本体 + 挂靠的流水/发放 + 缺失环节文案。 */
export interface ReconcileRow extends PaymentOrder {
  transactions: PaymentTransaction[]
  grants: Grant[]
  missing: string[]
}

/** ReconcileOrphan 是游离记录：有流水/发放却找不到对应订单。 */
export interface ReconcileOrphan {
  kind: 'transaction' | 'grant'
  order_no: string
  provider: string
  detail: string
}

export interface ReconcileData {
  orders: ReconcileRow[]
  orphans: ReconcileOrphan[]
  summary: {
    paid_orders: number
    paid_cents: number
    refunded_orders: number
    refunded_cents: number
    txn_cents: number
    grants: number
  }
}

export function getReconcile(limit?: number): Promise<ReconcileData> {
  return get<ReconcileData>(`/api/payments/reconcile${limit ? `?limit=${limit}` : ''}`)
}

/** 单笔订单三账详情（OD-2）。 */
export function getPaymentOrder(orderNo: string): Promise<ReconcileRow> {
  return get<ReconcileRow>(`/api/payments/orders/${encodeURIComponent(orderNo)}`)
}

/** 退款流转（OD-2）：仅 paid 可退，钱款退回经渠道后台操作，此处只记留痕。 */
export function refundOrder(orderNo: string, note: string): Promise<ReconcileRow> {
  return post<ReconcileRow>(`/api/payments/orders/${encodeURIComponent(orderNo)}/refund`, { note })
}

// ---- 分流规则库（SAVE-1）----

export interface RuleSet {
  name: string
  domains?: string[]
  ips?: string[]
  outbound_tag: string
}

export interface RoutingConfig {
  node_id: number
  sha256: string
  config: string
  sets: RuleSet[]
}

export interface NodeConfigRow {
  id: number
  proc: string
  kind: string
  version: string
  sha256: string
  status: 'pending' | 'applied' | 'failed'
  reverted: boolean
  validated: boolean
  error?: string
  created_at: string
}

// ---- 供给配置（OS-1）----

/** CloudProvider 是云提供商凭证视图：机密不回显，只有 has_access_key。 */
export interface CloudProvider {
  id: number
  name: string
  type: string
  enabled: boolean
  has_access_key: boolean
}

export function getProviders(): Promise<CloudProvider[]> {
  return get<CloudProvider[]>('/api/providers')
}

export function createProvider(body: { name: string; type: string; access_key: string }): Promise<CloudProvider> {
  return post<CloudProvider>('/api/providers', body)
}

export function updateProvider(id: number, body: { name?: string; type?: string; access_key?: string; enabled?: boolean }): Promise<CloudProvider> {
  return put<CloudProvider>(`/api/providers/${id}`, body)
}

export function deleteProvider(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/providers/${id}`)
}

/** ProvisionTemplate 是机型模板：OS-2 渲染 HCL 供给，OS-4 入池补全元数据。 */
export interface ProvisionTemplate {
  id: number
  name: string
  provider_id: number
  plan: string
  region: string
  bw_mbps: number
  billing_type: string
  monthly_cost_cents: number
  traffic_price_cents: number
  direction: 'out' | 'in' | 'both' | string
  line_type: string
  role: string
  transport: string
  config: string
}

export function getTemplates(): Promise<ProvisionTemplate[]> {
  return get<ProvisionTemplate[]>('/api/provision-templates')
}

export function createTemplate(body: Partial<ProvisionTemplate>): Promise<ProvisionTemplate> {
  return post<ProvisionTemplate>('/api/provision-templates', body)
}

export function updateTemplate(id: number, body: Partial<ProvisionTemplate>): Promise<ProvisionTemplate> {
  return put<ProvisionTemplate>(`/api/provision-templates/${id}`, body)
}

export function deleteTemplate(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/provision-templates/${id}`)
}

/** ProvisionJob 是一次供给执行留痕（OS-2）。 */
export interface ProvisionJob {
  id: number
  template_id: number
  template_name: string
  action: 'plan' | 'apply' | string
  status: 'running' | 'ok' | 'failed' | string
  log: string
  created_at: string
  finished_at: string | null
}

export function runProvision(templateId: number, action: 'plan' | 'apply', name?: string): Promise<ProvisionJob> {
  return post<ProvisionJob>(`/api/provision-templates/${templateId}/${action}`, name ? { name } : {})
}

export function getProvisionJobs(limit?: number): Promise<ProvisionJob[]> {
  return get<ProvisionJob[]>(`/api/provision-jobs${limit ? `?limit=${limit}` : ''}`)
}

// ---- 域名前置（BR-2）----

/** DNSProvider 是 DNS 商凭证视图：机密不回显，只有 has_api_key。 */
export interface DNSProvider {
  id: number
  name: string
  type: string
  enabled: boolean
  has_api_key: boolean
}

export function getDNSProviders(): Promise<DNSProvider[]> {
  return get<DNSProvider[]>('/api/dns-providers')
}

export function createDNSProvider(body: { name: string; type: string; api_key: string }): Promise<DNSProvider> {
  return post<DNSProvider>('/api/dns-providers', body)
}

export function updateDNSProvider(id: number, body: { name?: string; type?: string; api_key?: string; enabled?: boolean }): Promise<DNSProvider> {
  return put<DNSProvider>(`/api/dns-providers/${id}`, body)
}

export function deleteDNSProvider(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/dns-providers/${id}`)
}

/** DNSFront 是域名前置记录：常态指向 primary_ip，判封切备用 IP 轮换，恢复回切。 */
export interface DNSFront {
  id: number
  name: string
  domain: string
  provider_id: number
  primary_ip: string
  backup_ips: string // JSON 字符串数组
  switched: boolean
  current_ip: string
  switch_index: number
  created_at: string
  updated_at: string
}

export function getDNSFronts(): Promise<DNSFront[]> {
  return get<DNSFront[]>('/api/dns-fronts')
}

export function createDNSFront(body: Partial<DNSFront>): Promise<DNSFront> {
  return post<DNSFront>('/api/dns-fronts', body)
}

export function updateDNSFront(id: number, body: Partial<DNSFront>): Promise<DNSFront> {
  return put<DNSFront>(`/api/dns-fronts/${id}`, body)
}

export function deleteDNSFront(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/dns-fronts/${id}`)
}

/** 分地域对账补偿入口（E-27）：把 {区域slug}.{前置域名} A 记录对齐各区域代表入口。 */
export function geoSyncDns(): Promise<{ changed: number }> {
  return post<{ changed: number }>('/api/dns-fronts/geo-sync', {})
}

// ---- 证书任务（BR-4）----

/** CertTask 是一张证书的编排任务：面板管编排与到期，签发执行 acme.sh。 */
export interface CertTask {
  id: number
  name: string
  domain: string
  sans: string
  method: 'dns-01' | 'http-01' | string
  provider_id: number
  ca: string
  state: 'pending' | 'issuing' | 'ok' | 'failed' | string
  not_after: string | null
  last_error: string
  last_attempt: string | null
  created_at: string
  updated_at: string
}

export function getCertTasks(): Promise<CertTask[]> {
  return get<CertTask[]>('/api/cert-tasks')
}

export function createCertTask(body: Partial<CertTask>): Promise<CertTask> {
  return post<CertTask>('/api/cert-tasks', body)
}

export function updateCertTask(id: number, body: Partial<CertTask>): Promise<CertTask> {
  return put<CertTask>(`/api/cert-tasks/${id}`, body)
}

export function deleteCertTask(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/cert-tasks/${id}`)
}

export function issueCertTask(id: number): Promise<{ ok: boolean }> {
  return post<{ ok: boolean }>(`/api/cert-tasks/${id}/issue`)
}

/** Recovery 是一条封禁恢复流水线（BR-1）：一节点同一时间至多一条。 */
export interface Recovery {
  id: number
  node_id: number
  node_name: string
  level: number
  state: 'running' | 'done' | 'failed' | string
  action: string
  action_state: '' | 'running' | 'ok' | 'failed' | 'skipped' | string
  last_error: string
  level_started_at: string
  started_at: string
  updated_at: string
  finished_at: string | null
}

/** RecoveryAction 是流水线每级动作的留痕（BR-5）：回放用。 */
export interface RecoveryAction {
  id: number
  recovery_id: number
  node_id: number
  node_name: string
  level: number
  action: string
  state: 'running' | 'ok' | 'failed' | 'skipped' | 'timeout' | string
  detail: string
  started_at: string
  updated_at: string
  finished_at: string | null
}

export function getRecoveries(limit = 50): Promise<Recovery[]> {
  return get<Recovery[]>(`/api/recoveries?limit=${limit}`)
}

export function getRecoveryActions(id: number): Promise<RecoveryAction[]> {
  return get<RecoveryAction[]>(`/api/recoveries/${id}/actions`)
}

// ---- 站内信通知中心（NT-1）----

export interface NotificationRow {
  id: number
  user_id: number
  type: 'announcement' | 'expiry' | 'traffic' | 'system' | string
  title: string
  body?: string
  read_at: string | null
  created_at: string
}

export function getNotifications(limit?: number): Promise<NotificationRow[]> {
  return get<NotificationRow[]>(`/api/notifications${limit ? `?limit=${limit}` : ''}`)
}

/** 发布公告：扇出给全部启用用户，返回落行数。 */
export function createAnnouncement(title: string, body: string): Promise<{ created: number }> {
  return post('/api/notifications/announcement', { title, body })
}

export function deleteNotification(id: number): Promise<{ ok: boolean }> {
  return del(`/api/notifications/${id}`)
}

// ---- 节点被管进程状态（SAVE-3：心跳快照落库，含缓存命中统计）----

/** NodeProcRow 是节点被管进程的最近状态快照（agent 心跳上报）。 */
export interface NodeProcRow {
  proc: string
  state: 'running' | 'stopped' | 'crashed' | string
  pid?: number
  restarts: number
  metrics?: Record<string, number>
  updated_at: string
}

export function getNodeProcs(id: number): Promise<NodeProcRow[]> {
  return get<NodeProcRow[]>(`/api/nodes/${id}/procs`)
}

// ---- 流量节省报表（SAVE-7：save_stats 按日汇总，与成本看板同页）----

/** SaveStatsRow 是一个节点一天的节省汇总（直连/拦截字节 + 折算费用）。 */
export interface SaveStatsRow {
  node_id: number
  name: string
  day: string
  direct_bytes: number
  blocked_bytes: number
  cache_hit_bytes: number
  cost_cents: number
}

/** SaveStatsReport 是节省报表：按日行 + 全网合计（days 窗口内）。 */
export interface SaveStatsReport {
  days: number
  rows: SaveStatsRow[]
  total: {
    direct_bytes: number
    blocked_bytes: number
    cache_hit_bytes: number
    cost_cents: number
  }
}

export function getSaveStats(days?: number): Promise<SaveStatsReport> {
  return get<SaveStatsReport>(`/api/save-stats${days ? `?days=${days}` : ''}`)
}

// ---- 入口域名与断联态（TOUCH-3/TOUCH-7：断联容灾数据源与开关）----

/** EntryDomain 是入口域名（断联容灾与域名例行邮件的共同数据源）。 */
export interface EntryDomain {
  id: number
  domain: string
  role: 'primary' | 'backup' | string
  region?: string
  enabled: boolean
  updated_at: string
}

export function listEntryDomains(): Promise<EntryDomain[]> {
  return get('/api/entry-domains')
}

export function createEntryDomain(body: { domain: string; role: string; region?: string }): Promise<EntryDomain> {
  return post('/api/entry-domains', body)
}

export function updateEntryDomain(id: number, body: Partial<EntryDomain>): Promise<EntryDomain> {
  return put(`/api/entry-domains/${id}`, body)
}

export function deleteEntryDomain(id: number): Promise<void> {
  return del(`/api/entry-domains/${id}`)
}

/** OutageState 是断联态标记（管理员手动开关，订阅注释随之加警告行）。 */
export interface OutageState {
  enabled: boolean
}

export function getOutage(): Promise<OutageState> {
  return get('/api/outage')
}

export function putOutage(enabled: boolean): Promise<OutageState> {
  return put('/api/outage', { enabled })
}

// ---- 管理员登录与两步验证（安全设计 §1）----

export function adminLogin(body: { username: string; password: string; totp?: string }): Promise<{ token: string }> {
  return post<{ token: string }>('/admin/login', body)
}

export function getTwoFAStatus(): Promise<{ enabled: boolean }> {
  return get('/admin/2fa')
}

export function setupTwoFA(): Promise<{ secret: string; otpauth_url: string }> {
  return post('/admin/2fa/setup')
}

/** 绑定确认：校验一次 TOTP 后生效，明文恢复码只此一次返回。 */
export function enableTwoFA(code: string): Promise<{ recovery_codes: string[] }> {
  return post('/admin/2fa/enable', { code })
}

export function disableTwoFA(password: string): Promise<{ ok: boolean }> {
  return post('/admin/2fa/disable', { password })
}

/** LoginLogRow 是一条登录审计（login_logs，安全设计 §1）。 */
export interface LoginLogRow {
  id: number
  username: string
  ip: string
  ua: string
  ok: boolean
  created_at: string
}

export function getLoginLogs(page: number, pageSize: number): Promise<{ items: LoginLogRow[]; total: number; page: number; page_size: number }> {
  return get(`/admin/login-logs?page=${page}&page_size=${pageSize}`)
}

// ---- 成本参考库（E-31，套餐与成本设计 §4）----
// 手录成本唯一权威，参考价只产偏差提示不改价。

/** RefQuery 是一次牌价试查的定位键（商家/区域/配置档）。 */
export interface RefQuery {
  provider: string
  region: string
  spec: string
}

/** RefQuote 是一条参考牌价（快照口径）。 */
export interface RefQuote {
  monthly_cents: number
  traffic_price: number
  currency: string
  url?: string
  at: string
}

/** RefDeviation 是手录价 vs 牌价的偏差结论（pct 正=手录贵）。 */
export interface RefDeviation {
  pct: number
  off: boolean
}

/** RefProbe 是单点试查结果；未命中 hit=false，不可比时给 reason。 */
export interface RefProbe {
  source: string
  query: RefQuery
  hit: boolean
  quote?: RefQuote
  deviation?: RefDeviation
  reason?: string
}

/** RefCheckReport 是批量对账报告（只列命中且可比的行）。 */
export interface RefCheckReport {
  source: string
  tolerance_pct: number
  checked: number
  hits: number
  mismatches: number
  items: Array<{
    node_id: number
    node_name: string
    query: RefQuery
    quote: RefQuote
    manual_cents: number
    currency: string
    deviation: RefDeviation
  }>
}

export function probeCostRef(params: Record<string, string | number>): Promise<RefProbe> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== '' && v !== undefined) qs.set(k, String(v))
  }
  return get(`/api/cost/ref?${qs}`)
}

export function checkCostRef(): Promise<RefCheckReport> {
  return get('/api/cost/ref-check')
}

export function saveCostRefTable(table: string): Promise<{ saved?: boolean; cleared?: boolean; rows?: number }> {
  return put('/api/cost/ref-table', { table })
}

// ---- 价格关注（E-32，套餐与成本设计 §4.2）----
// 关注条件盯一个牌价键，扫描命中降价/到位经 Herald price_alert 提示。

/** PriceWatch 是一条关注条件（含扫描动态：最新/上次快照与涨跌）。 */
export interface PriceWatch {
  id: number
  provider: string
  region: string
  spec: string
  target_price: number
  enabled: boolean
  latest_cents?: number
  prev_cents?: number
  change_pct?: number
  at_target: boolean
  latest_url?: string
  captured_at?: string
  snapshot_done: boolean
}

/** PriceSnapshot 是一次关注键的牌价快照。 */
export interface PriceSnapshot {
  id: number
  watch_id: number
  monthly_cents: number
  source: string
  url?: string
  captured_at: string
}

export function listPriceWatches(): Promise<PriceWatch[]> {
  return get('/api/cost/watches')
}

export function createPriceWatch(input: { provider: string; region: string; spec: string; target_price?: number }): Promise<PriceWatch> {
  return post('/api/cost/watches', input)
}

export function updatePriceWatch(id: number, input: { target_price?: number; enabled?: boolean }): Promise<PriceWatch> {
  return put(`/api/cost/watches/${id}`, input)
}

export function deletePriceWatch(id: number): Promise<{ deleted: boolean }> {
  return del(`/api/cost/watches/${id}`)
}

export function listPriceSnapshots(id: number, limit = 20): Promise<PriceSnapshot[]> {
  return get(`/api/cost/watches/${id}/snapshots?limit=${limit}`)
}

// 分销代理（DS-1 账目层 / DS-2 管理面）。
export interface Distributor {
  id: number
  username: string
  discount_percent: number
  note: string
  enabled: boolean
  created_at: string
  updated_at: string
  sale_cents?: number
  commission_cents?: number
  payout_cents?: number
  balance_cents?: number
}

export interface DistributorLedger {
  id: number
  distributor_id: number
  order_no: string
  kind: 'sale' | 'commission' | 'payout' | 'adjust'
  amount_cents: number
  note: string
  created_at: string
}

export function listDistributors(): Promise<{ distributors: Distributor[] }> {
  return get('/api/distributors')
}

export function createDistributor(input: { username: string; password: string; discount_percent?: number; note?: string }): Promise<{ distributor: Distributor }> {
  return post('/api/distributors', input)
}

export function updateDistributor(id: number, input: { password?: string; discount_percent?: number; note?: string; enabled?: boolean }): Promise<{ distributor: Distributor }> {
  return put(`/api/distributors/${id}`, input)
}

export function distributorLedger(id: number, limit = 50): Promise<{ ledger: DistributorLedger[]; balance_cents: number }> {
  return get(`/api/distributors/${id}/ledger?limit=${limit}`)
}

// payout 结算打款（金额不超未结算余额）、adjust 人工调（±金额，0 拒）。
export function distributorPayout(id: number, input: { amount_cents: number; note?: string }): Promise<{ balance_cents: number }> {
  return post(`/api/distributors/${id}/payout`, input)
}

export function distributorAdjust(id: number, input: { amount_cents: number; note?: string }): Promise<{ balance_cents: number }> {
  return post(`/api/distributors/${id}/adjust`, input)
}

// 代理自面（DS-3）：只读视图，结算动作在管理员面（DS-2）。
export function distLogin(input: { username: string; password: string }): Promise<{ token: string; id: number; username: string; discount_percent: number }> {
  return post('/distributor/login', input)
}

export interface DistMe {
  id: number
  username: string
  discount_percent: number
  note: string
  enabled: boolean
  created_at: string
  sale_cents: number
  commission_cents: number
  payout_cents: number
  balance_cents: number
}

export interface DistCustomer {
  id: number
  username: string
  redeemed_cnt: number
  quota_bytes: number
  used_bytes: number
  expires_at: string | null
  active: boolean
  over_quota: boolean
  redeemed_last: string | null
}

export interface DistOrderRow {
  order_no: string
  user_id: number
  username: string
  provider: string
  amount_cents: number
  product: string
  status: string
  distributor_id: number
  created_at: string
  paid_at: string | null
}

export function getDistMe(): Promise<DistMe> {
  return get('/distributor/api/me')
}

export function getDistBatches(): Promise<CardBatch[]> {
  return get('/distributor/api/batches')
}

export function getDistBatchCodes(id: number): Promise<CardCode[]> {
  return get(`/distributor/api/batches/${id}/codes`)
}

export function getDistCustomers(): Promise<{ customers: DistCustomer[] }> {
  return get('/distributor/api/customers')
}

export function getDistOrders(limit = 50): Promise<{ orders: DistOrderRow[] }> {
  return get(`/distributor/api/orders?limit=${limit}`)
}

export function getDistLedger(limit = 100): Promise<{ ledger: DistributorLedger[]; balance_cents: number }> {
  return get(`/distributor/api/ledger?limit=${limit}`)
}
