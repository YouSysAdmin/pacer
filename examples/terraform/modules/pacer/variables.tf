# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# ---- naming + tagging ------------------------------------------------

variable "name_prefix" {
  description = "Prefix for orchestrator IAM resources, EC2 instance, security group, and EIP names."
  type        = string
  default     = "pacer"
}

variable "tags" {
  description = "Tags applied to every resource the module creates."
  type        = map(string)
  default     = {}
}

# ---- runner IAM ------------------------------------------------------

variable "runners" {
  description = "Runner roles to create. Map keyed by role + instance-profile name (the key is what you paste into each pool's \"IAM instance profile\" field in the Pacer UI). Each entry tunes whether SSM / CloudWatch logging is enabled and which extra managed-policy ARNs to attach. Pass an empty map to create none (then use additional_runner_role_arns to wire externally-declared roles)."
  type = map(object({
    enable_cloudwatch_logs = optional(bool, false)
    additional_policy_arns = optional(list(string), [])
  }))
  default = {
    "pacer-runner" = {}
  }
}

variable "additional_runner_role_arns" {
  description = "Extra runner role ARNs the orchestrator's iam:PassRole should permit. Use when you declare additional runner roles outside this module (cross-account, pre-existing). Roles created via var.runners are added automatically."
  type        = list(string)
  default     = []
}

# ---- networking ------------------------------------------------------

variable "vpc_id" {
  description = "VPC the host instance lands in."
  type        = string
}

variable "subnet_id" {
  description = "Subnet for the host. Public subnet if you want webhooks reachable from GitHub directly; private subnet + ALB / Tailscale otherwise."
  type        = string
}

