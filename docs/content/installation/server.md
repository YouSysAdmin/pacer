---
title: "Server side"
description: "Build the binary, write the YAML config, and run the serve subcommand."
weight: 30
---

Pacer ships as a single binary with one runtime subcommand, `serve` (plus `version`). Everything else is configured
through the web UI.

## 1. Build

The repo ships a `Makefile`. From the repo root:

```bash
# frontend (Vue 3 SPA, embedded into the binary) + Go binary
make all

# binary lands at:
./bin/pacer
```

If you only need the binary (frontend already built):

```bash
make build
```

Version stamping uses `git describe`:

```bash
./bin/pacer version
# → pacer v0.x.y-...
```

## 2. Write the YAML config

Create `/etc/pacer/config.yaml`:

```yaml
server:
  addr: :3000
  public_url: https://runner.example.com
  # When fronted by a reverse proxy / ALB / Cloudflare that terminates
  # TLS upstream, list its CIDRs here so X-Forwarded-For is honored
  # ONLY for requests coming through one of them. Leave empty when the
  # tool is the public endpoint - empty rejects spoofed XFF outright.
  # trusted_proxies: [10.0.0.0/8, 127.0.0.1, ::1]
  tls:
    mode: none           # none | manual | self | acme
    # mode: manual
    # cert: /etc/pacer/tls/fullchain.pem
    # key:  /etc/pacer/tls/privkey.pem
    # mode: self
    # fqdn: runner.internal.example.com
    # alg: rsa           # rsa (default) | ed25519
    # mode: acme
    # acme:
    #   hosts: [runner.example.com]
    #   email: ops@example.com
    #   cache_dir: /var/lib/pacer/acme
    #   http_addr: ":80"

aws:
  region: us-east-1
  profile: ""

github:
  app_id: 123456
  private_key_path: /etc/pacer/gh-app.pem
  webhook_secret: <hex you set in step 1 of GitHub App setup>
  callback_hmac_secret: <separate hex - generate with openssl rand -hex 32>

database:
  engine: sqlite
  path: /var/lib/pacer/state.db

logging:
  level: info            # debug | info | warn | error
  format: json           # json | text
  output: stdout         # stdout | /absolute/file/path
  color: false           # ANSI level prefixes; only honored when format=text

retention:
  audit_days: 90         # how many days the audit_log table is kept
  webhook_days: 7        # how many days webhook_deliveries are kept
  job_log_days: 31       # how many days a failed job keeps its captured log

auth:
  disabled: false        # true keeps the legacy pre-auth open behavior
  jwt_secret: <hex - generate with openssl rand -hex 32>
  session_ttl: 12h       # Go duration string
  local:
    enabled: true
    email: ops@example.com   # the bootstrap user's email
  # OIDC SSO. When oidc.enabled is true, local login is auto-disabled
  # at startup -- local is for first-setup / break-glass only.
  oidc:
    enabled: false
    issuer: https://accounts.example.com
    client_id: pacer
    client_secret: <set via PACER_AUTH_OIDC_CLIENT_SECRET>
    redirect_url: https://pacer.example.com/api/auth/oidc/callback
    scopes: [ openid, email, profile ]    # optional; these are the defaults
    require_email_verified: true        # default; flip if your IdP doesn't surface the claim
    # Allowlist (any-of). All-empty = the IdP itself is the gate.
    allowed_domains: [ example.com ]
    allowed_emails: [ ops@example.com ]
    # Group allowlist. groups_claim names the JSON claim key holding
    # the group list -- "groups" for most IdPs, "cognito:groups" for
    # AWS Cognito, "roles" for some Keycloak setups.
    groups_claim: groups
    allowed_groups: [ pacer-admins ]
```

