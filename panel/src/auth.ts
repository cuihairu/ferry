import { reactive } from 'vue'
import type { Me } from './api'

// P0 身份口径：订阅令牌即凭据，存 localStorage 随身携带（无密码无服务端会话，
// 见 server/internal/handler/panel.go；P1-1 登录落地后此处换会话载体）。

const STORAGE_KEY = 'ferry.panel.token'

export const auth = reactive({
  token: localStorage.getItem(STORAGE_KEY) ?? '',
  user: null as Me | null,
  unread: 0, // 站内信未读数（NT-1）：App 壳层拉取，通知页操作后同步
})

export function setToken(token: string): void {
  auth.token = token
  localStorage.setItem(STORAGE_KEY, token)
}

export function setUser(user: Me | null): void {
  auth.user = user
}

export function logout(): void {
  auth.token = ''
  auth.user = null
  auth.unread = 0
  localStorage.removeItem(STORAGE_KEY)
}

/** tokenFromInput 从裸令牌或完整订阅链接里解析令牌（用户最常见的两种复制形态）。 */
export function tokenFromInput(input: string): string {
  const raw = input.trim()
  if (!raw) return ''
  try {
    const u = new URL(raw)
    const segs = u.pathname.split('/').filter(Boolean)
    return segs.length ? segs[segs.length - 1] : raw
  } catch {
    return raw
  }
}
