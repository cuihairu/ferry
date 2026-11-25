import { createApp } from 'vue'
import './styles/theme.css'
import App from './App.vue'
import { router } from './router'

// Dark-first: the same Linear-style token set as dash.
document.documentElement.classList.add('dark')

createApp(App).use(router).mount('#app')
