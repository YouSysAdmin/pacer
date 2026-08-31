// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Who is signed in, for the sidebar foot and nothing else.
//
// The session itself is an HttpOnly cookie; there is no token in JS.
// Route protection stays with api/client.ts (401 -> /login?next=) and
// the server-side middleware - this store only answers "what should
// the sidebar say".

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { auth } from '@/api'

export interface AuthUser {
  email: string
}

interface MeResponse {
  auth_disabled?: boolean
  user?: AuthUser
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<AuthUser | null>(null)
  const authDisabled = ref(false)

  // Resolve the current session. Three outcomes:
  //   user populated     - show "logged in as ..."
  //   auth_disabled true - hide auth UI entirely
  //   401 / error        - the page-level api calls will redirect
  async function loadMe() {
    try {
      const r = (await auth.me()) as MeResponse | null
      if (!r) return
      if (r.auth_disabled) authDisabled.value = true
      else if (r.user) user.value = r.user
    } catch {
      // The client wrapper already handles the 401 redirect.
    }
  }

  async function logout() {
    try {
      await auth.logout()
    } catch {
      // Cookie clearing is best-effort, the caller's redirect still runs.
    }
    user.value = null
  }

  return { user, authDisabled, loadMe, logout }
})
