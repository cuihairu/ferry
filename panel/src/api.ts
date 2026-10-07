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

export function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: JSON.stringify(body) })
}

// ---- 兑换结果（对齐 server 的 JSON 载荷）----

export interface RedeemResult {
  order_no: string
  grant_type: 'add_quota' | 'extend_days'
  grant_value: number
  quota_bytes: number
  expires_at: string | null
}
