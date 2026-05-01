# Pacer Terraform examples

Reference module + a wired-up example. Provisioner is Terraform 1.5+
with AWS provider 5.x.

```
modules/
  pacer/      orchestrator IAM + runner IAM + EC2 host (single module)

example/    calls the module with a full set of inputs; bring your own VPC + DNS
```

## What the module creates

- IAM role + policy + instance profile for the orchestrator (the host
  EC2 assumes it).
- IAM role + instance profile for spawned runners. After apply, paste
  `runner_instance_profile_name` into each pool's "IAM instance
  profile" field in the Pacer UI.
- EC2 host (Amazon Linux 2023 ARM64 by default), security group with
  `listen_port` (and :80 when `tls_mode = "acme"`), optional EIP, and
  cloud-init that fetches the binary and bootstraps the systemd unit.

For multi-runner-profile deployments, declare extra runner roles
outside the module and pass their ARNs via
`additional_runner_role_arns` -- see `modules/pacer/README.md`.

## What this does NOT do

- **Build the runner AMI** -- see `../packer/runner-ami/`. Cloud-init
  on the spawned runner can also download the binary at boot, but a
  pre-baked AMI removes the bootstrap latency from every job.
- **Create networking** -- VPC, subnets, NAT, route tables stay
  outside this scope. Networking choices vary too much to encode in a
  generic module.
- **Manage projects / pools / repos** -- those go through the Pacer
  console. Treat the UI as the source of truth for runtime config; use
  Terraform only for the always-on infra.
- **Set up monitoring** -- Pacer logs JSON to stdout, journald
  collects it. Wire your own CloudWatch / Loki / etc. agent.

## Production hardening checklist

The example optimizes for "works out of the box". Before going to
production:

- Front the host with an ALB (set `tls_mode = "none"`, terminate TLS
  at the LB, restrict ingress to the LB SG).
- Tighten `ingress_cidrs` to the GitHub Meta webhook ranges
  (https://api.github.com/meta) refreshed periodically.
- Move state to a remote backend (`backend "s3"`) with state locking.
- Snapshot the SQLite EBS volume on a schedule; the binary serializes
  WAL writes so any consistent snapshot is restorable.
- Pin `pacer_version` to a SHA-tagged release rather than a moving
  tag.
