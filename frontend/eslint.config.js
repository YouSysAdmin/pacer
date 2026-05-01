// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Flat config (ESLint 9+). Lints plain .js plus Svelte 5 components
// via eslint-plugin-svelte. Build-output and tooling-cache directories
// are excluded explicitly -- ESLint walks them by default.
import js from "@eslint/js";
import svelte from "eslint-plugin-svelte";
import globals from "globals";

export default [
  {
    ignores: [
      "dist/**",
      ".svelte-kit/**",
      "node_modules/**",
      "build/**",
      "scripts/**", // build-time helpers; lint manually if needed
    ],
  },
  js.configs.recommended,
  ...svelte.configs["flat/recommended"],
  {
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
    rules: {
      // Underscore prefix opts into "intentionally unused".
      "no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],

      // SvelteKit 2's resolve()-everywhere convention. Pacer's routes
      // use absolute string-literal hrefs (`/projects`, `/jobs/...`)
      // and external links to GitHub. Useful as a hint, not a gate.
      "svelte/no-navigation-without-resolve": "warn",

      // SvelteDate / SvelteSet give fine-grained reactivity but the
      // existing $state(new Set()) / $state(new Date()) sites work
      // for current usage. Surface as a warning so future sites get
      // a nudge without churning the existing codebase.
      "svelte/prefer-svelte-reactivity": "warn",

      // $state + $effect vs $derived is judgment; the rule's
      // suggestion isn't always cleaner.
      "svelte/prefer-writable-derived": "warn",
    },
  },
];
