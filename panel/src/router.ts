import { createRouter, createWebHistory } from 'vue-router'
import { auth } from './auth'

// 三个一级页：登录 / 概览（用量与订阅链接）/ 兑换（PAY-7）。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
    { path: '/', component: () => import('./views/HomeView.vue') },
    { path: '/redeem', component: () => import('./views/RedeemView.vue') },
    { path: '/orders', component: () => import('./views/OrdersView.vue') },
    { path: '/notifications', component: () => import('./views/NotificationsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// 无令牌先去登录；已登录不回登录页。
router.beforeEach((to) => {
  if (to.path !== '/login' && !auth.token) return '/login'
  if (to.path === '/login' && auth.token) return '/'
  return true
})
