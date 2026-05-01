// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import adapter from "@sveltejs/adapter-static";
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

// adapter-static prerenders every route to plain HTML at build time.
// Output lands in `dist/`, picked up directly by //go:embed all:frontend/dist
// in the module-root spa.go. paths.relative rewrites asset URLs so the
// bundle works under any path.
export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({
      pages: "dist",
      assets: "dist",
      fallback: "index.html",
      precompress: false,
      strict: true,
    }),
    paths: { relative: true },
  },
};
