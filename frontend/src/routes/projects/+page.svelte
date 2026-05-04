<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { projects, pools as poolsAPI } from "$lib/api.js";
  import TagsEditor from "$lib/TagsEditor.svelte";
  import Modal from "$lib/Modal.svelte";
  import { confirmDialog } from "$lib/confirm.svelte.js";

  let list = $state([]);
  let poolCounts = $state({}); // project_id -> number
  let loading = $state(false);
  let error = $state(null);
  let success = $state(null);
  let editing = $state(null); // project being edited, or null
  let formOpen = $state(false);

  let form = $state(emptyForm());

  function emptyForm() {
    return {
      name: "",
      max_concurrent_runners: 0,
      tags: {},
      scope: "repo",
      org_name: "",
      runner_group_id: 0,
      disabled: false,
    };
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      const [ps, allPools] = await Promise.all([projects.list(), poolsAPI.list()]);
      list = ps || [];
      const counts = {};
      for (const p of allPools || []) {
        counts[p.project_id] = (counts[p.project_id] || 0) + 1;
      }
      poolCounts = counts;
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function openCreate() {
    editing = null;
    form = emptyForm();
    error = null;
    success = null;
    formOpen = true;
  }

  function startEdit(p) {
    editing = p.id;
    form = {
      name: p.name,
      max_concurrent_runners: p.max_concurrent_runners,
      tags: { ...(p.tags || {}) },
      scope: p.scope || "repo",
      org_name: p.org_name || "",
      runner_group_id: p.runner_group_id || 0,
      disabled: p.disabled,
    };
    error = null;
    success = null;
    formOpen = true;
  }

  function cancelEdit() {
    editing = null;
    form = emptyForm();
    error = null;
    formOpen = false;
  }

  function buildBody() {
    const scope = form.scope === "org" ? "org" : "repo";
    return {
      name: form.name.trim(),
      max_concurrent_runners: Number(form.max_concurrent_runners) || 0,
      tags: form.tags || {},
      scope,
      org_name: scope === "org" ? (form.org_name || "").trim() : "",
      runner_group_id: scope === "org" ? Number(form.runner_group_id) || 0 : 0,
      disabled: !!form.disabled,
    };
  }

  async function submit(e) {
    e.preventDefault();
    error = null;
    success = null;
    try {
      const body = buildBody();
      if (editing) {
        await projects.update(editing, body);
        success = `updated ${body.name}`;
      } else {
        await projects.create(body);
        success = `created ${body.name}`;
      }
      cancelEdit();
      await refresh();
    } catch (e) {
      error = e.message;
    }
  }

  async function remove(p) {
    const ok = await confirmDialog({
      title: "Delete project?",
      message: `Permanently delete project "${p.name}"? Pools and repo bindings inside it must already be removed; the backend will refuse otherwise.`,
      confirmLabel: "delete",
      confirmDanger: true,
    });
    if (!ok) return;
    error = null;
    success = null;
    try {
      await projects.delete(p.id);
      success = `deleted ${p.name}`;
      await refresh();
    } catch (e) {
      error = e.message;
    }
  }

  $effect(() => {
    refresh();
  });
</script>

<main>
  <div class="page-header">
    <h2>Projects</h2>
    <div class="row-actions">
      <button class="btn primary" onclick={openCreate}>+ new project</button>
      <button class="btn" onclick={refresh} disabled={loading}>refresh</button>
    </div>
  </div>

  {#if error && !formOpen}<div class="banner err">{error}</div>{/if}
  {#if success}<div class="banner ok">{success}</div>{/if}

  {#if list.length === 0}
    <div class="empty">
      <pre class="ascii">   .---. .---. .---.
   |   | |   | |   |
   '---' '---' '---'</pre>
      <h3>No projects yet</h3>
      <p>Projects group runners by team or workload. Create one to start binding repos and provisioning pools.</p>
      <div class="actions">
        <button class="btn primary" onclick={openCreate}>+ new project</button>
      </div>
    </div>
  {:else}
    <table class="tbl tbl-stack">
      <thead>
        <tr>
          <th>Name</th>
          <th>Scope</th>
          <th>Pools</th>
          <th>Project cap</th>
          <th>Tags</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each list as p (p.id)}
          <tr>
            <td data-label="Name">
              <strong>{p.name}</strong>
              {#if p.disabled}<span class="tag warn">disabled</span>{/if}
            </td>
            <td data-label="Scope">
              {#if p.scope === "org"}
                <span class="tag info">org</span>
                <span class="mono" style="font-size: 0.75rem;">{p.org_name}{p.runner_group_id ? `#${p.runner_group_id}` : ""}</span>
              {:else}
                <span class="tag">repo</span>
              {/if}
            </td>
            <td data-label="Pools"><a href="/pools?project={p.id}">{poolCounts[p.id] ?? 0}</a></td>
            <td data-label="Project cap">{p.max_concurrent_runners > 0 ? p.max_concurrent_runners : "—"}</td>
            <td class="mono" data-label="Tags" style="font-size: 0.75rem;">
              {#if p.tags && Object.keys(p.tags).length > 0}
                {Object.entries(p.tags).map(([k, v]) => `${k}=${v}`).join(", ")}
              {:else}
                <span class="muted">—</span>
              {/if}
            </td>
            <td>
              <div class="row-actions">
                <button class="btn xs" onclick={() => startEdit(p)}>edit</button>
                <button class="btn xs danger" onclick={() => remove(p)}>delete</button>
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</main>

<Modal bind:open={formOpen} title={editing ? "Edit project" : "New project"}>
  <p class="muted" style="margin: 0 0 0.85rem;">
    Project is a logical grouping. EC2 launch settings (AMI, instance types, subnets, etc.) live on the project's <a href="/pools">pools</a>.
  </p>
  {#if error}<div class="banner err">{error}</div>{/if}
  <form onsubmit={submit}>
    <div class="field-row">
      <div class="field">
        <label for="name">Name</label>
        <input id="name" class="input" bind:value={form.name}
          disabled={!!editing} placeholder="my-app" required />
      </div>
      <div class="field">
        <label for="cap">Max concurrent runners <span class="muted">(0 = no project-wide ceiling; per-pool caps still apply)</span></label>
        <input id="cap" class="input" type="number" min="0" bind:value={form.max_concurrent_runners} />
      </div>
    </div>

    <div class="field-row">
      <div class="field">
        <label for="scope">
          Scope
          <span class="muted">
            ({form.scope === "org"
              ? "route by repository.owner.login; shared across the org / runner group"
              : "bind individual repos; runners narrow to <owner>-<repo>"})
          </span>
        </label>
        <select id="scope" class="select" bind:value={form.scope}>
          <option value="repo">repo</option>
          <option value="org">org</option>
        </select>
      </div>
      {#if form.scope === "org"}
        <div class="field">
          <label for="org">Org login <span class="muted">(GitHub org name; case-insensitive match against repository.owner.login)</span></label>
          <input id="org" class="input mono" bind:value={form.org_name} placeholder="acme-inc" required />
        </div>
      {/if}
    </div>

    {#if form.scope === "org"}
      <div class="field">
        <label for="rg">Runner group id <span class="muted">(0 = GitHub's "Default" group, id 1; look up org-specific groups via <code>GET /orgs/&lt;org&gt;/actions/runner-groups</code>)</span></label>
        <input id="rg" class="input" type="number" min="0" bind:value={form.runner_group_id} />
      </div>
    {/if}

    <div class="field">
      <label for="tags-prj">
        Tags
        <span class="muted">
          (cascade to every pool's launch template + every spawned instance and EBS volume; pool tags override on key conflict, repo tags override pool tags. <code>gha:</code> prefix reserved.)
        </span>
      </label>
      <TagsEditor bind:value={form.tags} />
    </div>
    <div class="field">
      <label class="chk"><input type="checkbox" bind:checked={form.disabled} /> disabled</label>
    </div>
    <div class="row-actions">
      <button class="btn primary" type="submit">{editing ? "save" : "create"}</button>
      <button class="btn" type="button" onclick={cancelEdit}>cancel</button>
    </div>
  </form>
</Modal>
