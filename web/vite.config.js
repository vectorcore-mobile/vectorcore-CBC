import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: '/ui/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 800,
    rollupOptions: {
      output: {
        manualChunks: {
          'vendor-react': ['react', 'react-dom', 'react-router-dom'],
          'vendor-icons': ['lucide-react'],
        },
      },
    },
  },
  server: {
    proxy: {
      '/v1': {
        target: 'http://localhost:8087',
        changeOrigin: true,
      },
      '/metrics': {
        target: 'http://localhost:8087',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://localhost:8087',
        changeOrigin: true,
      },
      '/readyz': {
        target: 'http://localhost:8087',
        changeOrigin: true,
      },
    },
  },
})
