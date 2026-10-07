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
  expires_at: string | null
  enabled: boolean
  created_at: string
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
