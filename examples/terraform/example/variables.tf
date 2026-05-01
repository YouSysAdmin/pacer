# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

variable "region" {
  description = "AWS region. Single-region; the orchestrator can only operate in one."
  type        = string
  default     = "us-east-1"
}

variable "vpc_id" {
  description = "Existing VPC. The example does NOT create networking -- bring your own."
  type        = string
}

variable "subnet_id" {
  description = "Public subnet ID. Pacer needs an inbound route from GitHub for webhooks."
  type        = string
}

variable "fqdn" {
  description = "DNS name pointing at the Pacer host (A record, no scheme). E.g. pacer.example.com. Required for ACME-issued TLS."
  type        = string
}

variable "operator_email" {
  description = "Both the bootstrap-once console user AND the Let's Encrypt account contact."
  type        = string
}

variable "github_app_id" {
  description = "GitHub App ID."
  type        = string
}

variable "github_app_private_key" {
  description = "PEM contents. Pass via TF_VAR_github_app_private_key=\"$(cat key.pem)\"."
  type        = string
  sensitive   = true
}

variable "webhook_secret" {
  description = "GitHub App webhook secret."
  type        = string
  sensitive   = true
}

variable "callback_hmac_secret" {
  description = "openssl rand -hex 32"
  type        = string
  sensitive   = true
}

variable "jwt_secret" {
  description = "openssl rand -hex 32"
  type        = string
  sensitive   = true
}

variable "pacer_version" {
  description = "Release tag; must match a tarball in the pacer GitHub releases."
  type        = string
  default     = "v0.1.0"
}

variable "tags" {
  description = "Applied to every resource the modules create."
  type        = map(string)
  default = {
    "managed-by" = "terraform"
    "service"    = "pacer"
  }
}
