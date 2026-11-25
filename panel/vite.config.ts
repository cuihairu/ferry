import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In development, proxy the panel and backend API to the local ferry-server to avoid CORS configuration.
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/sub': 'http://localhost:8080',
    },
  },
})
