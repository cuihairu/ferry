import { auth, logout, logoutDist } from './auth'

// API client: uniformly parses the backend's {error} payload; the admin JWT travels along, and an
// admin-surface 401 (expired token) clears the session and returns to the login page. code is the
// backend's machine-readable error code (e.g. totp_required/totp_invalid on login, security design §1).
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
  // DS-3: distributor self-service endpoints inject the separate distributor token; the admin surface keeps the admin token.
  const isDist = path.startsWith('/distributor/api') || path === '/distributor/login'
  const token = isDist ? auth.distToken : auth.token
  if (token) headers['Authorization'] = `Bearer ${token}`
  const res = await fetch(path, { ...init, headers })
  const text = await res.text()
  const body = text ? JSON.parse(text) : null
  if (!res.ok) {
    if (res.status === 401) {
      if (isDist && auth.distToken && path !== '/distributor/login') {
        // The distributor token expired or was disabled (disabling kicks immediately) — clear the distributor session and return to the distributor login page.
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

// postRaw POSTs a raw request body (used for rule library data file distribution, SAVE-1).
export function postRaw<T>(path: string, body: Blob): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    body,
    headers: { 'Content-Type': 'application/octet-stream' },
  })
}

// ---- Resource types (aligned with the server's JSON payloads) ----

export interface Node {
  id: number
  name: string
  address: string
  port: number
  protocol: string
  config: string
  enabled: boolean
  /** maintenance mode (F-0746): node stays listed but is skipped by allocation and subscriptions. */
  maintenance: boolean
  token: string
  old_token?: string
  token_rotated_at?: string | null
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

/** DimensionStatus is the status light for the region/ISP aggregate verdict (GET /api/dimension-status). */
export interface DimensionStatus {
  scope: 'region' | 'isp'
  key: string
  state: 'healthy' | 'degraded' | 'failed'
  reason: string
}

/** LandingAssignment is one landing assignment (E-20): entry or region -> landing and weight. */
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

/** TransportHealth is the liveness summary of one transport within a region (E-17). */
export interface TransportHealth {
  transport: string
  total: number
  alive: number
}

/** TransportRow is the transport verdict for a region: current mainstream, recommended, and whether a line switch is advised. */
export interface TransportRow {
  region: string
  transports: TransportHealth[]
  current: string
  recommended: string
  switchneeded: boolean
}

/** AllocPolicy holds the per-direction tiers for automatic landing assignment plus the off-peak rebalancing window (E-21/E-22). */
export interface AllocPolicy {
  out: string
  in: string
  rebalance_start: number
  rebalance_end: number
  /** Multiplicative score coefficients for balanced (configurable in dash); the 0.5/0.3/0.2 defaults match the current formula. */
  w_load: number
  w_cost: number
  w_premium: number
}

/** AllocData bundles the policy with the auto-assignment rows currently in effect. */
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

/** QuotaLinkSetting is the quota-linkage configuration (SAVE-6): past a threshold, the user's subscription is automatically downgraded to a low-cost-tier entry. */
export interface QuotaLinkSetting {
  enabled: boolean
  /** Traffic-tier threshold (1-100): triggers when window usage reaches N% of the personal quota; 0 = no traffic-tier trigger */
  traffic_percent: number
  /** Cost-tier threshold (cents): triggers when the window's converted cost reaches N cents; 0 = no cost-tier trigger */
  cost_cents: number
  /** Downgrade cost-tier line (cents/GB): nodes with a monthly plan or unit price at or below the line count as low-cost tier; 0 = monthly plans only */
  max_price_cents: number
}

/** QuotaActionRow is one quota-linkage downgrade record (empty released_at = downgrade still in effect). */
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

/** QuotaLinkData is the linkage setting, the downgrade records (active ones first) and the username mapping. */
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

/** EventRow is one event outbox row (HERALD-1; failed = retries exhausted, dead-lettered and flagged red). */
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

/** EventsData is the event list plus per-status counts (failed>0 means an un-drained channel failure). */
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

/** EventDeliveryRow is one channel delivery receipt (HC-1, Herald receipts persisted). */
export interface EventDeliveryRow {
  id: number
  event_id: number
  channel: string
  status: 'sent' | 'failed'
  detail?: string
  at: string
}

export interface EventChannelHealth {
  channel: string
  sent: number
  failed: number
  health: 'healthy' | 'degraded' | 'down'
  last_failed_at?: string
  last_detail?: string
}

export function getEventDeliveries(channel = '', limit = 100): Promise<{ rows: EventDeliveryRow[]; channels: EventChannelHealth[] }> {
  const p = new URLSearchParams()
  if (channel) p.set('channel', channel)
  p.set('limit', String(limit))
  return get(`/api/events/deliveries?${p}`)
}

export function testEventChannel(): Promise<EventRow> {
  return post<EventRow>('/api/events/test')
}

/** EventSample is the most recent sample actually persisted for an alert class (HC-3, zero-drift preview). */
export interface EventSample {
  kind: string
  severity: 'critical' | 'warning' | 'info'
  title: string
  body: string
  status: string
  occurred_at: string
}

export function getEventSamples(): Promise<EventSample[]> {
  return get('/api/events/samples')
}

/** NodeCost is one node's monthly cost view (E-23). */
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

/** GroupCost is the cost summary along the region/ISP dimension (E-23). */
export interface GroupCost {
  key: string
  nodes: number
  traffic_cents: number
  fixed_cents: number
  projected_cents: number
}

/** NodeLoad is a node load snapshot: connection count / measured throughput / bandwidth utilization (E-25, util_pct=-1 means no sample). */
export interface NodeLoad {
  node_id: number
  conns: number
  mbps: number
  capacity_mbps: number
  util_pct: number
}

/** EveningHour is the return-trip quality of one hour slot (E-29, availability_pct=-1 means no samples). */
export interface EveningHour {
  hour: number
  samples: number
  avg_rtt_ms: number
  avg_loss_pct: number
  availability_pct: number
}

/** EveningWindow is a summary comparison block for evening peak / off-peak. */
export interface EveningWindow {
  samples: number
  avg_rtt_ms: number
  avg_loss_pct: number
  availability_pct: number
}

/** EveningReport is the evening-peak return-trip report: 24-hour detail plus evening-peak (19-23) / off-peak summaries. */
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
  bw_up_mbps: number
  bw_down_mbps: number
  device_limit: number
  ip_limit: number
  online: boolean
  last_node_id?: number | null
  last_node?: string
  last_seen_at?: string | null
  // Split gate state (G3): primary cause plus the two independent judgments.
  status?: 'active' | 'expired' | 'limited' | 'disabled'
  expired?: boolean
  limited?: boolean
}

