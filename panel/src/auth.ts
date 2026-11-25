import { reactive } from 'vue'
import type { Me } from './api'

// P0 identity model: the subscription token is the credential, kept in localStorage and carried along
// (no password, no server-side session, see server/internal/handler/panel.go; P1-1 will swap in a
// session carrier once login lands).

const STORAGE_KEY = 'ferry.panel.token'

export const auth = reactive({
  token: localStorage.getItem(STORAGE_KEY) ?? '',
  user: null as Me | null,
  unread: 0, // in-app notification unread count (NT-1): fetched by the app shell, synced after actions on the notifications page
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

/** tokenFromInput extracts the token from either a bare token or a full subscription link (the two most common copy formats). */
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
