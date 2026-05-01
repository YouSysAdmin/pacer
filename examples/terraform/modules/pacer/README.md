# pacer

Single-module deployment of Pacer on AWS: orchestrator IAM role + runner
IAM role + EC2 host (security group, optional EIP, cloud-init).

The runner role is created here so `iam:PassRole` is correctly scoped at
apply time. To allow the orchestrator to pass *additional* runner roles
declared elsewhere, pass their ARNs via `additional_runner_role_arns`.

## Layout

```
main.tf          EC2 instance + SG + EIP + AMI lookup
iam.tf           orchestrator + runner roles, policy, instance profiles
locals.tf        EC2 ARN prefixes used by the orchestrator policy
terraform.tf     provider requirements + caller identity
variables.tf     all inputs
outputs.tf       all outputs
templates/
  config.yaml.tftpl   pacer config rendered at apply time
  pacer.service.tftpl systemd unit
  user-data.sh.tftpl  cloud-init bootstrap
```

## What gets created

- IAM role `${name_prefix}-orchestrator` + managed policy + instance
  profile (assumed by the host EC2).
- One IAM role + instance profile per entry in `var.runners` (default:
  a single `pacer-runner`). Paste the instance-profile name into each
  pool's "IAM instance profile" field in the Pacer UI.
- Security group with ingress on `listen_port` (and :80 when
  `tls_mode = "acme"`), full egress.
- EC2 instance (Amazon Linux 2023 ARM64 by default), gp3 root volume,
  IMDSv2-only, optional EIP.

## Sensitive variables

All `sensitive = true`: `github_app_private_key`, `webhook_secret`,
`callback_hmac_secret`, `jwt_secret`, `tls_cert_pem`, `tls_key_pem`,
`oidc_client_secret`. Pass via env to keep them out of source control:

```
TF_VAR_github_app_private_key="$(cat path/to/app.pem)" \
TF_VAR_webhook_secret="$(cat path/to/webhook-secret)" \
TF_VAR_callback_hmac_secret="$(openssl rand -hex 32)" \
TF_VAR_jwt_secret="$(openssl rand -hex 32)" \
terraform apply
```

Or place them in a `terraform.tfvars` that is `.gitignore`d.

## TLS modes

`tls_mode` selects how the binary terminates TLS. All four modes the
binary supports are wired:

| mode             | required vars                      | what gets shipped                                                                                   |
|------------------|------------------------------------|-----------------------------------------------------------------------------------------------------|
| `none`           | --                                 | plain HTTP on `listen_port`. Use behind ALB / nginx that terminates TLS.                            |
| `acme` (default) | `fqdn`, `acme_email`               | Let's Encrypt http-01. Opens :80 in the SG too. `cache_dir = /var/lib/pacer/acme` survives reboots. |
| `manual`         | `tls_cert_pem`, `tls_key_pem`      | User-data writes `/etc/pacer/tls/{cert,key}.pem` (mode 0640, root:pacer).                           |
| `self`           | `fqdn` (+ optional `tls_self_alg`) | In-memory self-signed cert at startup. Handy for IP-only / private-network deploys.                 |

Preconditions on `aws_instance.pacer` enforce the per-mode required
inputs at plan time.

## OIDC SSO

Pass an `oidc` object (the binary auto-disables local login at startup
when OIDC is enabled):

```hcl
module "pacer" {
  source = "../modules/pacer"
  # ...
  oidc = {
    enabled                = true
    issuer                 = "https://accounts.example.com"
    client_id              = "pacer"
    redirect_url           = "https://pacer.example.com/api/auth/oidc/callback"
    scopes                 = ["openid", "email", "profile"]    # default
    require_email_verified = true                              # default
    allowed_domains        = ["example.com"]
    groups_claim           = "groups"                          # required if allowed_groups set
    allowed_groups         = ["pacer-admins"]
  }
}
```

`oidc_client_secret` is separate (sensitive). Pass via
`TF_VAR_oidc_client_secret="..."` so it doesn't sit in tfvars; the
systemd unit loads it as `PACER_AUTH_OIDC_CLIENT_SECRET`.

`groups_claim` varies per IdP: `groups` (Okta / Auth0 / Keycloak
default), `cognito:groups` (AWS Cognito), `roles` (some Keycloak
setups). Empty disables group checking.

## Defaults to know

- **AMI**: latest Amazon Linux 2023 ARM64 -- pairs with the `t4g.small`
  default. Override `ami_id` if you want x86 (and switch
  `instance_type` to a non-Graviton family like `t3`).
- **TLS**: `acme` (Let's Encrypt http-01). Requires `fqdn` to resolve
  to the instance and ports 80 + 443 reachable from the public
  internet. Set `tls_mode = "none"` if you front the host with an ALB
  / nginx that terminates TLS upstream - and in that case populate
  `trusted_proxies` with the upstream's CIDR(s) so the rate limiter
  and audit log see the real client IP from `X-Forwarded-For` instead
  of the proxy.
- **Ingress**: 0.0.0.0/0 by default -- GitHub webhook source IPs span a
  wide range and cycle. Tighten to the GitHub Meta endpoints
  programmatically in production, or front-end with Cloudflare.
- **Logging**: `info` / `json` / stdout. Tune via `log_level` +
  `log_format`. journald aggregates either way.

## Replacing the binary

Cloud-init writes the binary at boot, so a fresh AMI does NOT
auto-update the version. To upgrade:

1. Bump `pacer_version`.
2. `terraform taint module.<name>.aws_instance.pacer` to force a
   re-spawn -- or SSH in, `curl` the new tarball, replace
   `/usr/local/bin/pacer`, `systemctl restart pacer`.

The SQLite file lives on the root EBS volume; replacing the instance
without a snapshot loses state. Snapshot the EBS first if you care.

## Multiple runner profiles

Two ways to add more runner roles. Pick whichever fits your stack.

### Option 1: declare them in `var.runners` (preferred)

The module creates the role + instance profile + policy attachments
for you, and the orchestrator's `iam:PassRole` picks them up
automatically.

```hcl
module "pacer" {
  source = "../modules/pacer"
  # ...
  runners = {
    "pacer-runner"     = {}
    "pacer-runner-gpu" = {
      additional_policy_arns = [
        "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
      ]
    }
    "pacer-runner-debug" = {
      enable_cloudwatch_logs = true
    }
  }
}
```

The map key is the role + instance-profile name -- paste it into the
pool's "IAM instance profile" field in the Pacer UI. Outputs come back
as maps:

```hcl
terraform output -json runner_instance_profile_names
# => { "pacer-runner": "pacer-runner", "pacer-runner-gpu": "pacer-runner-gpu", ... }
```

### Option 2: declare roles externally (cross-account, pre-existing)

Pass their ARNs via `additional_runner_role_arns` so the orchestrator's
`iam:PassRole` allows them too:

```hcl
module "pacer" {
  source                      = "../modules/pacer"
  # ...
  additional_runner_role_arns = [
    "arn:aws:iam::123456789012:role/some-pre-existing-runner",
  ]
}
```

You're responsible for the role's trust policy and any extra managed
policies; the module just permits passing it through to EC2.

## What this does NOT do

- **Build the runner AMI** -- see `../../packer/runner-ami/`.
- **Create networking** -- bring your own VPC + subnet.
- **Manage projects / pools / repos** -- those go through the Pacer
  console.
- **Set up monitoring** -- Pacer logs JSON to stdout, journald collects
  it. Wire your own CloudWatch / Loki / etc. agent.
