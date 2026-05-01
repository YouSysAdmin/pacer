<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { auth } from "$lib/api.js";
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  let email = $state("");
  let password = $state("");
  let loading = $state(false);
  let error = $state(null);
  let info = $state(null);

  // Where to send the user after a successful login. Falls back to
  // the dashboard root when no `next=` param was carried in.
  function nextTarget() {
    const n = page.url.searchParams.get("next");
    if (!n || !n.startsWith("/")) return "/";
    if (n === "/login") return "/";
    return n;
  }

  // OIDC callback redirects back here with ?err=<code> on failure.
  function ssoErrorMessage(code) {
    switch (code) {
      case "sso_idp_error":      return "The identity provider rejected the sign-in.";
      case "sso_state_missing":  return "Sign-in session expired. Please try again.";
      case "sso_token_invalid":  return "Could not verify the identity-provider response.";
      case "sso_bad_callback":   return "Malformed callback from the identity provider.";
      case "sso_access_denied":  return "Access denied for this account.";
      default:                   return null;
    }
  }

  async function submit(e) {
    e.preventDefault();
    error = null;
    loading = true;
    try {
      await auth.login(email.trim(), password);
      goto(nextTarget());
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  function startOIDC() {
    // Full-page navigation: SSO flow needs the browser to follow the
    // 302 to the IdP and back.
    window.location.href = "/api/auth/oidc/start";
  }

  $effect(() => {
    const errCode = page.url.searchParams.get("err");
    if (errCode) error = ssoErrorMessage(errCode);

    auth.me()
      .then((r) => {
        if (r && r.auth_disabled) goto("/");
        else if (r && r.user) goto(nextTarget());
      })
      .catch(() => {});

    auth.info()
      .then((r) => { info = r; })
      .catch(() => {});
  });
</script>

<div class="login-page">
  <div class="login-card">
    <div class="login-brand">
      <img src="/logo/wordmark.svg" alt="Pacer" />
    </div>

    <h2>Sign in</h2>

    {#if error}<div class="banner err">{error}</div>{/if}

    {#if info && info.oidc_enabled}
      <button class="btn primary sso" type="button" onclick={startOIDC}>
        Sign in with {info.oidc_label || "SSO"}
      </button>
    {/if}

    {#if info && info.local_enabled}
      {#if info.oidc_enabled}<div class="divider"><span>or</span></div>{/if}
      <form onsubmit={submit}>
        <div class="field">
          <label for="email">Email</label>
          <input
            id="email"
            class="input"
            type="email"
            bind:value={email}
            placeholder="ops@example.com"
            autocomplete="username"
            required
          />
        </div>
        <div class="field">
          <label for="password">Password</label>
          <input
            id="password"
            class="input"
            type="password"
            bind:value={password}
            autocomplete="current-password"
            required
          />
        </div>
        <button class="btn primary" type="submit" disabled={loading}>
          {loading ? "signing in..." : "sign in"}
        </button>
      </form>
    {/if}

    {#if info && !info.local_enabled && !info.oidc_enabled && !info.auth_disabled}
      <p class="muted">No sign-in method is configured. Check <code>auth.local</code> or <code>auth.oidc</code> in your YAML.</p>
    {/if}
  </div>
</div>

<style>
  .login-page {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 40px 20px;
    background: var(--bg-0);
  }
  .login-card {
    width: 100%;
    max-width: 380px;
    background: var(--bg-2);
    border: 1px solid var(--line-2);
    border-radius: var(--r-md);
    padding: 28px 32px 32px;
  }
  .login-brand {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--fg-0);
    font-size: 14px;
    margin-bottom: 24px;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--line-1);
  }
  .login-brand img { height: 36px; width: auto; display: block; }
  .login-card h2 {
    font-size: 20px;
    margin: 0 0 18px;
    color: var(--fg-0);
    font-weight: 500;
  }
  .login-card .btn { width: 100%; margin-top: 8px; }
  .login-card .btn.sso { margin-top: 0; }
  .divider {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 18px 0;
    color: var(--fg-3);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .divider::before, .divider::after {
    content: "";
    flex: 1;
    height: 1px;
    background: var(--line-1);
  }
  .muted { color: var(--fg-3); font-size: 13px; }
</style>
