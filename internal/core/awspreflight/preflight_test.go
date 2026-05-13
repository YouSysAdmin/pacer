// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package awspreflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	smithy "github.com/aws/smithy-go"

	"github.com/yousysadmin/pacer/internal/core/health"
)

type apiErr struct {
	code, msg string
}

func (e *apiErr) Error() string                 { return e.code + ": " + e.msg }
func (e *apiErr) ErrorCode() string             { return e.code }
func (e *apiErr) ErrorMessage() string          { return e.msg }
func (e *apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type stubAPI struct {
	describeErr error
}

func (s *stubAPI) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return nil, s.describeErr
}

func TestRun_AllOK_ClearsHealth(t *testing.T) {
	h := health.New()
	// Pre-seed a stale preflight failure: a successful preflight
	// must clear it so a restart with newly-fixed perms self-heals
	// the banner without operator action.
	h.Set(healthComponent, "old failure")

	api := &stubAPI{describeErr: &apiErr{code: "DryRunOperation", msg: "would succeed"}}

	results := Run(context.Background(), api, h)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].OK {
		t.Errorf("describe result should be OK: reason=%q", results[0].Reason)
	}
	if _, ok := h.Get(healthComponent); ok {
		t.Fatal("Health.preflight must be cleared on all-OK preflight")
	}
}

func TestRun_DescribeUnauthorized_SetsHealth(t *testing.T) {
	h := health.New()
	api := &stubAPI{describeErr: &apiErr{code: "UnauthorizedOperation", msg: "denied"}}

	results := Run(context.Background(), api, h)
	if results[0].OK {
		t.Fatal("Describe result should be failed")
	}
	msg, ok := h.Get(healthComponent)
	if !ok {
		t.Fatal("Health.preflight should be set on unauthorized")
	}
	if !strings.Contains(msg, "ec2:DescribeInstances") {
		t.Fatalf("Health msg should name the failed action: %q", msg)
	}
}

func TestInterpretDryRun_AllPaths(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		wantOK bool
		want   string
	}{
		{"dry run ok", &apiErr{code: "DryRunOperation"}, true, ""},
		{"unauthorized", &apiErr{code: "UnauthorizedOperation", msg: "no"}, false, "IAM denies"},
		{"other api error", &apiErr{code: "ServerError", msg: "boom"}, false, "ServerError"},
		{"non-api error", errors.New("timeout"), false, "non-API error"},
		{"nil error", nil, false, "no response"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := interpretDryRun("ec2:Foo", c.err)
			if r.OK != c.wantOK {
				t.Errorf("OK: want %v, got %v (reason=%q)", c.wantOK, r.OK, r.Reason)
			}
			if c.want != "" && !strings.Contains(r.Reason, c.want) {
				t.Errorf("Reason should contain %q, got %q", c.want, r.Reason)
			}
		})
	}
}

func TestRun_NilHealth_NoCrash(t *testing.T) {
	// Cheap insurance: a caller wiring preflight before constructing
	// Health (or vice versa) must not crash. The results return is
	// still authoritative.
	api := &stubAPI{describeErr: &apiErr{code: "DryRunOperation"}}
	results := Run(context.Background(), api, nil)
	if len(results) != 1 {
		t.Fatalf("results len: %d", len(results))
	}
}