/** NodeShare is a single-node share link (P1-9). */
export interface NodeShare {
  node_id: number
  name: string
  link: string
}

/** BatchProcResult is the per-node receipt of a batch process operation (A-15). */
export interface BatchProcResult {
  node_id: number
  proc: string
  action: string
  ok: boolean
  error?: string
}

/** BatchConfigResult is the per-node receipt of a batch config push (A-15). */
export interface BatchConfigResult {
  node_id: number
  ok: boolean
  status?: string
  sha256?: string
  error?: string
  http_status?: number
}

/** Alert is one anomaly alert (A-22): process crash / cert near expiry / high load / config error reported by the agent. */
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

/** NodeTokenRotation is the response of POST /api/nodes/:id/token. */
export function rotateNodeToken(id: number): Promise<Node> {
  return post<Node>(`/api/nodes/${id}/token`)
}

/** revokeNodeToken closes the rotation grace window immediately. */
export function revokeNodeToken(id: number, token: string): Promise<Node> {
  return post<Node>(`/api/nodes/${id}/token/revoke`, { token })
}

export function resolveAlert(id: number): Promise<Alert> {
  return post<Alert>(`/api/alerts/${id}/resolve`)
}

/** UpgradeResult is the acceptance receipt of a self-upgrade instruction (A-23). */
export interface UpgradeResult {
  node_id: number
  version: string
  accepted: boolean
  current_version: string
}

export function upgradeNode(id: number, body: { version: string; url: string; sha256?: string }): Promise<UpgradeResult> {
  return post<UpgradeResult>(`/api/nodes/${id}/upgrade`, body)
}

