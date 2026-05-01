// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

// During `vite dev`, /api/* is proxied to the orchestrator so the
// browser can talk to it without CORS. Override with
// PACER_SERVER=https://host:port bun run dev when needed.
const target = process.env.PACER_SERVER || "http://localhost:3000";

export default defineConfig({
  plugins: [sveltekit()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target,
        changeOrigin: true,
        secure: false,
      },
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.{test,spec}.{js,ts}"],
  },
});
