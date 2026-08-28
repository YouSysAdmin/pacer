<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
    import { onMount } from "svelte";
    import { pools as poolsAPI, projects as projectsAPI } from "$lib/api.js";
    import { page } from "$app/state";
    import TagsEditor from "$lib/TagsEditor.svelte";
    import IdListEditor from "$lib/IdListEditor.svelte";
    import Modal from "$lib/Modal.svelte";
    import { confirmDialog } from "$lib/confirm.svelte.js";

    // Allocation strategies are labelled with the AWS strategy names the
    // Fleet request actually sends. The backend enum value stays the
    // short form (cost / lowest_price / capacity / priority).
    const ALLOC_STRATEGIES = [
        { value: "cost", label: "price-capacity-optimized (default)" },
        { value: "lowest_price", label: "lowest-price" },
        { value: "capacity", label: "capacity-optimized" },
        { value: "priority", label: "prioritized" },
    ];
    const ALLOC_HELP = {
        cost: {
            summary:
                "Cheapest with a capacity signal. AWS skips shallow spot pools even when they are momentarily cheaper, so interruptions stay rare. instance_types order is ignored.",
            onDemand: "lowest-price",
            spot: "price-capacity-optimized",
        },
        lowest_price: {
            summary:
                "Pure cheapest, no capacity signal. Picks the instantaneously cheapest spot pool even when it is shallow and likely to be interrupted. Use only when cost trumps reliability.",
            onDemand: "lowest-price",
            spot: "lowest-price",
        },
        capacity: {
            summary:
                "Deepest spot pool regardless of price. On-demand has no capacity concept and falls back to lowest price.",
            onDemand: "lowest-price",
            spot: "capacity-optimized",
        },
        priority: {
            summary:
                "Honors the instance_types list order. For spot, capacity is still primary and the list order breaks ties.",
            onDemand: "prioritized",
            spot: "capacity-optimized-prioritized",
        },
    };
    import {
        AMI_PATTERN,
        AMI_RE,
        POOL_NAME_PATTERN,
        POOL_NAME_RE,
        POSIX_USER_PATTERN,
        SG_PATTERN,
        SG_RE,
        SUBNET_PATTERN,
        SUBNET_RE,
        fieldErrorsFrom,
        isPosixUser,
        isReservedTagKey,
        notSelfHosted,
        sanitizeLabel,
    } from "$lib/validators.js";

    // Caps mirror domain/pool/endpoint.go::input.
    const NAME_MAX = 128;
    const POOL_RUNNER_USER_MAX = 32;
    const RUNNER_VERSION_MAX = 32;
    const EXTRA_LABEL_MAX = 64;
    const USER_DATA_MAX = 32768;
    const IAM_PROFILE_MAX = 128;
    const INSTANCE_TYPE_MAX = 64;
    // Slice caps -- entries, not characters. Backend rule is
    // `min=1,max=32` on instance_types / subnet_ids / security_group_ids.
    const SLICE_MIN = 1;
    const SLICE_MAX = 32;

    function matchAll(re, arr) {
        return (arr || []).every((s) => re.test(s));
    }

    // Live validity flag for the AMI input.  Empty value reports valid
    // so the warning doesn't flash before the user types anything --
    // `required` on the input still enforces non-empty at submit.
    const amiValid = $derived(!form.ami_id || AMI_RE.test(form.ami_id.trim()));

    // Live validity for the pool name. Empty -> valid (don't flash
    // before the user types). Required attribute catches it at submit.
    const nameValid = $derived(
        !form.name || POOL_NAME_RE.test(form.name.trim()),
    );

    let list = $state([]);
    let projectList = $state([]);
    let loading = $state(false);
    let error = $state(null);
    let success = $state(null);
    let editing = $state(null); // pool being edited, or null
    let copyingFrom = $state(""); // name of source pool when forking. "" otherwise
    let formOpen = $state(false);
    let projectFilter = $state("");
    // fieldErrors holds the per-field map from a backend validator
    // bounce. Live hints below take precedence for fields the client
    // can check itself.
    let fieldErrors = $state({});

    let form = $state(emptyForm());

    function emptyForm() {
        return {
            project_id: "",
            name: "",
            is_default: false,
            priority: 100,
            ami_id: "",
            instance_types: "m6i.large,m5.large",
            subnet_ids: [],
            security_group_ids: [],
            iam_instance_profile: "",
            root_volume_gb: 0,
            max_runtime_minutes: 60,
            max_concurrent_runners: 5,
            spot: true,
            spawn_method: "fleet",
            allocation_strategy: "cost",
            extra_labels: "",
            tags: {},
            runner_version: "",
            runner_user: "",
            user_data_extra: "",
            disabled: false,
        };
    }

    async function refresh() {
        loading = true;
        error = null;
        try {
            const [ps, prjs] = await Promise.all([
                poolsAPI.list(),
                projectsAPI.list(),
            ]);
            list = ps || [];
            projectList = prjs || [];
            if (!form.project_id && projectList.length > 0) {
                form.project_id = projectList[0].id;
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

    function openCreate() {
        if (projectList.length === 0) return;
        editing = null;
        copyingFrom = "";
        form = emptyForm();
        if (projectList.length > 0)
            form.project_id = projectFilter || projectList[0].id;
        error = null;
        success = null;
        fieldErrors = {};
        formOpen = true;
    }

    // startCopy opens the create-pool modal pre-filled with everything
    // from a source pool except identity bits (name) and the default
    // flag (clearing default avoids the partial-unique-index conflict
    // when an operator hits "save" without reading the form). The
    // backend treats this exactly like a brand-new pool: fresh ID,
    // fresh LT, audit row tagged pool.created.
    function startCopy(p) {
        editing = null;
        copyingFrom = p.name;
        fieldErrors = {};
        form = {
            project_id: p.project_id,
            name: "",
            is_default: false,
            priority: p.priority,
            ami_id: p.ami_id,
            instance_types: (p.instance_types || []).join(","),
            subnet_ids: [...(p.subnet_ids || [])],
            security_group_ids: [...(p.security_group_ids || [])],
            iam_instance_profile: p.iam_instance_profile,
            root_volume_gb: p.root_volume_gb,
            max_runtime_minutes: p.max_runtime_minutes,
            max_concurrent_runners: p.max_concurrent_runners,
            spot: p.spot,
            spawn_method: p.spawn_method || "fleet",
            allocation_strategy: p.allocation_strategy || "cost",
            extra_labels: (p.extra_labels || []).join(","),
            tags: { ...(p.tags || {}) },
            runner_version: p.runner_version || "",
            runner_user: p.runner_user || "",
            user_data_extra: p.user_data_extra || "",
            disabled: p.disabled,
        };
        error = null;
        success = null;
        formOpen = true;
    }

    function startEdit(p) {
        editing = p.id;
        fieldErrors = {};
        form = {
            project_id: p.project_id,
            name: p.name,
            is_default: p.is_default,
            priority: p.priority,
            ami_id: p.ami_id,
            instance_types: (p.instance_types || []).join(","),
            subnet_ids: [...(p.subnet_ids || [])],
            security_group_ids: [...(p.security_group_ids || [])],
            iam_instance_profile: p.iam_instance_profile,
            root_volume_gb: p.root_volume_gb,
            max_runtime_minutes: p.max_runtime_minutes,
            max_concurrent_runners: p.max_concurrent_runners,
            spot: p.spot,
            spawn_method: p.spawn_method || "fleet",
            allocation_strategy: p.allocation_strategy || "cost",
            extra_labels: (p.extra_labels || []).join(","),
            tags: { ...(p.tags || {}) },
            runner_version: p.runner_version || "",
            runner_user: p.runner_user || "",
            user_data_extra: p.user_data_extra || "",
            disabled: p.disabled,
        };
        error = null;
        success = null;
        formOpen = true;
    }

    function cancelEdit() {
        editing = null;
        copyingFrom = "";
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

    // parseListPreview splits a comma-separated input the same way
    // buildBody() does, so live extra_labels validation sees the same
    // entries the backend will. Whitespace-only entries collapse.
    function parseListPreview(s) {
        return (s || "")
            .split(",")
            .map((x) => x.trim())
            .filter(Boolean);
    }

    // numericLt0 detects a number-input that's been driven negative.
    // With <input type="number" bind:value=...> Svelte stores the
    // numeric form, but operators can still type "-100" or paste it.
    // The backend's Normalize() silently clamps negatives to 0 (or
    // to the per-field default for max_runtime_minutes /
    // max_concurrent_runners / priority), so without a UI hint the
    // user wouldn't notice the value they set was rewritten.
    function numericLt0(v) {
        const n = Number(v);
        return Number.isFinite(n) && n < 0;
    }

    // Live hints mirror domain/pool/endpoint.go::input rules. The
    // map is keyed by json field name so server-side err.fields
    // overlays cleanly in hintFor(). Messages are written for the
    // operator -- they reference the field's label as it appears in
    // the form, not the json tag.
    let liveHints = $derived(buildHints());
    function buildHints() {
        const h = {};
        if (form.name && form.name.length > NAME_MAX) {
            h.name = `Pool name must be at most ${NAME_MAX} characters`;
        }
        if (form.runner_user && !isPosixUser(form.runner_user)) {
            h.runner_user = "Run runner as must use only lowercase letters, digits, underscore, or dash, and not start with a digit or dash";
        }
        if (form.runner_user && form.runner_user.length > POOL_RUNNER_USER_MAX) {
            h.runner_user = `Run runner as must be at most ${POOL_RUNNER_USER_MAX} characters`;
        }
        if (form.runner_version && form.runner_version.length > RUNNER_VERSION_MAX) {
            h.runner_version = `Runner version must be at most ${RUNNER_VERSION_MAX} characters`;
        }
        if (form.user_data_extra && form.user_data_extra.length > USER_DATA_MAX) {
            h.user_data_extra = `Extra user-data is too large (${form.user_data_extra.length} characters; limit is ${(USER_DATA_MAX / 1024).toFixed(0)} KiB)`;
        }
        if (numericLt0(form.priority)) {
            h.priority = "Priority must be 0 or greater";
        }
        if (numericLt0(form.root_volume_gb)) {
            h.root_volume_gb = "Root volume GB must be 0 or greater (0 keeps the AMI's native size)";
        }
        if (numericLt0(form.max_runtime_minutes)) {
            h.max_runtime_minutes = "Max runtime must be 0 minutes or greater";
        }
        if (numericLt0(form.max_concurrent_runners)) {
            h.max_concurrent_runners = "Max concurrent runners must be 0 or greater";
        }
        // Comma-separated instance_types: at least 1, at most 32,
        // each entry 1..64 chars. Mirrors the backend
        // required,min=1,max=32,dive,min=1,max=64 rule.
        const types = parseListPreview(form.instance_types);
        if (types.length === 0) {
            h.instance_types = "Add at least one instance type";
        } else if (types.length > SLICE_MAX) {
            h.instance_types = `Too many instance types (${types.length}); the limit is ${SLICE_MAX}`;
        } else {
            for (const t of types) {
                if (t.length > INSTANCE_TYPE_MAX) {
                    h.instance_types = `Each instance type must be at most ${INSTANCE_TYPE_MAX} characters ("${t}" is ${t.length})`;
                    break;
                }
            }
        }
        // Slice caps for subnets / SGs. Pattern validation per entry
        // already lives on IdListEditor. Here we surface the count
        // bounds (0 entries = required-missing. >32 = past backend cap).
        if (!form.subnet_ids || form.subnet_ids.length === 0) {
            h.subnet_ids = "Add at least one subnet ID";
        } else if (form.subnet_ids.length > SLICE_MAX) {
            h.subnet_ids = `Too many subnet IDs (${form.subnet_ids.length}); the limit is ${SLICE_MAX}`;
        }
        if (!form.security_group_ids || form.security_group_ids.length === 0) {
            h.security_group_ids = "Add at least one security group ID";
        } else if (form.security_group_ids.length > SLICE_MAX) {
            h.security_group_ids = `Too many security group IDs (${form.security_group_ids.length}); the limit is ${SLICE_MAX}`;
        }
        if (form.iam_instance_profile && form.iam_instance_profile.length > IAM_PROFILE_MAX) {
            h.iam_instance_profile = `IAM instance profile name must be at most ${IAM_PROFILE_MAX} characters`;
        }
        // Extra labels are surfaced as a comma list. Mirror the backend
        // dive,...,gha_safe,runner_label,not_self_hosted rules.
        const labels = parseListPreview(form.extra_labels);
        for (const l of labels) {
            if (isReservedTagKey(l)) {
                h.extra_labels = `Extra runner labels must not start with "gha:" (that prefix is reserved)`;
                break;
            }
            if (!notSelfHosted(l)) {
                h.extra_labels = `Remove "self-hosted" from extra runner labels -- it's added automatically`;
                break;
            }
            if (sanitizeLabel(l) === "") {
                h.extra_labels = `"${l}" has no letters, digits, or underscores -- pick a label with at least one`;
                break;
            }
            if (l.length > EXTRA_LABEL_MAX) {
                h.extra_labels = `Each extra runner label must be at most ${EXTRA_LABEL_MAX} characters`;
                break;
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

    function hintFor(name) {
        return liveHints[name] || fieldErrors[name] || "";
    }

    // runsOnFor builds the YAML flow-style array a workflow can paste
    // under `runs-on:`. The repo-specific `<owner>-<repo>` label is
    // omitted intentionally - it's stamped per-spawn and would tie
    // the workflow to a single repo, defeating cross-repo reuse.
    function runsOnFor(p) {
        const labels = ["self-hosted"];
        const seen = new Set(["self-hosted"]);
        const add = (s) => {
            const x = sanitizeLabel(s);
            if (x && !seen.has(x)) {
                seen.add(x);
                labels.push(x);
            }
        };
        add(projectName(p.project_id));
        add(p.name);
        for (const e of p.extra_labels || []) add(e);
        return "[" + labels.join(", ") + "]";
    }

    async function copyRunsOn(p) {
        const s = runsOnFor(p);
        error = null;
        try {
            await navigator.clipboard.writeText(s);
            success = `copied runs-on: ${s}`;
            return;
        } catch {
            // Fallback path for browsers / contexts where the async
            // Clipboard API is gated (insecure context, missing perm).
            const ta = document.createElement("textarea");
            ta.value = s;
            ta.setAttribute("readonly", "");
            ta.style.position = "absolute";
            ta.style.left = "-9999px";
            document.body.appendChild(ta);
            ta.select();
            try {
                document.execCommand("copy");
                success = `copied runs-on: ${s}`;
            } catch {
                error = "clipboard write failed; copy manually: " + s;
            }
            ta.remove();
        }
    }

    function buildBody() {
        const parseList = (s) =>
            s
                .split(",")
                .map((x) => x.trim())
                .filter(Boolean);
        return {
            name: form.name.trim(),
            is_default: !!form.is_default,
            priority: Number(form.priority) || 100,
            ami_id: form.ami_id.trim(),
            instance_types: parseList(form.instance_types),
            subnet_ids: form.subnet_ids || [],
            security_group_ids: form.security_group_ids || [],
            iam_instance_profile: form.iam_instance_profile.trim(),
            root_volume_gb: Number(form.root_volume_gb) || 0,
            max_runtime_minutes: Number(form.max_runtime_minutes) || 60,
            max_concurrent_runners: Number(form.max_concurrent_runners) || 5,
            spot: !!form.spot,
            spawn_method: form.spawn_method || "fleet",
            // allocation_strategy is Fleet-only. Force 'cost' on
            // run_instances so a stale value (left over from toggling
            // away from Fleet) never reaches the validator.
            allocation_strategy:
                (form.spawn_method || "fleet") === "fleet"
                    ? form.allocation_strategy || "cost"
                    : "cost",
            extra_labels: parseList(form.extra_labels || ""),
            tags: form.tags || {},
            runner_version: (form.runner_version || "").trim(),
            runner_user: (form.runner_user || "").trim(),
            user_data_extra: form.user_data_extra,
            disabled: !!form.disabled,
        };
    }

    // Submit-time guard: the per-input pattern attributes already
    // block obviously malformed entries, but they don't trigger on
    // empty arrays.  Catch "no entries" and "list with bad row" here
    // so we can surface a clear banner instead of a server 400.
    function validate(body) {
        if (!POOL_NAME_RE.test(body.name)) {
            return "Pool name must be lowercase alphanumeric / underscore / dash, no leading or trailing dash";
        }
        if (!AMI_RE.test(body.ami_id)) {
            return `AMI ID must match ${AMI_PATTERN} (e.g. ami-0abcdef0123456789)`;
        }
        if (body.subnet_ids.length === 0)
            return "At least one subnet ID is required";
        if (!matchAll(SUBNET_RE, body.subnet_ids)) {
            return `Subnet IDs must match ${SUBNET_PATTERN}`;
        }
        if (body.security_group_ids.length === 0)
            return "At least one security group ID is required";
        if (!matchAll(SG_RE, body.security_group_ids)) {
            return `Security group IDs must match ${SG_PATTERN}`;
        }
        if (body.instance_types.length === 0)
            return "At least one instance type is required";
        return null;
    }

    async function submit(e) {
        e.preventDefault();
        error = null;
        success = null;
        fieldErrors = {};
        // Live hints cover the per-field rules. Block submit if any
        // are currently flagged so the user sees the inline message
        // instead of a server 400.
        const hints = buildHints();
        if (Object.keys(hints).length > 0) {
            error = "Please fix the highlighted fields";
            return;
        }
        const body = buildBody();
        const v = validate(body);
        if (v) {
            error = v;
            return;
        }
        try {
            if (editing) {
                await poolsAPI.update(editing, body);
                success = `updated ${body.name}`;
            } else {
                await poolsAPI.create(form.project_id, body);
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
            title: "Delete pool?",
            message: `Permanently delete pool "${p.name}" from ${projectName(p.project_id)}? Active jobs must already be drained; the EC2 launch template will be best-effort cleaned up.`,
            confirmLabel: "delete",
            confirmDanger: true,
        });
        if (!ok) return;
        error = null;
        success = null;
        try {
            await poolsAPI.delete(p.id);
            success = `deleted ${p.name}`;
            await refresh();
        } catch (e) {
            error = e.message;
        }
    }

    function visible() {
        if (!projectFilter) return list;
        return list.filter((p) => p.project_id === projectFilter);
    }

    // Mount-only setup -- read ?project=<id> from the URL so a
    // /projects link can scope the pool list, then load. Plain
    // $effect(() => refresh()) here re-fired on every form-field
    // edit and on every list mutation, doubling API calls.
    onMount(() => {
        const q = page.url.searchParams.get("project");
        if (q) projectFilter = q;
        refresh();
    });
</script>

<main>
    <div class="page-header">
        <h2>Pools</h2>
        <div class="row-actions">
            <select class="select" bind:value={projectFilter}>
                <option value="">all projects</option>
                {#each projectList as p (p.id)}
                    <option value={p.id}>{p.name}</option>
                {/each}
            </select>
            <button
                class="btn primary"
                onclick={openCreate}
                disabled={projectList.length === 0}>+ new pool</button
            >
            <button class="btn" onclick={refresh} disabled={loading}
                >refresh</button
            >
        </div>
    </div>

    {#if error && !formOpen}<div class="banner err">{error}</div>{/if}
    {#if success}<div class="banner ok">{success}</div>{/if}

    {#if list.length === 0}
        <div class="empty">
            <pre class="ascii">   .--------------.
   |  ::  ::  :: |
   |             |
   '--------------'</pre>
            <h3>No pools yet</h3>
            {#if projectList.length === 0}
                <p>
                    Pools own the EC2 launch shape (AMI, instance types,
                    subnets, IAM profile). You need a <a href="/projects"
                        >project</a
                    > before you can create one.
                </p>
                <div class="actions">
                    <a class="btn primary" href="/projects">+ new project</a>
                </div>
            {:else}
                <p>
                    Pools own the EC2 launch shape (AMI, instance types,
                    subnets, IAM profile). Each project needs at least one to
                    spawn runners.
                </p>
                <div class="actions">
                    <button class="btn primary" onclick={openCreate}
                        >+ new pool</button
                    >
                </div>
            {/if}
        </div>
    {:else if visible().length === 0}
        <div class="empty">
            <pre class="ascii">      .  .  .
    .    ?    .
      .  .  .</pre>
            <h3>No pools match this project</h3>
            <p>
                Nothing in <strong>{projectName(projectFilter)}</strong>. Clear
                the filter to see every pool.
            </p>
            <div class="actions">
                <button class="btn" onclick={() => (projectFilter = "")}
                    >clear filter</button
                >
                <button class="btn primary" onclick={openCreate}
                    >+ new pool</button
                >
            </div>
        </div>
    {:else}
        <table class="tbl tbl-stack">
            <thead>
                <tr>
                    <th>Project</th>
                    <th>Pool</th>
                    <th>AMI</th>
                    <th>Instance types</th>
                    <th>Cap</th>
                    <th>LT</th>
                    <th></th>
                </tr>
            </thead>
            <tbody>
                {#each visible() as p (p.id)}
                    <tr>
                        <td data-label="Project">{projectName(p.project_id)}</td>
                        <td data-label="Pool">
                            <strong>{p.name}</strong>
                            {#if p.is_default}<span class="tag info"
                                    >default</span
                                >{/if}
                            {#if p.disabled}<span class="tag warn"
                                    >disabled</span
                                >{/if}
                        </td>
                        <td class="mono" data-label="AMI">{p.ami_id}</td>
                        <td class="mono" data-label="Instance types"
                            >{(p.instance_types || []).join(", ")}</td
                        >
                        <td data-label="Cap">{p.max_concurrent_runners}</td>
                        <td class="mono" data-label="LT">
                            {#if p.launch_template_id}
                                {p.launch_template_id}<span class="muted">
                                    v{p.launch_template_version}</span
                                >
                            {:else}
                                <span class="muted">&mdash;</span>
                            {/if}
                        </td>
                        <td>
                            <div class="row-actions">
                                <button
                                    class="btn xs"
                                    title="copy runs-on labels for a workflow YAML"
                                    onclick={() => copyRunsOn(p)}
                                    >runs-on</button
                                >
                                <button
                                    class="btn xs"
                                    title="open the new-pool form pre-filled from this pool"
                                    onclick={() => startCopy(p)}>copy</button
                                >
                                <button
                                    class="btn xs"
                                    onclick={() => startEdit(p)}>edit</button
                                >
                                <button
                                    class="btn xs danger"
                                    onclick={() => remove(p)}>delete</button
                                >
                            </div>
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</main>

<Modal bind:open={formOpen} title={editing ? "Edit pool" : copyingFrom ? `New pool (copied from ${copyingFrom})` : "New pool"}>
    {#if error}<div class="banner err">{error}</div>{/if}
    <form onsubmit={submit}>
        <div class="field-row">
            <div class="field">
                <label for="proj">Project</label>
                <select
                    id="proj"
                    class="select"
                    bind:value={form.project_id}
                    disabled={!!editing}
                    required
                >
                    {#each projectList as p (p.id)}
                        <option value={p.id}>{p.name}</option>
                    {/each}
                </select>
            </div>
            <div class="field">
                <label for="name"
                    >Pool name
                    <br />
                    <span class="muted"
                        >Used as a runner label - lowercase, digits, underscore,
                        or dash, no leading / trailing dash</span
                    ></label
                >
                <div>
                    <input
                        id="name"
                        class="input"
                        bind:value={form.name}
                        placeholder="large, medium, arm"
                        pattern={POOL_NAME_PATTERN}
                        title="lowercase alphanumeric, underscore, or dash; not starting or ending with a dash"
                        required
                        maxlength={NAME_MAX}
                        oninput={() => clearFieldError("name")}
                        aria-invalid={!nameValid || !!hintFor("name")}
                    />
                    {#if !nameValid}
                        <span class="field-warn">
                            lowercase alphanumeric / underscore / dash, no
                            leading or trailing dash
                        </span>
                    {:else if hintFor("name")}
                        <span class="field-warn">{hintFor("name")}</span>
                    {/if}
                    {#if editing}
                        <span class="muted name-hint">
                            Renaming changes the runner label this pool
                            registers under - update every workflow's
                            <code>runs-on:</code> in lock-step or jobs will stop matching
                            this pool.
                        </span>
                    {/if}
                </div>
            </div>
        </div>

        <div class="field">
            <label class="chk">
                <input type="checkbox" bind:checked={form.disabled} />
                Disabled
            </label>
            <br />
            <span class="muted">
                Pool stops claiming new jobs. Existing instances keep running until they finish or hit max runtime.
            </span>
        </div>

        <div class="field-row">
            <div class="field">
                <label class="chk">
                    <input type="checkbox" bind:checked={form.is_default} />
                    This is the default pool
                </label>
                <br />
                <span class="muted">
                    Catches workflows that don't name a specific pool
                </span>
            </div>

            <div class="field">
                <label for="prio"
                    >Priority <span class="muted"
                        >(lower = preferred when multiple match)</span
                    ></label
                >
                <div>
                    <input
                        id="prio"
                        class="input"
                        type="number"
                        min="0"
                        bind:value={form.priority}
                        oninput={() => clearFieldError("priority")}
                        aria-invalid={!!hintFor("priority")}
                    />
                    {#if hintFor("priority")}
                        <span class="field-warn">{hintFor("priority")}</span>
                    {/if}
                </div>
            </div>
        </div>

        <div class="field">
            <label for="ami"
                >AMI ID <span class="muted"
                    >(<code>ami-</code> + 8-17 hex chars)</span
                ></label
            >
            <input
                id="ami"
                class="input mono"
                bind:value={form.ami_id}
                pattern={AMI_PATTERN}
                placeholder="ami-0abcdef0123456789"
                title="AMI ID must match ami- followed by 8-17 hex characters"
                required
                oninput={() => clearFieldError("ami_id")}
                aria-invalid={!amiValid || !!hintFor("ami_id")}
            />
            {#if !amiValid}
                <span class="field-warn"
                    >expected <code>ami-xxxxxxxx</code> (8-17 hex chars)</span
                >
            {:else if hintFor("ami_id")}
                <span class="field-warn">{hintFor("ami_id")}</span>
            {/if}
        </div>

        <div class="field">
            <label for="types"
                >Instance types <span class="muted"
                    >(comma-separated, priority order for spot fallback)</span
                ></label
            >
            <input
                id="types"
                class="input"
                bind:value={form.instance_types}
                required
                oninput={() => clearFieldError("instance_types")}
                aria-invalid={!!hintFor("instance_types")}
            />
            {#if hintFor("instance_types")}
                <span class="field-warn">{hintFor("instance_types")}</span>
            {/if}
        </div>

        <div class="field-row">
            <div class="field">
                <label for="subnets-anchor"
                    >Subnet IDs <span class="muted"
                        >(<code>subnet-</code> + 8-17 hex chars)</span
                    ></label
                >
                <div>
                    <span id="subnets-anchor"></span>
                    <IdListEditor
                        bind:value={form.subnet_ids}
                        prefix="subnet-"
                        placeholder="subnet-0abcdef0123456789"
                        addLabel="+ add subnet"
                    />
                    {#if hintFor("subnet_ids")}
                        <span class="field-warn">{hintFor("subnet_ids")}</span>
                    {/if}
                </div>
            </div>
            <div class="field">
                <label for="sgs-anchor"
                    >Security group IDs <span class="muted"
                        >(<code>sg-</code> + 8-17 hex chars)</span
                    ></label
                >
                <div>
                    <span id="sgs-anchor"></span>
                    <IdListEditor
                        bind:value={form.security_group_ids}
                        prefix="sg-"
                        placeholder="sg-0abcdef0123456789"
                        addLabel="+ add security group"
                    />
                    {#if hintFor("security_group_ids")}
                        <span class="field-warn">{hintFor("security_group_ids")}</span>
                    {/if}
                </div>
            </div>
        </div>

        <div class="field">
            <label for="iam"
                >IAM instance profile name <span class="muted"
                    >(optional, leave blank if the runner host doesn't need AWS
                    API access)</span
                ></label
            >
            <input
                id="iam"
                class="input"
                bind:value={form.iam_instance_profile}
                placeholder="(none)"
                maxlength={IAM_PROFILE_MAX}
                oninput={() => clearFieldError("iam_instance_profile")}
                aria-invalid={!!hintFor("iam_instance_profile")}
            />
            {#if hintFor("iam_instance_profile")}
                <span class="field-warn">{hintFor("iam_instance_profile")}</span>
            {/if}
        </div>

        <div class="field-row">
            <div class="field">
                <label for="vol"
                    >Root volume GB
                    <br />
                    <span class="muted">
                        0 = inherit the AMI's native size
                        <br />
                        any positive value must be &gt;= AMI size
                    </span>
                </label>
                <div>
                    <input
                        id="vol"
                        class="input"
                        type="number"
                        min="0"
                        bind:value={form.root_volume_gb}
                        placeholder="0"
                        oninput={() => clearFieldError("root_volume_gb")}
                        aria-invalid={!!hintFor("root_volume_gb")}
                    />
                    {#if hintFor("root_volume_gb")}
                        <span class="field-warn">{hintFor("root_volume_gb")}</span>
                    {/if}
                </div>
            </div>
            <div class="field">
                <label for="rt">
                    Max runtime (min)
                    <br />
                    <span class="muted">
                        Maximum time an instance can run before an instance is
                        forced to terminate.
                    </span>
                </label>
                <div>
                    <input
                        id="rt"
                        class="input"
                        type="number"
                        min="0"
                        bind:value={form.max_runtime_minutes}
                        oninput={() => clearFieldError("max_runtime_minutes")}
                        aria-invalid={!!hintFor("max_runtime_minutes")}
                    />
                    {#if hintFor("max_runtime_minutes")}
                        <span class="field-warn">{hintFor("max_runtime_minutes")}</span>
                    {/if}
                </div>
            </div>
        </div>

        <div class="field">
            <label for="conc">Max concurrent runners</label>
            <input
                id="conc"
                class="input"
                type="number"
                min="0"
                bind:value={form.max_concurrent_runners}
                oninput={() => clearFieldError("max_concurrent_runners")}
                aria-invalid={!!hintFor("max_concurrent_runners")}
            />
            {#if hintFor("max_concurrent_runners")}
                <span class="field-warn">{hintFor("max_concurrent_runners")}</span>
            {/if}
        </div>

        <div class="field">
            <label>Spawn method</label>
            <div class="method-toggle">
                <div
                    class="method-card"
                    class:sel={form.spawn_method === "fleet"}
                    role="radio"
                    tabindex="0"
                    aria-checked={form.spawn_method === "fleet"}
                    onclick={() => (form.spawn_method = "fleet")}
                    onkeydown={(e) => {
                        if (e.key === " " || e.key === "Enter") {
                            e.preventDefault();
                            form.spawn_method = "fleet";
                        }
                    }}
                >
                    <div class="n">Fleet</div>
                    <div class="d">
                        CreateFleet, multi-type + multi-AZ. AWS picks an
                        available (instance_type x subnet) combo using your
                        allocation strategy.
                    </div>
                    <div class="rec">RECOMMENDED</div>
                </div>
                <div
                    class="method-card"
                    class:sel={form.spawn_method === "run_instances"}
                    role="radio"
                    tabindex="0"
                    aria-checked={form.spawn_method === "run_instances"}
                    onclick={() => (form.spawn_method = "run_instances")}
                    onkeydown={(e) => {
                        if (e.key === " " || e.key === "Enter") {
                            e.preventDefault();
                            form.spawn_method = "run_instances";
                        }
                    }}
                >
                    <div class="n">RunInstances</div>
                    <div class="d">
                        Serial loop, single instance type per call, first
                        subnet only. Legacy path kept for parity with older
                        deployments.
                    </div>
                </div>
            </div>
        </div>

        <div class="field">
            <label>Market</label>
            <div class="method-toggle">
                <div
                    class="method-card"
                    class:sel={form.spot}
                    role="radio"
                    tabindex="0"
                    aria-checked={form.spot}
                    onclick={() => (form.spot = true)}
                    onkeydown={(e) => {
                        if (e.key === " " || e.key === "Enter") {
                            e.preventDefault();
                            form.spot = true;
                        }
                    }}
                >
                    <div class="n">Spot</div>
                    <div class="d">
                        Cheaper, interruptible. AWS guarantees price will not
                        exceed on-demand. Right for ephemeral CI runners.
                    </div>
                    <div class="rec">RECOMMENDED</div>
                </div>
                <div
                    class="method-card"
                    class:sel={!form.spot}
                    role="radio"
                    tabindex="0"
                    aria-checked={!form.spot}
                    onclick={() => (form.spot = false)}
                    onkeydown={(e) => {
                        if (e.key === " " || e.key === "Enter") {
                            e.preventDefault();
                            form.spot = false;
                        }
                    }}
                >
                    <div class="n">On-demand</div>
                    <div class="d">
                        Stable, full price. Pick when interruption mid-job
                        would break the workflow.
                    </div>
                </div>
            </div>
        </div>

        {#if form.spawn_method === "fleet"}
            <div class="field">
                <label for="alloc">
                    Allocation strategy
                    <span class="muted">
                        (Fleet picks among instance_type x subnet combos)
                    </span>
                </label>
                <select
                    id="alloc"
                    class="select"
                    bind:value={form.allocation_strategy}
                >
                    {#each ALLOC_STRATEGIES as s (s.value)}
                        <option value={s.value}>{s.label}</option>
                    {/each}
                </select>
                {#if ALLOC_HELP[form.allocation_strategy]}
                    <span class="muted alloc-help">
                        {ALLOC_HELP[form.allocation_strategy].summary}
                        <br />
                        On-demand: <code>{ALLOC_HELP[form.allocation_strategy].onDemand}</code>.
                        Spot: <code>{ALLOC_HELP[form.allocation_strategy].spot}</code>.
                    </span>
                {/if}
            </div>
        {/if}

        <div class="field">
            <label for="extra-labels">
                Extra runner labels
                <br />
                <span class="muted">
                    Comma-separated, appended to the auto-derived <code
                        >self-hosted, &lt;project&gt;, &lt;pool&gt;,
                        &lt;owner&gt;-&lt;repo&gt;</code
                    >.
                    <br />
                    Use for capability tags like <code>gpu</code>,
                    <code>arm64</code>, <code>large</code>. Sanitized to
                    GitHub's charset. The <code>gha:</code> prefix is reserved.
                </span>
            </label>
            <input
                id="extra-labels"
                class="input mono"
                bind:value={form.extra_labels}
                placeholder="gpu,arm64"
                oninput={() => clearFieldError("extra_labels")}
                aria-invalid={!!hintFor("extra_labels")}
            />
            {#if hintFor("extra_labels")}
                <span class="field-warn">{hintFor("extra_labels")}</span>
            {/if}
        </div>

        <div class="field">
            <label for="tags-pool">
                Tags
                <br />
                <span class="muted">
                    Applied to the launch template, every spawned instance, and
                    its EBS volumes.
                    <br />
                    Pool tags override project tags, repo tags override pool tags.
                    <code>gha:</code> prefix reserved.
                </span>
            </label>
            <TagsEditor bind:value={form.tags} />
            {#if hintFor("tags")}
                <span class="field-warn">{hintFor("tags")}</span>
            {/if}
        </div>

        <div class="field">
            <label for="rv">
                Runner version
                <br />
                <span class="muted">
                    Actions/runner release. Leave blank for server-resolved
                    latest, e.g. <code>2.319.1</code>)
                </span>
            </label>
            <input
                id="rv"
                class="input mono"
                bind:value={form.runner_version}
                placeholder="(latest)"
                maxlength={RUNNER_VERSION_MAX}
                oninput={() => clearFieldError("runner_version")}
                aria-invalid={!!hintFor("runner_version")}
            />
            {#if hintFor("runner_version")}
                <span class="field-warn">{hintFor("runner_version")}</span>
            {/if}
        </div>

        <div class="field">
            <label for="ru"
                >Run runner as <span class="muted">
                    <br />
                    OS user on the spawned instance, leave blank to run as root with
                    <code>RUNNER_ALLOW_RUNASROOT=1</code><br /> set to
                    <code>admin</code>
                    / <code>ec2-user</code> / <code>ubuntu</code> when the AMI installs
                    CI tooling per-user.</span
                ></label
            >
            <input
                id="ru"
                class="input mono"
                bind:value={form.runner_user}
                placeholder="(root)"
                pattern={POSIX_USER_PATTERN}
                maxlength={POOL_RUNNER_USER_MAX}
                oninput={() => clearFieldError("runner_user")}
                aria-invalid={!!hintFor("runner_user")}
            />
            {#if hintFor("runner_user")}
                <span class="field-warn">{hintFor("runner_user")}</span>
            {/if}
        </div>

        <div class="field">
            <label for="ude">
                Extra user-data
                <br />
                <span class="muted"> Appended after the runner shutdown </span>
            </label>
            <textarea id="ude" bind:value={form.user_data_extra}
                oninput={() => clearFieldError("user_data_extra")}
                aria-invalid={!!hintFor("user_data_extra")}></textarea>
            {#if hintFor("user_data_extra")}
                <span class="field-warn">{hintFor("user_data_extra")}</span>
            {/if}
        </div>

        <div class="row-actions">
            <button class="btn primary" type="submit"
                >{editing ? "save" : "create"}</button
            >
            <button class="btn" type="button" onclick={cancelEdit}
                >cancel</button
            >
        </div>
    </form>
</Modal>

<style>
    /* Hint text under the pool-name input. The wrapping div keeps the
       parent .field's 2-row subgrid layout intact (label + one
       input-cell). display:block + margin-top puts the hint on its
       own line under the input rather than tucking inline. */
    .name-hint {
        display: block;
        margin-top: 6px;
        font-size: 12px;
        line-height: 1.4;
    }
    .alloc-help {
        display: block;
        margin-top: 6px;
        font-size: 12px;
        line-height: 1.5;
    }
</style>