| Key                                     | What it is                                                                                                                                                                                     |
|-----------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `server.addr`                           | Listen address. The HTTP server serves the API + webhook + embedded SPA from a single port.                                                                                                    |
| `server.public_url`                     | The URL that's reachable from GitHub (webhook) and from spawned EC2 (runner callback). See AWS-side step 4.                                                                                    |
| `server.trusted_proxies`                | List of proxy IPs / CIDRs whose `X-Forwarded-For` is honored when sourcing the client IP. Empty (default) = use the direct peer's address only - right answer when the tool is the public endpoint. Set this to your LB / reverse-proxy CIDRs (e.g. `[10.0.0.0/8]`) when something else terminates TLS upstream so the rate limiter, audit log, and access log see the real client IP. |
| `server.tls.mode`                       | `none` (plain HTTP), `manual` (operator-supplied PEM), `self` (self-signed in-memory cert), `acme` (Let's Encrypt via autocert + HTTP-01).                                                     |
| `server.tls.cert` / `.key`              | (manual mode) PEM file paths. Must be readable by the running user.                                                                                                                            |
| `server.tls.fqdn`                       | (self mode) SubjectAltName for the in-memory cert; `localhost` is always added too.                                                                                                            |
| `server.tls.alg`                        | (self mode) `rsa` (default; widest client compat) or `ed25519`.                                                                                                                                |
| `server.tls.acme.hosts`                 | (acme mode) FQDN whitelist. At least one. Anything else is refused — protects the rate limit.                                                                                                  |
| `server.tls.acme.email`                 | (acme mode) Optional but recommended; LE uses it for renewal-failure notifications.                                                                                                            |
| `server.tls.acme.cache_dir`             | (acme mode) Where issued certs + private keys live between restarts. Defaults to `./certs` — give it a persistent path in production.                                                          |
| `server.tls.acme.http_addr`             | (acme mode) HTTP-01 challenge listener. Defaults to `:80`. Must be reachable from the Internet at the apex `http://<host>/.well-known/...`.                                                    |
| `aws.region`                            | Single region for all pools. Cannot be overridden per project today.                                                                                                                           |
| `aws.profile`                           | Empty = SDK default chain (instance profile, env vars, `~/.aws/credentials`). Named profile = use that profile.                                                                                |
| `github.app_id`                         | App ID from the App settings page (step 4.1 of GitHub-side setup).                                                                                                                             |
| `github.private_key_path`               | Path to the App's `.pem` (step 4.2). The running user must have read access. `0400` is recommended.                                                                                            |
| `github.webhook_secret`                 | The hex you set as the App's Webhook secret. GitHub HMAC-signs every webhook with this; the tool verifies it.                                                                                  |
| `github.callback_hmac_secret`           | **NOT a GitHub setting.** Tool-side secret used to sign runner self-registration tokens. Generate fresh.                                                                                       |
| `database.engine`                       | `sqlite` is the only shipped engine today. Postgres / MySQL are on the polish roadmap.                                                                                                         |
| `database.path`                         | SQLite file path. The tool runs goose migrations on startup.                                                                                                                                   |
| `logging.level`                         | `debug` / `info` / `warn` / `error`. Default `info`.                                                                                                                                           |
| `logging.format`                        | `json` (default — production) or `text` (human-readable for dev).                                                                                                                              |
| `logging.output`                        | `stdout` (default) or an absolute file path.                                                                                                                                                   |
| `logging.color`                         | Adds ANSI level prefixes; only honored when `format=text`.                                                                                                                                     |
| `retention.audit_days`                  | Default retention for `audit_log` rows. The daily pruner deletes anything older. Default `90`. Overridable at runtime in **Settings → Log retention** (UI override persists in the `settings` table and trumps this value at the next sweep). Range `1..3650`. |
| `retention.webhook_days`                | Default retention for `webhook_deliveries` rows (dedup + redelivery debug trail). Default `7`. Same override semantics as `audit_days`. Range `1..365`.                                        |
| `retention.job_log_days`                | Default retention for the bootstrap log captured on a **failed** job (up to 64 KiB each). Default `31`. Past this age the pruner clears the log and **keeps the job row**, so cost and runtime history stay intact. Same override semantics as `audit_days`. Range `1..365`. |
| `auth.disabled`                         | `true` keeps the pre-auth posture (deploy behind a private network or reverse-proxy auth). When `false`, the operator console is gated.                                                        |
| `auth.jwt_secret`                       | Required when `!auth.disabled`. **Must be at least 32 characters.** Generate with `openssl rand -hex 32`. Rotating this invalidates every live session.                                        |
| `auth.session_ttl`                      | Cookie lifetime. Empty defaults to `12h`. Validated at startup — typos like `12hours` fail-fast.                                                                                               |
| `auth.local.enabled`                    | Turns on email + password local login. The bootstrap flow runs at first start when both `local.enabled` and an empty `users` table coincide. **Auto-disabled when `auth.oidc.enabled: true`.** |
| `auth.local.email`                      | The bootstrap user's email. Lower-cased + trimmed at config-load time so casing in YAML doesn't cause a lookup miss.                                                                           |
| `auth.oidc.enabled`                     | Turns on the OIDC Authorization Code + PKCE flow. Local login is auto-disabled when this is on. Required peers: `issuer`, `client_id`, `client_secret`, `redirect_url`.                        |
| `auth.oidc.issuer`                      | The IdP's issuer URL (e.g. `https://accounts.google.com`, `https://oauth.id.jumpcloud.com/`, `https://login.microsoftonline.com/<tenant>/v2.0`). Must include `http://` or `https://` and a host -- bare identifiers are rejected at startup. Discovery (`/.well-known/openid-configuration`) happens at startup. Must NOT share a host with `redirect_url` (issuer is the IdP, redirect_url is pacer). |
| `auth.oidc.client_id` / `client_secret` | App registration on the IdP. Prefer `PACER_AUTH_OIDC_CLIENT_SECRET=...` env var to keep the secret out of YAML.                                                                                |
| `auth.oidc.redirect_url`                | Callback URL the IdP redirects back to. Must be `<server.public_url>/api/auth/oidc/callback` and registered with the IdP exactly.                                                              |
| `auth.oidc.scopes`                      | OIDC scopes; defaults to `[openid, email, profile]`. Add `groups` (or whatever your IdP uses) if you set `allowed_groups`.                                                                     |
| `auth.oidc.require_email_verified`      | Reject sign-ins where the ID token's `email_verified` is false. Default `true`. Disable only for IdPs that don't surface the claim.                                                            |
| `auth.oidc.allowed_emails`              | Explicit allowlist of email addresses (e.g. `[ops@example.com]`). Most-specific match — when an entry matches, other allowlists are skipped. All-empty allowlists pass automatically (the IdP is the gate). Entries must be fully-qualified addresses; bare `@example.com`-style entries are rejected at startup — use `allowed_domains` for that. |
| `auth.oidc.allowed_domains`             | Domain allowlist (e.g. `[example.com]`). The user's email must end in `@<domain>` (case-insensitive).                                                                                          |
| `auth.oidc.groups_claim`                | JSON claim key the IdP returns groups under. Varies: `groups` (Okta / Auth0 / Keycloak / JumpCloud / Entra ID), `cognito:groups` (Cognito), `roles` (some Keycloak setups). Required when `allowed_groups` is set. |
| `auth.oidc.allowed_groups`              | Group allowlist. The user's group list (read from `groups_claim`) must intersect this list. Match is case-insensitive on both sides — `Admins` in YAML matches `ADMINS` from Cognito or `admins` from Okta. |

> ⚠️ `github.webhook_secret` and `github.callback_hmac_secret` are **two different secrets** that happen to share a
> HMAC-SHA256 shape. The first is GitHub-side (configured on github.com); the second is tool-side (generate with
`openssl rand -hex 32`).

### Env-var overrides

Any config key can be overridden with `RUNNER_<UPPERCASE_DOTTED_KEY>`:

```bash
PACER_GITHUB_WEBHOOK_SECRET=$(cat /run/secrets/gh-webhook) \
PACER_DATABASE_PATH=/data/state.db \
./bin/pacer serve --config /etc/pacer/config.yaml
```

This is the recommended path for long-lived secrets — they don't have to live in YAML.

## 3. Filesystem layout on the host

```
/etc/pacer/
├── config.yaml             # the YAML above (mode 0640)
└── gh-app.pem              # the GitHub App private key (mode 0400)

/var/lib/pacer/
└── state.db                # sqlite (created on first run)
```

Make sure the user that runs the tool can:

- read both files in `/etc/pacer/`
- read+write the directory in `/var/lib/pacer/`

## 4. Run

```bash
./bin/pacer serve --config /etc/pacer/config.yaml
```

A typical systemd unit:

```ini
[Unit]
Description=Pacer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pacer
Group=pacer
ExecStart=/usr/local/bin/pacer serve --config /etc/pacer/config.yaml
Restart=on-failure
RestartSec=2s

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/pacer

[Install]
WantedBy=multi-user.target
```

One process, one PID — no supervisord-style config needed.

## 5. Smoke-test

```bash
# health check
curl -fsS http://127.0.0.1:3000/healthz
# → ok

# version
./bin/pacer version
```

Then go to GitHub App settings → **Advanced** → **Recent Deliveries** and trigger a `ping` — confirm the tool returns
200.

After that, head into the web UI to create your first project, pool, and repo binding. (UI walkthrough is on the polish
roadmap; for now the pages are self-explanatory: `/projects`, `/pools`, `/repos`, `/jobs`.)

## Docker

The shipped `Dockerfile` is intentionally minimal: it wraps a pre-built `pacer` Linux binary in
`gcr.io/distroless/static-debian12:nonroot` (UID 65532, port `:3000`, SQLite + WAL/SHM under `/data`). Compilation is
done **on the host** by the Makefile or by the goreleaser release pipeline; the Dockerfile itself never invokes the Go
or bun toolchains. This keeps image builds quick and makes the released image bit-identical to the binary in the
GitHub release archive.

### Build a dev image

```bash
make build-docker
# -> tags pacer:dev (linux/<host-arch>)

# Cross-arch dev image:
make build-docker DOCKER_GOARCH=amd64
make build-docker DOCKER_GOARCH=arm64

# Custom tag:
make build-docker DOCKER_IMAGE=ghcr.io/yousysadmin/pacer:dev
```

In order: builds the SPA, cross-compiles the Linux binary into `bin/docker/`, then runs `docker build` against that
directory.

### Release images

Tagged releases publish multi-arch images to GHCR via goreleaser:

- `ghcr.io/yousysadmin/pacer:<version>` (multi-arch manifest)
- `ghcr.io/yousysadmin/pacer:latest`

The release workflow cross-compiles the binary natively for each platform (no QEMU emulation), so amd64 and arm64
builds finish in seconds rather than the 10-15 minutes a from-source-in-Docker build would take.

### Run

Mount the YAML config + GitHub App PEM read-only, and persist `/data`:

```bash
docker run -d --name pacer \
  -p 3000:3000 \
  -v pacer-data:/data \
  -v $(pwd)/pacer.yaml:/etc/pacer/pacer.yaml:ro \
  -v $(pwd)/gh-app.pem:/etc/pacer/gh-app.pem:ro \
  -e PACER_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
  -e PACER_GITHUB_WEBHOOK_SECRET=<hex> \
  -e PACER_GITHUB_CALLBACK_HMAC_SECRET=$(openssl rand -hex 32) \
  ghcr.io/yousysadmin/pacer:latest
```

Inside `pacer.yaml`, paths must point at the in-container locations:

- `database.path: /data/state.db`
- `github.private_key_path: /etc/pacer/gh-app.pem`

Logs go to stdout — `docker logs -f pacer`.

### Volume ownership

The image runs as `65532:65532`. **Named volumes** (`docker volume create`) inherit the right perms automatically.
**Bind-mounted host directories** must be pre-chowned:

```bash
mkdir -p /srv/pacer-data && chown 65532:65532 /srv/pacer-data
docker run -v /srv/pacer-data:/data ...
```

### docker-compose

```yaml
services:
  pacer:
    image: ghcr.io/yousysadmin/pacer:latest
    container_name: pacer
    ports:
      - "3000:3000"
    volumes:
      - pacer-data:/data
      - ./pacer.yaml:/etc/pacer/pacer.yaml:ro
      - ./gh-app.pem:/etc/pacer/gh-app.pem:ro
    environment:
      PACER_AUTH_JWT_SECRET: ${PACER_AUTH_JWT_SECRET}
      PACER_GITHUB_WEBHOOK_SECRET: ${PACER_GITHUB_WEBHOOK_SECRET}
      PACER_GITHUB_CALLBACK_HMAC_SECRET: ${PACER_GITHUB_CALLBACK_HMAC_SECRET}
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/app/pacer", "version"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  pacer-data:
```

Pair with a `.env` file (gitignored) for the three `PACER_*` secrets.

### AWS credentials in Docker

The container picks up AWS credentials from the standard SDK chain. Three common shapes:

- **EC2 host with an instance profile** — nothing to mount; the SDK reads IMDSv2 from the container. Requires the
  container to be on the host network or to allow egress to `169.254.169.254`.
- **Long-lived access keys** — `-e AWS_ACCESS_KEY_ID=... -e AWS_SECRET_ACCESS_KEY=...`. Fine for dev; avoid in
  production.
- **`~/.aws/credentials` file** — `-v $HOME/.aws:/home/nonroot/.aws:ro -e AWS_PROFILE=pacer`. Useful for local testing
  against a real AWS account.

## Kubernetes

Pacer is **single-writer SQLite** — run **exactly one replica**. No HPA, no rolling updates that briefly attach the PVC
to two pods. On EKS, use **IRSA** (IAM Roles for Service Accounts) so the pod reads AWS credentials from a projected
service-account token instead of long-lived access keys.

> The HA story is the Postgres backend on the polish roadmap. Until that lands, treat Pacer as a singleton service.

### Layout

A minimal kustomize tree:

```
deploy/k8s/
├── kustomization.yaml
├── namespace.yaml
├── serviceaccount.yaml          # annotated for IRSA
├── configmap.yaml               # pacer.yaml
├── secret.yaml                  # gh-app.pem + PACER_* env secrets
├── pvc.yaml                     # ReadWriteOnce, ~1Gi for SQLite
├── deployment.yaml
├── service.yaml
└── ingress.yaml                 # or HTTPRoute / LoadBalancer Service
```

### IAM role + IRSA trust policy

Create the IAM role that the pod will assume. The trust policy ties one specific `(namespace, serviceaccount)` pair to
the cluster's OIDC provider:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::<ACCOUNT_ID>:oidc-provider/oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>:sub": "system:serviceaccount:pacer:pacer",
        "oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>:aud": "sts.amazonaws.com"
      }
    }
  }]
}
```

Attach the policy from [`docs/iam-role.json`](https://github.com/yousysadmin/pacer/blob/main/docs/iam-role.json) to this
role. The `iam:PassRole` Resource scope still applies — it must list only the runner instance profile ARN, even when
Pacer itself is running in-cluster.

```bash
# OIDC provider ID for the cluster
aws eks describe-cluster --name <cluster> --query "cluster.identity.oidc.issuer" --output text

