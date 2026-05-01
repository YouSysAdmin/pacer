#!/bin/bash
# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# Final pass: shrink the AMI by clearing caches + per-build artifacts.
# Skip the SSH host-key reset that some Packer recipes do -- AL2023
# regenerates them on first boot via cloud-init anyway.
set -euo pipefail

dnf -y clean all
rm -rf /var/cache/dnf /var/log/dnf.* /tmp/* /var/tmp/*

# Strip the build-time scripts directory we uploaded.
rm -rf /tmp/scripts

# Truncate logs that captured the build session.
find /var/log -type f -name "*.log" -exec truncate -s 0 {} \;
truncate -s 0 /var/log/wtmp /var/log/btmp /var/log/lastlog 2>/dev/null || true

# Clear cloud-init state so the first boot of a spawned instance runs
# user-data fresh.
cloud-init clean --logs --machine-id

echo "==> cleanup done"
