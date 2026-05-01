# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

module "pacer" {
  source = "../modules/pacer"

  vpc_id    = var.vpc_id
  subnet_id = var.subnet_id
  region    = var.region

  public_url     = "https://${var.fqdn}"
  fqdn           = var.fqdn
  tls_mode       = "acme"
  acme_email     = var.operator_email
  operator_email = var.operator_email

  github_app_id          = var.github_app_id
  github_app_private_key = var.github_app_private_key
  webhook_secret         = var.webhook_secret
  callback_hmac_secret   = var.callback_hmac_secret
  jwt_secret             = var.jwt_secret

  pacer_version = var.pacer_version
  tags          = var.tags
}
