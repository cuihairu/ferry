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

export function del<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' })
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
  region: string
  isp: string
  last_seen: string | null
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
