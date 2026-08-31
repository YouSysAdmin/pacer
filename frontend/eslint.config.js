// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import pluginVue from 'eslint-plugin-vue'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'

export default defineConfigWithVueTs(
  { ignores: ['dist/**', 'node_modules/**'] },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
  {
    rules: {
      // Prettier owns formatting; keep eslint on correctness only.
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/multiline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/html-indent': 'off',
      'vue/html-closing-bracket-newline': 'off',
      'vue/first-attribute-linebreak': 'off',
      // The component vocabulary (Notice, Pagination) and route
      // components (Jobs, Pools) are single words on purpose; nothing
      // here shadows a native element.
      'vue/multi-word-component-names': 'off',
      // Every v-html renders a constant from layouts/icons.ts or
      // components/statIcons.ts -- our own inline SVG, never remote or
      // user-supplied content. Keep it that way.
      'vue/no-v-html': 'off',
    },
  },
)
