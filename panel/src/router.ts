import { createRouter, createWebHistory } from 'vue-router'
import { auth } from './auth'

// Three top-level pages: login / overview (usage and subscription links) / redeem (PAY-7).
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
    { path: '/', component: () => import('./views/HomeView.vue') },
    { path: '/redeem', component: () => import('./views/RedeemView.vue') },
    { path: '/shop', component: () => import('./views/ShopView.vue') },
    { path: '/orders', component: () => import('./views/OrdersView.vue') },
    { path: '/subscriptions', component: () => import('./views/SubscriptionsView.vue') },
    { path: '/notifications', component: () => import('./views/NotificationsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// Without a token, go to login first; an already-logged-in user never returns to the login page.
router.beforeEach((to) => {
  if (to.path !== '/login' && !auth.token) return '/login'
  if (to.path === '/login' && auth.token) return '/'
  return true
})
