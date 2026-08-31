// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package ec2lt

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/yousysadmin/pacer/internal/models/pool"
)

// userDataScript is the bash payload baked into every launch template
// at materialize time and run by cloud-init on every spawned instance.
//
// Static across spawns - per-job state (the HMAC-signed callback
// token) is fetched from pacer's /api/runner/bootstrap endpoint using
// the global bootstrap API token baked in here as the bearer secret.
// The orchestrator stashes the per-job HMAC token on jobs.bootstrap_token
// at spawn time. Bootstrap returns it (and clears the column,
// enforcing single-use) so subsequent /register POSTs authenticate
// normally.
//
// The AMI must ship: bash, curl, jq, tar.
// The actions/runner is auto-installed at boot from the GitHub
// release tarball, version resolved server-side at LT materialize
// time. The AMI doesn't have to bake it.
//
// Robustness:
//   - exec > >(tee LOG) 2>&1 captures everything for the error trap
//   - IMDSv2 token fetch retries (cold-boot timing)
//   - bootstrap POST retries on transient server / network failures
//   - trap ERR posts the captured log to /api/runner/error
//   - shutdown -h on terminal failure so the host doesn't sit idle
//     waiting for the 60s reaper sweep
const userDataScript = `#!/bin/bash
set -euo pipefail

# ---------------------------------------------------------------- vars
SERVER_URL={{.ServerURL | sh}}
RUNNER_VERSION={{.RunnerVersion | sh}}
RUNNER_USER={{.RunnerUser | sh}}
BOOTSTRAP_API_TOKEN={{.BootstrapAPIToken | sh}}
RUNNER_HOME="${RUNNER_HOME:-/opt/actions-runner}"
LOG=/var/log/runner-bootstrap.log
STAGE="init"

# Filled in below once /api/runner/bootstrap returns.
JOB_ID=""
CALLBACK_TOKEN=""

mkdir -p "$(dirname "$LOG")" || true
exec > >(tee -a "$LOG") 2>&1

# ---------------------------------------------------------------- error path
report_error() {
    local exit_code="$1"
    local line="$2"
    [ "$exit_code" = "0" ] && return 0
    echo "BOOTSTRAP FAIL stage=$STAGE exit=$exit_code line=$line"
    # Without a callback token (bootstrap never returned) we can't
    # authenticate the error report - log and skip the POST. The
    # reaper sweep is the safety net.
    if [ -n "$CALLBACK_TOKEN" ]; then
        local payload
        payload=$(jq -Rsa --arg job_id "$JOB_ID" --arg tok "$CALLBACK_TOKEN" \
            --arg stage "$STAGE" --argjson exit "$exit_code" --argjson line "$line" \
            '{job_id:$job_id, callback_token:$tok, stage:$stage, exit_code:$exit, line:$line, log:.}' \
            < "$LOG" || echo "{}")
        # Not -f: if pacer refuses the report (a job already
        # finalized, a token past its window) the reason belongs in
        # the console output, which outlives the instance via
        # "aws ec2 get-console-output". This is the last thing that
        # runs, so a lost explanation here is a dead end.
        curl -sS -o - -w '\n' -X POST "$SERVER_URL/api/runner/error" \
            -H "Content-Type: application/json" \
            -d "$payload" || true
    fi
    sleep 5
    sudo shutdown -h now "runner bootstrap failed" 2>/dev/null || shutdown -h now "runner bootstrap failed" || true
}
trap 'report_error $? $LINENO' ERR

# api_post POSTs to pacer and prints the response body on stdout.
#
# It exists because "curl -f" - which every one of these calls used
# to use - exits 22 and THROWS THE BODY AWAY. The operator reading a
# failed job then sees "error: 424" and nothing else, while the
# sentence that explains it ("jitconfig: 403 Forbidden: Resource not
# accessible by integration") is discarded a few milliseconds before
# the log is shipped. Here the body is captured either way, and
# printed to the log when the request fails.
#
# --retry covers curl's transient set (connection failures, 408, 429,
# 5xx) and nothing else, which is why the server answers a permanent
# GitHub refusal with 424 rather than 500: retrying that would burn
# the whole budget re-asking a settled question.
#
# Args: <path> <json-body> [extra curl args...]
# Prints: response body. Returns: 0 on 2xx, 1 otherwise.
api_post() {
    local path="$1" data="$2"
    shift 2
    local raw code body
    # A single trailing line holds the status, so one request yields
    # both halves without a second round trip.
    raw=$(curl -sS -o - -w '\n%{http_code}' --retry 12 --retry-delay 6 --retry-connrefused \
        -X POST "$SERVER_URL$path" \
        -H "Content-Type: application/json" \
        "$@" -d "$data") || {
        echo "$path: curl failed (exit $?)"
        return 1
    }
    code=${raw##*$'\n'}
    body=${raw%$'\n'*}
    if [ "$code" -lt 200 ] || [ "$code" -ge 300 ]; then
        # The body is the whole reason this helper exists.
        echo "$path: HTTP $code: $body"
        return 1
    fi
    printf '%s' "$body"
}

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

# ---------------------------------------------------------------- bootstrap
# Fetch the per-job HMAC callback token from pacer. The bootstrap API
# token (operator-managed, rotatable via Settings UI) authenticates
# the request; pacer matches our instance_id against jobs.instance_id
# (status=claimed, claimed_at within TTL, bootstrap_token still present)
# and returns the per-job token. Single-use server-side: a second call
# returns 410.
#
# --retry 12 + --retry-delay 6 covers transient network blips during
# pacer restarts. 4xx (including 401 / 403 / 410) do NOT retry -
# those are permanent.
STAGE="bootstrap"
echo "POST /api/runner/bootstrap"
RESP=$(api_post /api/runner/bootstrap \
    "{\"instance_id\":\"$INSTANCE_ID\",\"instance_type\":\"$INSTANCE_TYPE\",\"az\":\"$AZ\"}" \
    -H "Authorization: Bearer $BOOTSTRAP_API_TOKEN")

CALLBACK_TOKEN=$(echo "$RESP" | jq -r .callback_token)
JOB_ID=$(echo "$RESP" | jq -r .job_id)
if [ -z "$CALLBACK_TOKEN" ] || [ "$CALLBACK_TOKEN" = "null" ]; then
    echo "bootstrap: no callback_token in response: $RESP"
    exit 13
fi
if [ -z "$JOB_ID" ] || [ "$JOB_ID" = "null" ]; then
    echo "bootstrap: no job_id in response: $RESP"
    exit 14
fi
echo "bootstrap ok: job_id=$JOB_ID"

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
# This is the only request the runner CANNOT survive losing - the JIT
# config is single-use and we have no graceful fallback if it doesn't
# come back. --retry 12 + --retry-delay 6 = up to ~72s of retry,
# covering a typical pacer redeploy window without bouncing the
# instance.
#
# We deliberately do NOT use --retry-all-errors here: a 400 from this
# endpoint means the job moved out of "claimed" (e.g. GitHub cancelled
# it because a higher-priority workflow run superseded this one), which
# is permanent. Retrying just burns ~72s of compute before the trap
# fires. --retry-connrefused covers the "server restarted mid-deploy"
# case (which is what the retry budget is actually for) without
# extending into 4xx territory; default --retry already handles 5xx
# + 408 + 429.
STAGE="register"
echo "POST /api/runner/register"
RESP=$(api_post /api/runner/register \
    "{\"job_id\":\"$JOB_ID\",\"instance_id\":\"$INSTANCE_ID\",\"instance_type\":\"$INSTANCE_TYPE\",\"az\":\"$AZ\",\"callback_token\":\"$CALLBACK_TOKEN\"}")

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

# A runner that exits non-zero never reached the ERR trap: the
# "|| RUNNER_EXIT=$?" above catches the failure by design, so the
# script walks on to "complete" and the log dies with the instance a
# minute later. That hid the single most common class of failure -
# GitHub refusing the registration at connect time (a runner version
# it no longer accepts, a JIT config already consumed, a runner group
# that went away) - because all of that is printed by run.sh, not by
# anything the server ever sees.
#
# So report it the same way a bootstrap failure is reported. This is
# NOT the trap: the job is genuinely finished, and "complete" below
# still runs to stamp termination and finalize cost.
if [ "$RUNNER_EXIT" -ne 0 ]; then
    STAGE="run"
    echo "RUNNER FAIL stage=run exit=$RUNNER_EXIT"
    payload=$(jq -Rsa --arg job_id "$JOB_ID" --arg tok "$CALLBACK_TOKEN" \
        --arg stage "run" --argjson exit "$RUNNER_EXIT" --argjson line 0 \
        '{job_id:$job_id, callback_token:$tok, stage:$stage, exit_code:$exit, line:$line, log:.}' \
        < "$LOG" || echo "{}")
    curl -sS -o /dev/null -X POST "$SERVER_URL/api/runner/error" \
        -H "Content-Type: application/json" \
        -d "$payload" || true
fi

# ---------------------------------------------------------------- complete
STAGE="complete"
curl -sS -o /dev/null -X POST "$SERVER_URL/api/runner/complete" \
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
	ServerURL         string
	RunnerVersion     string
	RunnerUser        string
	BootstrapAPIToken string
	UserDataExtra     string
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

// renderUserData fills the static user-data template baked into the
// launch template at materialize time.
//
// serverURL is the orchestrator's public URL. runnerVersion is the
// resolved actions/runner tag (pool pin or server-cached latest at
// the moment the LT was materialized). The script skips the runner
// download when empty and uses whatever the AMI baked.
// bootstrapAPIToken authenticates POST /api/runner/bootstrap - it's
// the operator-managed shared secret in the settings table.
//
// The per-job callback token + job id are NOT inputs here - the
// script fetches them via the bootstrap endpoint after IMDSv2 brings
// up its own instance_id.
func renderUserData(p *pool.Pool, serverURL, runnerVersion, bootstrapAPIToken string) (string, error) {
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, userDataVars{
		ServerURL:         serverURL,
		RunnerVersion:     runnerVersion,
		RunnerUser:        p.RunnerUser,
		BootstrapAPIToken: bootstrapAPIToken,
		UserDataExtra:     p.UserDataExtra,
	}); err != nil {
		return "", fmt.Errorf("render user-data: %w", err)
	}
	return buf.String(), nil
}
