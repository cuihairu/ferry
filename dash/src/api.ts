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
  status: 'pending' | 'paid' | 'failed' | 'expired' | string
  grant_type?: string
  grant_value?: number
  created_at: string
  paid_at: string | null
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
  summary: { paid_orders: number; paid_cents: number; txn_cents: number; grants: number }
}

export function getReconcile(limit?: number): Promise<ReconcileData> {
  return get<ReconcileData>(`/api/payments/reconcile${limit ? `?limit=${limit}` : ''}`)
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
