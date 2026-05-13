<!--
  SPDX-License-Identifier: LicenseRef-DSL-1.0
  Deferred Source License (DSL)
  Pacer, Copyright (c) 2026 YouSysAdmin
-->

<script>
    import "../app.css";
    import { page } from "$app/state";
    import { goto } from "$app/navigation";
    import { base } from "$app/paths";
    import { auth, systemHealth } from "$lib/api.js";
    import ConfirmDialog from "$lib/ConfirmDialog.svelte";

    let { children } = $props();
    let user = $state(null);
    let authDisabled = $state(false);
    // Background-worker issues from GET /api/health. Polled every
    // 30s so a failure (missing IAM perm, panicked reaper) becomes
    // visible without operator action; cleared automatically when
    // the next clean reaper tick clears Health server-side.
    let healthIssues = $state([]);

    // /login renders full-screen with no sidebar.  Detect via path so
    // we don't have to set up a separate route group.
    const isLogin = $derived(page.url.pathname.startsWith("/login"));

    // Two groups: observation and configuration.
    const groups = [
        {
            label: "Control",
            items: [
                { href: "/", label: "Overview" },
                { href: "/jobs", label: "Jobs" },
                { href: "/stats", label: "Stats" },
                { href: "/audit", label: "Audit" },
            ],
        },
        {
            label: "Config",
            items: [
                { href: "/projects", label: "Projects" },
                { href: "/repos", label: "Repos" },
                { href: "/pools", label: "Pools" },
                { href: "/backup", label: "Backup" },
                { href: "/settings", label: "Settings" },
            ],
        },
    ];

    function isActive(href) {
        const path = page.url.pathname;
        if (href === "/")
            return path === "/" || path === "" || path === base + "/";
        return path === href || path.startsWith(href + "/");
    }

    // Resolve the current session.  Three outcomes:
    //   user populated     -- show "logged in as ..."
    //   auth_disabled true -- hide auth UI entirely
    //   401 / error        -- the page-level api calls will redirect
    $effect(() => {
        if (isLogin) return;
        auth.me()
            .then((r) => {
                if (!r) return;
                if (r.auth_disabled) authDisabled = true;
                else if (r.user) user = r.user;
            })
            .catch(() => {});
    });

    // Background-worker health poll. 30s cadence is the sweet spot:
    // fast enough that a freshly-fixed IAM perm or a fresh panic
    // surfaces within one screen-refresh cycle, slow enough that the
    // tab doesn't hammer /api/health while idle. Skip on /login (no
    // session yet) and swallow errors silently -- the api.js wrapper
    // already handles the 401 redirect.
    $effect(() => {
        if (isLogin) return;
        async function tick() {
            try {
                const r = await systemHealth.list();
                healthIssues = (r && r.issues) || [];
            } catch {
                // Don't surface fetch errors as banners -- a flaky
                // poll shouldn't look like a server-side problem.
            }
        }
        tick();
        const t = setInterval(tick, 30000);
        return () => clearInterval(t);
    });

    async function logout() {
        try {
            await auth.logout();
        } catch {}
        user = null;
        goto("/login");
    }
</script>

{#if isLogin}
    {@render children()}
{:else}
    <div class="shell">
        <aside class="sidebar" aria-label="Primary navigation">
            <a href="/" class="brand" aria-label="Pacer">
                <img src="{base}/logo/wordmark.svg" alt="Pacer" />
            </a>

            {#each groups as g (g.label)}
                <nav class="nav-group" aria-label={g.label}>
                    <div class="nav-group-label">{g.label}</div>
                    {#each g.items as item (item.href)}
                        <a
                            href={item.href}
                            class="nav-item"
                            class:active={isActive(item.href)}
                        >
                            {item.label}
                        </a>
                    {/each}
                </nav>
            {/each}

            <!-- External resources. Kept as its own group so the docs
                 link survives the 900px sidebar-foot hide rule and
                 stays reachable on phones. -->
            <nav class="nav-group" aria-label="Help">
                <div class="nav-group-label">Help</div>
                <a
                    href="https://pacer.yousysadmin.com/"
                    class="nav-item"
                    target="_blank"
                    rel="noopener noreferrer"
                >
                    Docs
                </a>
            </nav>

            <div class="sidebar-foot">
                {#if user}
                    <div class="sidebar-user">
                        <div class="user-email" title={user.email}>
                            {user.email}
                        </div>
                        <button class="btn xs" onclick={logout}>logout</button>
                    </div>
                {:else}
                    <span class="mode" title="Server is reachable">
                        <span class="dot"></span>
                        <span
                            >{authDisabled
                                ? "auth disabled"
                                : "connected"}</span
                        >
                    </span>
                {/if}
            </div>
        </aside>

        <div class="shell-main">
            {#if healthIssues.length > 0}
                <div class="banner err health-banner" role="status">
                    {#each healthIssues as i (i.component)}
                        <div><strong>{i.component}:</strong> {i.message}</div>
                    {/each}
                </div>
            {/if}
            {@render children()}
        </div>
    </div>
{/if}

<!-- Single instance: every confirmDialog() call routes through this. -->
<ConfirmDialog />
