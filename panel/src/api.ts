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

export function patch<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'PATCH', body: JSON.stringify(body) })
}

export function del<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' })
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
  device_limit: number
  ip_limit: number
  devices: number
  ips: number
}

/** RedeemResult 是兑换成功后的权益回执。 */
export interface RedeemResult {
  order_no: string
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  quota_bytes: number
  expires_at: string | null
}

/** PanelProduct 是 GET /api/panel/products 的单条：可售套餐（设价未过期有余量）。 */
export interface PanelProduct {
  id: number
  name: string
  price_cents: number
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  remaining: number
}

export function getPanelProducts(): Promise<PanelProduct[]> {
  return get<PanelProduct[]>('/api/panel/products')
}

/** PanelChannel 是 GET /api/panel/channels 的单条：渠道注册态（SHOP-1）。 */
export interface PanelChannel {
  name: string
  enabled: boolean
}

export function getPanelChannels(): Promise<PanelChannel[]> {
  return get<PanelChannel[]>('/api/panel/channels')
}

/** CreateOrderResult 是 POST /api/panel/orders 的响应：订单锚点 + 收银台地址。 */
export interface CreateOrderResult {
  order_no: string
  provider: string
  product: string
  amount_cents: number
  pay_url: string
  external_id: string
  expires_at: string
}

export function createPanelOrder(p: { batch_id: number; provider: string; coupon_code?: string }): Promise<CreateOrderResult> {
  return post<CreateOrderResult>('/api/panel/orders', p)
}

/** OrderStatus 是 GET /api/panel/orders/:order_no 的响应：单笔订单终态与发放。 */
export interface OrderStatus {
  order_no: string
  provider: string
  product: string
  amount_cents: number
  status: string
  grant_type: string
  grant_value: number
  created_at: string
  paid_at: string | null
  refund_at: string | null
  grants: Array<{ grant_type: string; grant_value: number }>
}

export function getPanelOrderStatus(orderNo: string): Promise<OrderStatus> {
  return get<OrderStatus>(`/api/panel/orders/${encodeURIComponent(orderNo)}`)
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

/** SavingsSummary 是 GET /api/panel/savings 的响应（SAVE-8）：本月「已为你省下」。 */
export interface SavingsSummary {
  window: string
  direct_bytes: number
  cache_hit_bytes: number
  blocked_bytes: number
}

export function getSavings(): Promise<SavingsSummary> {
  return get<SavingsSummary>('/api/panel/savings')
}

/** ContactInfo 是 GET/PUT /api/panel/contact 的载荷（TOUCH-1）。 */
export interface ContactInfo {
  email: string
  tg_chat_id: string
  routine_emails: boolean
  stale: boolean
  bound_at?: string | null
}

export function getContact(): Promise<ContactInfo> {
  return get<ContactInfo>('/api/panel/contact')
}

export function updateContact(p: Partial<ContactInfo>): Promise<ContactInfo> {
  return put('/api/panel/contact', p)
}

// ---- 自建订阅（ferry-sub-filters，2026-11-21）----

/** SubFilter 是自建订阅的过滤维度（三维度全可选，空数组=该维度不过滤）。 */
export interface SubFilter {
  regions?: string[]
  labels?: string[]
  protocols?: string[]
}

/** SubConfig 是自建订阅的 per-sub 配置定制（缺省沿用现有行为）。 */
export interface SubConfig {
  remark_template?: string
  sort?: '' | 'region' | 'rtt' | 'name'
  max_nodes?: number
  include_rtt?: boolean
}

/** SubscriptionRow 是 /api/subscriptions 的记录：filter/config 为 JSON 原文。 */
export interface SubscriptionRow {
  id: number
  user_id: number
  token: string
  name: string
  filter: string
  config: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface SubCreatePayload {
  name: string
  filter?: SubFilter
  config?: SubConfig
}

export interface SubUpdatePayload {
  name?: string
  filter?: SubFilter
  config?: SubConfig
  enabled?: boolean
}

export function getSubscriptions(): Promise<SubscriptionRow[]> {
  return get<SubscriptionRow[]>('/api/subscriptions')
}

export function createSubscription(p: SubCreatePayload): Promise<SubscriptionRow> {
  return post('/api/subscriptions', p)
}

export function updateSubscription(id: number, p: SubUpdatePayload): Promise<SubscriptionRow> {
  return patch(`/api/subscriptions/${id}`, p)
}

export function deleteSubscription(id: number): Promise<{ ok: boolean }> {
  return del(`/api/subscriptions/${id}`)
}

export function resetSubscriptionToken(id: number): Promise<SubscriptionRow> {
  return post(`/api/subscriptions/${id}/reset-token`)
}
