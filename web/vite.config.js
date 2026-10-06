import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true
  },
  server: {
    port: 5173,
    proxy: {
      '/emby': 'http://127.0.0.1:18097',
      '/admin': 'http://127.0.0.1:18097'
    }
  }
})
