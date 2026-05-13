# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# Trust policy: shared by orchestrator + runner roles (both are EC2).
data "aws_iam_policy_document" "ec2_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

# ----------------------------------------------------------------------
# Runner roles: attached to spawned EC2 instances. iam:PassRole on the
# orchestrator side is scoped to every runner role this module creates
# plus var.additional_runner_role_arns.
# ----------------------------------------------------------------------
resource "aws_iam_role" "runner" {
  for_each           = var.runners
  name               = each.key
  assume_role_policy = data.aws_iam_policy_document.ec2_trust.json
  tags               = var.tags
}

resource "aws_iam_instance_profile" "runner" {
  for_each = var.runners
  name     = each.key
  role     = aws_iam_role.runner[each.key].name
  tags     = var.tags
}

# SSM is handy for break-glass shell access. Off by default because the
# actions/runner already streams stdout to GitHub and reaped instances
# should be replaced, not debugged in-place.
resource "aws_iam_role_policy_attachment" "runner_ssm" {
  for_each   = { for k, v in var.runners : k => v if v.enable_cloudwatch_logs }
  role       = aws_iam_role.runner[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy_attachment" "runner_cloudwatch" {
  for_each   = { for k, v in var.runners : k => v if v.enable_cloudwatch_logs }
  role       = aws_iam_role.runner[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
}

# Flatten (runner, policy_arn) pairs so each attachment gets a unique key.
resource "aws_iam_role_policy_attachment" "runner_extra" {
  for_each = merge([
    for runner_key, runner in var.runners : {
      for arn in runner.additional_policy_arns :
      "${runner_key}::${arn}" => { runner = runner_key, arn = arn }
    }
  ]...)
  role       = aws_iam_role.runner[each.value.runner].name
  policy_arn = each.value.arn
}

# ----------------------------------------------------------------------
# Orchestrator policy. Mirrors docs/iam-role.json (the canonical
# human-facing reference) using the data-source form so the policy is
# validated at plan time. Edits should land in both places when the
# policy shape changes.
# ----------------------------------------------------------------------
data "aws_iam_policy_document" "orchestrator" {
  # Validation reads at pool-save time + spot-price snapshot at spawn.
  statement {
    sid = "DescribeForValidation"
    actions = [
      "ec2:DescribeImages",
      "ec2:DescribeSubnets",
      "ec2:DescribeSecurityGroups",
      "ec2:DescribeSpotPriceHistory",
    ]
    resources = ["*"]
  }

  # Reaper batches DescribeInstances every 60s to detect spawned
  # instances that AWS has already terminated (spot reclaim,
  # store-failure, manual termination from the console) so the row
  # gets marked lost immediately instead of waiting for the
  # max-runtime cutoff. Resource-level scoping isn't available for
  # this action, so it's "*" -- read-only, no mutation risk.
  statement {
    sid       = "DescribeInstancesForReaper"
    actions   = ["ec2:DescribeInstances"]
    resources = ["*"]
  }

  # On-demand price lookups for cost rollups.
  statement {
    sid       = "ReadOnDemandPricing"
    actions   = ["pricing:GetProducts"]
    resources = ["*"]
  }

  # Verify that runner instance profiles exist before stamping a pool.
  statement {
    sid       = "ValidateInstanceProfileAtPoolSave"
    actions   = ["iam:GetInstanceProfile"]
    resources = ["arn:aws:iam::${local.account_id}:instance-profile/*"]
  }

  # Create LTs only when they carry our managed-by tag.
  statement {
    sid       = "CreateTaggedLaunchTemplate"
    actions   = ["ec2:CreateLaunchTemplate"]
    resources = [local.lt_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # Modify / delete LTs we already own.
  statement {
    sid = "ModifyOnlyOurLaunchTemplates"
    actions = [
      "ec2:CreateLaunchTemplateVersion",
      "ec2:ModifyLaunchTemplate",
      "ec2:DeleteLaunchTemplate",
      "ec2:DeleteLaunchTemplateVersions",
    ]
    resources = [local.lt_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # RunInstances reads on the read-only resources it touches.
  statement {
    sid     = "RunInstancesReadOnlyResources"
    actions = ["ec2:RunInstances"]
    resources = [
      "${local.ec2_arn_prefix}::image/*",
      "${local.ec2_arn_prefix}:*:subnet/*",
      "${local.ec2_arn_prefix}:*:security-group/*",
      "${local.ec2_arn_prefix}:*:network-interface/*",
      "${local.ec2_arn_prefix}:*:key-pair/*",
      "${local.ec2_arn_prefix}:*:placement-group/*",
    ]
  }

  # RunInstances bound to our LT.
  statement {
    sid       = "RunInstancesFromOurLaunchTemplate"
    actions   = ["ec2:RunInstances"]
    resources = [local.lt_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # New instance + volume creation MUST stamp our managed-by tag.
  statement {
    sid       = "RunInstancesTaggedInstanceAndVolume"
    actions   = ["ec2:RunInstances"]
    resources = [local.instance_arn, local.volume_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # CreateFleet: same gate; the orchestrator stamps the fleet itself
  # with managed-by so this Sid fires.
  statement {
    sid       = "CreateFleetFromOurLaunchTemplate"
    actions   = ["ec2:CreateFleet"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # CreateTags at resource-creation time.
  statement {
    sid       = "TagOnCreate"
    actions   = ["ec2:CreateTags"]
    resources = ["${local.ec2_arn_prefix}:*:*"]
    condition {
      test     = "StringEquals"
      variable = "ec2:CreateAction"
      values = [
        "RunInstances",
        "CreateFleet",
        "CreateLaunchTemplate",
        "CreateLaunchTemplateVersion",
      ]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # Post-Fleet CreateTags: gha:job_id / gha:repo / repo user tags land
  # AFTER the fleet returns. Gated on the LT-applied managed-by tag.
  statement {
    sid       = "TagAfterFleetLaunch"
    actions   = ["ec2:CreateTags"]
    resources = [local.instance_arn, local.volume_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # Reaper / job-failure terminate path.
  statement {
    sid       = "TerminateOnlyOurInstances"
    actions   = ["ec2:TerminateInstances"]
    resources = [local.instance_arn]
    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/gha:managed-by"
      values   = ["pacer"]
    }
  }

  # iam:PassRole is the privilege-escalation gate. Scoped to every
  # runner role this module creates plus any additional runner roles
  # operators declare elsewhere. Without PassedToService any holder of
  # this role could pass the runner role to other AWS services.
  statement {
    sid     = "PassRunnerInstanceProfile"
    actions = ["iam:PassRole"]
    resources = concat(
      [for r in aws_iam_role.runner : r.arn],
      var.additional_runner_role_arns,
    )
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "orchestrator" {
  name               = "${var.name_prefix}-orchestrator"
  assume_role_policy = data.aws_iam_policy_document.ec2_trust.json
  tags               = var.tags
}

resource "aws_iam_policy" "orchestrator" {
  name   = "${var.name_prefix}-orchestrator"
  policy = data.aws_iam_policy_document.orchestrator.json
  tags   = var.tags

  lifecycle {
    precondition {
      condition     = length(var.runners) > 0 || length(var.additional_runner_role_arns) > 0
      error_message = "At least one runner role is required: populate var.runners (created by this module) or var.additional_runner_role_arns (declared externally)."
    }
  }
}

resource "aws_iam_role_policy_attachment" "orchestrator" {
  role       = aws_iam_role.orchestrator.name
  policy_arn = aws_iam_policy.orchestrator.arn
}

resource "aws_iam_instance_profile" "orchestrator" {
  name = "${var.name_prefix}-orchestrator"
  role = aws_iam_role.orchestrator.name
  tags = var.tags
}
