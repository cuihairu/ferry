import { auth } from './auth'

// API 客户端：统一解析后端 {error} 错误载荷，自动携带订阅令牌。

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {}
  if (init?.body) headers['Content-Type'] = 'application/json'
  if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`
  const res = await fetch(path, { ...init, headers })
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

// ---- 资源类型（对齐 server 的 JSON 载荷）----

/** Me 是 GET /api/panel/me 的响应：身份、用量与可用状态。 */
export interface Me {
  id: number
  username: string
  sub_token: string
  quota_bytes: number
  used_bytes: number
  expires_at: string | null
  active: boolean
  over_quota: boolean
  created_at: string
}

/** RedeemResult 是兑换成功后的权益回执。 */
export interface RedeemResult {
  order_no: string
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  quota_bytes: number
  expires_at: string | null
}

/** OrderRow 是 GET /api/panel/orders 的单条：订单 + 归并的发放记录。 */
export interface OrderRow {
  order_no: string
  provider: string
  product: string
  amount_cents: number
  status: string
  created_at: string
  paid_at: string | null
  /** 退款时间（OD-2）：退款由管理员在渠道后台操作后在面板留痕。 */
  refund_at: string | null
  grants: Array<{ grant_type: string; grant_value: number }>
}

// ---- 通知中心（NT-1）----

export interface NotificationRow {
  id: number
  type: 'announcement' | 'expiry' | 'traffic' | 'system' | string
  title: string
  body?: string
  read_at: string | null
  created_at: string
}

export function getNotifications(unread = false): Promise<NotificationRow[]> {
  return get<NotificationRow[]>(`/api/panel/notifications${unread ? '?unread=1' : ''}`)
}

export function getUnreadCount(): Promise<number> {
  return get<{ count: number }>('/api/panel/notifications/unread-count').then((v) => v.count)
}

export function markRead(id: number): Promise<{ ok: boolean }> {
  return post(`/api/panel/notifications/${id}/read`)
}

export function markAllRead(): Promise<{ updated: number }> {
  return post('/api/panel/notifications/read-all')
}

/** NotifyPrefs 是 GET/PUT /api/panel/notify-prefs 的载荷（NT-2）。 */
export interface NotifyPrefs {
  notify_expiry: boolean
  notify_traffic: boolean
  traffic_warn_percent: number
}

export function getNotifyPrefs(): Promise<NotifyPrefs> {
  return get<NotifyPrefs>('/api/panel/notify-prefs')
}

export function updateNotifyPrefs(p: Partial<NotifyPrefs>): Promise<{ ok: boolean }> {
  return put('/api/panel/notify-prefs', p)
}
