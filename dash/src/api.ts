// API 客户端：统一解析后端 {error} 错误载荷。
export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    ...init,
  })
  const text = await res.text()
  const body = text ? JSON.parse(text) : null
  if (!res.ok) {
    throw new ApiError(res.status, body?.error ?? res.statusText)
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
