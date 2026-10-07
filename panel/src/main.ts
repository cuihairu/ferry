import { createApp } from 'vue'
import './styles/theme.css'
import App from './App.vue'
import { router } from './router'

// 暗色为主：与 dash 同一套 Linear 风格 token。
document.documentElement.classList.add('dark')

createApp(App).use(router).mount('#app')
