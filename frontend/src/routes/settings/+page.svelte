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

  // Retention card state. retention is the GET-shaped payload from
  // /api/settings/retention. auditInput / webhookInput are the
  // editable fields (decoupled from `retention` so the user can
  // change them without round-tripping the server). saveMsg /
  // saveError act as inline status after PUT.
  let retention = $state(null);
  let auditInput = $state("");
  let webhookInput = $state("");
  let savingRetention = $state(false);
  let retentionSaveMsg = $state(null);
  let retentionSaveError = $state(null);

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
      const [s, r] = await Promise.all([
        settings.getBootstrapToken(),
        settings.getRetention(),
      ]);
      status = s;
      retention = r;
      // Seed the editable inputs with the current EFFECTIVE values
      // (empty string when the override matches the default would
      // hide the value the operator is actually on. Show the number
      // unconditionally and rely on the "default: N" hint + the
      // "use default" button for the cleared state).
      auditInput = String(r.audit_days);
      webhookInput = String(r.webhook_days);
    } catch (e) {
      loadError = e.message;
    } finally {
      loading = false;
    }
  }

  // Build a PUT body that only includes the fields the operator
  // actually changed -- avoids overwriting one field while editing
  // the other.
  function buildRetentionBody() {
    const body = {};
    const a = parseInt(auditInput, 10);
    const w = parseInt(webhookInput, 10);
    if (retention && a !== retention.audit_days) {
      if (Number.isNaN(a)) return { __err: "audit days: not a number" };
      body.audit_days = a;
    }
    if (retention && w !== retention.webhook_days) {
      if (Number.isNaN(w)) return { __err: "webhook days: not a number" };
      body.webhook_days = w;
    }
    return body;
  }

  async function saveRetention() {
    retentionSaveMsg = null;
    retentionSaveError = null;
    const body = buildRetentionBody();
    if (body.__err) {
      retentionSaveError = body.__err;
      return;
    }
    if (Object.keys(body).length === 0) {
      retentionSaveError = "nothing changed";
      return;
    }
    savingRetention = true;
    try {
      retention = await settings.putRetention(body);
      auditInput = String(retention.audit_days);
      webhookInput = String(retention.webhook_days);
      retentionSaveMsg = "Saved. Next prune sweep (within 24 h) will use the new periods.";
    } catch (e) {
      retentionSaveError = e.message;
    } finally {
      savingRetention = false;
    }
  }

  // Per-field "use default": sends 0 (the explicit clear-override
  // sentinel) for that field only.
  async function resetField(field) {
    retentionSaveMsg = null;
    retentionSaveError = null;
    savingRetention = true;
    try {
      retention = await settings.putRetention({ [field]: 0 });
      auditInput = String(retention.audit_days);
      webhookInput = String(retention.webhook_days);
      retentionSaveMsg = `Reverted ${field} to default (${field === "audit_days" ? retention.audit_default : retention.webhook_default}).`;
    } catch (e) {
      retentionSaveError = e.message;
    } finally {
      savingRetention = false;
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

  <section class="card">
    <h3>Log retention</h3>
    <p class="muted">
      How long the audit log and webhook delivery records are kept
      before the daily pruner deletes them. The server starts with the
      YAML defaults below. Values entered here override those at
      runtime and persist in the settings table. Changes take effect
      at the next daily prune sweep -- use the
      <a href="/audit">manual prune</a> button on the audit page if
      you need to clean up immediately.
    </p>

    {#if retention}
      <div class="retention-grid">
        <label class="retention-row">
          <span class="retention-label">
            Audit log
            <span class="muted retention-hint">
              default: {retention.audit_default} days
              {#if retention.audit_overridden}<span class="tag info"> overridden</span>{/if}
            </span>
          </span>
          <input
            class="input retention-input"
            type="number"
            min="1"
            max="3650"
            bind:value={auditInput}
            disabled={savingRetention}
          />
          <span class="retention-unit">days</span>
          <button
            class="btn xs"
            onclick={() => resetField("audit_days")}
            disabled={savingRetention || !retention.audit_overridden}
            title="Revert to the YAML default"
          >use default</button>
        </label>

        <label class="retention-row">
          <span class="retention-label">
            Webhook deliveries
            <span class="muted retention-hint">
              default: {retention.webhook_default} days
              {#if retention.webhook_overridden}<span class="tag info"> overridden</span>{/if}
            </span>
          </span>
          <input
            class="input retention-input"
            type="number"
            min="1"
            max="365"
            bind:value={webhookInput}
            disabled={savingRetention}
          />
          <span class="retention-unit">days</span>
          <button
            class="btn xs"
            onclick={() => resetField("webhook_days")}
            disabled={savingRetention || !retention.webhook_overridden}
            title="Revert to the YAML default"
          >use default</button>
        </label>
      </div>

      {#if retentionSaveMsg}
        <div class="banner ok">{retentionSaveMsg}</div>
      {/if}
      {#if retentionSaveError}
        <div class="banner err">{retentionSaveError}</div>
      {/if}

      <div class="actions">
        <button
          class="btn primary"
          onclick={saveRetention}
          disabled={savingRetention}
        >{savingRetention ? "saving..." : "save"}</button>
      </div>
    {/if}
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

  /* Retention card: one row per setting, each row inlined as
     [label][number-input][unit][use-default-button]. The label
     stack carries the field name + a default/overridden hint. */
  .retention-grid {
    display: flex;
    flex-direction: column;
    gap: 14px;
    margin-top: 14px;
  }
  .retention-row {
    display: grid;
    grid-template-columns: 1fr 110px auto auto;
    align-items: center;
    gap: 10px;
  }
  .retention-label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 13px;
    color: var(--fg-1);
  }
  .retention-hint {
    font-size: 11px;
    font-family: var(--font-mono);
  }
  .retention-input {
    height: 32px;
    text-align: right;
  }
  .retention-unit {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--fg-3);
  }
  @media (max-width: 720px) {
    .retention-row {
      grid-template-columns: 1fr;
      gap: 4px;
    }
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
