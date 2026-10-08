import { reactive } from 'vue'

// 管理员会话（安全设计 §1）：登录签发的 JWT 存 localStorage，路由守卫与
// api 的 Authorization 注入共用。令牌只对 /admin/* 面生效（2FA 绑定、登录
// 审计、备份下载）；/api 数据面现状走网络边界隔离口径，不受此影响。

const STORAGE_KEY = 'ferry.admin.token'

export const auth = reactive({
  token: localStorage.getItem(STORAGE_KEY) ?? '',
})

export function setToken(token: string): void {
  auth.token = token
  localStorage.setItem(STORAGE_KEY, token)
}

export function logout(): void {
  auth.token = ''
  localStorage.removeItem(STORAGE_KEY)
}