variable "ingress_cidrs" {
  description = "CIDRs allowed to reach the public listener. Default 0.0.0.0/0 because GitHub webhook IPs span a wide range -- restrict if you front the host with an ALB / Cloudflare Tunnel that itself accepts from GitHub."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "associate_public_ip" {
  description = "Allocate + associate an Elastic IP. Public subnet only."
  type        = bool
  default     = true
}

# ---- host ------------------------------------------------------------

variable "instance_type" {
  description = "EC2 instance type for the host. Default fits a couple-projects deployment; bump for high webhook volume or large SQLite footprint."
  type        = string
  default     = "t4g.small"
}

variable "ami_id" {
  description = "AMI for the host. Empty string = latest Amazon Linux 2023 ARM64 (must match the architecture of instance_type)."
  type        = string
  default     = ""
}

variable "key_name" {
  description = "Optional EC2 keypair for SSH break-glass. Leave empty to disable; SSM Session Manager remains available if the runner role has it."
  type        = string
  default     = ""
}

variable "root_volume_gb" {
  description = "Root EBS size. SQLite + audit log + ACME cache fit comfortably in 20 GB; bump if you keep a long audit history."
  type        = number
  default     = 30
}

# ---- pacer config ----------------------------------------------------

variable "region" {
  description = "AWS region the orchestrator operates in. Single-region; the orchestrator can only operate in one."
  type        = string
}

variable "aws_profile" {
  description = "AWS profile name the binary uses. Empty = SDK default credential chain (instance profile when running on EC2, env vars, ~/.aws/credentials)."
  type        = string
  default     = ""
}

variable "log_level" {
  description = "logging.level: debug | info | warn | error."
  type        = string
  default     = "info"
  validation {
    condition     = contains(["debug", "info", "warn", "error"], var.log_level)
    error_message = "log_level must be one of: debug, info, warn, error."
  }
}

variable "log_format" {
  description = "logging.format: json (production) | text (human-readable, dev)."
  type        = string
  default     = "json"
  validation {
    condition     = contains(["json", "text"], var.log_format)
    error_message = "log_format must be \"json\" or \"text\"."
  }
}

variable "public_url" {
  description = "Full https://... URL the tool reaches GitHub webhooks AND spawned instances at. Must include scheme."
  type        = string
}

variable "fqdn" {
  description = "Hostname only (no scheme), used by ACME mode for cert issuance. Required when tls_mode=acme."
  type        = string
  default     = ""
}

variable "tls_mode" {
  description = "TLS termination strategy: \"none\" (plain HTTP behind ALB / nginx), \"acme\" (Let's Encrypt http-01), \"manual\" (operator-supplied PEM), \"self\" (in-memory self-signed). Per-mode required inputs: acme -> fqdn + acme_email; manual -> tls_cert_pem + tls_key_pem; self -> fqdn (+ optional tls_self_alg)."
  type        = string
  default     = "acme"
  validation {
    condition     = contains(["none", "acme", "manual", "self"], var.tls_mode)
    error_message = "tls_mode must be one of: none, acme, manual, self."
  }
}

variable "acme_email" {
  description = "Contact email for Let's Encrypt account. Required when tls_mode=acme."
  type        = string
  default     = ""
}

variable "tls_cert_pem" {
  description = "PEM certificate contents (full chain). Required when tls_mode=manual. Written to /etc/pacer/tls/cert.pem by user-data. Pass via TF_VAR_tls_cert_pem=\"$(cat fullchain.pem)\"."
  type        = string
  default     = ""
  sensitive   = true
}

variable "tls_key_pem" {
  description = "PEM private key contents. Required when tls_mode=manual. Written to /etc/pacer/tls/key.pem by user-data."
  type        = string
  default     = ""
  sensitive   = true
}

variable "tls_self_alg" {
  description = "Algorithm for the in-memory cert when tls_mode=self. \"rsa\" (default; widest client compat) | \"ed25519\"."
  type        = string
  default     = "rsa"
  validation {
    condition     = contains(["rsa", "ed25519"], var.tls_self_alg)
    error_message = "tls_self_alg must be \"rsa\" or \"ed25519\"."
  }
}

variable "listen_port" {
  description = "Port the binary listens on. Cloud-init grants AmbientCapability NET_BIND_SERVICE so privileged ports work."
  type        = number
  default     = 443
}

variable "trusted_proxies" {
  description = "IPs / CIDRs of any reverse proxy / ALB / Cloudflare edge that fronts the tool. When set, X-Forwarded-For is honored ONLY for requests from these peers, so the rate limiter, audit log, and access log see real client IPs. Empty (default) = trust no XFF - the right answer when the tool is the public endpoint."
  type        = list(string)
  default     = []
}

variable "auth_disabled" {
  description = "Set true only on private networks. The console then has no auth -- anyone reachable on the address can edit pools."
  type        = bool
  default     = false
}

variable "auth_local_enabled" {
  description = "Enable local email+password auth (with bootstrap-once user). Disable when running OIDC-only -- the binary auto-disables this anyway when oidc.enabled=true, but flipping it explicitly keeps the YAML honest."
  type        = bool
  default     = true
}

variable "session_ttl" {
  description = "Operator session cookie lifetime as a Go duration (e.g. 12h, 30m, 2h30m)."
  type        = string
  default     = "12h"
}

variable "operator_email" {
  description = "Bootstrap-once local user email. Pacer logs the random password to journald exactly once on first boot; tail with: journalctl -u pacer | grep 'first-run password'. Required when auth_local_enabled=true."
  type        = string
  default     = ""
}

variable "oidc" {
  description = "OIDC SSO config (client_secret is separate -- see var.oidc_client_secret). When enabled, the binary auto-disables local login at startup; local stays as break-glass via auth_local_enabled flip + restart."
  type = object({
    enabled                = optional(bool, false)
    issuer                 = optional(string, "")
    client_id              = optional(string, "")
    redirect_url           = optional(string, "")
    scopes                 = optional(list(string), ["openid", "email", "profile"])
    require_email_verified = optional(bool, true)
    allowed_domains        = optional(list(string), [])
    allowed_emails         = optional(list(string), [])
    groups_claim           = optional(string, "")
    allowed_groups         = optional(list(string), [])
  })
  default = {}
}

variable "oidc_client_secret" {
  description = "OIDC client_secret. Required when oidc.enabled=true. Pass via TF_VAR_oidc_client_secret so it never lands in source control. Loaded by the systemd unit as PACER_AUTH_OIDC_CLIENT_SECRET."
  type        = string
  default     = ""
  sensitive   = true
}

# ---- secrets ---------------------------------------------------------

variable "github_app_id" {
  description = "GitHub App ID (numeric, from the App settings page)."
  type        = string
}

variable "github_app_private_key" {
  description = "Contents of the GitHub App PEM. Pass via TF_VAR_github_app_private_key=\"$(cat key.pem)\" so it never lands in source control."
  type        = string
  sensitive   = true
}

variable "webhook_secret" {
  description = "GitHub webhook secret (matches App settings)."
  type        = string
  sensitive   = true
}

variable "callback_hmac_secret" {
  description = "Tool-side HMAC secret for runner self-registration tokens. Generate with: openssl rand -hex 32."
  type        = string
  sensitive   = true
  validation {
    condition     = length(var.callback_hmac_secret) >= 32
    error_message = "callback_hmac_secret must be at least 32 characters."
  }
}

variable "jwt_secret" {
  description = "HS256 secret for the operator session cookie. Generate with: openssl rand -hex 32."
  type        = string
  sensitive   = true
  validation {
    condition     = length(var.jwt_secret) >= 32
    error_message = "jwt_secret must be at least 32 characters."
  }
}

# ---- release ---------------------------------------------------------

variable "pacer_version" {
  description = "Release tag downloaded by user-data (e.g. \"v0.1.0\"). Must match a published release in pacer_repo."
  type        = string
  default     = "v0.1.0"
}

variable "pacer_repo" {
  description = "GitHub repo that hosts the pacer release tarballs (owner/name)."
  type        = string
  default     = "yousysadmin/pacer"
}

variable "pacer_release_url" {
  description = "Override the default release-tarball URL (e.g. internal artifact mirror). Empty = derive from pacer_repo + pacer_version."
  type        = string
  default     = ""
}
