---
title: "GitHub side"
description: "Create the GitHub App, set the right permissions and webhook URL, generate a private key, install on repos."
weight: 10
---

The tool authenticates to GitHub as a **GitHub App** — not a personal access token. One App covers all repos you want to
manage runners for; each repo `Install`s the App.

PAT auth is **not** supported: minting JIT runner configs requires an installation token, which only Apps can produce.

## 1. Create the App

- Personal account: <https://github.com/settings/apps/new>
- Organization: `https://github.com/organizations/<org>/settings/apps/new`

Fill in:

| Field                                | Value                                                                       |
|--------------------------------------|-----------------------------------------------------------------------------|
| **GitHub App name**                  | anything unique, e.g. `pacer-<your-org>`                                    |
| **Homepage URL**                     | anything (e.g. your `server.public_url`)                                    |
| **Webhook URL**                      | `<server.public_url>/api/webhook`                                           |
| **Webhook secret**                   | a random hex string — save it, you'll paste it into `github.webhook_secret` |
| **Where can this App be installed?** | *Only on this account* (recommended for self-hosted)                        |

Generate the webhook secret with:

```bash
openssl rand -hex 32
```

Uncheck **Active** under "User authorization (OAuth)" — we don't use OAuth.

## 2. Permissions

Set ONLY the permissions the tool actually uses. The set you need depends on whether you'll run **repo-scoped**
projects (one project per repo binding), **org-scoped** projects (one project per GitHub org), or both.

### Repository permissions (always required)

| Permission         | Access         | Why                                                                                       |
|--------------------|----------------|-------------------------------------------------------------------------------------------|
| **Actions**        | Read-only      | read the `workflow_job` event payload                                                     |
| **Administration** | Read and write | required by `POST /repos/.../actions/runners/generate-jitconfig` (repo-scoped JIT config) |
| **Metadata**       | Read-only      | mandatory default                                                                         |

### Organization permissions (only for org-scoped projects)

| Permission              | Access         | Why                                                                                       |
|-------------------------|----------------|-------------------------------------------------------------------------------------------|
| **Self-hosted runners** | Read and write | required by `POST /orgs/<org>/actions/runners/generate-jitconfig` (org-scoped JIT config) |

Leave every other permission at *No access*.

> Without `administration:write` you'll see `jit config: 403 Forbidden` for repo-scoped projects. Without
`organization_self_hosted_runners:write` you'll see the same for org-scoped ones. Both keep working independently — pick
> the permission that matches the scope you actually use.

## 3. Subscribe to events

Under "Subscribe to events", check **only**:

- ✅ **Workflow job**

Nothing else. The webhook handler ignores other events (logged-and-dropped at 200) but receiving them wastes deliveries
on both sides.

## 4. Create the App + private key

Click **Create GitHub App**. On the resulting settings page:

1. Note the **App ID** at the top (a 6–7 digit number) → goes into `github.app_id`.
2. Scroll to **Private keys** → **Generate a private key** → save the downloaded `.pem` file somewhere on the server (
   e.g. `/etc/pacer/gh-app.pem`, mode `0400`, owned by the user that runs the tool) → path goes into
   `github.private_key_path`.
3. The webhook secret you set in step 1 → goes into `github.webhook_secret`.

## 5. Install the App

In the App settings sidebar → **Install App** → pick the account.

- **Repo-scoped projects only**: choose **Selected repositories** and pick the repos you want runners for. Safer than
  *All repositories* — the tool only sees `workflow_job` events from repos that explicitly trust it.
- **Org-scoped projects**: install at the **organization** level (when "Where can this App be installed?" is set to *Any
  account* or the App lives in the org's account). The org-level install is what makes
  `/orgs/<org>/actions/runners/generate-jitconfig` work; without it the org JIT call returns 404. *All repositories* +
  org install is the typical org-scoped posture.

> **No per-repo webhook configuration.** GitHub Apps centralize webhooks: the App has **one** webhook URL (the one you
> set in step 1), and events from every installed repo flow through that single URL. Do **not** add a webhook under each
> repo's *Settings → Webhooks*; those are for non-App integrations and would result in duplicate / unsigned deliveries.

GitHub assigns each install an **installation_id**; the tool reads it from each `workflow_job` webhook payload, so you
don't need to record it manually.

## 6. Verify deliveries

Once the server is running and reachable at `server.public_url`:

- GitHub App settings → **Advanced** → **Recent Deliveries** → trigger a `ping` (Redeliver). The tool returns 200.
- Trigger a real workflow run on a bound repo with `runs-on: [self-hosted, <project>]`. The tool's Jobs page should show
  it transition `queued → claimed → starting → running → completed`.

## Common gotchas

- **HMAC verification failures in logs** — the webhook secret on GitHub doesn't match `github.webhook_secret` in YAML.
  Re-paste both.
- **Job stuck at `queued` forever** — repo not bound, or no pool's labels match the workflow's `runs-on`. Check **Recent
  Deliveries** on the App and look for `audit.job.no_pool_match` in the tool's logs.
- **Workflow runs but never appears in the Jobs page** — the workflow's `runs-on` doesn't include `self-hosted` (e.g.
  `runs-on: ubuntu-latest`). Github-hosted workflows are silently ignored at the webhook gate; they don't enter pacer's
  queue and don't write an audit row. Add `self-hosted` plus your project / pool labels to `runs-on` to route the job
  through pacer.
- **`config invalid: github.private_key_path required`** — the tool can't find the PEM at the configured path; check
  perms (the running user must have read access).
- **`jit config: 403 Forbidden`** — the App is missing `administration:write`. Re-edit the App's permissions; existing
  installs auto-pick up the new permission set on the next webhook (no reinstall needed).
