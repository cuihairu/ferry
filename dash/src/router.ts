import { createRouter, createWebHistory } from 'vue-router'
import { auth, role } from './auth'

// P0-13: the three top-level pages — users/nodes/settings — expanded later in operational batches;
// since the security batch (§1) every page sits behind the admin login guard. DS-3: the distributor
// role has its own login (/dist-login) and workbench (/dist), and the guard trims by role —
// distributors only reach the distributor surface, admins never do.
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
    { path: '/promos', component: () => import('./views/PromosView.vue') },
    { path: '/admins', component: () => import('./views/AdminsView.vue') },
    { path: '/cost', component: () => import('./views/CostView.vue') },
    { path: '/bills', component: () => import('./views/BillsView.vue') },
    { path: '/provision', component: () => import('./views/ProvisionView.vue') },
    { path: '/dns', component: () => import('./views/DnsView.vue') },
    { path: '/recoveries', component: () => import('./views/RecoveryView.vue') },
    { path: '/notifications', component: () => import('./views/NotificationsView.vue') },
    { path: '/landings', component: () => import('./views/LandingsView.vue') },
    // Path plane (F-1427~F-1429): path list/detail and path group management.
    { path: '/paths', component: () => import('./views/PathsView.vue') },
    { path: '/settings', component: () => import('./views/SettingsView.vue') },
    { path: '/audit', component: () => import('./views/AuditView.vue') },
    // Cloud resource management (F-0411~F-0432): accounts, resources and operations.
    { path: '/cloud', component: () => import('./views/CloudView.vue') },
  ],
})

const DIST_PATHS = ['/dist', '/dist-login']
const LOGIN_PATHS = ['/login', '/dist-login']

// Without a token, redirect to the matching login page (with a redirect back); distributors only
// reach the distributor surface and admins never do (the two tokens have different claim shapes,
// so crossing would only 401).
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
