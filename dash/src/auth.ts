import { computed, reactive } from 'vue'

// Admin session (security design §1): the JWT issued at login is kept in localStorage and shared
// between the router guard and the api's Authorization injection. The token only applies to the
// /admin/* surface (2FA binding, login audit, backup download); the /api data surface currently
// relies on network-boundary isolation and is unaffected.
// DS-3: the distributor session has its own storage key ferry.dist.token (role=distributor token),
// and the two roles never cross into each other's surface — the guard trims what is visible per role
// (design §3.2).

const STORAGE_KEY = 'ferry.admin.token'
const DIST_STORAGE_KEY = 'ferry.dist.token'

export const auth = reactive({
  token: localStorage.getItem(STORAGE_KEY) ?? '',
  distToken: localStorage.getItem(DIST_STORAGE_KEY) ?? '',
})

// role: the current session role — the admin token wins (when both tokens are present the admin
// surface is entered), distributor when only the distributor token exists, and empty when neither
// is present (not logged in).
export const role = computed<'admin' | 'distributor' | ''>(() => {
  if (auth.token) return 'admin'
  if (auth.distToken) return 'distributor'
  return ''
})

export function setToken(token: string): void {
  auth.token = token
  localStorage.setItem(STORAGE_KEY, token)
}

export function logout(): void {
  auth.token = ''
  localStorage.removeItem(STORAGE_KEY)
}

export function setDistToken(token: string): void {
  auth.distToken = token
  localStorage.setItem(DIST_STORAGE_KEY, token)
}

export function logoutDist(): void {
  auth.distToken = ''
  localStorage.removeItem(DIST_STORAGE_KEY)
}
