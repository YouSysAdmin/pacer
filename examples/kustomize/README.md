# Pacer Kubernetes / Kustomize examples

Reusable base + a wired-up example overlay. Targets Kubernetes 1.27+
with the bundled `kubectl -k` (no separate `kustomize` install
needed).

```
base/                  reusable resources (Namespace, SA, ConfigMap,
                       PVC, Deployment, Service)
overlays/example/      one wired-up overlay - bring your own image
                       tag, FQDN, IRSA role ARN, and secret material
```

## Constraints worth knowing up front

- **Single replica only.** Pacer is single-writer SQLite; the
  Deployment is pinned at `replicas: 1` and `strategy: Recreate` so
  the PVC never has two pods on it. Postgres-backed HA is on the
  polish roadmap; until it lands, do not raise the replica count.
- **IRSA on EKS.** The base ServiceAccount is annotated for IAM
  Roles for Service Accounts; the trust policy on the IAM role must
  match `system:serviceaccount:<namespace>:pacer`. See
  [docs/installation/server.md#kubernetes](https://yousysadmin.github.io/pacer/installation/server/#kubernetes)
  for the trust-policy JSON.
- **TLS at the edge.** The example overlay assumes an Ingress
  (cert-manager + nginx in the sample) terminates TLS. Pacer's
  `server.tls.mode` stays `none`. ACME-in-pod with `:80` works only
  when the cluster gives the pod a stable public IP, which is
  unusual.
- **`server.trusted_proxies` is required behind an Ingress.** The
  ingress controller terminates the TCP connection, so without the
  trusted-proxy list every request looks like it came from the same
  IP. The example overlay sets the standard private CIDRs
  (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) - check your
  cluster's pod CIDR (`kubectl cluster-info dump | grep -i cidr`)
  and tighten as appropriate.
- **`fsGroup: 65532`** is load-bearing. The distroless image runs as
  UID 65532, but a fresh PVC mount is owned by root unless `fsGroup`
  re-owns it.

## What's in the base

| File                  | Resource                                                                                               |
| --------------------- | ------------------------------------------------------------------------------------------------------ |
| `namespace.yaml`      | `Namespace/pacer` (overridable via overlay's `namespace:` field).                                      |
| `serviceaccount.yaml` | `ServiceAccount/pacer`. **Annotate via overlay** with the IRSA role ARN.                               |
| `configmap.yaml`      | `pacer-config` carrying `pacer.yaml`. Defaults are placeholders - patch via overlay.                  |
| `pvc.yaml`            | `pacer-data`, ReadWriteOnce, 1Gi. Bump via overlay if your retention is heavier.                       |
| `service.yaml`        | ClusterIP `pacer:80 -> http`.                                                                          |
| `deployment.yaml`     | `replicas: 1`, `strategy: Recreate`, `runAsNonRoot`, `fsGroup: 65532`, `readOnlyRootFilesystem: true`. |
| `kustomization.yaml`  | Aggregates the above with common labels.                                                               |

The base does **not** include Secrets or an Ingress - those are
inherently per-environment, so they live in the overlay.

## Using the example overlay

```bash
cd examples/kustomize/overlays/example

# 1. Edit kustomization.yaml - set the image tag + IRSA role ARN
#    annotation patch.
# 2. Edit secret-env.yaml + secret-ghapp.yaml - paste real values
#    (or replace these with external-secrets / sealed-secrets /
#    SOPS in production).
# 3. Edit configmap-patch.yaml - set server.public_url, github.app_id,
#    and tighten server.trusted_proxies to your cluster's pod CIDR.
# 4. Edit ingress.yaml - set the host + cert-manager issuer.

kubectl apply -k .

kubectl -n pacer logs -l app=pacer -f
```

First-start bootstrap prints the random admin password to stdout
once. Capture it from the logs before the pod restarts:

```bash
kubectl -n pacer logs -l app=pacer | grep -A2 'AUTH BOOTSTRAP'
```

## What this does NOT do

- **Manage AWS infra.** The IAM role + instance profile + AMI live in
  `../terraform/` and `../packer/`. The kustomize base only assumes
  IRSA is wired correctly.
- **Manage projects / pools / repos.** Those go through the Pacer
  console after the pod is running. Treat the UI as the source of
  truth for runtime config.
- **Ship a real secret store.** `overlays/example/secret-*.yaml` are
  placeholders. In production replace them with
  [external-secrets](https://external-secrets.io/),
  [sealed-secrets](https://github.com/bitnami-labs/sealed-secrets),
  or SOPS.
- **Build the image.** The repo's top-level `Dockerfile` does that;
  push to your registry and reference the tag from
  `overlays/example/kustomization.yaml`.

## Production hardening checklist

- Replace the inline Secrets with external-secrets / sealed-secrets /
  SOPS so cluster manifests stop being a credential store.
- Wire a `NetworkPolicy` allowing egress to `api.github.com:443`,
  AWS service endpoints in your region, and ingress only from the
  Ingress controller's namespace.
- Pin the image to an immutable digest (`@sha256:...`) rather than
  the floating tag in the example.
- Snapshot the PVC on a schedule - SQLite serializes WAL writes, so
  any consistent snapshot is restorable.
- Add a `PriorityClass` so the pod isn't evicted before workloads
  that depend on it (or, equivalently, place it on a dedicated
  node pool).