# Role + policy
aws iam create-role --role-name pacer --assume-role-policy-document file://trust.json
aws iam put-role-policy --role-name pacer --policy-name pacer --policy-document file://docs/iam-role.json
```

### ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pacer
  namespace: pacer
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::<ACCOUNT_ID>:role/pacer
```

Leave `aws.profile` empty in the YAML; IRSA fills the default credential chain.

### ConfigMap (`pacer.yaml`)

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pacer-config
  namespace: pacer
data:
  pacer.yaml: |
    server:
      addr: :3000
      public_url: https://pacer.example.com
      tls:
        mode: none                  # terminate at the Ingress / LoadBalancer
    aws:
      region: us-east-1
      profile: ""                   # IRSA fills the default chain
    github:
      app_id: 123456
      private_key_path: /etc/pacer/gh-app.pem
    database:
      engine: sqlite
      path: /data/state.db
    auth:
      disabled: false
      session_ttl: 12h
      local:
        enabled: true
        email: ops@example.com
    logging:
      level: info
      format: json
      output: stdout
```

Long-lived secrets (JWT signing key, webhook HMAC, callback HMAC, OIDC client secret) stay **out** of the ConfigMap —
deliver them via env vars from a Secret.

### Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pacer-env
  namespace: pacer
type: Opaque
stringData:
  PACER_AUTH_JWT_SECRET: <hex - openssl rand -hex 32>
  PACER_GITHUB_WEBHOOK_SECRET: <hex from the GitHub App webhook secret>
  PACER_GITHUB_CALLBACK_HMAC_SECRET: <hex - openssl rand -hex 32>
---
apiVersion: v1
kind: Secret
metadata:
  name: pacer-ghapp
  namespace: pacer
type: Opaque
stringData:
  gh-app.pem: |
    -----BEGIN RSA PRIVATE KEY-----
    ...paste the App private key here...
    -----END RSA PRIVATE KEY-----
```

