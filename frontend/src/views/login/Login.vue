<script setup lang="ts">
// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Sign in: local email + password and/or OIDC, driven by what
// GET /api/auth/info says is configured.
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { auth } from '@/api'
import AuthPage from '@/components/AuthPage.vue'
import FormField from '@/components/FormField.vue'
import Notice from '@/components/Notice.vue'

interface AuthInfo {
  local_enabled?: boolean
  oidc_enabled?: boolean
  oidc_label?: string
  auth_disabled?: boolean
}

const route = useRoute()
const router = useRouter()

const email = ref('')
const password = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const info = ref<AuthInfo | null>(null)

// Where to send the user after a successful login. Falls back to
// the dashboard root when no `next=` param was carried in.
function nextTarget(): string {
  const n = route.query.next
  if (typeof n !== 'string' || !n.startsWith('/')) return '/'
  if (n === '/login') return '/'
  return n
}

// OIDC callback redirects back here with ?err=<code> on failure.
function ssoErrorMessage(code: string): string | null {
  switch (code) {
    case 'sso_idp_error':
      return 'The identity provider rejected the sign-in.'
    case 'sso_state_missing':
      return 'Sign-in session expired. Please try again.'
    case 'sso_token_invalid':
      return 'Could not verify the identity-provider response.'
    case 'sso_bad_callback':
      return 'Malformed callback from the identity provider.'
    case 'sso_access_denied':
      return 'Access denied for this account.'
    default:
      return null
  }
}

async function submit() {
  error.value = null
  loading.value = true
  try {
    await auth.login(email.value.trim(), password.value)
    router.push(nextTarget())
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    loading.value = false
  }
}

function startOIDC() {
  // Full-page navigation: SSO flow needs the browser to follow the
  // 302 to the IdP and back.
  window.location.href = '/api/auth/oidc/start'
}

onMounted(() => {
  const errCode = route.query.err
  if (typeof errCode === 'string') error.value = ssoErrorMessage(errCode)

  // Already signed in (or auth off entirely): nothing to ask.
  auth
    .me()
    .then((r) => {
      const me = r as { auth_disabled?: boolean; user?: unknown } | null
      if (me?.auth_disabled) router.push('/')
      else if (me?.user) router.push(nextTarget())
    })
    .catch(() => {})

  auth
    .info()
    .then((r) => {
      info.value = r as AuthInfo
    })
    .catch(() => {})
})
</script>

<template>
  <AuthPage title="Sign in">
    <Notice v-if="error" kind="danger" class="login-error">{{ error }}</Notice>

    <button
      v-if="info?.oidc_enabled"
      class="btn btn-primary btn-block"
      type="button"
      @click="startOIDC"
    >
      Sign in with {{ info?.oidc_label || 'SSO' }}
    </button>

    <template v-if="info?.local_enabled">
      <div v-if="info?.oidc_enabled" class="auth-or"><span>or</span></div>
      <form class="auth-form" @submit.prevent="submit">
        <FormField label="Email">
          <input
            v-model="email"
            class="form-input"
            type="email"
            placeholder="ops@example.com"
            autocomplete="username"
            required
          />
        </FormField>
        <FormField label="Password">
          <input
            v-model="password"
            class="form-input"
            type="password"
            autocomplete="current-password"
            required
          />
        </FormField>
        <button class="btn btn-primary btn-block" type="submit" :disabled="loading">
          {{ loading ? 'Signing in...' : 'Sign in' }}
        </button>
      </form>
    </template>

    <p
      v-if="info && !info.local_enabled && !info.oidc_enabled && !info.auth_disabled"
      class="auth-note"
    >
      No sign-in method is configured. Check <code>auth.local</code> or <code>auth.oidc</code> in
      your YAML.
    </p>
  </AuthPage>
</template>

<style scoped>
.login-error {
  margin-bottom: 14px;
}
</style>
