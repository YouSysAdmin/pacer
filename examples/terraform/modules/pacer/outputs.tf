# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# ---- host ------------------------------------------------------------

output "instance_id" {
  description = "EC2 instance ID."
  value       = aws_instance.pacer.id
}

output "private_ip" {
  description = "Private IP of the host."
  value       = aws_instance.pacer.private_ip
}

output "public_ip" {
  description = "Allocated EIP (when associate_public_ip=true) or the instance's public IP."
  value       = var.associate_public_ip ? aws_eip.pacer[0].public_ip : aws_instance.pacer.public_ip
}

output "security_group_id" {
  description = "ID of the host's security group. Reference from your DNS / ALB module if you front the host."
  value       = aws_security_group.pacer.id
}

output "console_url" {
  description = "Where to point your browser once cloud-init finishes (~2 min)."
  value       = var.public_url
}

# ---- orchestrator IAM ------------------------------------------------

output "orchestrator_role_arn" {
  description = "ARN of the orchestrator IAM role. The host already assumes this via its instance profile; exposed for cross-stack reference."
  value       = aws_iam_role.orchestrator.arn
}

output "orchestrator_role_name" {
  description = "Name of the orchestrator IAM role."
  value       = aws_iam_role.orchestrator.name
}

output "orchestrator_instance_profile_name" {
  description = "Instance-profile attached to the Pacer host."
  value       = aws_iam_instance_profile.orchestrator.name
}

output "orchestrator_instance_profile_arn" {
  description = "Orchestrator instance-profile ARN."
  value       = aws_iam_instance_profile.orchestrator.arn
}

output "orchestrator_policy_arn" {
  description = "ARN of the managed policy attached to the orchestrator role."
  value       = aws_iam_policy.orchestrator.arn
}

# ---- runner IAM ------------------------------------------------------

output "runner_role_names" {
  description = "Map keyed by var.runners key -> IAM role name."
  value       = { for k, r in aws_iam_role.runner : k => r.name }
}

output "runner_role_arns" {
  description = "Map keyed by var.runners key -> IAM role ARN."
  value       = { for k, r in aws_iam_role.runner : k => r.arn }
}

output "runner_instance_profile_names" {
  description = "Map keyed by var.runners key -> instance-profile name. Paste the relevant value into each pool's \"IAM instance profile\" field in the Pacer UI."
  value       = { for k, p in aws_iam_instance_profile.runner : k => p.name }
}

output "runner_instance_profile_arns" {
  description = "Map keyed by var.runners key -> instance-profile ARN."
  value       = { for k, p in aws_iam_instance_profile.runner : k => p.arn }
}
