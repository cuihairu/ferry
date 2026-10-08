import { createRouter, createWebHistory } from 'vue-router'
import { auth, role } from './auth'

// P0-13：用户/节点/设置三个一级页面，后续按运营批扩充；安全批（§1）起全部
// 页面走管理员登录守卫。DS-3：代理角色独立登录（/dist-login）与工作台
//（/dist），守卫按角色裁剪——代理只进代理面，管理员不进代理面。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
    { path: '/dist-login', component: () => import('./views/DistLoginView.vue') },
    { path: '/dist', component: () => import('./views/DistView.vue') },
    { path: '/', redirect: '/nodes' },
    { path: '/nodes', component: () => import('./views/NodesView.vue') },
    { path: '/users', component: () => import('./views/UsersView.vue') },
    { path: '/cards', component: () => import('./views/CardsView.vue') },
    { path: '/payments', component: () => import('./views/ReconcileView.vue') },
    { path: '/cost', component: () => import('./views/CostView.vue') },
    { path: '/provision', component: () => import('./views/ProvisionView.vue') },
    { path: '/dns', component: () => import('./views/DnsView.vue') },
    { path: '/recoveries', component: () => import('./views/RecoveryView.vue') },
    { path: '/notifications', component: () => import('./views/NotificationsView.vue') },
    { path: '/landings', component: () => import('./views/LandingsView.vue') },
    { path: '/settings', component: () => import('./views/SettingsView.vue') },
  ],
})

const DIST_PATHS = ['/dist', '/dist-login']
const LOGIN_PATHS = ['/login', '/dist-login']

// 无令牌先去对应登录页（带 redirect 回跳）；代理只进代理面、管理员不进
// 代理面（两种令牌 claim 形状不同，互访只会 401）。
router.beforeEach((to) => {
  const r = role.value
  if (!LOGIN_PATHS.includes(to.path) && !r) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  if (r === 'distributor' && !DIST_PATHS.includes(to.path)) return '/dist'
  if (r === 'admin' && to.path === '/dist') return '/'
  if (to.path === '/login' && auth.token) return '/'
  if (to.path === '/dist-login' && auth.distToken) return '/dist'
  return true
})
