import { auth } from './auth'

// API client: uniformly parses the backend's {error} payload and automatically carries the subscription token.

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

// ---- Resource types (aligned with the server's JSON payloads) ----

/** Me is the GET /api/panel/me response: identity, usage and availability. */
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

/** RedeemResult is the entitlement receipt after a successful redemption. */
export interface RedeemResult {
  order_no: string
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  quota_bytes: number
  expires_at: string | null
}

/** PanelProduct is one item of GET /api/panel/products: a sellable plan (priced, not expired, with remaining stock). */
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

/** PanelChannel is one item of GET /api/panel/channels: a payment channel's registration state (SHOP-1). */
export interface PanelChannel {
  name: string
  enabled: boolean
}

export function getPanelChannels(): Promise<PanelChannel[]> {
  return get<PanelChannel[]>('/api/panel/channels')
}

/** CreateOrderResult is the POST /api/panel/orders response: the order anchor + the cashier URL. */
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

/** OrderStatus is the GET /api/panel/orders/:order_no response: a single order's final state and grants. */
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

/** OrderRow is one item of GET /api/panel/orders: an order + its merged grant records. */
export interface OrderRow {
  order_no: string
  provider: string
  product: string
  amount_cents: number
  status: string
  created_at: string
  paid_at: string | null
  /** Refund time (OD-2): the refund is executed by an admin in the channel back office, then recorded on the panel. */
  refund_at: string | null
  grants: Array<{ grant_type: string; grant_value: number }>
}

// ---- Notification center (NT-1) ----

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

/** NotifyPrefs is the GET/PUT /api/panel/notify-prefs payload (NT-2). */
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

/** SavingsSummary is the GET /api/panel/savings response (SAVE-8): this month's "saved for you". */
export interface SavingsSummary {
  window: string
  direct_bytes: number
  cache_hit_bytes: number
  blocked_bytes: number
}

export function getSavings(): Promise<SavingsSummary> {
  return get<SavingsSummary>('/api/panel/savings')
}

/** ContactInfo is the GET/PUT /api/panel/contact payload (TOUCH-1). */
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

// ---- Custom subscriptions (ferry-sub-filters, 2026-11-21) ----

/** SubFilter is the filtering dimensions of a custom subscription (all three optional; an empty array = no filtering on that dimension). */
export interface SubFilter {
  regions?: string[]
  labels?: string[]
  protocols?: string[]
}

/** SubConfig is the per-sub configuration customization of a custom subscription (defaults keep the existing behavior). */
export interface SubConfig {
  remark_template?: string
  sort?: '' | 'region' | 'rtt' | 'name'
  max_nodes?: number
  include_rtt?: boolean
}

/** SubscriptionRow is a /api/subscriptions record: filter/config hold the raw JSON text. */
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
