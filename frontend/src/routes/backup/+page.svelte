<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { backup } from "$lib/api.js";

  // Export pulls the JSON down through fetch() rather than the call()
  // wrapper so we can preserve the Content-Disposition filename the
  // backend stamps. URL.createObjectURL + a synthetic <a> click is the
  // standard browser-side download trick.
  let exporting = $state(false);
  let exportError = $state(null);

  async function doExport() {
    exporting = true;
    exportError = null;
    try {
      const res = await backup.exportRaw();
      if (!res.ok) {
        const text = await res.text();
        let msg = `HTTP ${res.status}`;
        try { const j = JSON.parse(text); if (j.error) msg = j.error; } catch {}
        throw new Error(msg);
      }
      const blob = await res.blob();
      const cd = res.headers.get("content-disposition") || "";
      const m = cd.match(/filename="([^"]+)"/);
      const filename = m ? m[1] : `pacer-backup-${new Date().toISOString().slice(0, 10)}.json`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      exportError = e.message;
    } finally {
      exporting = false;
    }
  }

  // Import accepts either a file upload or pasted JSON. File takes
  // precedence when both are populated. Submission shows the per-
  // section counts the backend returns plus any per-row errors.
  let importFile = $state(null);
  let importText = $state("");
  let importing = $state(false);
  let importError = $state(null);
  let importResult = $state(null);

  function pickFile(ev) {
    importFile = ev.target.files?.[0] || null;
  }

  async function doImport() {
    importing = true;
    importError = null;
    importResult = null;
    try {
      let raw;
      if (importFile) {
        raw = await importFile.text();
      } else if (importText.trim()) {
        raw = importText;
      } else {
        throw new Error("provide a file or paste JSON below");
      }
      let snap;
      try {
        snap = JSON.parse(raw);
      } catch (e) {
        throw new Error("not valid JSON: " + e.message);
      }
      importResult = await backup.import(snap);
    } catch (e) {
      importError = e.message;
    } finally {
      importing = false;
    }
  }

  function clearImport() {
    importFile = null;
    importText = "";
    importError = null;
    importResult = null;
    const input = document.getElementById("backup-file");
    if (input) input.value = "";
  }
</script>

<main>
  <div class="page-header">
    <h2>Backup</h2>
  </div>

  <div class="card">
    <h3>Export</h3>
    <p class="muted">
      Downloads every project, pool, and repo binding as a single JSON
      document. Operational data (jobs, instances, audit log, users,
      secrets) is intentionally excluded.
    </p>
    <div class="row-actions">
      <button class="btn primary" onclick={doExport} disabled={exporting}>
        {exporting ? "preparing..." : "download backup"}
      </button>
    </div>
    {#if exportError}
      <div class="banner err">{exportError}</div>
    {/if}
  </div>

  <div class="card">
    <h3>Import</h3>
    <p class="muted">
      Upserts by stable name: projects by name, pools by
      <code>(project, pool)</code>, repos by <code>full_name</code>.
      Existing rows are updated in place; new rows are created.
      Pool imports re-materialize the EC2 launch template.
    </p>
    <div class="field">
      <label for="backup-file">Backup file</label>
      <input id="backup-file" class="input" type="file" accept="application/json,.json"
             onchange={pickFile} />
    </div>
    <div class="field">
      <label for="backup-text">...or paste JSON</label>
      <textarea id="backup-text" class="input mono" rows="8"
                placeholder="paste exported JSON here"
                bind:value={importText}></textarea>
    </div>
    <div class="row-actions">
      <button class="btn primary" onclick={doImport} disabled={importing}>
        {importing ? "importing..." : "import"}
      </button>
      <button class="btn" onclick={clearImport} disabled={importing}>clear</button>
    </div>
    {#if importError}
      <div class="banner err">{importError}</div>
    {/if}
    {#if importResult}
      <div class="banner ok">
        Import complete:
        projects {importResult.projects.created} created / {importResult.projects.updated} updated,
        pools {importResult.pools.created} created / {importResult.pools.updated} updated,
        repos {importResult.repos.created} created / {importResult.repos.updated} updated.
      </div>
      {#if importResult.errors && importResult.errors.length > 0}
        <div class="banner warn">
          <strong>{importResult.errors.length} row error{importResult.errors.length === 1 ? "" : "s"}:</strong>
          <ul class="err-list">
            {#each importResult.errors as e}<li>{e}</li>{/each}
          </ul>
        </div>
      {/if}
    {/if}
  </div>
</main>

<style>
  textarea.input {
    width: 100%;
    resize: vertical;
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.45;
    background: var(--bg-0);
  }
  .err-list {
    margin: 6px 0 0;
    padding-left: 18px;
  }
  .err-list li {
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.5;
  }
  code {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-0);
    padding: 1px 4px;
    border-radius: var(--r-sm);
  }
</style>
