// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'node:path'

export default defineConfig({
  plugins: [vue()],
  // Served from the domain root by the Go binary (routes.go mounts the
  // embedded dist at "/"), so the default base applies.
  base: '/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // Dev flow: `make dev` runs the API on :3000, this dev server
      // proxies API calls to it. Override with PACER_SERVER=... when
      // the backend runs elsewhere.
      '/api': {
        target: process.env.PACER_SERVER || 'http://localhost:3000',
        changeOrigin: true,
        secure: false,
      },
      '/healthz': {
        target: process.env.PACER_SERVER || 'http://localhost:3000',
        changeOrigin: true,
        secure: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.{test,spec}.{js,ts}'],
  },
})
