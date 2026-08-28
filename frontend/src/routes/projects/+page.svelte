<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
  import { onMount } from "svelte";
  import { projects, pools as poolsAPI } from "$lib/api.js";
  import TagsEditor from "$lib/TagsEditor.svelte";
  import Modal from "$lib/Modal.svelte";
  import { confirmDialog } from "$lib/confirm.svelte.js";
  import { fieldErrorsFrom, isReservedTagKey, noSlashOrSpace } from "$lib/validators.js";

  // Caps mirror the Go DTO tags on project/endpoint.go::input. Keep
  // these in sync when the backend rules move. Drift shows up as a
  // green client tick followed by a server 400.
  const NAME_MAX = 128;
  const ORG_NAME_MAX = 39;

  let list = $state([]);
  let poolCounts = $state({}); // project_id -> number
  let loading = $state(false);
  let error = $state(null);
  let success = $state(null);
  let editing = $state(null); // project being edited, or null
  let formOpen = $state(false);

  // fieldErrors maps a json field name (matches the backend's
  // validator.RegisterTagNameFunc(json)) to an inline message rendered
  // under the offending input. Server-side errors populate this in
  // submit()'s catch. User input clears the specific entry on each
  // keystroke so stale errors don't linger after a fix.
  let fieldErrors = $state({});

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
    fieldErrors = {};
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
    fieldErrors = {};
    formOpen = true;
  }

  function cancelEdit() {
    editing = null;
    form = emptyForm();
    error = null;
    fieldErrors = {};
    formOpen = false;
  }

  // clearFieldError drops a single entry from fieldErrors as the user
  // types so the inline warning disappears the moment they start
  // fixing the offending input.
  function clearFieldError(name) {
    if (fieldErrors[name]) {
      const next = { ...fieldErrors };
      delete next[name];
      fieldErrors = next;
    }
  }

  // numericLt0 detects a number-input that's been driven negative
  // (Svelte's `bind:value` on type=number stores numbers but the
  // user can still paste / keyboard in a negative). The backend
  // silently clamps negatives via Normalize(), so without an inline
  // hint the value the user typed would disappear without comment.
  function numericLt0(v) {
    const n = Number(v);
    return Number.isFinite(n) && n < 0;
  }

  // Live derived hints. These mirror the backend rules at
  // project/endpoint.go::input -- name length, org_name shape, and
  // the "required_if scope=org" conditional. The map is keyed by
  // the json field name (so server-side err.fields overlays cleanly
  // in hintFor()), but the messages themselves are written for the
  // operator using the labels they see in the form.
  let liveHints = $derived(buildHints());
  function buildHints() {
    const h = {};
    if (form.name && form.name.length > NAME_MAX) {
      h.name = `Name must be at most ${NAME_MAX} characters`;
    }
    if (numericLt0(form.max_concurrent_runners)) {
      h.max_concurrent_runners = "Max concurrent runners must be 0 or greater";
    }
    if (numericLt0(form.runner_group_id)) {
      h.runner_group_id = "Runner group id must be 0 or greater";
    }
    if (form.scope === "org") {
      if (!form.org_name) {
        h.org_name = "Org login is required when scope is set to org";
      } else if (!noSlashOrSpace(form.org_name)) {
        h.org_name = "Org login must not contain slashes or spaces";
      } else if (form.org_name.length > ORG_NAME_MAX) {
        h.org_name = `Org login must be at most ${ORG_NAME_MAX} characters`;
      }
    }
    for (const k of Object.keys(form.tags || {})) {
      if (isReservedTagKey(k)) {
        h.tags = `Tag keys starting with "gha:" are reserved; pick a different prefix`;
        break;
      }
    }
    return h;
  }

  // hintFor returns the live hint, falling back to the server-side
  // field error if there's no client-side issue. Server errors win
  // when they cover a rule the client doesn't (length / uniqueness /
  // anything cross-row).
  function hintFor(name) {
    return liveHints[name] || fieldErrors[name] || "";
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
    fieldErrors = {};
    // Re-run the live derived hints synchronously so a click on
    // submit-with-an-empty-org-name surfaces the same inline message
    // the user would see when focused on the input. This isn't a
    // hard gate (the server still validates), but it gives faster
    // feedback than waiting for the round-trip.
    const hints = buildHints();
    if (Object.keys(hints).length > 0) {
      error = "Please fix the highlighted fields";
      return;
    }
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
      fieldErrors = fieldErrorsFrom(e);
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

  // Mount-only fetch. A bare $effect(() => refresh()) re-fires every
  // time any reactive variable in the script tracks (form fields,
  // poolCounts, success banner, etc.), causing duplicate API calls
  // during normal interaction. The list refresh after a successful
  // create/update/delete is triggered explicitly by submit().
  onMount(() => { refresh(); });
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
            <td data-label="Project cap">{p.max_concurrent_runners > 0 ? p.max_concurrent_runners : "\u2014"}</td>
            <td class="mono" data-label="Tags" style="font-size: 0.75rem;">
              {#if p.tags && Object.keys(p.tags).length > 0}
                {Object.entries(p.tags).map(([k, v]) => `${k}=${v}`).join(", ")}
              {:else}
                <span class="muted">&mdash;</span>
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
        <div>
          <input id="name" class="input" bind:value={form.name}
            oninput={() => clearFieldError("name")}
            disabled={!!editing} placeholder="my-app" required
            maxlength={NAME_MAX}
            aria-invalid={!!hintFor("name")} />
          {#if hintFor("name")}<span class="field-warn">{hintFor("name")}</span>{/if}
        </div>
      </div>
      <div class="field">
        <label for="cap">Max concurrent runners <span class="muted">(0 = no project-wide ceiling. per-pool caps still apply)</span></label>
        <div>
          <input id="cap" class="input" type="number" min="0" bind:value={form.max_concurrent_runners}
            oninput={() => clearFieldError("max_concurrent_runners")}
            aria-invalid={!!hintFor("max_concurrent_runners")} />
          {#if hintFor("max_concurrent_runners")}<span class="field-warn">{hintFor("max_concurrent_runners")}</span>{/if}
        </div>
      </div>
    </div>

    <div class="field-row">
      <div class="field">
        <label for="scope">
          Scope
          <span class="muted">
            ({form.scope === "org"
              ? "route by repository.owner.login. Shared across the org / runner group"
              : "bind individual repos. Runners narrow to <owner>-<repo>"})
          </span>
        </label>
        <div>
          <select id="scope" class="select" bind:value={form.scope}
            onchange={() => clearFieldError("scope")}
            aria-invalid={!!hintFor("scope")}>
            <option value="repo">repo</option>
            <option value="org">org</option>
          </select>
          {#if hintFor("scope")}<span class="field-warn">{hintFor("scope")}</span>{/if}
        </div>
      </div>
      {#if form.scope === "org"}
        <div class="field">
          <label for="org">Org login <span class="muted">(GitHub org name. case-insensitive match against repository.owner.login)</span></label>
          <div>
            <input id="org" class="input mono" bind:value={form.org_name} placeholder="acme-inc" required
              oninput={() => clearFieldError("org_name")}
              maxlength={ORG_NAME_MAX}
              aria-invalid={!!hintFor("org_name")} />
            {#if hintFor("org_name")}<span class="field-warn">{hintFor("org_name")}</span>{/if}
          </div>
        </div>
      {/if}
    </div>

    {#if form.scope === "org"}
      <div class="field">
        <label for="rg">Runner group id <span class="muted">(0 = GitHub's "Default" group, id 1. Look up org-specific groups via <code>GET /orgs/&lt. Org&gt. /actions/runner-groups</code>)</span></label>
        <input id="rg" class="input" type="number" min="0" bind:value={form.runner_group_id}
          oninput={() => clearFieldError("runner_group_id")}
          aria-invalid={!!hintFor("runner_group_id")} />
        {#if hintFor("runner_group_id")}<span class="field-warn">{hintFor("runner_group_id")}</span>{/if}
      </div>
    {/if}

    <div class="field">
      <label for="tags-prj">
        Tags
        <span class="muted">
          (cascade to every pool's launch template + every spawned instance and EBS volume. Pool tags override on key conflict, repo tags override pool tags. <code>gha:</code> prefix reserved.)
        </span>
      </label>
      <TagsEditor bind:value={form.tags} />
      {#if hintFor("tags")}<span class="field-warn">{hintFor("tags")}</span>{/if}
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