/** UserTemplate is the default user template (P1-5): default quota / duration / reset cycle / bandwidth limits that can be applied when creating a user. */
export interface UserTemplate {
  quota_bytes: number
  expire_days: number
  reset_cycle: 'none' | 'day' | 'week' | 'month'
  bw_up_mbps: number
  bw_down_mbps: number
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
  distributor_id: number // DS-1: 0 = sold through the panel itself
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

// ---- Payment three-ledger reconciliation (PAY-9) ----

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
  /** Refund record (OD-2): money returns are executed in the channel's back office; the panel only records the state transition. */
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

/** ReconcileRow is the three-ledger view of one order: the order itself + attached transactions/grants + wording for the missing links. */
export interface ReconcileRow extends PaymentOrder {
  transactions: PaymentTransaction[]
  grants: Grant[]
  missing: string[]
}

/** ReconcileOrphan is a stray record: it has a transaction/grant but no matching order. */
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

/** Three-ledger detail for a single order (OD-2). */
export function getPaymentOrder(orderNo: string): Promise<ReconcileRow> {
  return get<ReconcileRow>(`/api/payments/orders/${encodeURIComponent(orderNo)}`)
}

/** Refund transition (OD-2): only paid orders can be refunded; the money return happens in the channel's back office, so this only records it. */
export function refundOrder(orderNo: string, note: string): Promise<ReconcileRow> {
  return post<ReconcileRow>(`/api/payments/orders/${encodeURIComponent(orderNo)}/refund`, { note })
}

// ---- Routing rule library (SAVE-1) ----

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

// ---- Provisioning configuration (OS-1) ----


// ---- Cloud resource management (F-0411~F-0432) ----

/** CloudProvider is a provider account: credentials stay sealed server-side (has_access_key only). */
export interface CloudProvider {
  id: number
  name: string
  type: string
  enabled: boolean
  has_access_key: boolean
  note?: string
  region?: string
  key_version?: number
  prev_set_at?: string | null
}

export interface CloudResource {
  id: number
  provider_id: number
  external_id: string
  region: string
  name: string
  state: string
  public_ip: string
  bw_mbps: number
  spec: string
  tags: string
  node_id?: number | null
  unbound_at?: string | null
  last_seen_at: string
  missing: boolean
  missing_since?: string | null
  last_sync_at?: string | null
  created_at: string
  updated_at: string
}

export interface CloudSyncLog {
  id: number
  provider_id: number
  region: string
  trigger: string
  ok: boolean
  added: number
  updated: number
  missing: number
  error?: string
  started_at: string
  finished_at: string
}

export interface CloudOperation {
  id: number
  resource_id: number
  batch_id?: string
  provider_id: number
  op: string
  params?: string
  state: string
  idempotency_key: string
  actor: string
  actor_kind: string
  error?: string
  provider_code?: string
  result?: string
  confirmed: boolean
  allow_cancel: boolean
  created_at: string
  updated_at: string
  finished_at?: string | null
}

export interface CloudOperationLog {
  id: number
  operation_id: number
  state: string
  message: string
  created_at: string
}

export interface CloudProviderTest {
  ok: boolean
  mode: string
  message?: string
  error?: string
  error_type?: string
  api_version?: string
  capabilities?: unknown
}

export function getProviders(): Promise<CloudProvider[]> {
  return get<CloudProvider[]>('/api/providers')
}

export function createProvider(body: { name: string; type: string; access_key: string; note?: string; region?: string }): Promise<CloudProvider> {
  return post<CloudProvider>('/api/providers', body)
}

export function updateProvider(id: number, body: Partial<{ name: string; type: string; access_key: string; enabled: boolean; note: string; region: string }>): Promise<CloudProvider> {
  return put<CloudProvider>(`/api/providers/${id}`, body)
}

export function deleteProvider(id: number): Promise<{ ok: boolean }> {
  return del<{ ok: boolean }>(`/api/providers/${id}`)
}

export function testProvider(id: number): Promise<CloudProviderTest> {
  return post<CloudProviderTest>(`/api/providers/${id}/test`)
}

export function syncProvider(id: number): Promise<{ added: number; updated: number; missing: number; ok: boolean }> {
  return post(`/api/providers/${id}/sync`)
}

export function getCloudResources(params?: { provider_id?: number; region?: string; state?: string; node_id?: number }): Promise<CloudResource[]> {
  const q = new URLSearchParams()
  if (params?.provider_id) q.set('provider_id', String(params.provider_id))
  if (params?.region) q.set('region', params.region)
  if (params?.state) q.set('state', params.state)
  if (params?.node_id) q.set('node_id', String(params.node_id))
  return get<CloudResource[]>(`/api/cloud-resources${q.size ? `?${q}` : ''}`)
}

export function getCloudResource(id: number): Promise<CloudResource> {
  return get<CloudResource>(`/api/cloud-resources/${id}`)
}

export function bindCloudResource(id: number, nodeId: number): Promise<{ ok: boolean }> {
  return post(`/api/cloud-resources/${id}/bind`, { node_id: nodeId })
}

export function unbindCloudResource(id: number): Promise<{ ok: boolean }> {
  return post(`/api/cloud-resources/${id}/unbind`)
}

export function linkCloudResource(id: number): Promise<{ resource_id: number; effect: unknown; changed: boolean }> {
  return post(`/api/cloud-resources/${id}/link`)
}

export function getCloudSyncLogs(providerId?: number): Promise<CloudSyncLog[]> {
  return get<CloudSyncLog[]>(`/api/cloud-sync-logs${providerId ? `?provider_id=${providerId}` : ''}`)
}

export function getCloudOperations(params?: { batch_id?: string; resource_id?: number; state?: string }): Promise<CloudOperation[]> {
  const q = new URLSearchParams()
  if (params?.batch_id) q.set('batch_id', params.batch_id)
  if (params?.resource_id) q.set('resource_id', String(params.resource_id))
  if (params?.state) q.set('state', params.state)
  return get<CloudOperation[]>(`/api/cloud-operations${q.size ? `?${q}` : ''}`)
}

export function getCloudOperation(id: number): Promise<{ operation: CloudOperation; logs: CloudOperationLog[] }> {
  return get(`/api/cloud-operations/${id}`)
}

export function createCloudOperation(body: { provider_id: number; op: string; specs: { resource_id: number; params?: Record<string, unknown> }[]; confirmed?: boolean }): Promise<{ id: number; count: number; batch_id: string; operations: CloudOperation[] }> {
  return post('/api/cloud-operations', body)
}

export function cancelCloudOperation(id: number): Promise<{ ok: boolean }> {
  return post(`/api/cloud-operations/${id}/cancel`)
}

export function getCloudShutdownCheck(nodeId: number): Promise<{ node_id: number; risk: boolean; recent_conns: number; message: string }> {
  return get(`/api/nodes/${nodeId}/cloud-shutdown-check`)
}

export function drainCloudNode(nodeId: number, reason: string): Promise<{ ok: boolean }> {
  return post(`/api/nodes/${nodeId}/cloud-drain`, { reason })
}

export function checkCloudNodeBoot(nodeId: number): Promise<{ agent_online: boolean; heartbeat_fresh: boolean; config_valid: boolean; crash_alert: boolean; healthy: boolean }> {
  return post(`/api/nodes/${nodeId}/cloud-boot-check`)
}

export function getCloudPathHealth(nodeId: number): Promise<{ node_id: number; paths: Record<string, string> }> {
  return get(`/api/nodes/${nodeId}/cloud-path-health`)
}

/** ProvisionTemplate is a machine-type template: OS-2 renders the HCL provisioning, OS-4 fills in metadata when it joins the pool. */
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

/** ProvisionJob is the record of one provisioning run (OS-2). */
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

// ---- Domain fronting (BR-2) ----

/** DNSProvider is the DNS vendor credential view: secrets are never echoed back, only has_api_key. */
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

/** DNSFront is a domain-fronting record: normally it points at primary_ip, switches to a rotating backup IP on a block verdict, and switches back on recovery. */
export interface DNSFront {
  id: number
  name: string
  domain: string
  provider_id: number
  primary_ip: string
  backup_ips: string // JSON string array
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

/** Region-by-region reconciliation/compensation entry point (E-27): aligns the {region-slug}.{front-domain} A records with each region's representative entry. */
export function geoSyncDns(): Promise<{ changed: number }> {
  return post<{ changed: number }>('/api/dns-fronts/geo-sync', {})
}

// ---- Certificate tasks (BR-4) ----

/** CertTask is the orchestration task for one certificate: the panel owns orchestration and expiry, while acme.sh performs the issuance. */
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

/** Recovery is one block-recovery pipeline (BR-1): at most one per node at a time. */
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

/** RecoveryAction is the record of each pipeline stage's action (BR-5): kept for replay. */
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

// ---- In-app notification center (NT-1) ----

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

/** Publish an announcement: starts_at/ends_at are optional (RFC3339, the P2-5 window; a future starts_at means scheduled publication); audience is the target group (all/expiring:N). */
export function createAnnouncement(
  title: string,
  body: string,
  opts?: { starts_at?: string; ends_at?: string; audience?: string },
): Promise<{ created: number; scheduled: boolean }> {
  return post('/api/notifications/announcement', { title, body, ...opts })
}

/** AnnouncementStat is an announcement reach row (P2-5 (2)): the anchor row plus n sent / m read. */
export interface AnnouncementStat {
  id: number
  title: string
  body?: string
  starts_at: string | null
  ends_at: string | null
  fanout_at: string | null
  resend_at: string | null
  audience?: string
  created_at: string
  sent: number
  read: number
}

export function getAnnouncements(limit?: number): Promise<AnnouncementStat[]> {
  return get<AnnouncementStat[]>(`/api/notifications/announcements${limit ? `?limit=${limit}` : ''}`)
}

/** External-channel resend (P2-5 (3)): sends one more round of notice events outward, rate-limited to 6h per announcement (429). */
export function resendAnnouncement(id: number): Promise<{ emitted: number }> {
  return post(`/api/notifications/announcements/${id}/resend`)
}

export function deleteNotification(id: number): Promise<{ ok: boolean }> {
  return del(`/api/notifications/${id}`)
}

// ---- Managed process status on nodes (SAVE-3: heartbeat snapshots persisted, including cache hit statistics) ----

/** NodeProcRow is the most recent status snapshot of a node's managed processes (reported by the agent heartbeat). */
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

// ---- Node operational state aggregation (F-0750: agent / relay / probe tri-state in one call) ----

/** NodeStatusAggregate is the agent/relay/probe tri-state of one node (GET /api/nodes/:id/status). */
export interface NodeStatusAggregate {
  agent: {
    status: string
    last_seen?: string | null
    enabled: boolean
    maintenance: boolean
  }
  relay: {
    state: string
    pid?: number
    restarts: number
    updated_at: string
    role: string
    pool_state: string
  } | null
  probe: {
    target_kind: string
    direction: string
    rtt_ms: number
    loss_pct: number
    reachable: boolean
    verdict: string
    probed_at: string
  } | null
}

export function getNodeStatus(id: number): Promise<NodeStatusAggregate> {
  return get<NodeStatusAggregate>(`/api/nodes/${id}/status`)
}

// ---- Traffic savings report (SAVE-7: save_stats aggregated daily, sharing the cost dashboard page) ----

/** SaveStatsRow is one node's savings summary for a day (direct/blocked bytes + converted cost). */
export interface SaveStatsRow {
  node_id: number
  name: string
  day: string
  direct_bytes: number
  blocked_bytes: number
  cache_hit_bytes: number
  cost_cents: number
}

/** SaveStatsReport is the savings report: per-day rows plus a network-wide total (within the days window). */
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

// ---- Entry domains and outage state (TOUCH-3/TOUCH-7: data source and switch for disaster tolerance) ----

/** EntryDomain is an entry domain (the shared data source of disaster tolerance and routine domain emails). */
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

/** OutageState is the outage flag (manually toggled by an admin; the subscription comment then gains a warning line). */
export interface OutageState {
  enabled: boolean
}

export function getOutage(): Promise<OutageState> {
  return get('/api/outage')
}

export function putOutage(enabled: boolean): Promise<OutageState> {
  return put('/api/outage', { enabled })
}

// ---- Admin login and two-factor verification (security design §1) ----

export function adminLogin(body: { username: string; password: string; totp?: string }): Promise<{ token: string }> {
  return post<{ token: string }>('/admin/login', body)
}

// External admin logins (G7): both surfaces are off unless the server enables them; the login page
// probes availability so it never shows dead buttons.
export interface AdminLoginAvailability {
  password: boolean
  oidc: boolean
  ldap: boolean
}

export function adminLoginAvailable(): Promise<AdminLoginAvailability> {
  return get('/admin/login/available')
}

export function adminLdapLogin(body: { username: string; password: string }): Promise<{ token: string }> {
  return post<{ token: string }>('/admin/login/ldap', body)
}

export function getTwoFAStatus(): Promise<{ enabled: boolean }> {
  return get('/admin/2fa')
}

export function setupTwoFA(): Promise<{ secret: string; otpauth_url: string }> {
  return post('/admin/2fa/setup')
}

/** Binding confirmation: it takes effect after one TOTP is verified; the plaintext recovery codes are returned exactly once. */
export function enableTwoFA(code: string): Promise<{ recovery_codes: string[] }> {
  return post('/admin/2fa/enable', { code })
}

export function disableTwoFA(password: string): Promise<{ ok: boolean }> {
  return post('/admin/2fa/disable', { password })
}

/** LoginLogRow is one login audit record (login_logs, security design §1). */
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

// ---- Action audit (AU, promoted in P2-6) ----
// The admin write-action trail (audit_logs): who / action / target / request-body snapshot (sanitized) / success or failure.

/** AuditLogRow is one write-action audit record (audit_logs, AU-1). */
export interface AuditLogRow {
  id: number
  actor: string
  actor_kind: string
  action: string
  target: string
  body: string
  success: boolean
  ip: string
  created_at: string
}

export interface AuditQuery {
  actor?: string
  method?: string
  from?: string
  to?: string
  limit?: number
  offset?: number
}

// Multi-level admins / sub-admin quotas (P2-2): the roster of sub-admins (operator) managed by super.
export interface AdminRow {
  id: number
  username: string
  enabled: boolean
  role: 'super' | 'operator'
  max_users: number
  totp_enabled: boolean
  created_by: string
}

export function getAdmins(): Promise<AdminRow[]> {
  return get('/api/admins')
}

export function createAdmin(username: string, password: string, maxUsers: number): Promise<AdminRow> {
  return post<AdminRow>('/api/admins', { username, password, max_users: maxUsers })
}

export function updateAdmin(
  id: number,
  body: { password?: string; enabled?: boolean; max_users?: number },
): Promise<AdminRow> {
  return put<AdminRow>(`/api/admins/${id}`, body)
}

export function demoteAdmin(id: number): Promise<{ demoted: boolean }> {
  return del(`/api/admins/${id}`)
}

export function getAuditLogs(q: AuditQuery): Promise<{ audit_logs: AuditLogRow[] }> {
  const p = new URLSearchParams()
  if (q.actor) p.set('actor', q.actor)
  if (q.method) p.set('method', q.method)
  if (q.from) p.set('from', q.from)
  if (q.to) p.set('to', q.to)
  p.set('limit', String(q.limit ?? 50))
  if (q.offset) p.set('offset', String(q.offset))
  return get(`/api/audit-logs?${p}`)
}

// ---- Coupons (PROMO-1/2, operations design §5) ----
// One discount source per order, no stacking (DS §3.3); redemption happens at portal checkout, and the snapshot lands in the order's three ledgers for reconciliation.

export interface CouponRow {
  id: number
  code: string
  kind: 'cut' | 'pct'
  value: number // cut = amount off (cents); pct = percentage off (basis points, 10000 = no discount)
  scope: string // all | batch:<id>
  min_amount: number // usage threshold (cents), 0 = none
  starts_at: string | null
  ends_at: string | null
  total: number // total issued, 0 = unlimited
  per_user: number // per-user usage limit
  used: number
  created_at: string
}

export interface CouponInput {
  code?: string // auto-generated when left empty (FERRY-XXXXXXXX)
  kind: 'cut' | 'pct'
  value: number
  scope?: string
  min_amount?: number
  starts_at?: string | null
  ends_at?: string | null
  total?: number
  per_user?: number
}

export function getCoupons(): Promise<CouponRow[]> {
  return get('/api/coupons')
}

export function createCoupon(input: CouponInput): Promise<CouponRow> {
  return post('/api/coupons', input)
}

export function updateCoupon(id: number, input: CouponInput): Promise<CouponRow> {
  return put(`/api/coupons/${id}`, input)
}

export function deleteCoupon(id: number): Promise<{ ok: boolean }> {
  return del(`/api/coupons/${id}`)
}

// ---- Cost reference library (E-31, plan and cost design §4) ----
// Hand-entered costs are the sole authority; reference prices only produce deviation hints and never change prices.

/** RefQuery is the lookup key for one list-price probe (vendor/region/plan). */
export interface RefQuery {
  provider: string
  region: string
  spec: string
}

/** RefQuote is one reference list price (snapshot semantics). */
export interface RefQuote {
  monthly_cents: number
  traffic_price: number
  currency: string
  url?: string
  at: string
}

/** RefDeviation is the verdict on hand-entered price vs list price (positive pct = hand-entered is pricier). */
export interface RefDeviation {
  pct: number
  off: boolean
}

/** RefProbe is a single-point probe result; hit=false on a miss, and reason explains incomparable cases. */
export interface RefProbe {
  source: string
  query: RefQuery
  hit: boolean
  quote?: RefQuote
  deviation?: RefDeviation
  reason?: string
}

/** RefCheckReport is the batch reconciliation report (only lists rows that were hit and are comparable). */
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

// ---- Price watch (E-32, plan and cost design §4.2) ----
// A watch condition tracks one list-price key; when a scan hits a price drop or the target price, Herald sends a price_alert.

/** PriceWatch is one watch condition (including scan dynamics: latest/previous snapshot and the price change). */
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

/** PriceSnapshot is one list-price snapshot of a watch key. */
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

// Distributor agents (DS-1 ledger layer / DS-2 admin surface).
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

// payout settles money out (the amount must not exceed the unsettled balance); adjust is a manual adjustment (positive or negative amount, 0 refused).
export function distributorPayout(id: number, input: { amount_cents: number; note?: string }): Promise<{ balance_cents: number }> {
  return post(`/api/distributors/${id}/payout`, input)
}

export function distributorAdjust(id: number, input: { amount_cents: number; note?: string }): Promise<{ balance_cents: number }> {
  return post(`/api/distributors/${id}/adjust`, input)
}

// Distributor self-service surface (DS-3): read-only views; settlement actions live on the admin surface (DS-2).
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

// ---- Cost dashboard aggregation (§4.7): actual bills, spend rankings, trends, path estimates ----

export interface SpendRow {
  key: string
  target_id?: number
  name?: string
  currency: string
  actual_cents: number
  allocated_cents: number
  estimate_cents: number
  report_cents: number
  count: number
}

export interface SpendTrendBucket {
  bucket: string
  currency: string
  actual_cents: number
  count: number
}

export interface CostRecordRow {
  id: number
  cost_provider_id: number
  source: string
  bill_id: string
  revision: string
  account: string
  region: string
  currency: string
  amount_cents: number
  pre_tax_cents: number
  tax_cents: number
  incurred_at: string
  period_start: string
  period_end: string
  imported_at: string
}

export interface BillItemRow {
  id: number
  cost_record_id: number
  resource_id: string
  product: string
  quantity: string
  currency: string
  cost_cents: number
  usage_start: string | null
  usage_end: string | null
}

export interface CostEstimateRow {
  id: number
  path_id: number
  period_start: string
  period_end: string
  estimated_cents: number
  currency: string
  created_at: string
}

export interface PathCostLeg {
  role: string
  node_id: number
  name: string
  billing_type: string
  unit: string
  cents_per_gb: number
  monthly_cents: number
  known: boolean
  fresh: boolean
  as_at: string
  source: string
  unknown: boolean
}

export interface PathCostRow {
  path_id: number
  name: string
  direction: string
  currency: string
  total_cents: number
  precise: boolean
  marginal_per_gb_cents: number
  explain: PathCostLeg[]
}

/** spendByScope returns per-currency actual/allocated/estimate sums for a scope. */
export function spendByScope(scope: string, from?: string, to?: string): Promise<{ scope: string; rows: SpendRow[] }> {
  const params = new URLSearchParams({ scope })
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  return get(`/api/cost/spend?${params}`)
}

/** spendTrend returns actual spend buckets by day/month/billing cycle. */
export function spendTrend(bucket: string, from?: string, to?: string): Promise<{ bucket: string; rows: SpendTrendBucket[] }> {
  const params = new URLSearchParams({ bucket })
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  return get(`/api/cost/spend/trend?${params}`)
}

/** getBillRecords lists imported vendor bills, filterable by date/source/account/region. */
export function getBillRecords(filters: Record<string, string> = {}): Promise<{ records: CostRecordRow[] }> {
  const qs = new URLSearchParams(filters).toString()
  return get(`/api/cost/records${qs ? `?${qs}` : ''}`)
}

/** getBillItems lists the line items of one vendor bill. */
export function getBillItems(id: number): Promise<{ items: BillItemRow[] }> {
  return get(`/api/cost/records/${id}/items`)
}

/** getCostEstimates lists stored estimates. */
export function getCostEstimates(): Promise<{ estimates: CostEstimateRow[] }> {
  return get('/api/cost/estimates')
}

/** getPathCosts lists explainable per-path cost estimates; unknown/stale prices drop precise. */
export function getPathCosts(from: string): Promise<{ rows: PathCostRow[] }> {
  return get(`/api/cost/path-estimates?from=${encodeURIComponent(from)}`)
}

// ---- Bill sync admin (§4.7, F-0632: sources, sync runs, manual triggers) ----

export interface CostSourceRow {
  id: number
  name: string
  provider_id?: number
  source: string
  account: string
  region: string
  currency: string
  enabled: boolean
  note: string
  last_sync_at: string | null
  last_error: string
}

export interface BillImportRow {
  id: number
  cost_provider_id: number
  source: string
  trigger: string
  period_start: string
  period_end: string
  account: string
  region: string
  state: string
  fetched: number
  added: number
  updated: number
  skipped: number
  error?: string
  retry_count: number
  started_at: string
  finished_at?: string | null
}

export interface CostSourceInput {
  name: string
  provider_id?: number | null
  source: string
  account: string
  region: string
  currency: string
  note: string
  enabled?: boolean
}

export interface ImportAPIInput {
  from: string
  to: string
  account?: string
  region?: string
  incremental?: boolean
}

export function getCostSources(): Promise<{ sources: CostSourceRow[] }> {
  return get('/api/cost/sources')
}

export function createCostSource(input: CostSourceInput): Promise<CostSourceRow> {
  return post('/api/cost/sources', input)
}

export function updateCostSource(id: number, patch: Partial<CostSourceInput>): Promise<CostSourceRow> {
  return put(`/api/cost/sources/${id}`, patch)
}

export function getBillImports(costProviderId?: number): Promise<{ imports: BillImportRow[] }> {
  return get(`/api/cost/imports${costProviderId ? `?cost_provider_id=${costProviderId}` : ''}`)
}

export function importBillsAPI(id: number, input: ImportAPIInput): Promise<{ ok: boolean; error?: string; run?: BillImportRow }> {
  return post(`/api/cost/sources/${id}/import-api`, input)
}

/** importBillsFile uploads a vendor bill JSON file (multipart `file` field). */
export function importBillsFile(id: number, file: File): Promise<{ ok: boolean; error?: string; run?: BillImportRow }> {
  const form = new FormData()
  form.append('file', file)
  return request(`/api/cost/sources/${id}/import-file`, { method: 'POST', body: form })
}

export interface CostReconciliationRow {
  id: number
  scope_kind: string
  target_id: number
  target_key: string
  period_start: string
  period_end: string
  currency: string
  estimate_cents: number
  actual_cents: number
  diff_cents: number
  status: string
  note: string
  approved_by: string
  created_at: string
}

export interface CostAllocationRow {
  id: number
  cost_item_id: number
  rule_id: number
  rule_version: number
  target_kind: string
  target_id: number
  target_key: string
  basis: string
  ratio_ppm: number
  original_cents: number
  allocated_cents: number
  currency: string
}

export interface CostAllocationRuleRow {
  id: number
  name: string
  kind: string
  version: number
  enabled: boolean
  params?: string
  note: string
  created_at: string
}

export function getReconciliations(): Promise<{ reconciliations: CostReconciliationRow[] }> {
  return get('/api/cost/reconciliations')
}

/** createReconciliation compares stored actual vs estimate for one scope/period. */
export function createReconciliation(input: {
  scope_kind: string
  target_id?: number
  target_key?: string
  from: string
  to: string
  currency: string
}): Promise<CostReconciliationRow> {
  return post('/api/cost/reconciliations', input)
}

export function confirmReconciliation(id: number, status: string, note: string): Promise<CostReconciliationRow> {
  return post(`/api/cost/reconciliations/${id}/confirm`, { status, note })
}

export function getAllocationRules(): Promise<{ rules: CostAllocationRuleRow[] }> {
  return get('/api/cost/allocation-rules')
}

export function createAllocationRule(input: { name: string; kind: string; params: string; note: string }): Promise<CostAllocationRuleRow> {
  return post('/api/cost/allocation-rules', input)
}

export function editAllocationRule(id: number, patch: { name?: string; kind?: string; params?: string; note?: string; enabled?: boolean }): Promise<CostAllocationRuleRow> {
  return post(`/api/cost/allocation-rules/${id}/edit`, patch)
}

export function getAllocations(costItemId?: number): Promise<{ allocations: CostAllocationRow[] }> {
  return get(`/api/cost/allocations${costItemId ? `?cost_item_id=${costItemId}` : ''}`)
}

export function recomputeAllocations(recordId: number, ruleId = 0, version = 0): Promise<{ count: number }> {
  return post(`/api/cost/records/${recordId}/recompute`, { rule_id: ruleId, version })
}
