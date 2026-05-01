# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# Latest Amazon Linux 2023 for ARM64 -- the default instance_type
# (t4g.small) is Graviton. Override var.ami_id for x86 / a custom image.
data "aws_ami" "al2023_arm64" {
  count       = var.ami_id == "" ? 1 : 0
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-arm64"]
  }
  filter {
    name   = "state"
    values = ["available"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

locals {
  ami_id = var.ami_id != "" ? var.ami_id : data.aws_ami.al2023_arm64[0].id

  config_yaml = templatefile("${path.module}/templates/config.yaml.tftpl", {
    listen_port        = var.listen_port
    public_url         = var.public_url
    trusted_proxies    = var.trusted_proxies
    tls_mode           = var.tls_mode
    fqdn               = var.fqdn
    acme_email         = var.acme_email
    tls_self_alg       = var.tls_self_alg
    region             = var.region
    aws_profile        = var.aws_profile
    github_app_id      = var.github_app_id
    log_level          = var.log_level
    log_format         = var.log_format
    auth_disabled      = var.auth_disabled
    auth_local_enabled = var.auth_local_enabled
    session_ttl        = var.session_ttl
    operator_email     = var.operator_email
    oidc               = var.oidc
  })

  systemd_unit = templatefile("${path.module}/templates/pacer.service.tftpl", {})

  user_data = templatefile("${path.module}/templates/user-data.sh.tftpl", {
    pacer_version          = var.pacer_version
    pacer_repo             = var.pacer_repo
    pacer_release_url      = var.pacer_release_url
    config_yaml            = local.config_yaml
    systemd_unit           = local.systemd_unit
    jwt_secret             = var.jwt_secret
    webhook_secret         = var.webhook_secret
    callback_hmac_secret   = var.callback_hmac_secret
    github_app_private_key = var.github_app_private_key
    tls_mode               = var.tls_mode
    tls_cert_pem           = var.tls_cert_pem
    tls_key_pem            = var.tls_key_pem
    oidc_client_secret     = var.oidc_client_secret
  })
}

resource "aws_security_group" "pacer" {
  name        = "${var.name_prefix}-host"
  description = "Pacer orchestrator host: ingress for webhook + console, plus :80 for ACME http-01 when enabled."
  vpc_id      = var.vpc_id
  tags        = var.tags
}

resource "aws_vpc_security_group_ingress_rule" "listener" {
  for_each          = toset(var.ingress_cidrs)
  security_group_id = aws_security_group.pacer.id
  cidr_ipv4         = each.value
  from_port         = var.listen_port
  to_port           = var.listen_port
  ip_protocol       = "tcp"
  description       = "Pacer HTTPS listener / webhook ingress"
}

# ACME http-01 challenge listener. Only opened when tls_mode=acme.
resource "aws_vpc_security_group_ingress_rule" "acme" {
  for_each          = var.tls_mode == "acme" ? toset(var.ingress_cidrs) : toset([])
  security_group_id = aws_security_group.pacer.id
  cidr_ipv4         = each.value
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
  description       = "ACME http-01 challenge (Lets Encrypt)"
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.pacer.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
  description       = "EC2 calls to AWS APIs + GitHub webhook deliveries + ACME validation."
}

resource "aws_instance" "pacer" {
  ami                         = local.ami_id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = [aws_security_group.pacer.id]
  iam_instance_profile        = aws_iam_instance_profile.orchestrator.name
  associate_public_ip_address = var.associate_public_ip
  key_name                    = var.key_name != "" ? var.key_name : null
  user_data                   = local.user_data
  user_data_replace_on_change = true

  metadata_options {
    http_tokens                 = "required" # IMDSv2 only
    http_endpoint               = "enabled"
    http_put_response_hop_limit = 2
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = var.root_volume_gb
    encrypted   = true
    tags        = merge(var.tags, { Name = "${var.name_prefix}-host-root" })
  }

  tags = merge(var.tags, { Name = "${var.name_prefix}-host" })

  lifecycle {
    # Avoid replacing the host on every AMI refresh -- the binary updates
    # in place via user-data. To force a refresh, taint the instance.
    ignore_changes = [ami]

    precondition {
      condition     = var.tls_mode != "acme" || (var.fqdn != "" && var.acme_email != "")
      error_message = "tls_mode = \"acme\" requires both fqdn and acme_email."
    }
    precondition {
      condition     = var.tls_mode != "manual" || (var.tls_cert_pem != "" && var.tls_key_pem != "")
      error_message = "tls_mode = \"manual\" requires both tls_cert_pem and tls_key_pem."
    }
    precondition {
      condition     = var.tls_mode != "self" || var.fqdn != ""
      error_message = "tls_mode = \"self\" requires fqdn."
    }
    precondition {
      condition     = var.auth_disabled || !var.auth_local_enabled || var.operator_email != ""
      error_message = "operator_email is required when auth_local_enabled = true (and auth_disabled = false)."
    }
    precondition {
      condition = !var.oidc.enabled || (
        var.oidc.issuer != "" &&
        var.oidc.client_id != "" &&
        var.oidc.redirect_url != "" &&
        var.oidc_client_secret != ""
      )
      error_message = "oidc.enabled = true requires oidc.issuer, oidc.client_id, oidc.redirect_url, and oidc_client_secret."
    }
    precondition {
      condition     = length(var.oidc.allowed_groups) == 0 || var.oidc.groups_claim != ""
      error_message = "oidc.allowed_groups requires oidc.groups_claim (e.g. \"groups\", \"cognito:groups\", \"roles\")."
    }
  }
}

resource "aws_eip" "pacer" {
  count    = var.associate_public_ip ? 1 : 0
  instance = aws_instance.pacer.id
  domain   = "vpc"
  tags     = merge(var.tags, { Name = "${var.name_prefix}-host" })
}
