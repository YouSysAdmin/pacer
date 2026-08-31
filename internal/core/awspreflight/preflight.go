// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package awspreflight exercises the IAM permissions the reaper
// depends on via EC2 DryRun=true at server startup. Missing perms
// land on Runtime.Health so the UI banner shows the problem before
// any orphan instances accumulate. We do NOT fail-fast on a missing
// perm - an operator running with intentionally trimmed permissions
// (e.g. read-only console mode) should still get the server up. The
// banner makes the cost explicit.
package awspreflight

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"

	"github.com/yousysadmin/pacer/internal/core/health"
)

// healthComponent is the key written to Runtime.Health on failure.
// Kept distinct from the reaper's "reaper" key so the banner can
// surface both at once if both are unhappy (e.g. missing perm AND
// the reaper goroutine has since panicked).
const healthComponent = "preflight"

// API is the EC2 client surface used here. Narrow so tests can stub
// it. *ec2.Client satisfies it for free.
//
// We intentionally only exercise DescribeInstances, not Terminate.
// The IAM template gates Terminate on aws:ResourceTag/gha:managed-by,
// which doesn't resolve against a fake instance ID - so DryRun
// would false-positive UnauthorizedOperation even on a healthy role.
// A genuinely missing terminate perm still surfaces visibly the
// moment a real reap is attempted (reaper logs an Error and the row
// stays alive until the next sweep), so we don't need a preflight
// for it.
type API interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// Result is one perm's verdict. OK == permission granted.
type Result struct {
	Action string
	OK     bool
	Reason string // populated when OK is false
}

// Run exercises every permission the reaper needs via DryRun.
// On success: clears health.preflight. On any failure: aggregates
// the failed action names into a single Health message so the
// banner reads "preflight: missing ec2:DescribeInstances".
//
// Returns the per-action results so callers (cli/serve.go) can log
// them individually - the banner gets a one-line summary, the log
// gets the detail.
func Run(ctx context.Context, c API, h *health.Health) []Result {
	checks := []func(context.Context, API) Result{
		describeInstancesCheck,
	}
	results := make([]Result, 0, len(checks))
	for _, fn := range checks {
		r := fn(ctx, c)
		results = append(results, r)
	}
	if h != nil {
		if msg := summarizeFailures(results); msg != "" {
			h.Set(healthComponent, msg)
		} else {
			h.Clear(healthComponent)
		}
	}
	return results
}

func describeInstancesCheck(ctx context.Context, c API) Result {
	const action = "ec2:DescribeInstances"
	_, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		DryRun:     aws.Bool(true),
		MaxResults: aws.Int32(5),
	})
	return interpretDryRun(action, err)
}

// interpretDryRun maps an AWS DryRun response into a Result.
//
// The success signal for DryRun is the API error code
// "DryRunOperation": AWS confirms the call would have been
// authorized and short-circuits. Anything else means we couldn't
// verify the permission - including "UnauthorizedOperation"
// (clearly denied), a transport error, or no error at all (an
// SDK-side stub that didn't reach AWS, unlikely in prod).
func interpretDryRun(action string, err error) Result {
	if err == nil {
		// DryRun==true and no error == we never reached AWS. Treat
		// as inconclusive rather than success so a misconfigured
		// endpoint can't silently pass preflight.
		return Result{Action: action, OK: false, Reason: "no response (DryRun did not reach AWS)"}
	}
	ae, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return Result{Action: action, OK: false, Reason: fmt.Sprintf("non-API error: %v", err)}
	}
	switch ae.ErrorCode() {
	case "DryRunOperation":
		return Result{Action: action, OK: true}
	case "UnauthorizedOperation":
		return Result{Action: action, OK: false, Reason: "IAM denies " + action}
	default:
		return Result{Action: action, OK: false, Reason: ae.ErrorCode() + ": " + ae.ErrorMessage()}
	}
}

func summarizeFailures(rs []Result) string {
	var failed []string
	for _, r := range rs {
		if !r.OK {
			failed = append(failed, r.Action)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return "missing perms: " + joinComma(failed)
}

func joinComma(xs []string) string {
	var out strings.Builder
	for i, x := range xs {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(x)
	}
	return out.String()
}

// LogResults emits one log line per check so an operator can map a
// banner summary back to the specific action that failed. Called by
// the caller (cli/serve.go) after Run - separated so callers
// without a logger can still use Run.
func LogResults(log *slog.Logger, results []Result) {
	if log == nil {
		log = slog.Default()
	}
	for _, r := range results {
		if r.OK {
			log.Info("preflight: ok", "action", r.Action)
			continue
		}
		log.Error("preflight: failed", "action", r.Action, "reason", r.Reason)
	}
}
