import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发期把用户面板请求代理到本机 ferry-server，避免跨域配置。
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/sub': 'http://localhost:8080',
    },
  },
})
