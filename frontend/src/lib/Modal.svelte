<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  // Lightweight modal.  Parent controls visibility via `bind:open`.
  // Child content is passed as the default snippet.  Closes on
  // backdrop click or Escape.
  let { open = $bindable(false), title = "", children } = $props();

  function close() { open = false; }

  function onkeydown(e) {
    if (e.key === "Escape" && open) close();
  }

  function onBackdropClick(e) {
    if (e.target === e.currentTarget) close();
  }
</script>

<svelte:window onkeydown={onkeydown} />

{#if open}
  <div
    class="modal-backdrop"
    role="presentation"
    onclick={onBackdropClick}
  >
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-head">
        <h3>{title}</h3>
        <button type="button" class="btn xs" onclick={close} aria-label="close">close</button>
      </div>
      <div class="modal-body">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}
