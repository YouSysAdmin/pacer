# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# IAM-side helpers consumed by iam.tf. Per-host locals (ami_id,
# config_yaml, systemd_unit, user_data) live next to the resources
# that consume them in main.tf -- a single locals block can't be
# split across files in terraform, so we colocate by usage.
locals {
  account_id = data.aws_caller_identity.current.account_id

  # Resource-ARN templates derived once so the policy statements stay
  # readable. Region-scoped where possible -- only iam:* and the
  # describe/pricing actions need to be wildcard.
  ec2_arn_prefix = "arn:aws:ec2:${var.region}"
  lt_arn         = "${local.ec2_arn_prefix}:*:launch-template/*"
  instance_arn   = "${local.ec2_arn_prefix}:*:instance/*"
  volume_arn     = "${local.ec2_arn_prefix}:*:volume/*"
}
