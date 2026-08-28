<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  // List editor for prefixed AWS IDs (subnet-..., sg-..., etc.).
  // Parent passes a string array via bind:value plus a prefix. We
  // render one input per entry with a remove button and a "+ add"
  // at the bottom.  Validation is soft -- the regex feeds the
  // browser's HTML5 `pattern` check (which is skipped on empty
  // inputs) and a visual warning.  Empty rows are filtered when we
  // serialize back to the parent, so the user can transiently leave
  // a blank row open without breaking the form.
  let {
    value = $bindable([]),
    prefix = "",
    placeholder = "",
    addLabel = "+ add",
  } = $props();

  let rows = $state(rowsFrom(value));
  let lastSerialized = $state(JSON.stringify(value || []));

  function rowsFrom(arr) {
    return (arr || []).map((v) => ({ v }));
  }

  // Re-seed when the parent replaces value wholesale (cancel/edit).
  $effect(() => {
    const incoming = JSON.stringify(value || []);
    if (incoming !== lastSerialized) {
      rows = rowsFrom(value);
      lastSerialized = incoming;
    }
  });

  function serialize() {
    const out = rows.map((r) => (r.v || "").trim()).filter(Boolean);
    value = out;
    lastSerialized = JSON.stringify(out);
  }

  function add() {
    rows = [...rows, { v: "" }];
    serialize();
  }

  function remove(i) {
    rows = rows.filter((_, idx) => idx !== i);
    serialize();
  }

  // 8-17 lowercase hex chars after the prefix matches both legacy
  // (8-char) and modern (17-char) AWS resource IDs.  Empty prefix
  // disables validation entirely.  $derived so the pattern tracks
  // the prop if a parent ever swaps it out.
  const pattern = $derived(prefix ? prefix + "[a-f0-9]{8,17}" : "");
  function isValid(v) {
    if (!v || !prefix) return true;
    return new RegExp("^" + pattern + "$").test(v.trim());
  }
</script>

<div class="id-list">
  {#if rows.length === 0}
    <div class="id-empty muted">No entries. Click <strong>{addLabel}</strong> to add one.</div>
  {:else}
    {#each rows as row, i (i)}
      <div class="id-row">
        <input
          class="input mono"
          {placeholder}
          {pattern}
          bind:value={row.v}
          oninput={serialize}
          aria-invalid={!isValid(row.v)}
          aria-label={placeholder || "ID"}
          title={prefix ? `expected ${prefix}<8-17 hex>` : ""}
        />
        <button
          type="button"
          class="btn xs danger"
          onclick={() => remove(i)}
          aria-label="Remove"
          title="Remove"
        >
          remove
        </button>
        {#if !isValid(row.v)}
          <span class="field-warn row-span">expected <code>{prefix}xxxxxxxx</code> (8-17 hex chars)</span>
        {/if}
      </div>
    {/each}
  {/if}
  <button type="button" class="btn sm" onclick={add}>{addLabel}</button>
</div>

<style>
  .id-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .id-row {
    display: grid;
    grid-template-columns: 1fr auto;
    align-items: center;
    gap: 8px;
  }
  .id-empty {
    padding: 8px 0;
    font-size: 13px;
  }
  /* Make the global .field-warn span the full width of the row so
     it sits underneath both the input and the remove button rather
     than squeezing into the input column. */
  .row-span { grid-column: 1 / -1; margin-top: 0; }
</style>
