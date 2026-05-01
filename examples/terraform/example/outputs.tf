# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

output "console_url" {
  description = "Browse here once cloud-init finishes (~2 min after apply). The first-run bootstrap password is in journalctl on the host."
  value       = module.pacer.console_url
}

output "instance_id" {
  description = "EC2 instance id. SSH-via-SSM: aws ssm start-session --target <id>"
  value       = module.pacer.instance_id
}

output "public_ip" {
  description = "Point your DNS A record at this address."
  value       = module.pacer.public_ip
}

output "runner_instance_profiles" {
  description = "Map of runner key -> instance-profile name. Paste the relevant value into each pool's \"IAM instance profile\" field in the Pacer UI."
  value       = module.pacer.runner_instance_profile_names
}

output "orchestrator_role_arn" {
  description = "Just for reference -- the host already assumes this via its instance profile."
  value       = module.pacer.orchestrator_role_arn
}
