# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

variable "region" {
  type        = string
  description = "AWS region the AMI is built in. The resulting AMI is region-scoped; copy it across regions yourself if you need that."
  default     = "eu-central-1"
}

variable "instance_type" {
  type        = string
  description = "Build-host instance type. Must match `arch`: c7g.* / t4g.* for arm64, c7i.* / t3.* for amd64."
  default     = "t4g.medium"
}

variable "arch" {
  type        = string
  description = "Target CPU architecture: arm64 (Graviton) or amd64."
  default     = "arm64"
  validation {
    condition     = contains(["arm64", "amd64"], var.arch)
    error_message = "Arch must be arm64 or amd64."
  }
}

variable "source_ami_owner" {
  type        = string
  description = "Owner ID for the source-AMI filter. Default 137112412989 = Amazon Linux."
  default     = "137112412989"
}

variable "source_ami_name_filter" {
  type        = string
  description = "Glob for the source AMI's Name tag. Default = latest Amazon Linux 2023 for the chosen arch."
  default     = "al2023-ami-*"
}

variable "ami_name" {
  type        = string
  description = "Name of the resulting AMI. A timestamp is appended at build time."
  default     = "pacer-runner"
}

variable "runner_version" {
  type        = string
  description = "actions/runner release to bake in. Empty = ask the GitHub API for `latest` at build time. Pin once you have a green build."
  default     = ""
}

variable "include_docker" {
  type        = bool
  description = "Install Docker (rootful, ec2-user added to docker group). Workflows that build container images need this."
  default     = true
}

variable "include_node" {
  type        = bool
  description = "Install Node.js LTS. Most actions/* steps already bring their own; flip on if your workflows expect a system Node."
  default     = false
}

variable "ssh_username" {
  type        = string
  description = "SSH user Packer connects as. ec2-user for Amazon Linux."
  default     = "ec2-user"
}

variable "tags" {
  type        = map(string)
  description = "Tags applied to the AMI + the build instance + the resulting snapshot."
  default = {
    "gha:managed-by" = "pacer-runner-ami"
    "service"        = "pacer"
  }
}
