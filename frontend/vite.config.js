import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 3000,
    proxy: {
      '/ws': { target: 'ws://gateway:8080', ws: true, changeOrigin: true },
      '/events': { target: 'http://gateway:8080', changeOrigin: true },
      '/chaos': { target: 'http://gateway:8080', changeOrigin: true },
      '/leader': { target: 'http://gateway:8080', changeOrigin: true },
    }
  }
})
