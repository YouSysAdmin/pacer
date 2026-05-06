---
title: "Description"
description: "What Pacer is and how the request flows from a workflow_job webhook to a terminated EC2 instance."
weight: 10
---

## What it is

**Pacer** is a single Go binary that turns GitHub Actions `workflow_job` webhooks into short-lived EC2 instances (
on-demand or spot, per pool). Each instance:

1. Boots from an AMI you control.
2. Downloads the `actions/runner` binary from GitHub releases (or uses a pre-baked one).
3. Calls back to Pacer to pick up its one-time runner configuration.
4. Registers itself as an ephemeral GitHub Actions runner, claims exactly one job, and runs it.
5. Calls `shutdown -h` when the job is done.

The launch template on the EC2 side has `InstanceInitiatedShutdownBehavior=terminate`, so `shutdown` becomes a real
instance termination — no orphaned EBS volumes. Anything that overstays its pool's max runtime is terminated
automatically.

**Minimal AWS surface** — Pacer talks to EC2, IAM, and the Pricing API; the job queue, scheduler, and state all live
inside the binary alongside the operator console. The trade you make: one process, one host. No automatic horizontal
scale, no managed durability. In return: one binary to deploy, one place to read logs, one SQLite file to back up.

## Who it is for

- Teams running GitHub Actions self-hosted runners on AWS who want simple, predictable infrastructure with **on-demand
  or spot** EC2 economics.
- Single-operator setups where one bootstrap user (HS256 JWT cookie auth) is enough — or even no auth at all on a
  private network (`auth.disabled: true`).
- Projects that need **multiple runner shapes** (e.g. large x86, ARM Graviton, GPU) selectable per workflow via
  `runs-on` labels.

## What it is not

- **Not multi-region.** A single deployment runs in one AWS region (configured in YAML).
- **Not multi-user.** Single-operator: one bootstrap user, HS256 JWT cookie auth. OIDC and roles are deferred to a later
  release.
- **Not a swap for github-hosted runners** when your workloads fit the free quota — it's only worth it once you outgrow
  them or need custom AMIs / private VPC / spot pricing.
- **Not a workflow_run / push / installation event handler.** Subscribes to `workflow_job` only.
- **Not org-scoped today.** Repo-level JIT registration only; org runners + runner groups are on the roadmap.

## Pipeline

```
GitHub workflow_job:queued
         │
         ▼
[ webhook ]──▶ pool match ──▶ job queued
                                 │
                                 ▼
                         [ orchestrator ]──▶ EC2 instance launches
                                                       │
                                                       ▼
                                       runner picks up its one-time config
                                                       │
                                                       ▼
                                              runner runs the job
                                                       │
                                                       ▼
                                       runner reports completion + shuts down
                                 [ reaper ]
                          (terminates anything past its pool's max runtime)
```

How a job moves through:

- **Webhook** — when a `workflow_job` event arrives from GitHub, Pacer matches the workflow's `runs-on` labels to a
  pool and queues the job.
- **Orchestrator** — queued jobs are picked up and EC2 instances launch. Each pool decides how, via its `spawn_method`:
  - **`fleet`** (default) — Pacer asks AWS to launch from any of the pool's instance types in any of its subnets, and
    AWS picks an available combination. Multi-AZ "just works" as long as the pool's subnets span AZs. Spot price never
    exceeds on-demand (AWS guarantees this), so the worst case is paying on-demand rates briefly.
  - **`run_instances`** (opt-in) — tries the pool's instance types one by one against the first subnet only. No
    multi-AZ. Kept for operators who specifically need it.
- **Allocation strategy** (Fleet only) — per-pool `allocation_strategy` picks how Fleet decides:
  - **`cost`** (default) — AWS picks the cheapest capacity-safe combination; the order in which you list
    `instance_types` doesn't matter.
  - **`priority`** — honors `instance_types` list order: first item is preferred, second is fallback, etc. For spot,
    capacity is still the first concern (priority is a tiebreaker) so you avoid high-interruption pools.
- **Capacity-aware retry** — if AWS reports no capacity for any instance type / subnet you listed, the job is
  **rescheduled** with a 30s/60s/120s/240s/300s-capped backoff (12 attempts, ~50 minutes). Permanent errors (bad AMI,
  missing IAM role) still fail immediately with a clear message.
- **Reaper** — anything past its pool's `max_runtime_minutes` is terminated.

If the bootstrap script on a runner fails, its log is captured and shown in the Jobs UI's per-row **details modal**
alongside the rest of the job context (timeline, instance details, parsed webhook payload, audit trail) so failures
don't disappear with the terminated host.

## Routing model

Each project picks one of two scopes:

