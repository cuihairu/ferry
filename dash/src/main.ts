import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles/theme.css'
import App from './App.vue'
import { router } from './router'

// Dark-first (P0-14): apply the dark class directly and use our own Linear-style variables.
document.documentElement.classList.add('dark')

createApp(App).use(createPinia()).use(router).use(ElementPlus).mount('#app')
