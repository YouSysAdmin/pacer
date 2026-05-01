// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/yousysadmin/pacer/internal/models/job"
	"github.com/yousysadmin/pacer/internal/models/pool"
)

// userDataScript is the bash payload run by cloud-init on every
// spawned instance.
// Generated per-spawn (carries job_id + callback_token + the resolved runner version),
// so it can't live in the launch template.
//
// The AMI must ship: bash, curl, jq, tar.
// The actions/runner is auto-installed at boot from the GitHub release tarball, version
// resolved server-side; the AMI doesn't have to bake it.
//
// Robustness patterns borrowed from production runner setups:
//   - exec > >(tee LOG) 2>&1 captures everything for the error trap
//   - IMDSv2 token fetch retries (cold-boot timing)
//   - trap ERR posts the captured log to /api/runner/error
//   - shutdown -h on terminal failure so the host doesn't sit idle
//     waiting for the 60s reaper sweep
//
// TODO: maybe this should be moved somewhere where the user can edit it (DB/Config/etc.)
const userDataScript = `#!/bin/bash
set -euo pipefail

# ---------------------------------------------------------------- vars
JOB_ID={{.JobID | sh}}
CALLBACK_TOKEN={{.CallbackToken | sh}}
SERVER_URL={{.ServerURL | sh}}
RUNNER_VERSION={{.RunnerVersion | sh}}
RUNNER_USER={{.RunnerUser | sh}}
RUNNER_HOME="${RUNNER_HOME:-/opt/actions-runner}"
LOG=/var/log/runner-bootstrap.log
STAGE="init"

mkdir -p "$(dirname "$LOG")" || true
exec > >(tee -a "$LOG") 2>&1

# ---------------------------------------------------------------- error path
report_error() {
    local exit_code="$1"
    local line="$2"
    [ "$exit_code" = "0" ] && return 0
    echo "BOOTSTRAP FAIL stage=$STAGE exit=$exit_code line=$line"
    # Stuff the captured log into JSON via jq -Rs (reads stdin as a
    # single string) and POST it.  Failures here are best-effort --
    # the instance still self-terminates afterwards.
    local payload
    payload=$(jq -Rsa --arg job_id "$JOB_ID" --arg tok "$CALLBACK_TOKEN" \
        --arg stage "$STAGE" --argjson exit "$exit_code" --argjson line "$line" \
        '{job_id:$job_id, callback_token:$tok, stage:$stage, exit_code:$exit, line:$line, log:.}' \
        < "$LOG" || echo "{}")
    curl -fsS -X POST "$SERVER_URL/api/runner/error" \
        -H "Content-Type: application/json" \
        -d "$payload" || true
    # Give the curl + log writes time to flush before shutdown.
    sleep 5
    sudo shutdown -h now "runner bootstrap failed" 2>/dev/null || shutdown -h now "runner bootstrap failed" || true
}
trap 'report_error $? $LINENO' ERR

# ---------------------------------------------------------------- imdsv2
STAGE="imdsv2"
echo "fetching IMDSv2 token"
IMDS_TOKEN=""
for i in $(seq 1 40); do
    IMDS_TOKEN=$(curl -fs -X PUT "http://169.254.169.254/latest/api/token" \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 600" 2>/dev/null || true)
    [ -n "$IMDS_TOKEN" ] && break
    echo "IMDSv2 token attempt $i failed, retrying"
    sleep 3
done
[ -n "$IMDS_TOKEN" ] || { echo "IMDSv2 token unavailable"; exit 11; }

INSTANCE_ID=$(curl -fs -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/instance-id)
INSTANCE_TYPE=$(curl -fs -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/instance-type)
AZ=$(curl -fs -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/placement/availability-zone)
echo "instance_id=$INSTANCE_ID type=$INSTANCE_TYPE az=$AZ"

# ---------------------------------------------------------------- runner sync
STAGE="runner-sync"
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  RUNNER_ARCH=x64 ;;
    aarch64) RUNNER_ARCH=arm64 ;;
    *)       echo "unsupported arch $ARCH"; exit 21 ;;
esac

# Find the installed runner version (we stamp it on install) so we
# can skip the download when the AMI already has the right binary.
INSTALLED=""
[ -f "$RUNNER_HOME/.runner_version" ] && INSTALLED=$(cat "$RUNNER_HOME/.runner_version" 2>/dev/null || true)
echo "runner installed='$INSTALLED' target='$RUNNER_VERSION' arch=$RUNNER_ARCH"

if [ -z "$RUNNER_VERSION" ]; then
    echo "no target runner version provided; skipping sync (using AMI default)"
elif [ "$INSTALLED" != "$RUNNER_VERSION" ]; then
    echo "downloading actions-runner $RUNNER_VERSION ($RUNNER_ARCH)"
    TARBALL="actions-runner-linux-${RUNNER_ARCH}-${RUNNER_VERSION}.tar.gz"
    URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${TARBALL}"
    curl -fsSL --retry 5 --retry-delay 3 -o "/tmp/${TARBALL}" "$URL"
    rm -rf "$RUNNER_HOME"
    mkdir -p "$RUNNER_HOME"
    tar -xzf "/tmp/${TARBALL}" -C "$RUNNER_HOME"
    rm -f "/tmp/${TARBALL}"
    echo "$RUNNER_VERSION" > "$RUNNER_HOME/.runner_version"
fi

# ---------------------------------------------------------------- register
STAGE="register"
echo "POST /api/runner/register"
RESP=$(curl -fsS -X POST "$SERVER_URL/api/runner/register" \
    -H "Content-Type: application/json" \
    -d "{\"job_id\":\"$JOB_ID\",\"instance_id\":\"$INSTANCE_ID\",\"instance_type\":\"$INSTANCE_TYPE\",\"az\":\"$AZ\",\"callback_token\":\"$CALLBACK_TOKEN\"}")

JIT_CONFIG=$(echo "$RESP" | jq -r .jit_config)
if [ -z "$JIT_CONFIG" ] || [ "$JIT_CONFIG" = "null" ]; then
    echo "register: no jit_config in response: $RESP"
    exit 31
fi

# ---------------------------------------------------------------- run
STAGE="run"
RUNNER_EXIT=0
cd "$RUNNER_HOME"
if [ -n "$RUNNER_USER" ]; then
    # Run the runner as a non-root user so AMI-baked per-user tooling
    # (rbenv / nvm / asdf / per-user gem prefixes) is reachable.  We
    # chown the runner home so the user can write _diag/ + _work/
    # under it.  The runner refuses to run as root by default, so the
    # branch below sets RUNNER_ALLOW_RUNASROOT=1 explicitly when no
    # user is configured.
    echo "running as user '$RUNNER_USER'"
    chown -R "$RUNNER_USER" "$RUNNER_HOME"
    sudo --preserve-env=RUNNER_HOME -u "$RUNNER_USER" -- ./run.sh --jitconfig "$JIT_CONFIG" || RUNNER_EXIT=$?
else
    echo "running as root (no runner_user configured)"
    RUNNER_ALLOW_RUNASROOT=1 ./run.sh --jitconfig "$JIT_CONFIG" || RUNNER_EXIT=$?
fi
echo "run.sh exited with $RUNNER_EXIT"

# Anything past this point is best-effort cleanup.  Disarm the ERR
# trap so a curl failure doesn't trigger a duplicate error report.
trap - ERR

# ---------------------------------------------------------------- complete
STAGE="complete"
curl -fs -X POST "$SERVER_URL/api/runner/complete" \
    -H "Content-Type: application/json" \
    -d "{\"job_id\":\"$JOB_ID\",\"callback_token\":\"$CALLBACK_TOKEN\",\"exit_code\":$RUNNER_EXIT}" || true

{{if .UserDataExtra}}
# Pool tail script:
{{.UserDataExtra}}
{{end}}

# +1 minute gives any in-flight curl/log writes time to settle.
sudo shutdown -h +1 "actions-runner job complete" || shutdown -h +1 "actions-runner job complete"
`

