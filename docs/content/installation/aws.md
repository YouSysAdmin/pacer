---
title: "AWS side"
description: "IAM policy for the tool's role, the runner-instance role for PassRole, and the AMI / VPC prerequisites for the launch templates the tool will materialize."
weight: 20
---

The tool runs under **one assumed IAM role**. That role needs permission to:

- create launch templates that carry the `gha:managed-by=pacer` tag, and modify only the launch templates that already
  carry it
- describe images, subnets, security groups (so it can validate user input before persisting a pool)
- describe spot price history (for the at-launch cost snapshot — optional, see below)
- read on-demand pricing from `pricing:GetProducts` (also optional, see below)
- run instances from a tagged launch template, producing instances + volumes that are themselves tagged
- terminate instances **only when they carry the tool's `gha:managed-by` tag** (the privilege-escalation gate)
- pass exactly one role to EC2 (the runner-instance role, not a powerful one)

The tag-based scoping is **defense-in-depth**: even if the credentials leak, the role can only modify resources the tool
itself created (or attaches the tag to during creation).

## 1. Pick the runner-instance role

Spawned EC2 instances **may** have their own instance profile — the role *they* run under, separate from the tool's
role.

> **This step is optional.** If your CI workflows are self-contained (no S3 / ECR / Secrets Manager / Parameter Store /
> cross-account hops from inside the runner), you can launch instances with **no instance profile at all**. The pool's "
> IAM instance profile" field accepts blank, and the orchestrator's `iam:PassRole` permission isn't exercised. Skip the
> rest of this section and go straight to step 2 with the role field blank in the policy generator.

What the runner-instance role needs depends on what your CI does. At minimum, it needs whatever the workflows themselves
want (S3 access, ECR pulls, etc). The GitHub Actions runner binary is downloaded from GitHub releases at boot — that's a
public download and doesn't need any AWS permission.

A minimal runner-instance role is **no AWS permissions**, just a trust policy that allows EC2 to assume it. This is the
safest default — if a workflow needs AWS access, attach the specific permissions it requires.

The tool's IAM policy scopes `iam:PassRole` to **this role only**. Pick the role name now; you'll plug it into the
policy template in step 2.

> **Role vs. instance profile.** EC2 launches instances with an *instance profile*, which is a separate IAM object that
> wraps the role. They commonly share the same name, but they're not the same thing. After creating the role, run
`aws iam create-instance-profile --instance-profile-name <name>` and
`aws iam add-role-to-instance-profile --instance-profile-name <name> --role-name <name>` so the wrapper exists.

## 2. Apply the tool's IAM policy

Easiest path: open the **[interactive IAM policy builder](../iam-policy-builder/)**, fill in your account ID + region (
and runner-instance role name, if you decided to use one in step 1), and copy or download the generated JSON. The
builder is a static page — values never leave your browser.

