<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  // Two-way bound tag map editor.  Renders one row per entry with
  // (key, value, remove) controls plus an "+ add tag" button.
  // Parent passes an object via bind:value -- e.g.
  //   <TagsEditor bind:value={form.tags} reservedPrefix="gha:" />
  // Internally we keep an array (rows) so duplicate / blank keys
  // can exist while typing. serialize() rebuilds the object on
  // every change.
  let { value = $bindable({}), reservedPrefix = "gha:" } = $props();

  // Seed rows from the incoming object.  We only re-seed when value
  // changes to something we didn't author -- the rows-out-of-sync
  // case happens when a parent loads an existing record into the
  // form (startEdit).
  let rows = $state(rowsFrom(value));
  let lastSerialized = $state(JSON.stringify(value || {}));

  function rowsFrom(obj) {
    const entries = Object.entries(obj || {});
    return entries.map(([k, v]) => ({ k, v }));
  }

  // Re-seed if the parent replaces value wholesale (e.g. cancel/edit
  // toggles).  We compare to the last value we serialized so our own
  // edits don't trigger a reseed.
  $effect(() => {
    const incoming = JSON.stringify(value || {});
    if (incoming !== lastSerialized) {
      rows = rowsFrom(value);
      lastSerialized = incoming;
    }
  });

  function serialize() {
    const out = {};
    for (const r of rows) {
      const k = (r.k || "").trim();
      if (!k) continue;
      out[k] = (r.v || "").trim();
    }
    value = out;
    lastSerialized = JSON.stringify(out);
  }

  function add() {
    rows = [...rows, { k: "", v: "" }];
    serialize();
  }

  function remove(i) {
    rows = rows.filter((_, idx) => idx !== i);
    serialize();
  }

  function isReserved(key) {
    if (!reservedPrefix) return false;
    return (key || "").toLowerCase().startsWith(reservedPrefix.toLowerCase());
  }
</script>

<div class="tags-editor">
  {#if rows.length === 0}
    <div class="tags-empty muted">No tags. Click <strong>+ add tag</strong> to add one.</div>
  {:else}
    {#each rows as row, i (i)}
      <div class="tag-row">
        <input
          class="input mono"
          placeholder="key"
          bind:value={row.k}
          oninput={serialize}
          aria-label="Tag key"
        />
        <span class="tag-eq">=</span>
        <input
          class="input mono"
          placeholder="value"
          bind:value={row.v}
          oninput={serialize}
          aria-label="Tag value"
        />
        <button
          type="button"
          class="btn xs danger"
          onclick={() => remove(i)}
          aria-label="Remove tag"
          title="Remove"
        >
          remove
        </button>
        {#if isReserved(row.k)}
          <span class="tag-warn">reserved prefix</span>
        {/if}
      </div>
    {/each}
  {/if}
  <button type="button" class="btn sm" onclick={add}>+ add tag</button>
</div>

<style>
  .tags-editor {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .tag-row {
    display: grid;
    grid-template-columns: 1fr auto 1fr auto auto;
    align-items: center;
    gap: 8px;
  }
  .tag-eq {
    color: var(--fg-3);
    font-family: var(--font-mono);
    font-size: 13px;
  }
  .tags-empty {
    padding: 8px 0;
    font-size: 13px;
  }
  .tag-warn {
    grid-column: 1 / -1;
    color: var(--crit);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  @media (max-width: 700px) {
    .tag-row {
      grid-template-columns: 1fr auto 1fr;
    }
    .tag-row .btn {
      grid-column: 1 / -1;
      justify-self: end;
    }
  }
</style>
