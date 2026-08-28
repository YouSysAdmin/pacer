<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { onMount } from "svelte";
  import { repos, projects } from "$lib/api.js";
  import TagsEditor from "$lib/TagsEditor.svelte";
  import Modal from "$lib/Modal.svelte";
  import { confirmDialog } from "$lib/confirm.svelte.js";
  import { fieldErrorsFrom, isRepoFullName, isReservedTagKey } from "$lib/validators.js";

  // Caps mirror domain/repo/endpoint.go::bindInput.
  const FULL_NAME_MAX = 140;

  let list = $state([]);
  let projectList = $state([]);
  let loading = $state(false);
  let error = $state(null);
  let success = $state(null);
  let editing = $state(null); // full_name being edited, or null
  let formOpen = $state(false);
  let fieldErrors = $state({});

  let form = $state(emptyForm());

  function emptyForm() {
    return {
      full_name: "",
      project_id: "",
      max_concurrent_runners: "",
      tags: {},
    };
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      const [rs, ps] = await Promise.all([repos.list(), projects.list()]);
      list = rs || [];
      projectList = ps || [];
      const firstBindable = (projectList || []).find((p) => (p.scope || "repo") !== "org");
      if (!form.project_id && firstBindable) {
        form.project_id = firstBindable.id;
      }
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function projectName(id) {
    const p = projectList.find((p) => p.id === id);
    return p ? p.name : id;
  }

  // Org-scoped projects don't accept repo bindings -- the webhook routes
  // by repository.owner.login instead. Filter them out of the picker so
  // operators can't pick one and hit a 400 on submit.
  let bindableProjects = $derived((projectList || []).filter((p) => (p.scope || "repo") !== "org"));

  function openCreate() {
    if (bindableProjects.length === 0) return;
    editing = null;
    form = emptyForm();
    form.project_id = bindableProjects[0].id;
    error = null;
    success = null;
    fieldErrors = {};
    formOpen = true;
  }

  function startEdit(r) {
    editing = r.full_name;
    form = {
      full_name: r.full_name,
      project_id: r.project_id,
      max_concurrent_runners: r.max_concurrent_runners ?? "",
      tags: { ...(r.tags || {}) },
    };
    error = null;
    success = null;
    fieldErrors = {};
    formOpen = true;
  }

  function cancelEdit() {
    editing = null;
    form = emptyForm();
    if (projectList.length > 0) form.project_id = projectList[0].id;
    error = null;
    fieldErrors = {};
    formOpen = false;
  }

  function clearFieldError(name) {
    if (fieldErrors[name]) {
      const next = { ...fieldErrors };
      delete next[name];
      fieldErrors = next;
    }
  }

  // numericLt0 detects a number-input that's been driven negative.
  // Mirrors the same helper in projects / pools forms. The backend
  // does not have an explicit rule for max_concurrent_runners on
  // repos (the field is *int with omitempty) but the operator
  // intent is unambiguous.
  function numericLt0(v) {
    if (v === "" || v == null) return false;
    const n = Number(v);
    return Number.isFinite(n) && n < 0;
  }

  // Live hints mirror domain/repo/endpoint.go::bindInput rules.
  // Messages reference the labels the operator sees on the form
  // ("Repository", "Max concurrent runners") rather than the json
  // field names.
  let liveHints = $derived(buildHints());
  function buildHints() {
    const h = {};
    if (form.full_name) {
      if (!isRepoFullName(form.full_name)) {
        h.full_name = `Repository must be in "owner/name" form (e.g. octocat/hello-world)`;
      } else if (form.full_name.length > FULL_NAME_MAX) {
        h.full_name = `Repository must be at most ${FULL_NAME_MAX} characters`;
      }
    }
    if (numericLt0(form.max_concurrent_runners)) {
      h.max_concurrent_runners = "Max concurrent runners must be 0 or greater (leave blank to inherit the project cap)";
    }
    for (const k of Object.keys(form.tags || {})) {
      if (isReservedTagKey(k)) {
        h.tags = `Tag keys starting with "gha:" are reserved; pick a different prefix`;
        break;
      }
    }
    return h;
  }

  function hintFor(name) {
    return liveHints[name] || fieldErrors[name] || "";
  }

  async function bind(e) {
    e.preventDefault();
    error = null;
    success = null;
    fieldErrors = {};
    const hints = buildHints();
    if (Object.keys(hints).length > 0) {
      error = "Please fix the highlighted fields";
      return;
    }
    const body = {
      full_name: form.full_name.trim(),
      project_id: form.project_id,
      tags: form.tags || {},
    };
    const cap = Number(form.max_concurrent_runners);
    if (cap > 0) body.max_concurrent_runners = cap;
    try {
      await repos.bind(body);
      success = editing ? `updated ${body.full_name}` : `bound ${body.full_name}`;
      cancelEdit();
      await refresh();
    } catch (e) {
      error = e.message;
      fieldErrors = fieldErrorsFrom(e);
    }
  }

  async function unbind(r) {
    const ok = await confirmDialog({
      title: "Unbind repo?",
      message: `Remove the binding between ${r.full_name} and its project? Workflows from this repo will stop matching pacer pools until rebound.`,
      confirmLabel: "unbind",
      confirmDanger: true,
    });
    if (!ok) return;
    error = null;
    success = null;
    try {
      await repos.unbind(r.full_name);
      success = `unbound ${r.full_name}`;
      await refresh();
    } catch (e) {
      error = e.message;
    }
  }

  // Mount-only fetch -- see projects/+page.svelte for the rationale.
  onMount(() => { refresh(); });
</script>

<main>
  <div class="page-header">
    <h2>Repos</h2>
    <div class="row-actions">
      <button class="btn primary" onclick={openCreate} disabled={bindableProjects.length === 0}>+ bind repo</button>
      <button class="btn" onclick={refresh} disabled={loading}>refresh</button>
    </div>
  </div>

  {#if error && !formOpen}<div class="banner err">{error}</div>{/if}
  {#if success}<div class="banner ok">{success}</div>{/if}

  {#if list.length === 0}
    <div class="empty">
      <pre class="ascii">   .--------.       .--------.
   |  repo  | ----> |  proj  |
   '--------'       '--------'</pre>
      <h3>No bindings yet</h3>
      {#if bindableProjects.length === 0}
        <p>A repo binds to a <strong>repo-scoped</strong> project so its workflow jobs can claim runners. Create one (or switch an existing project's scope from <em>org</em> to <em>repo</em>) under <a href="/projects">Projects</a>. Org-scoped projects route by <code>repository.owner.login</code> and don't need bindings.</p>
        <div class="actions">
          <a class="btn primary" href="/projects">+ new project</a>
        </div>
      {:else}
        <p>Bind a GitHub repo (<code>owner/name</code>) to one of your repo-scoped projects so its workflow jobs can claim runners.</p>
        <div class="actions">
          <button class="btn primary" onclick={openCreate}>+ bind repo</button>
        </div>
      {/if}
    </div>
  {:else}
    <table class="tbl tbl-stack">
      <thead>
        <tr>
          <th>Repository</th>
          <th>Project</th>
          <th>Cap override</th>
          <th>Tags</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each list as r (r.full_name)}
          <tr>
            <td class="mono" data-label="Repository">{r.full_name}</td>
            <td data-label="Project">{projectName(r.project_id)}</td>
            <td data-label="Cap override">{r.max_concurrent_runners ?? "\u2014"}</td>
            <td class="mono" data-label="Tags" style="font-size: 0.75rem;">
              {#if r.tags && Object.keys(r.tags).length > 0}
                {Object.entries(r.tags).map(([k, v]) => `${k}=${v}`).join(", ")}
              {:else}
                <span class="muted">&mdash;</span>
              {/if}
            </td>
            <td>
              <div class="row-actions">
                <button class="btn xs" onclick={() => startEdit(r)}>edit</button>
                <button class="btn xs danger" onclick={() => unbind(r)}>unbind</button>
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</main>

<Modal bind:open={formOpen} title={editing ? "Edit binding" : "Bind repo to project"}>
  {#if error}<div class="banner err">{error}</div>{/if}
  <form onsubmit={bind}>
    <div class="field-row">
      <div class="field">
        <label for="fn">Repository <span class="muted">(owner/name)</span></label>
        <div>
          <input id="fn" class="input" bind:value={form.full_name}
            disabled={!!editing} placeholder="octocat/hello-world" required
            oninput={() => clearFieldError("full_name")}
            maxlength={FULL_NAME_MAX}
            aria-invalid={!!hintFor("full_name")} />
          {#if hintFor("full_name")}<span class="field-warn">{hintFor("full_name")}</span>{/if}
        </div>
      </div>
      <div class="field">
        <label for="pj">Project</label>
        <div>
          <select id="pj" class="select" bind:value={form.project_id} required
            onchange={() => clearFieldError("project_id")}
            aria-invalid={!!hintFor("project_id")}>
            {#each bindableProjects as p (p.id)}
              <option value={p.id}>{p.name}</option>
            {/each}
          </select>
          {#if hintFor("project_id")}<span class="field-warn">{hintFor("project_id")}</span>{/if}
        </div>
      </div>
    </div>
    <div class="field">
      <label for="cap">Max concurrent runners <span class="muted">(blank = inherit project cap)</span></label>
      <input id="cap" class="input" type="number" min="0" bind:value={form.max_concurrent_runners}
        oninput={() => clearFieldError("max_concurrent_runners")}
        aria-invalid={!!hintFor("max_concurrent_runners")} />
      {#if hintFor("max_concurrent_runners")}<span class="field-warn">{hintFor("max_concurrent_runners")}</span>{/if}
    </div>
    <div class="field">
      <label for="tags-repo">
        Tags
        <span class="muted">
          (override pool + project tags on key conflict. Stamped on the spawned instance + EBS volumes only -- not on the pool's launch template, which is shared. <code>gha:</code> prefix reserved.)
        </span>
      </label>
      <TagsEditor bind:value={form.tags} />
      {#if hintFor("tags")}<span class="field-warn">{hintFor("tags")}</span>{/if}
    </div>
    <div class="row-actions">
      <button class="btn primary" type="submit">{editing ? "save" : "bind"}</button>
      <button class="btn" type="button" onclick={cancelEdit}>cancel</button>
    </div>
  </form>
</Modal>
