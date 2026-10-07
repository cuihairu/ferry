import { createRouter, createWebHistory } from 'vue-router'

// P0-13：用户/节点/设置三个一级页面，后续按运营批扩充。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/nodes' },
    { path: '/nodes', component: () => import('./views/NodesView.vue') },
    { path: '/users', component: () => import('./views/UsersView.vue') },
    { path: '/cards', component: () => import('./views/CardsView.vue') },
    { path: '/settings', component: () => import('./views/SettingsView.vue') },
  ],
})
