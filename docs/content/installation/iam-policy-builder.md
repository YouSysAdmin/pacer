---
title: "IAM policy builder"
description: "Generate the orchestrator role's IAM policy with your account ID, region, and runner instance role substituted in. Copy or download — no values leave the browser."
weight: 21
---

Fill in the three fields below. The policy regenerates as you type. Nothing is sent anywhere — substitution runs entirely in your browser.

<style>
.iam-builder { display: grid; gap: 1rem; margin: 1.25rem 0 1.5rem; }
.iam-builder .field { display: grid; gap: 0.35rem; }
.iam-builder label { font-size: 0.85rem; font-weight: 600; }
.iam-builder .hint { font-size: 0.75rem; opacity: 0.7; }
.iam-builder input {
  font: 14px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  padding: 0.55rem 0.75rem;
  border: 1px solid rgba(255,255,255,0.12);
  background: rgba(255,255,255,0.03);
  color: inherit;
  border-radius: 6px;
  outline: none;
  transition: border-color 120ms;
}
.iam-builder input:focus { border-color: rgba(245,166,35,0.55); }
.iam-builder input.invalid { border-color: rgba(255,99,99,0.6); }
.iam-builder .row { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
@media (max-width: 700px) { .iam-builder .row { grid-template-columns: 1fr; } }
.iam-builder .actions { display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: center; }
.iam-builder button {
  font: 13px/1 inherit;
  padding: 0.55rem 0.9rem;
  border: 1px solid rgba(255,255,255,0.18);
  background: rgba(255,255,255,0.04);
  color: inherit;
  border-radius: 6px;
  cursor: pointer;
}
.iam-builder button.primary {
  border-color: rgba(245,166,35,0.6);
  background: rgba(245,166,35,0.12);
}
.iam-builder button:hover { background: rgba(255,255,255,0.08); }
.iam-builder button.primary:hover { background: rgba(245,166,35,0.22); }
.iam-builder .status { font-size: 0.8rem; opacity: 0.7; min-height: 1.2em; }
.iam-builder pre.output {
  max-height: 28rem;
  overflow: auto;
  margin: 0;
  padding: 1rem;
  border: 1px solid rgba(255,255,255,0.08);
  border-radius: 8px;
  font: 12.5px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>

<div class="iam-builder">
  <div class="row">
    <div class="field">
      <label for="iam-account">AWS account ID</label>
      <input id="iam-account" type="text" placeholder="123456789012" inputmode="numeric" autocomplete="off" />
      <span class="hint">12 digits. The account where the orchestrator runs.</span>
    </div>
    <div class="field">
      <label for="iam-region">AWS region</label>
      <input id="iam-region" type="text" placeholder="us-east-1" autocomplete="off" />
      <span class="hint">Single region. Matches <code>aws.region</code> in your YAML config.</span>
    </div>
  </div>
  <div class="field">
    <label for="iam-role">Runner instance role name <span class="hint">(optional)</span></label>
    <input id="iam-role" type="text" placeholder="github-runner-role" autocomplete="off" />
    <span class="hint">The role the orchestrator passes to spawned EC2 instances. Just the name, not the ARN. Leave blank if your pools won't attach an instance profile -- the <code>PassRunnerInstanceProfile</code> statement is omitted from the generated policy.</span>
  </div>
  <div class="actions">
    <button id="iam-copy" class="primary" type="button">Copy JSON</button>
    <button id="iam-download" type="button">Download iam-role.json</button>
    <button id="iam-reset" type="button">Reset</button>
    <span id="iam-status" class="status"></span>
  </div>
  <pre class="output"><code id="iam-output">// fill the fields above to generate the policy</code></pre>
</div>

<script>
(function () {
  const TEMPLATE = {
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
        "Condition": { "StringEquals": { "aws:RequestTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "ModifyOnlyOurLaunchTemplates",
        "Effect": "Allow",
        "Action": ["ec2:CreateLaunchTemplateVersion", "ec2:ModifyLaunchTemplate", "ec2:DeleteLaunchTemplate", "ec2:DeleteLaunchTemplateVersions"],
        "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:launch-template/*",
        "Condition": { "StringEquals": { "aws:ResourceTag/gha:managed-by": "pacer" } }
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
        "Condition": { "StringEquals": { "aws:ResourceTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "RunInstancesTaggedInstanceAndVolume",
        "Effect": "Allow",
        "Action": "ec2:RunInstances",
        "Resource": [
          "arn:aws:ec2:REPLACE_AWS_REGION:*:instance/*",
          "arn:aws:ec2:REPLACE_AWS_REGION:*:volume/*"
        ],
        "Condition": { "StringEquals": { "aws:RequestTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "CreateFleetFromOurLaunchTemplate",
        "Effect": "Allow",
        "Action": "ec2:CreateFleet",
        "Resource": "*",
        "Condition": { "StringEquals": { "aws:RequestTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "TagOnCreate",
        "Effect": "Allow",
        "Action": "ec2:CreateTags",
        "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:*",
        "Condition": {
          "StringEquals": {
            "ec2:CreateAction": ["RunInstances", "CreateFleet", "CreateLaunchTemplate", "CreateLaunchTemplateVersion"],
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
        "Condition": { "StringEquals": { "aws:ResourceTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "TerminateOnlyOurInstances",
        "Effect": "Allow",
        "Action": "ec2:TerminateInstances",
        "Resource": "arn:aws:ec2:REPLACE_AWS_REGION:*:instance/*",
        "Condition": { "StringEquals": { "aws:ResourceTag/gha:managed-by": "pacer" } }
      },
      {
        "Sid": "PassRunnerInstanceProfile",
        "Effect": "Allow",
        "Action": "iam:PassRole",
        "Resource": "arn:aws:iam::REPLACE_ACCOUNT_ID:role/REPLACE_RUNNER_INSTANCE_ROLE",
        "Condition": { "StringEquals": { "iam:PassedToService": "ec2.amazonaws.com" } }
      }
    ]
  };

  const $ = (id) => document.getElementById(id);
  const accountEl = $("iam-account");
  const regionEl  = $("iam-region");
  const roleEl    = $("iam-role");
  const outputEl  = $("iam-output");
  const statusEl  = $("iam-status");

  // Reasonable region default; user can override.
  if (!regionEl.value) regionEl.value = "us-east-1";

  const ACCOUNT_RE = /^[0-9]{12}$/;
  const REGION_RE  = /^[a-z]{2}-[a-z]+-[0-9]+$/;
  const ROLE_RE    = /^[A-Za-z0-9+=,.@_-]{1,64}$/;

  function deepReplace(obj, account, region, role) {
    if (typeof obj === "string") {
      return obj
        .replaceAll("REPLACE_ACCOUNT_ID", account)
        .replaceAll("REPLACE_AWS_REGION", region)
        .replaceAll("REPLACE_RUNNER_INSTANCE_ROLE", role);
    }
    if (Array.isArray(obj)) return obj.map((v) => deepReplace(v, account, region, role));
    if (obj && typeof obj === "object") {
      const out = {};
      for (const k of Object.keys(obj)) out[k] = deepReplace(obj[k], account, region, role);
      return out;
    }
    return obj;
  }

  function validate() {
    const account = accountEl.value.trim();
    const region  = regionEl.value.trim();
    const role    = roleEl.value.trim();

    accountEl.classList.toggle("invalid", account !== "" && !ACCOUNT_RE.test(account));
    regionEl.classList.toggle("invalid", region !== "" && !REGION_RE.test(region));
    roleEl.classList.toggle("invalid", role !== "" && !ROLE_RE.test(role));

    return { account, region, role };
  }

  function render() {
    const { account, region, role } = validate();
    if (!account || !region) {
      outputEl.textContent = "// fill account ID and region to generate the policy";
      return null;
    }
    // Reject mid-edit invalid values so the output never contains
    // half-typed substitutions.
    if (!ACCOUNT_RE.test(account) || !REGION_RE.test(region) || (role && !ROLE_RE.test(role))) {
      outputEl.textContent = "// fix the highlighted field(s) to generate the policy";
      return null;
    }
    // Role is optional.  When blank, drop the PassRunnerInstanceProfile
    // Sid -- pools without an instance profile don't trigger iam:PassRole,
    // so granting it would be wasted privilege.  An operator who later
    // adds an instance profile to a pool can re-generate.
    const tpl = JSON.parse(JSON.stringify(TEMPLATE));
    if (!role) {
      tpl.Statement = tpl.Statement.filter((s) => s.Sid !== "PassRunnerInstanceProfile");
    }
    const filled = deepReplace(tpl, account, region, role || "REPLACE_RUNNER_INSTANCE_ROLE");
    const json = JSON.stringify(filled, null, 4);
    outputEl.textContent = json;
    return json;
  }

  function flash(msg, ms = 1800) {
    statusEl.textContent = msg;
    setTimeout(() => { if (statusEl.textContent === msg) statusEl.textContent = ""; }, ms);
  }

  accountEl.addEventListener("input", render);
  regionEl.addEventListener("input", render);
  roleEl.addEventListener("input", render);

  $("iam-copy").addEventListener("click", async () => {
    const json = render();
    if (!json) { flash("fill account ID and region first"); return; }
    try {
      await navigator.clipboard.writeText(json);
      flash("copied to clipboard");
    } catch (e) {
      flash("clipboard blocked; select the text manually");
    }
  });

  $("iam-download").addEventListener("click", () => {
    const json = render();
    if (!json) { flash("fill account ID and region first"); return; }
    const blob = new Blob([json], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "iam-role.json";
    a.click();
    URL.revokeObjectURL(url);
    flash("downloaded iam-role.json");
  });

  $("iam-reset").addEventListener("click", () => {
    accountEl.value = "";
    roleEl.value = "";
    regionEl.value = "us-east-1";
    accountEl.classList.remove("invalid");
    regionEl.classList.remove("invalid");
    roleEl.classList.remove("invalid");
    render();
    flash("reset");
  });

  render();
})();
</script>

## Apply the policy

Save what you copied as `iam-role.json` (or use the **Download** button), then attach it to the orchestrator's IAM role with the AWS CLI:

```bash
ROLE=pacer-orchestrator     # the role your tool assumes; not the runner-instance role

aws iam put-role-policy \
  --role-name "$ROLE" \
  --policy-name pacer-orchestrator \
  --policy-document file://iam-role.json
```

Verify it landed:

```bash
aws iam get-role-policy \
  --role-name "$ROLE" \
  --policy-name pacer-orchestrator \
  --query 'PolicyDocument.Statement[].Sid' --output table
```

You should see the Sids that were generated — `DescribeForValidation`, `ReadOnDemandPricing`, `ValidateInstanceProfileAtPoolSave`, `CreateTaggedLaunchTemplate`, `ModifyOnlyOurLaunchTemplates`, `RunInstancesReadOnlyResources`, `RunInstancesFromOurLaunchTemplate`, `RunInstancesTaggedInstanceAndVolume`, `TagOnCreate`, `TerminateOnlyOurInstances`, and (only when you provided a runner instance role above) `PassRunnerInstanceProfile`.

## Notes

- The `gha:managed-by` value is fixed at `pacer` inside the binary. Don't change it unless you also fork the binary.
- **Runner instance role is optional.** Leave the field blank if your pools won't attach an instance profile -- the generator drops the `PassRunnerInstanceProfile` statement entirely. The orchestrator's `iam:PassRole` permission is only exercised when a pool's `iam_instance_profile` field is non-empty AND the launch causes EC2 to attach a profile to the instance. If you later add an instance profile to a pool, regenerate the policy with the role name filled in -- otherwise the spawn fails with `Insufficient privileges to pass role`.
- Cost-tracking statements (`ReadOnDemandPricing`, `ec2:DescribeSpotPriceHistory` inside `DescribeForValidation`) are optional. Drop them if you don't want at-launch cost snapshots; the orchestrator will log a warning and stamp NULL prices.
- `ValidateInstanceProfileAtPoolSave` is optional. Without it the pool save still works, but a missing instance profile only surfaces at the first spawn (with a cryptic EC2 error). Harmless to keep even when no pool uses an instance profile.
- For multiple runner-instance roles, edit the `Resource` of `PassRunnerInstanceProfile` to a list of role ARNs, or to a path-based wildcard like `arn:aws:iam::ACCOUNT:role/runners/*`.
- Want to verify the policy's effective decisions before applying? See [the simulator commands](../aws/) at the bottom of the AWS-side guide.
