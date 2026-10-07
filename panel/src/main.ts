import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles/theme.css'
import App from './App.vue'

// 暗色为主：直接挂 dark 类，与管理端共用同一套 token 口径。
document.documentElement.classList.add('dark')

createApp(App).use(ElementPlus).mount('#app')
