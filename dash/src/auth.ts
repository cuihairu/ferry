import { computed, reactive } from 'vue'

// 管理员会话（安全设计 §1）：登录签发的 JWT 存 localStorage，路由守卫与
// api 的 Authorization 注入共用。令牌只对 /admin/* 面生效（2FA 绑定、登录
// 审计、备份下载）；/api 数据面现状走网络边界隔离口径，不受此影响。
// DS-3：代理会话独立存储键 ferry.dist.token（role=distributor 令牌），
// 两种角色互不越界——守卫按角色裁剪可见面（设计 §3.2）。

const STORAGE_KEY = 'ferry.admin.token'
const DIST_STORAGE_KEY = 'ferry.dist.token'

export const auth = reactive({
  token: localStorage.getItem(STORAGE_KEY) ?? '',
  distToken: localStorage.getItem(DIST_STORAGE_KEY) ?? '',
})

// role：当前会话角色——管理员令牌优先（两种令牌并存时按管理员面进），
// 只有代理令牌时为 distributor，均无为空（未登录）。
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
