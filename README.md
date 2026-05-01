# Pacer

![](frontend/static/logo/wordmark.svg)

Self-hosted GitHub Actions runner orchestrator for AWS. A single Go binary that receives GitHub `workflow_job` webhooks and spawns short-lived EC2 instances per job (on-demand or spot, per pool), then reaps them when the job finishes.

Minimal AWS surface — only EC2, IAM, and the Pricing API. The agent owns its queue, scheduler, and state.

## Features

- **Single binary, single process.** API + UI + orchestrator + reaper in one Go process; SQLite as the queue.
- **Project → Pools → Launch Template.** A project is a logical grouping; each project has one or more pools, and each pool materializes one EC2 launch template.
- **Per-job pool selection** from `workflow_job.labels[]`, with a default-pool fallback.
- **GitHub App auth + JIT runner registration.** No long-lived runner tokens on instances.
- **On-demand or spot, per pool.** Cost-optimized (`lowest-price` / `price-capacity-optimized`) or priority-based allocation.
- **Capacity-aware retries.** Transient EC2 capacity errors are backed off and retried; permanent errors fail fast.
- **Best-effort cost stamping.** At-launch USD/hour quote via the AWS Pricing API, rolled up into per-job cost on completion.
- **Built-in web UI** (Svelte SPA) for project / pool / repo / job CRUD.
- **Optional console auth.** Local (email + password) or OIDC (Authorization Code + PKCE) with domain / email / group allowlists.
- **TLS modes.** Plain HTTP, operator-supplied PEM, in-memory self-signed, or Let's Encrypt via ACME.

## Routing model

Each pool advertises a label set on its runners:

```
[self-hosted, <project>, <pool>, <owner>-<repo>]
```

Workflow authors target a specific pool with `runs-on`:

```yaml
runs-on: [self-hosted, my-app, large]               # picks the "large" pool
runs-on: [self-hosted, my-app, arm]                 # picks the "arm" pool
runs-on: [self-hosted, my-app]                      # picks the project's default pool
runs-on: [self-hosted, my-app, large, octocat-hello-world]  # narrowest — exact (pool, repo)
```

Match algorithm:

1. Filter to enabled pools whose label set ⊇ workflow's `runs-on` labels.
2. If any match has its name explicitly in `runs-on` → lowest-priority such pool wins.
3. Otherwise → the project's `is_default` pool (if among matches).
4. Otherwise → lowest-priority match.
5. No match → drop the job.

Labels are case-insensitive and sanitized identically on both sides — `MyApp` and `my-app` are treated as the same label.

## Configuration

```yaml
server:
  addr: :3000
  public_url: https://runner.example.com   # spawned instances POST callbacks here
  tls:
    mode: none                             # none | manual | self | acme
aws:
  region: us-east-1                        # single region, all projects
  profile: ""                              # empty = SDK default chain
github:
  app_id: 123456
  private_key_path: /etc/pacer/gh-app.pem
  webhook_secret: <random hex>
  callback_hmac_secret: <random hex>       # signs runner self-reg tokens
database:
  engine: sqlite
  path: /var/lib/pacer/state.db
auth:
  disabled: false
  jwt_secret: <random hex>
  session_ttl: 12h
  local:
    enabled: true
    email: admin@example.com
  oidc:
    enabled: false
logging:
  level: info        # debug | info | warn | error
  format: json       # json | text
  output: stdout     # stdout or absolute file path
```

Every key is overridable via `PACER_*` environment variables (dot becomes underscore — e.g. `PACER_GITHUB_WEBHOOK_SECRET`).

## Installation

Full setup guides live on the docs site:

- **[GitHub App setup](https://yousysadmin.github.io/pacer/installation/github/)** — creating the App, permissions, events, installing on repos.
- **[AWS / IAM setup](https://yousysadmin.github.io/pacer/installation/aws/)** — assumed role policy, tag-scoped permissions, the runner instance profile.
- **[Server setup](https://yousysadmin.github.io/pacer/installation/server/)** — running the binary, TLS, auth, first-run bootstrap.

The IAM policy template is at [`docs/iam-role.json`](docs/iam-role.json). Replace `REPLACE_AWS_REGION`, `REPLACE_ACCOUNT_ID`, and `REPLACE_RUNNER_INSTANCE_ROLE` before applying.
