---
title: "Installation"
description: "Stand up Pacer end-to-end: GitHub App on the GitHub side, IAM role and prerequisites on the AWS side, then the binary itself."
weight: 20
---

Three pieces have to line up before the first webhook can spawn an instance:

1. **[GitHub side](github/)** — create a GitHub App, set its permissions and webhook URL, install it on the repos you want to manage.
2. **[AWS side](aws/)** — apply an IAM policy with launch-template management, `RunInstances`, and tag-scoped `TerminateInstances`. Decide which AMI / subnets / security groups your pools will use.
3. **[Server](server/)** — install the Go binary, write the YAML config that ties the GitHub App credentials to the AWS region, run it.

The order matters: you need the GitHub App's `webhook_secret` and the AWS region in the YAML before the server can usefully start. Once it's running, project / pool / repo configuration happens through the web UI (covered in a separate doc; not yet written).

## Prerequisites

Before you start, have:

- An AWS account where you can create IAM policies + roles, an EC2 launch template, and start instances.
- Either an EC2 host or a private host that can:
  - reach `https://api.github.com/` (outbound).
  - be reached from `https://api.github.com/` (inbound, for webhooks). A reverse proxy / VPN / Cloudflare Tunnel is fine — the public URL goes into `server.public_url`.
- An AMI for the runner instances with `bash`, `curl`, `jq`, `tar`, and `sudo` available. **The GitHub Actions runner itself is auto-installed at boot** by the user-data script (it downloads the binary from GitHub releases on every launch and caches it on the EBS volume). Pre-baking the runner is optional and only saves ~2-5s of boot time per spawn. (An AMI build script is on the polish roadmap.)
- A way to terminate TLS for `server.public_url`: the tool can do it in-process (`server.tls.mode: self|manual|acme`) or you can front it with a reverse proxy.
- A GitHub account or organization where you can create a GitHub App.
