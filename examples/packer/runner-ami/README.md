# Pacer runner AMI (Packer)

Pre-bakes the actions/runner binary plus baseline tooling (jq, curl,
git, awscli v2, optional Docker / Node) into a custom AMI. Spawned
runners boot from this image instead of pulling the runner from GitHub
on every job, cutting bootstrap time from ~30s to ~5s.

```
runner.pkr.hcl       Packer source + build definition
variables.pkr.hcl    All knobs (region, arch, version, what to install)
scripts/
  install-tools.sh   curl, jq, git, awscli v2, optional docker/nodejs
  install-runner.sh  fetches actions/runner-linux-<arch>-<version>.tar.gz to /opt/actions-runner
  cleanup.sh         dnf clean, log truncate, cloud-init clean
```

## Build

```bash
cd examples/packer/runner-ami/
packer init .
packer build -var "region=eu-central-1" -var "arch=arm64" .
```

The resulting AMI id lands in `manifest.json` (post-processor) and on
stdout. Plug it into the relevant pool's `ami_id` field in the Pacer UI.

## Variables worth knowing

| name             | default        | notes                                                               |
|------------------|----------------|---------------------------------------------------------------------|
| `region`         | `eu-central-1` | Single-region build; copy to other regions yourself.                |
| `arch`           | `arm64`        | Pair with a Graviton `instance_type` (default `t4g.medium`).        |
| `runner_version` | `""` (latest)  | Pin once you have a green build to avoid surprise upgrades.         |
| `include_docker` | `true`         | Workflows that build images need this.                              |
| `include_node`   | `false`        | Most workflows install Node themselves via `actions/setup-node`.    |
| `instance_type`  | `t4g.medium`   | Build host only; the resulting AMI runs on whatever the pool picks. |

## Pool wiring

Pacer's user-data checks `/opt/actions-runner/run.sh` first. If present
AND the version stamped in `/opt/actions-runner/.version` matches the
pool's pin (or matches the server-resolved latest when no pin is set),
user-data uses the baked binary. Otherwise it falls through to the
download path -- so a stale AMI still works, just slower.

To always use the baked version, set `Pool.RunnerVersion` (in the UI)
to whatever value the AMI's `.version` file holds.

## Tags

The AMI, build instance, and snapshot all carry
`gha:managed-by=pacer-runner-ami` so you can find them via the EC2
console's tag filter and clean up old builds:

```bash
aws ec2 describe-images \
  --owners self \
  --filters "Name=tag:gha:managed-by,Values=pacer-runner-ami" \
  --query 'Images[*].[ImageId,Name,CreationDate]' --output table
```

## Production hardening checklist

- Pin `runner_version` once a build is green. Floating `latest` means
  every rebuild risks breakage from upstream changes.
- Run Packer inside a CI pipeline (GitHub Actions, ironically, works
  fine) on a schedule -- weekly is a reasonable cadence for picking up
  base-image security patches.
- Build in a dedicated AWS account if you're sharing the AMI across
  accounts; AMIs share via launch-permission grants without copying
  the underlying snapshot.
- Audit the included tooling against your compliance posture; the
  defaults here are minimum-viable, not exhaustive.
