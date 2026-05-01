#!/bin/bash
# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# Base tooling that virtually every workflow needs.  Slimmest viable
# set: jq + curl + git + tar + unzip for the runner bootstrap; awscli
# v2 for IAM-assumed access from inside workflows.
set -euo pipefail

dnf -y update
dnf -y install \
    curl \
    jq \
    git \
    tar \
    gzip \
    unzip \
    which \
    rsync \
    openssl \
    ca-certificates \
    shadow-utils

# AWS CLI v2 -- the OS package is v1; v2 ships standalone.
ARCH="$(uname -m)"
case "$ARCH" in
  aarch64) AWSCLI_URL="https://awscli.amazonaws.com/awscli-exe-linux-aarch64.zip" ;;
  x86_64)  AWSCLI_URL="https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac
curl -fsSL "$AWSCLI_URL" -o /tmp/awscliv2.zip
unzip -q /tmp/awscliv2.zip -d /tmp
/tmp/aws/install
rm -rf /tmp/awscliv2.zip /tmp/aws

if [ "${INCLUDE_DOCKER:-false}" = "true" ]; then
  dnf -y install docker
  systemctl enable docker
  usermod -aG docker ec2-user
fi

if [ "${INCLUDE_NODE:-false}" = "true" ]; then
  # Amazon Linux 2023 ships nodejs 18+ in the dnf repo.
  dnf -y install nodejs
fi

echo "==> baseline tools installed"
