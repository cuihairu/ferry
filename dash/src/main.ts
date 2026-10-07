import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles/theme.css'
import App from './App.vue'
import { router } from './router'

// 暗色为主（P0-14）：直接挂 dark 类，走自定 Linear 风格变量。
document.documentElement.classList.add('dark')

createApp(App).use(createPinia()).use(router).use(ElementPlus).mount('#app')
