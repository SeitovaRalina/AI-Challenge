import path from 'node:path'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        // VITE_BACKEND_PORT lets a second `npm run dev -- --port <N>` talk to
        // a second backend instance (see README's "Multiple profiles") —
        // unset, this is identical to the previous hardcoded :8080.
        target: `http://localhost:${process.env.VITE_BACKEND_PORT || 8080}`,
        changeOrigin: true,
      },
    },
  },
})
