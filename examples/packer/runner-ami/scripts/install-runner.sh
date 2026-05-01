#!/bin/bash
# SPDX-License-Identifier: LicenseRef-DSL-1.0
# Deferred Source License (DSL)
# Pacer, Copyright (c) 2026 YouSysAdmin

# Pre-bake the actions/runner binary at /opt/actions-runner so user-data
# on the spawned instance can skip the download. Pacer's user-data
# checks for /opt/actions-runner/run.sh and uses it if present.
set -euo pipefail

# Resolve target version. Empty = ask GitHub for "latest" at build time.
if [ -z "${RUNNER_VERSION:-}" ]; then
  RUNNER_VERSION="$(curl -fsSL https://api.github.com/repos/actions/runner/releases/latest | jq -r .tag_name | sed 's/^v//')"
  echo "==> resolved latest actions/runner version: $RUNNER_VERSION"
fi
RUNNER_VERSION="${RUNNER_VERSION#v}"

case "${TARGET_ARCH:-arm64}" in
  arm64) RUNNER_PKG_ARCH="arm64" ;;
  amd64) RUNNER_PKG_ARCH="x64" ;;
  *) echo "unsupported TARGET_ARCH: ${TARGET_ARCH}" >&2; exit 1 ;;
esac

URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${RUNNER_PKG_ARCH}-${RUNNER_VERSION}.tar.gz"

install -d -m 0755 /opt/actions-runner
echo "==> downloading $URL"
curl -fsSL "$URL" -o /tmp/actions-runner.tar.gz
tar -xzf /tmp/actions-runner.tar.gz -C /opt/actions-runner
rm -f /tmp/actions-runner.tar.gz

# The runner ships its own dependency installer for systems missing
# libicu / krb5; AL2023 has these already, but run it for safety.
if [ -x /opt/actions-runner/bin/installdependencies.sh ]; then
  /opt/actions-runner/bin/installdependencies.sh || true
fi

# Stash the version so user-data can verify the baked binary matches
# what the pool expects -- if Pool.RunnerVersion differs, user-data
# falls through to the download path.
echo "$RUNNER_VERSION" >/opt/actions-runner/.version

# Permissions: tightened to 0755 + ownership change-safe -- the
# user-data script will chown -R to whichever runner_user the pool
# selects (root, ec2-user, deployer, etc.).
chmod -R go-w /opt/actions-runner

echo "==> actions/runner ${RUNNER_VERSION} installed at /opt/actions-runner"
