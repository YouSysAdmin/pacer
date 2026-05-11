<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { settings } from "$lib/api.js";

  let status = $state(null);
  let loading = $state(true);
  let loadError = $state(null);

  let rotating = $state(false);
  let rotateError = $state(null);
  let rotateResult = $state(null);
  let confirmOpen = $state(false);

  function fmt(t) {
    if (!t) return "never";
    try {
      return new Date(t).toLocaleString();
    } catch {
      return t;
    }
  }

  async function refresh() {
    loading = true;
    loadError = null;
    try {
      status = await settings.getBootstrapToken();
    } catch (e) {
      loadError = e.message;
    } finally {
      loading = false;
    }
  }

  async function doRotate() {
    rotating = true;
    rotateError = null;
    rotateResult = null;
    try {
      rotateResult = await settings.rotateBootstrapToken();
      // refresh status (updated_at, masked) after a successful rotate
      await refresh();
    } catch (e) {
      rotateError = e.message;
    } finally {
      rotating = false;
      confirmOpen = false;
    }
  }

  $effect(() => {
    refresh();
  });
</script>

<main>
  <div class="page-header">
    <h2>Settings</h2>
    <button class="btn" onclick={refresh} disabled={loading}>
      {loading ? "loading..." : "refresh"}
    </button>
  </div>

  {#if loadError}
    <div class="banner err">{loadError}</div>
  {/if}

  <section class="card">
    <h3>Bootstrap API Token</h3>
    <p class="muted">
      The shared secret baked into every pool's launch-template user-data.
      The in-instance bootstrap script presents it as
      <code>Authorization: Bearer &lt;token&gt;</code> when calling
      <code>POST /api/runner/bootstrap</code> to fetch its per-job
      callback token. Rotate the token if you suspect leakage or as
      part of a routine rotation policy.
    </p>

    {#if status && status.set}
      <div class="stats">
        <div class="stat">
          <div class="label">Token</div>
          <div class="value mono">{status.masked}</div>
        </div>
        <div class="stat">
          <div class="label">Last rotated</div>
          <div class="value">{fmt(status.updated_at)}</div>
        </div>
      </div>
    {:else if !loading}
      <div class="banner err">No bootstrap API token in the settings table. Restart pacer to auto-generate one.</div>
    {/if}

    {#if rotateResult}
      <div class="banner ok">
        Token rotated.
        {rotateResult.pools_rematerialized} pool{rotateResult.pools_rematerialized === 1 ? "" : "s"} re-materialized.
        {#if rotateResult.pools_failed && rotateResult.pools_failed.length > 0}
          Failed: <span class="mono">{rotateResult.pools_failed.join(", ")}</span> -- re-save manually.
        {/if}
      </div>
    {/if}

    {#if rotateError}
      <div class="banner err">{rotateError}</div>
    {/if}

    <div class="actions">
      <button class="btn danger" onclick={() => (confirmOpen = true)} disabled={rotating || !status?.set}>
        {rotating ? "rotating..." : "Rotate"}
      </button>
    </div>
  </section>

  {#if confirmOpen}
    <div class="modal-backdrop" onclick={() => (confirmOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <h3>Rotate bootstrap API token?</h3>
        <p>
          A new token will be generated and stored in the settings table.
          Every pool's launch template will be re-materialized with the
          new token (one new LT version per pool).
        </p>
        <p class="muted">
          <strong>In-flight spawns</strong> launched against an older LT
          version will fail to bootstrap (401 from the bootstrap endpoint)
          and will be marked failed by the orchestrator. Drain pending
          jobs before rotating if you can't tolerate that.
        </p>
        <div class="actions">
          <button class="btn" onclick={() => (confirmOpen = false)} disabled={rotating}>Cancel</button>
          <button class="btn danger" onclick={doRotate} disabled={rotating}>
            {rotating ? "rotating..." : "Rotate now"}
          </button>
        </div>
      </div>
    </div>
  {/if}
</main>

<style>
  .actions {
    margin-top: 1rem;
    display: flex;
    gap: 0.5rem;
  }
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--bg, #1a1a1a);
    border: 1px solid var(--border, #333);
    border-radius: 6px;
    padding: 1.5rem;
    max-width: 32rem;
    width: 90%;
  }
  .modal h3 {
    margin-top: 0;
  }
</style>