For production, deliver these via [external-secrets](https://external-secrets.io/),
[sealed-secrets](https://github.com/bitnami-labs/sealed-secrets), or SOPS. The plain `Secret` above is for the
walkthrough only.

### PVC + Service

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: pacer-data
  namespace: pacer
spec:
  accessModes: [ ReadWriteOnce ]
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: pacer
  namespace: pacer
spec:
  selector:
    app: pacer
  ports:
    - name: http
      port: 80
      targetPort: http
```

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pacer
  namespace: pacer
spec:
  replicas: 1                       # single-writer SQLite -- never scale up
  strategy:
    type: Recreate                  # never two pods on the PVC at once
  selector:
    matchLabels:
      app: pacer
  template:
    metadata:
      labels:
        app: pacer
    spec:
      serviceAccountName: pacer
      securityContext:
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532              # re-owns the PVC mount on attach
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: pacer
          image: ghcr.io/yousysadmin/pacer:latest
          args: [ "serve", "--config", "/etc/pacer/pacer.yaml" ]
          ports:
            - { name: http, containerPort: 3000 }
          envFrom:
            - secretRef:
                name: pacer-env
          volumeMounts:
            - { name: config, mountPath: /etc/pacer/pacer.yaml, subPath: pacer.yaml, readOnly: true }
            - { name: ghapp,  mountPath: /etc/pacer/gh-app.pem, subPath: gh-app.pem, readOnly: true }
            - { name: data,   mountPath: /data }
          readinessProbe:
            httpGet: { path: /healthz, port: http }
            periodSeconds: 5
          livenessProbe:
            httpGet: { path: /healthz, port: http }
            periodSeconds: 30
            failureThreshold: 3
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits:   { cpu: 1,    memory: 512Mi }
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ "ALL" ]
      volumes:
        - name: config
          configMap:
            name: pacer-config
        - name: ghapp
          secret:
            secretName: pacer-ghapp
            defaultMode: 0400
        - name: data
          persistentVolumeClaim:
            claimName: pacer-data
```

### Ingress

The Ingress (or HTTPRoute / LoadBalancer Service) terminates TLS and points GitHub at it via the App's webhook URL. Set
`server.public_url` to the same FQDN.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: pacer
  namespace: pacer
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
    - hosts: [ pacer.example.com ]
      secretName: pacer-tls
  rules:
    - host: pacer.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: pacer
                port: { number: 80 }
```

### kustomization.yaml

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: pacer
resources:
  - namespace.yaml
  - serviceaccount.yaml
  - configmap.yaml
  - secret.yaml
  - pvc.yaml
  - deployment.yaml
  - service.yaml
  - ingress.yaml
images:
  - name: ghcr.io/yousysadmin/pacer
    newTag: latest
```

Apply:

```bash
kubectl apply -k deploy/k8s/
kubectl logs -n pacer -l app=pacer -f
kubectl port-forward -n pacer svc/pacer 3000:80   # local smoke-test
```

### Notes

- **One replica only.** No HPA. No `PodDisruptionBudget` with `minAvailable: > 0` that would block a `Recreate`-style
  restart.
- **`strategy: Recreate`** keeps the PVC attached to one pod at a time. With `RollingUpdate` on a `ReadWriteOnce` volume,
  the new pod will fail to schedule until the old one detaches; that just looks like a slow restart.
- **`fsGroup: 65532`** is load-bearing — the distroless image runs as UID 65532, but a fresh PVC mount is owned by root
  unless you set `fsGroup`.
- **`readOnlyRootFilesystem: true`** is fine — Pacer only writes to `/data`. Bootstrap-password output goes to stdout.
- **First-start bootstrap.** The plaintext bootstrap password lands in pod logs once. Capture it before the pod
  restarts (or wipe the user row in SQLite and restart, see "First-start bootstrap" above).
- **`public_url` matches the Ingress host.** The webhook URL on the GitHub App settings page must match too — pacer
  doesn't rewrite it.
- **TLS at the edge.** Use `server.tls.mode: none` and let the Ingress / LoadBalancer terminate. ACME-in-pod with
  `:80` works only if the cluster gives the pod a stable public IP, which is unusual.

## TLS

The tool can terminate TLS in-process via `server.tls.mode`:

| Mode     | Use it when                                                                                                  |
|----------|--------------------------------------------------------------------------------------------------------------|
| `none`   | You have a load balancer / reverse proxy in front that terminates TLS upstream.                              |
| `manual` | You have your own PEM cert + key (e.g. corporate CA) and don't want autocert.                                |
| `self`   | Dev / private deployments where the cert won't be validated by external clients. SAN = `fqdn` + `localhost`. |
| `acme`   | Production with public DNS pointing at the host. Let's Encrypt issues a cert via HTTP-01 on `:80`.           |

ACME notes:

- The HTTP-01 listener (`server.tls.acme.http_addr`, default `:80`) must be reachable from the Internet at
  `http://<host>/.well-known/acme-challenge/...`. Open the firewall accordingly.
- Persist `server.tls.acme.cache_dir` to a real volume (not an ephemeral tmpfs / container layer). Otherwise every
  restart re-requests certs and you'll burn through Let's Encrypt's rate limits.
- The HTTP listener also serves as a redirect: any non-challenge request is 308-redirected to the HTTPS URL,
  preserving the request method (a misdirected `POST /api/...` to port 80 won't get rewritten to a `GET`).
- Behind an ALB or NLB doing TLS termination, set `mode: none`. ACME is for terminating in-process.

### Behind a reverse proxy

When something else terminates TLS upstream (ALB, Cloudflare, Nginx, Caddy, an Ingress controller), set
`server.trusted_proxies` to the proxy's IPs / CIDRs. Without it, Pacer sees the proxy address as the client and the
rate limiter, audit log, and access log all attribute every request to the same IP — a spam of failed logins from one
operator looks identical to a spam from a thousand attackers.

```yaml
server:
  trusted_proxies:
    - 10.0.0.0/8
    - 127.0.0.1
    - ::1
```

The list is strict: an `X-Forwarded-For` header from a non-listed peer is ignored, so an attacker who reaches the
process directly cannot spoof their source by setting the header themselves. Leave the list empty when the tool is
the public endpoint.

## Auth posture

When `auth.disabled: false`, the operator console is gated by a session cookie. The webhook from GitHub and the
callbacks from spawned runners stay HMAC-only regardless — they don't accept session cookies.

### First-start bootstrap

On first start with `auth.local.enabled: true` and an empty `users` table, the tool mints one user with the email from
`auth.local.email` and a random 16-char password, then logs the **plaintext password to stderr ONCE**:

```
========================================================
AUTH BOOTSTRAP: created user ops@example.com
  password: aB3xY9k2pQ7mN4vL
  (this is the only time the password is shown)
========================================================
```

Copy it before that message scrolls off — there is no recovery path. Subsequent starts find the user and no-op (the
message doesn't print again).

If you lose the password, wipe the row and restart:

```bash
sqlite3 /var/lib/pacer/state.db "DELETE FROM users WHERE email = 'ops@example.com';"
systemctl restart pacer
```

### Cookie + Bearer

The session is a `pacer_session` cookie, `HttpOnly + SameSite=Strict + Secure` (when served over HTTPS), TTL =
`auth.session_ttl` (default 12h). For `curl` callers, the same token is accepted via `Authorization: Bearer <token>`.

### Authorization tier

v1 auth is **in or out**: every authenticated, non-disabled user has full access to every protected route. The
`User.Role` and `User.SuperUser` fields exist on the model and table for forward-compat (so the schema doesn't churn
when role gating lands), but no handler enforces them today. If you need least-privilege gating before that lands, do
it at the **IdP / allowlist layer** (`auth.oidc.allowed_groups`) rather than hand-rolling an admin check in a single
handler - the rest of the surface won't honor it and that creates a misleading "this is gated" signal.

For the same reason, the first user provisioned (local bootstrap or first OIDC sign-in) is set to `role=admin`
automatically; subsequent JIT-provisioned OIDC users default to `role=user`. The eventual upgrade to enforced role
gating is a no-op for the operator who set up the install.

### Rotating secrets

- **JWT secret leak / suspicion:** rotate `auth.jwt_secret` and restart. Every live session is invalidated immediately.
- **GitHub webhook secret:** update `github.webhook_secret` in YAML AND on the GitHub App settings page in lock-step.
  In-flight deliveries between the two changes will fail HMAC verify; GitHub retries.
- **GitHub App private key:** generate a new one in App settings; replace the `.pem` at `github.private_key_path`;
  restart. Old keys are revoked instantly on GitHub's side.

### Pre-auth deploys (auth.disabled: true)

The legacy posture is still supported for private-network deployments:

- Deploy behind a **private network** (VPN, Tailscale, ZeroTier, Cloudflare Tunnel), or
- Front the tool with a **reverse proxy that does auth** (Nginx + basic auth, oauth2-proxy, Caddy + forward auth).

When `auth.disabled: true` the middleware short-circuits to no-op; the project / pool / repo / job / stats pages are
open. Don't expose this configuration to the public Internet.

### OIDC SSO

Pacer uses standard OpenID Connect (Authorization Code flow with PKCE). Discovery happens at startup, so issuer-side
typos or outages fail fast instead of surfacing on the first sign-in.

#### User provisioning

On each sign-in, Pacer looks up the user first by the IdP's stable `sub` claim, then by email. If a local user with
that email already exists, the row is linked to the IdP on first sign-in (so an existing local admin keeps their
account). If neither matches, a new user row is created automatically.

The very first user provisioned (whether local-bootstrap or first OIDC sign-in) is set to `admin`; subsequent users
default to `user`. Today the middleware is "in or out" — every authenticated user has full access — so the role
distinction is descriptive metadata only, kept on the schema for when role gating lands.

Removing access:

- Disable the user in the IdP — the next sign-in fails at the IdP.
- Or disable the user inside Pacer — the next callback short-circuits with an access-denied page.

#### Allowlist surface

All four allowlists are independent; an empty list disables that check. **All-empty means the IdP is the gate** -- any
user it authenticates is admitted.

| Field                    | Effect                                                                                       |
|--------------------------|----------------------------------------------------------------------------------------------|
| `require_email_verified` | Default true; rejects sign-ins where `email_verified=false` on the ID token.                 |
| `allowed_emails`         | Most-specific match. When an entry matches, the other allowlists are skipped.                |
| `allowed_domains`        | Email must end in `@<domain>` (case-insensitive).                                            |
| `allowed_groups`         | The user's groups (read from the operator-supplied `groups_claim`) must intersect this list. |

#### Coexistence with local auth

When `auth.oidc.enabled: true`, `Validate()` auto-disables `auth.local.enabled` at startup. Local is only useful for *
*first-time setup** (mint a bootstrap admin, log in, configure the rest) or **break-glass recovery** (flip the YAML and
restart if the IdP is unreachable). Both methods are never simultaneously open in production.

#### Audit events

Sign-in attempts (success and failure) are recorded in the audit log. The user-facing message is always a generic
"Access denied" so the failure mode doesn't leak which leg of the allowlist refused — the specific cause shows in
the audit log instead.

#### Tested IdPs

The flow is standard OIDC and should work with any conformant provider. Common ones, with the issuer URL shape and any
provider-specific gotchas:

| Provider               | `issuer`                                                          | `groups_claim`     | Notes                                                                                                                                                                  |
|------------------------|-------------------------------------------------------------------|--------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Okta**               | `https://<org>.okta.com/oauth2/default`                          | `groups`           | Add the `groups` scope on the Okta app config; the `default` authorization server emits it.                                                                            |
| **Auth0**              | `https://<tenant>.auth0.com/`                                     | `groups`           | Configure a custom claim via Auth0 Action / Rule to surface `groups` in the ID token (Auth0 doesn't ship the claim by default).                                        |
| **Google Workspace**   | `https://accounts.google.com`                                     | -                  | No groups in the ID token. Use `allowed_domains` (your Workspace domain) and / or `allowed_emails` for gating.                                                         |
| **AWS Cognito**        | `https://cognito-idp.<region>.amazonaws.com/<region>_<poolID>`   | `cognito:groups`   | Use the pool **ID** (e.g. `us-east-1_abc123`), not the pool name. Get it from `aws cognito-idp list-user-pools --max-results 50` or the console.                       |
| **Keycloak**           | `https://<host>/realms/<realm>`                                   | `groups` (default) | Some setups expose `roles` instead - check what your client mappers emit. Add a "Group Membership" mapper if `groups` is empty.                                        |
| **Azure AD / Entra ID** | `https://login.microsoftonline.com/<tenant>/v2.0`                | `groups`           | Groups arrive as object IDs (UUIDs) by default. Either configure the app registration to emit group **names** in the token, or list the UUIDs in `allowed_groups`.     |
| **JumpCloud**          | `https://oauth.id.jumpcloud.com/`                                 | `groups`           | Add `groups` to `scopes` (default `[openid, email, profile]` doesn't carry it). Bind the JumpCloud user group to the OIDC application or users hit consent and stop.   |

The issuer URL is what pacer fetches `<issuer>/.well-known/openid-configuration` from -- the IdP's URL, never your
own. Pointing `issuer` at pacer's own URL fails fast at startup with a clear error since validation rejects matching
`issuer` and `redirect_url` hosts.

## Cost tracking

When the tool can talk to the AWS Pricing API, it stamps a launch-time USD/hour quote on every spawned instance and
multiplies it by elapsed time at completion / fail / reap. Two places surface the result:

- The **Stats** page rolls cost up by project, pool, or repo over a chosen date range.
- The **Top users** panel on the same page shows who's driving the bill (by GitHub sender on the workflow run).

This feature requires `pricing:GetProducts` and `ec2:DescribeSpotPriceHistory` in the IAM policy. Drop those statements
if you don't want cost estimates — Pacer logs a warning and stamps NULL prices, and the rollup skips those rows.

Estimates are **best-effort** — launch-time price * elapsed time, ignoring spot-price drift, EBS, and data-transfer.
They are not authoritative for billing.

## Logging

`logging.format: json` (default) is the right choice for production — every log line is structured. `text` is for dev.
Levels are `debug` / `info` / `warn` / `error`.

Behind a load balancer or systemd, prefer JSON to stdout and let the supervisor capture it:

```bash
journalctl -u pacer -f
journalctl -u pacer --since "1 hour ago" -o json | jq 'select(.PRIORITY <= "4")'
```

Override anything from the environment without editing YAML:

```bash
PACER_LOGGING_LEVEL=debug \
PACER_LOGGING_FORMAT=text \
./bin/pacer serve --config /etc/pacer/config.yaml
```

## Network: ports and outbound paths

Inbound:

- **`server.addr`** (default `:3000`) — operator console + GitHub webhook + runner self-registration. Front with TLS via
  `server.tls.mode` or a reverse proxy.
- **`:80`** — only when `server.tls.mode: acme`. The HTTP-01 challenge listener.

Outbound (the tool host needs to reach):

- `api.github.com:443` — App auth, runner registration, latest-release lookup.
- `api.pricing.us-east-1.amazonaws.com:443` (and other AWS service endpoints in `aws.region`).
- `acme-v02.api.letsencrypt.org:443` — only when `server.tls.mode: acme`.

Outbound from spawned EC2 instances:

- `<server.public_url>/api/runner/{register,complete,error}` — runner self-registration + completion + bootstrap-failure
  capture.
- `api.github.com:443` and `objects.githubusercontent.com:443` — GitHub Actions runner protocol + binary download.
- Whatever the workflow itself needs (S3, ECR, etc.).

