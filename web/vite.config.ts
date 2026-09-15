import { fileURLToPath, URL } from 'node:url'

import UnoCSS from '@unocss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  base: '/admin/',
  plugins: [
    vue({
      features: {
        optionsAPI: false,
      },
    }),
    UnoCSS(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/healthz': 'http://127.0.0.1:3333',
      '/readyz': 'http://127.0.0.1:3333',
      '/v1': 'http://127.0.0.1:3333',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    target: 'es2022',
  },
})
