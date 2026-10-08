import { createRouter, createWebHistory } from 'vue-router'
import { auth } from './auth'

// P0-13：用户/节点/设置三个一级页面，后续按运营批扩充；安全批（§1）起全部
// 页面走管理员登录守卫。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
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

// 无令牌先去登录（带 redirect 回跳）；已登录不回登录页。
router.beforeEach((to) => {
  if (to.path !== '/login' && !auth.token) return { path: '/login', query: { redirect: to.fullPath } }
  if (to.path === '/login' && auth.token) return '/'
  return true
})