type userDataVars struct {
	JobID         string
	CallbackToken string
	ServerURL     string
	RunnerVersion string
	RunnerUser    string
	UserDataExtra string
}

// shellEscape wraps a string in single quotes for safe shell embedding.
// Used by the {{.X | sh}} template pipe to harden the variable
// initialization lines against tokens with metacharacters.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var userDataTmpl = template.Must(template.New("userdata").
	Funcs(template.FuncMap{"sh": shellEscape}).
	Parse(userDataScript))

// renderUserData fills the user-data template for a specific job + pool.
// serverURL is the orchestrator's public URL; runnerVersion
// is the resolved actions/runner tag (pool pin or server-cached latest).
// Empty runnerVersion is allowed -- the script will skip
// the sync step and use whatever the AMI baked.
// Empty pool.RunnerUser means run as root with RUNNER_ALLOW_RUNASROOT=1;
// non-empty sudo-drops to that user before invoking ./run.sh.
func renderUserData(j *job.Job, p *pool.Pool, callbackToken, serverURL, runnerVersion string) (string, error) {
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, userDataVars{
		JobID:         j.ID,
		CallbackToken: callbackToken,
		ServerURL:     serverURL,
		RunnerVersion: runnerVersion,
		RunnerUser:    p.RunnerUser,
		UserDataExtra: p.UserDataExtra,
	}); err != nil {
		return "", fmt.Errorf("render user-data: %w", err)
	}
	return buf.String(), nil
}
