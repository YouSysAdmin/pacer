# pacer terraform example

Calls the `pacer` module with a full set of inputs. Bring your own VPC +
subnet + DNS.

## Prereqs

1. **AWS account** with credentials configured (`AWS_PROFILE` or env vars).
2. **VPC + public subnet** -- the example doesn't create networking.
3. **DNS A record** for `var.fqdn`. Point it at the EIP after the first
   apply (you can `terraform apply` twice -- first to allocate the EIP,
   then update DNS, then the ACME run on next reboot will succeed).
4. **GitHub App** registered with `workflow_job` webhook subscribed,
   private key downloaded as PEM. See
   [`docs/content/installation/github.md`](../../../docs/content/installation/github.md).

## Apply

```bash
cd examples/terraform/example/
cp terraform.tfvars.example terraform.tfvars
$EDITOR terraform.tfvars

terraform init

# Pass secrets through env vars so they don't sit in tfvars on disk.
TF_VAR_github_app_private_key="$(cat path/to/app.pem)" \
TF_VAR_webhook_secret="$(cat path/to/webhook-secret)" \
TF_VAR_callback_hmac_secret="$(openssl rand -hex 32)" \
TF_VAR_jwt_secret="$(openssl rand -hex 32)" \
terraform apply
```

## After apply

1. Update the DNS A record for `<fqdn>` to the `public_ip` output.
2. Wait ~2 min for cloud-init + first ACME issuance.
3. SSH-via-SSM (no keypair required) to grab the bootstrap password:
   ```bash
   aws ssm start-session --target $(terraform output -raw instance_id)
   sudo journalctl -u pacer | grep 'first-run password'
   ```
4. Browse to `https://<fqdn>/`. Log in as `var.operator_email` with
   that password.
5. Register a project and a pool. For the pool's "IAM instance profile"
   field, paste `terraform output -raw runner_instance_profile`.
6. Build a runner AMI with Packer (`examples/packer/runner-ami/`) and
   paste the AMI id into the pool's `ami_id` field.

## Updating

- **Binary**: bump `pacer_version`, run `terraform taint
  module.pacer.aws_instance.pacer`, `terraform apply`. Snapshot the
  SQLite EBS first if you need state continuity.
- **IAM policy**: editing `modules/pacer/iam.tf` (the orchestrator
  policy data source) triggers an in-place update -- no host churn.
- **Adding pools / projects / repos**: through the Pacer UI; not
  managed by terraform.
