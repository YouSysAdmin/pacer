// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
// Variable weight only -- the italic axis would double the font payload
// for a face nothing italicizes.
import '@fontsource-variable/geist/wght.css'
import '@fontsource-variable/geist-mono/wght.css'
import './assets/styles.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
