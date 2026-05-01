<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  // Single instance mounted in +layout.svelte. Reads state from
  // confirm.svelte.js and renders when open. All three buttons
  // (confirm, cancel, backdrop, Escape) settle the underlying
  // Promise via settleConfirm().
  import Modal from "./Modal.svelte";
  import { confirmState, settleConfirm } from "./confirm.svelte.js";

  const s = confirmState();

  function onCancel() { settleConfirm(false); }
  function onConfirm() { settleConfirm(true); }

  // The Modal already closes on Escape / backdrop click. Hook into
  // its `open` binding so those paths route through settleConfirm
  // (i.e., resolve as Cancel) instead of leaving the Promise dangling.
  let bound = $state(false);
  $effect(() => {
    bound = s.open;
  });
  $effect(() => {
    // Bound went false but state.open is still true -- user clicked
    // the modal's close affordance (X / Escape / backdrop). Settle
    // as Cancel.
    if (!bound && s.open) {
      settleConfirm(false);
    }
  });
</script>

<Modal bind:open={bound} title={s.title}>
  {#if s.message}
    <p class="confirm-message">{s.message}</p>
  {/if}
  <div class="row-actions confirm-actions">
    <button type="button" class="btn primary {s.confirmDanger ? 'danger' : ''}" onclick={onConfirm}>
      {s.confirmLabel}
    </button>
    <button type="button" class="btn" onclick={onCancel}>
      {s.cancelLabel}
    </button>
  </div>
</Modal>

<style>
  .confirm-message {
    margin: 0 0 16px;
    line-height: 1.5;
  }
  .confirm-actions {
    justify-content: flex-end;
  }
</style>
