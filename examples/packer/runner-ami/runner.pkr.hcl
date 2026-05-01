# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

packer {
  required_version = ">= 1.10"
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = ">= 1.3"
    }
  }
}

locals {
  timestamp = formatdate("YYYYMMDD-hhmmss", timestamp())
}

source "amazon-ebs" "runner" {
  region        = var.region
  instance_type = var.instance_type
  ssh_username  = var.ssh_username

  source_ami_filter {
    filters = {
      name                = "${var.source_ami_name_filter}-${var.arch == "arm64" ? "arm64" : "x86_64"}"
      virtualization-type = "hvm"
      root-device-type    = "ebs"
      state               = "available"
    }
    owners      = [var.source_ami_owner]
    most_recent = true
  }

  ami_name        = "${var.ami_name}-${var.arch}-${local.timestamp}"
  ami_description = "Pacer GitHub Actions runner AMI; baked with actions/runner${var.runner_version != "" ? " ${var.runner_version}" : " (latest at build time)"}, jq, curl, awscli, git${var.include_docker ? ", docker" : ""}${var.include_node ? ", nodejs" : ""}."

  tags          = var.tags
  run_tags      = merge(var.tags, { Name = "${var.ami_name}-builder" })
  snapshot_tags = var.tags

  # IMDSv2 only on the resulting instances. Build instance also locked
  # down -- no point in shipping IMDS-vulnerable images.
  metadata_options {
    http_tokens                 = "required"
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 2
    instance_metadata_tags      = "enabled"
  }

  ami_block_device_mappings {
    device_name           = "/dev/xvda"
    volume_size           = 30
    volume_type           = "gp3"
    encrypted             = true
    delete_on_termination = true
  }
}

build {
  name    = "pacer-runner"
  sources = ["source.amazon-ebs.runner"]

  provisioner "file" {
    source      = "${path.root}/scripts/"
    destination = "/tmp/scripts/"
  }

  provisioner "shell" {
    inline = ["chmod +x /tmp/scripts/*.sh"]
  }

  provisioner "shell" {
    environment_vars = [
      "RUNNER_VERSION=${var.runner_version}",
      "INCLUDE_DOCKER=${var.include_docker}",
      "INCLUDE_NODE=${var.include_node}",
      "TARGET_ARCH=${var.arch}",
    ]
    execute_command = "{{ .Vars }} sudo -E bash '{{ .Path }}'"
    scripts = [
      "${path.root}/scripts/install-tools.sh",
      "${path.root}/scripts/install-runner.sh",
      "${path.root}/scripts/cleanup.sh",
    ]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
  }
}