If you'd rather work from the canonical template directly, it ships with the repo at [
`docs/iam-role.json`](https://github.com/yousysadmin/pacer/blob/main/docs/iam-role.json) — copy that file rather than
transcribing the JSON below, since the canonical version is the one we keep in sync with the actual AWS calls the binary
makes.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DescribeForValidation",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeImages",
        "ec2:DescribeSubnets",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSpotPriceHistory"
      ],
      "Resource": "*"
    },
    {
      "Sid": "DescribeInstancesForReaper",
      "Effect": "Allow",
      "Action": "ec2:DescribeInstances",
      "Resource": "*"
    },
    {
      "Sid": "ReadOnDemandPricing",
      "Effect": "Allow",
      "Action": "pricing:GetProducts",
      "Resource": "*"
    },
    {
      "Sid": "ValidateInstanceProfileAtPoolSave",
      "Effect": "Allow",
      "Action": "iam:GetInstanceProfile",
      "Resource": "arn:aws:iam::REPLACE_ACCOUNT_ID:instance-profile/*"
    },
    {
      "Sid": "CreateTaggedLaunchTemplate",
      "Effect": "Allow",
      "Action": "ec2:CreateLaunchTemplate",
      "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:launch-template/*",
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "ModifyOnlyOurLaunchTemplates",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateLaunchTemplateVersion",
        "ec2:ModifyLaunchTemplate",
        "ec2:DeleteLaunchTemplate",
        "ec2:DeleteLaunchTemplateVersions"
      ],
      "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:launch-template/*",
      "Condition": {
        "StringEquals": {
          "aws:ResourceTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "RunInstancesReadOnlyResources",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": [
        "arn:aws:ec2:REPLACE_AWS_REGION::image/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:subnet/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:security-group/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:network-interface/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:key-pair/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:placement-group/*"
      ]
    },
    {
      "Sid": "RunInstancesFromOurLaunchTemplate",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:launch-template/*",
      "Condition": {
        "StringEquals": {
          "aws:ResourceTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "RunInstancesTaggedInstanceAndVolume",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": [
        "arn:aws:ec2:REPLACE_AWS_REGION:*:instance/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:volume/*"
      ],
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "CreateFleetFromOurLaunchTemplate",
      "Effect": "Allow",
      "Action": "ec2:CreateFleet",
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "TagOnCreate",
      "Effect": "Allow",
      "Action": "ec2:CreateTags",
      "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:*",
      "Condition": {
        "StringEquals": {
          "ec2:CreateAction": [
            "RunInstances",
            "CreateFleet",
            "CreateLaunchTemplate",
            "CreateLaunchTemplateVersion"
          ],
          "aws:RequestTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "TagAfterFleetLaunch",
      "Effect": "Allow",
      "Action": "ec2:CreateTags",
      "Resource": [
        "arn:aws:ec2:REPLACE_AWS_REGION:*:instance/*",
        "arn:aws:ec2:REPLACE_AWS_REGION:*:volume/*"
      ],
      "Condition": {
        "StringEquals": {
          "aws:ResourceTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "TerminateOnlyOurInstances",
      "Effect": "Allow",
      "Action": "ec2:TerminateInstances",
      "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:instance/*",
      "Condition": {
        "StringEquals": {
          "aws:ResourceTag/gha:managed-by": "pacer"
        }
      }
    },
    {
      "Sid": "PassRunnerInstanceProfile",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": "arn:aws:iam::REPLACE_ACCOUNT_ID:role/REPLACE_RUNNER_INSTANCE_ROLE",
      "Condition": {
        "StringEquals": {
          "iam:PassedToService": "ec2.amazonaws.com"
        }
      }
    }
  ]
}
```

Replace the placeholders before applying:

| Placeholder                    | What to put                                                                                                                                     |
|--------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `REPLACE_AWS_REGION`           | The AWS region the tool runs in (matches `aws.region` in the YAML config). Restricting the role to a single region prevents cross-region abuse. |
| `REPLACE_ACCOUNT_ID`           | Your AWS account ID (12 digits).                                                                                                                |
| `REPLACE_RUNNER_INSTANCE_ROLE` | The IAM role attached to the runner instance profile (from step 1).                                                                             |

The literal `pacer` value of the `gha:managed-by` tag is fixed inside the binary; do not change it unless you also
fork the binary.

The policy is **tag-scoped** — Pacer can only modify launch templates and instances that it itself tagged with
`gha:managed-by=pacer`. The `iam:PassRole` permission is scoped to the single runner-instance role you picked in step 1,
which is the privilege-escalation boundary: that's the only role Pacer can hand to a spawned EC2 instance.

Apply the policy as either:

- An **inline policy** on an IAM role you create for the tool, then run the tool host with that role attached (EC2
  instance profile, ECS task role, etc).
- A **standalone policy** + an IAM user with `aws_access_key_id` / `aws_secret_access_key`, configured via
  `~/.aws/credentials` and pointed to in `aws.profile`.

## 3. Pick networking + AMI

Before you can save your first pool, you need:

- **One or more VPC subnet IDs** — instances launch here. List subnets across multiple AZs; the default `fleet` spawn
  method passes every (instance_type × subnet) combo to AWS and picks an available one. The legacy `run_instances` spawn
  method uses only the first subnet.
- **A security group ID** — at minimum, allow outbound HTTPS to `api.github.com` and to your `server.public_url`. No
  inbound is needed (the runner connects out, not in).
- **An AMI ID** — must include `bash`, `curl`, `jq`, `tar`, and `sudo`. The user-data script downloads `actions/runner`
  from GitHub at boot (cached on the EBS volume after the first run); pre-baking it on the AMI is optional and only
  saves a few seconds. Use the `runner_version` field on the pool to pin a specific runner release; leave it empty to
  track whatever the server resolved as `actions/runner`'s latest tag.

> **Single region.** The AWS region is configured globally in YAML (`aws.region`). Per-project regions are out of scope.
> All pools you create go into this one region.

> **Root volume size.** Pool form's `root_volume_gb=0` means "inherit the AMI's native size". Any positive value must be
**>=** the AMI's snapshot size or the pool save fails fast (EC2 won't launch otherwise).

## 4. Decide on `server.public_url`

Each spawned EC2 instance calls back to Pacer over HTTPS to register itself, then again to report completion. The
public URL must be:

- reachable from the runner subnets (the spawned EC2 instances need to reach it)
- HTTPS (ideally) — the calls carry HMAC tokens, but TLS is still preferred to avoid token replay risk on shared
  networks
- the same value you put in the GitHub App's **Webhook URL** in [the GitHub side](../github/) — Pacer serves the
  GitHub webhook from the same server

Two options for terminating TLS:

- **In-process** — set `server.tls.mode` in the YAML config: `manual` (operator PEMs), `self` (self-signed; dev /
  private), or `acme` (Let's Encrypt via HTTP-01). See the [server-side](../server/) guide for the full schema.
- **Reverse proxy / load balancer** — set `server.tls.mode: none` and front the tool with an ALB / Cloudflare / nginx
  that terminates TLS upstream. ACME mode is for terminating in-process; if you're behind a load balancer, leave it
  `none`.

## What's next

- **[Server side](../server/)** — install the binary, write the YAML config, run it.