- **`repo`** (default) — 1..N repos bind to the project. Webhooks route via the repo binding (
  `repository.full_name -> project`). Runners carry an `<owner>-<repo>` narrowing label so they only claim jobs from the
  bound repo (no cross-repo poaching).
- **`org`** — webhooks route by `repository.owner.login` (one project per GitHub org). No per-repo bindings; JIT config
  registers against `/orgs/<org>/actions/runners/generate-jitconfig` with the project's `runner_group_id` (0 = "
  Default", id 1). The `<owner>-<repo>` narrowing label is dropped so the runners are shared across every repo in the
  org / runner group.

Webhook routing tries the per-repo binding first (most specific). When no binding exists, it falls back to an org-scoped
project for `repository.owner.login`. This lets operators run repo-scoped and org-scoped projects side-by-side in the
same org and migrate gradually.

Each project has 1..N **pools**, each pool materializes one EC2 launch template. Pool selection happens per job by
matching the workflow's `runs-on` labels.

Each pool advertises a label set on its runners:

```
repo scope: [self-hosted, <project>, <pool>, <owner>-<repo>] + pool.extra_labels
org scope:  [self-hosted, <project>, <pool>]                 + pool.extra_labels
```

The auto-derived prefix is mandatory; `extra_labels` is an operator-supplied list (per pool) that appends to that set.
Use it for cross-cutting capability tags (`gpu`, `arm64`, `large`, `windows`) that workflows can target via `runs-on`.
Sanitized identically; `gha:` prefix reserved.

Workflow authors target a specific pool with `runs-on`:

```yaml
runs-on: [self-hosted, my-app, large]               # picks the "large" pool
runs-on: [self-hosted, my-app, arm]                 # picks the "arm" pool
runs-on: [self-hosted, my-app]                      # picks the project's default pool
runs-on: [self-hosted, my-app, large, octocat-hello-world]  # narrowest — exact (pool, repo)
runs-on: [self-hosted, my-app, gpu]                 # picks any pool that lists "gpu" in extra_labels
```

Match algorithm:

0. **Pre-filter:** if `runs-on` doesn't include `self-hosted`, the job is silently ignored. Pacer pools always advertise
   `self-hosted` (it's the first auto-derived label), so a workflow without it can't match any pacer pool by definition —
   it targets github-hosted runners. No audit row, no project lookup, just a 200 back to GitHub. Keeps the audit log free
   of `no_pool_match` noise from every `ubuntu-latest` workflow run in a bound repo.
1. Filter to enabled pools whose label set is a superset of the workflow's `runs-on` labels.
2. If any match has its name explicitly in `runs-on` → the lowest-priority such pool wins.
3. Otherwise → the project's `is_default` pool (if among matches).
4. Otherwise → the lowest-priority match.
5. No match → the job is dropped (audited as `job.no_pool_match`).

Labels are case-insensitive and sanitized identically on both sides — `MyApp` and `my-app` are treated as the same
label, `octocat/hello.world` becomes `octocat-hello-world`.

## Tag taxonomy

Four layers; later layers override earlier ones on key conflict. The merge order is **project -> pool -> repo ->
gha:\***.

1. **Project user tags** (`Project.Tags`, cascade, broadest): set once on the project, applied to every pool's LT and
   every instance + volume the project ever spawns. Use for project-wide cost-allocation (`cost_center`,
   `business_unit`).
2. **Pool user tags** (`Pool.Tags`, override): set on the pool. Applied to that pool's LT and every instance + volume.
   Overrides project tags on key conflict.
3. **Repo user tags** (`Repo.Tags`, override, most-specific): set on the repo binding. Stamped at orchestrator spawn
   time on the instance + volume only — **not** on the launch template (one LT serves many repos). Overrides pool tags
   on key conflict.
4. **Tool-managed** (always, last): `gha:managed-by`, `gha:project`, `gha:pool`. Per-spawn the orchestrator additionally
   stamps `gha:job_id` + `gha:repo` on the instance + volume.

The `gha:*` prefix is **reserved** — the API rejects user tags with that prefix at create / update time, and the
orchestrator stamps `gha:*` tags last so any user tag that somehow slipped through cannot shadow them.

> Updating project tags requires re-saving each affected pool to bump the LT version with the new tag shape.
> Newly-spawned instances pick up the merged tags immediately (the orchestrator re-merges per spawn); only the LT itself
> goes stale until the pool is re-saved. Repo tags need no LT churn since they only land at spawn time.

## What's next

- **[Installation](/installation/)** — configure the GitHub App, the AWS IAM role, install and run the binary.
- **[IAM policy builder](/installation/iam-policy-builder/)** — generate the orchestrator's IAM policy with your account
  ID, region, and (optional) runner-instance role substituted in.
