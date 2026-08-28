// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/domain/store"
	"github.com/yousysadmin/pacer/internal/models/instance"
)

// fakeAPIError satisfies smithy.APIError so the reaper's NotFound
// branch path can be exercised without real AWS plumbing.
type fakeAPIError struct {
	code string
	msg  string
}

func (e *fakeAPIError) Error() string                 { return e.code + ": " + e.msg }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.msg }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*fakeAPIError)(nil)

// stubEC2 is a hand-rolled ec2API for tests. The Describe/Terminate
// closures let each test program the response sequence; both are
// invocation-counted so assertions can pin the retry path.
type stubEC2 struct {
	describeCalls  int
	terminateCalls int
	describeFns    []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
	terminateFn    func(*ec2.TerminateInstancesInput) (*ec2.TerminateInstancesOutput, error)
}

func (s *stubEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	idx := s.describeCalls
	s.describeCalls++
	if idx >= len(s.describeFns) {
		return &ec2.DescribeInstancesOutput{}, nil
	}
	return s.describeFns[idx](in)
}

func (s *stubEC2) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	s.terminateCalls++
	if s.terminateFn == nil {
		return &ec2.TerminateInstancesOutput{}, nil
	}
	return s.terminateFn(in)
}

func TestSafeTick_RecoversPanic(t *testing.T) {
	rt := &env.Runtime{Health: health.New()}

	err := safeTick(rt, func() error {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected non-nil err from recovered panic")
	}
	if !strings.Contains(err.Error(), "panic") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err should mention panic+boom: %v", err)
	}
	msg, ok := rt.Health.Get(healthComponent)
	if !ok {
		t.Fatal("Health.reaper should be set after panic")
	}
	if !strings.Contains(msg, "panic") || !strings.Contains(msg, "boom") {
		t.Fatalf("Health msg should mention panic+boom: %q", msg)
	}
}

func TestSafeTick_PassesThroughError(t *testing.T) {
	rt := &env.Runtime{Health: health.New()}

	sentinel := errors.New("listalive failed")
	err := safeTick(rt, func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if _, ok := rt.Health.Get(healthComponent); ok {
		t.Fatal("Health.reaper must NOT be set on plain error (no panic)")
	}
}

func TestSafeTick_NilRuntime_NoCrash(t *testing.T) {
	// Defensive: a Reaper constructed without a Runtime would crash
	// inside the panic-recovery path itself if we didn't guard for
	// nil. The whole point of safeTick is keeping the goroutine
	// alive -- it must not become its own failure mode.
	err := safeTick(nil, func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected non-nil err even with nil runtime")
	}
}

func TestSafeTick_NilHealth_NoCrash(t *testing.T) {
	rt := &env.Runtime{} // Health is nil
	err := safeTick(rt, func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected non-nil err with nil Health")
	}
}

func TestCheckEC2HealthVia_DescribeError_SetsHealth(t *testing.T) {
	h := health.New()
	stub := &stubEC2{
		describeFns: []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error){
			func(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return nil, &fakeAPIError{code: "UnauthorizedOperation", msg: "no perm"}
			},
		},
	}
	insts := []*instance.Instance{{ID: "i-abc"}}

	view := checkEC2HealthVia(context.Background(), stub, h, insts)
	if len(view.Dead) != 0 {
		t.Fatalf("want empty dead map on describe failure, got %d", len(view.Dead))
	}
	if len(view.SeenAlive) != 0 {
		t.Fatalf("want empty SeenAlive on describe failure, got %d", len(view.SeenAlive))
	}
	msg, ok := h.Get(healthComponent)
	if !ok {
		t.Fatal("Health.reaper should be set on describe failure")
	}
	if !strings.Contains(msg, "describe instances failed") {
		t.Fatalf("Health msg unexpected: %q", msg)
	}
}

func TestCheckEC2HealthVia_SuccessClearsHealth(t *testing.T) {
	h := health.New()
	// Pre-seed a stale health entry from a prior failed tick. The
	// next successful tick must clear it -- this is what makes the
	// UI banner self-heal.
	h.Set(healthComponent, "old failure")

	stub := &stubEC2{
		describeFns: []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error){
			func(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{
						Instances: []ec2types.Instance{{
							InstanceId: aws.String("i-abc"),
							State: &ec2types.InstanceState{
								Name: ec2types.InstanceStateNameTerminated,
							},
							StateReason: &ec2types.StateReason{
								Code:    aws.String("Client.UserInitiatedShutdown"),
								Message: aws.String("user terminated"),
							},
						}},
					}},
				}, nil
			},
		},
	}
	insts := []*instance.Instance{{ID: "i-abc"}}

	view := checkEC2HealthVia(context.Background(), stub, h, insts)
	if d, ok := view.Dead["i-abc"]; !ok {
		t.Fatal("want i-abc in dead map")
	} else if d.StateName != string(ec2types.InstanceStateNameTerminated) {
		t.Fatalf("dead state: want terminated, got %q", d.StateName)
	}
	if len(view.SeenAlive) != 0 {
		t.Fatalf("dead instance must not appear in SeenAlive: %v", view.SeenAlive)
	}
	if _, ok := h.Get(healthComponent); ok {
		t.Fatal("Health.reaper must be cleared after a successful describe")
	}
}

func TestCheckEC2HealthVia_AliveInstance_GoesToSeenAlive(t *testing.T) {
	// The whole point of the heartbeat: an instance AWS reports as
	// "running" (or pending) ends up in SeenAlive so the reaper
	// can stamp last_seen_at on the row.
	h := health.New()
	stub := &stubEC2{
		describeFns: []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error){
			func(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{
						Instances: []ec2types.Instance{
							{
								InstanceId: aws.String("i-running"),
								State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
							},
							{
								InstanceId: aws.String("i-pending"),
								State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNamePending},
							},
							{
								InstanceId: aws.String("i-dead"),
								State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameTerminated},
							},
						},
					}},
				}, nil
			},
		},
	}
	insts := []*instance.Instance{
		{ID: "i-running"}, {ID: "i-pending"}, {ID: "i-dead"},
	}

	view := checkEC2HealthVia(context.Background(), stub, h, insts)
	if _, ok := view.Dead["i-dead"]; !ok {
		t.Fatal("i-dead should be in Dead")
	}
	if len(view.SeenAlive) != 2 {
		t.Fatalf("want 2 SeenAlive (running + pending), got %d: %v", len(view.SeenAlive), view.SeenAlive)
	}
	got := map[string]bool{}
	for _, id := range view.SeenAlive {
		got[id] = true
	}
	if !got["i-running"] || !got["i-pending"] {
		t.Fatalf("SeenAlive missing one of running/pending: %v", view.SeenAlive)
	}
	if got["i-dead"] {
		t.Fatal("dead instance must NOT be in SeenAlive (last_seen_at is handled via UpdateState)")
	}
}

func TestCheckEC2HealthVia_NotFoundDoesNotSetHealth(t *testing.T) {
	// AWS purged the row -- this is a normal outcome (>1h after
	// termination). Must NOT toggle Health.
	h := health.New()
	stub := &stubEC2{
		describeFns: []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error){
			func(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return nil, &fakeAPIError{
					code: "InvalidInstanceID.NotFound",
					msg:  "The instance IDs 'i-abc' do not exist",
				}
			},
		},
	}
	insts := []*instance.Instance{{ID: "i-abc"}}

	view := checkEC2HealthVia(context.Background(), stub, h, insts)
	if _, ok := view.Dead["i-abc"]; !ok {
		t.Fatal("NotFound should mark the instance lost")
	}
	if _, ok := h.Get(healthComponent); ok {
		t.Fatal("NotFound is normal; Health.reaper must NOT be set")
	}
}

func TestCheckEC2HealthVia_PartialNotFoundDoesPartialRetry(t *testing.T) {
	// Mixed batch: one ID is unknown, the other is terminated. The
	// first call fails NotFound for the missing one; the retry
	// covers the survivor. Both must end up in the dead map.
	h := health.New()
	stub := &stubEC2{
		describeFns: []func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error){
			func(_ *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return nil, &fakeAPIError{
					code: "InvalidInstanceID.NotFound",
					msg:  "The instance IDs 'i-missing' do not exist",
				}
			},
			func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				if len(in.InstanceIds) != 1 || in.InstanceIds[0] != "i-alive" {
					t.Errorf("retry should ask only for i-alive, got %v", in.InstanceIds)
				}
				return &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{
						Instances: []ec2types.Instance{{
							InstanceId: aws.String("i-alive"),
							State: &ec2types.InstanceState{
								Name: ec2types.InstanceStateNameTerminated,
							},
						}},
					}},
				}, nil
			},
		},
	}
	insts := []*instance.Instance{{ID: "i-missing"}, {ID: "i-alive"}}

	view := checkEC2HealthVia(context.Background(), stub, h, insts)
	if _, ok := view.Dead["i-missing"]; !ok {
		t.Error("i-missing should be in dead map")
	}
	if _, ok := view.Dead["i-alive"]; !ok {
		t.Error("i-alive should be in dead map (state=terminated)")
	}
	if stub.describeCalls != 2 {
		t.Errorf("want 2 describe calls (initial + retry), got %d", stub.describeCalls)
	}
	if _, ok := h.Get(healthComponent); ok {
		t.Error("Health.reaper must be cleared after successful retry")
	}
}

func TestCheckEC2HealthVia_EmptyInputNoCalls(t *testing.T) {
	// Trivial guard: no alive instances means no AWS call. The
	// describe path runs every tick; skipping it on an empty list
	// avoids a needless API hit + lets idle Pacer instances stay
	// completely quiet.
	h := health.New()
	stub := &stubEC2{}
	view := checkEC2HealthVia(context.Background(), stub, h, nil)
	if len(view.Dead) != 0 || len(view.SeenAlive) != 0 {
		t.Fatalf("empty input: want empty view, got dead=%d alive=%d", len(view.Dead), len(view.SeenAlive))
	}
	if stub.describeCalls != 0 {
		t.Fatalf("empty input must not call DescribeInstances, got %d calls", stub.describeCalls)
	}
}

// Sanity assertion: ReapInterval must stay short enough that the UI
// banner self-heals on the order of a minute. If someone bumps it
// past a few minutes the operator experience regresses badly.
func TestTerminateLostVia_TerminatesStoppedOnly(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		state         string
		wantTerminate bool
	}{
		{string(ec2types.InstanceStateNameStopped), true},
		{string(ec2types.InstanceStateNameStopping), true},
		{string(ec2types.InstanceStateNameTerminated), false},
		{string(ec2types.InstanceStateNameShuttingDown), false},
		{"", false}, // vanished from AWS entirely
	} {
		stub := &stubEC2{}
		got := terminateLostVia(ctx, stub, "i-lost", deadState{StateName: tc.state})
		if got != tc.wantTerminate {
			t.Fatalf("state %q: attempted=%v, want %v", tc.state, got, tc.wantTerminate)
		}
		wantCalls := 0
		if tc.wantTerminate {
			wantCalls = 1
		}
		if stub.terminateCalls != wantCalls {
			t.Fatalf("state %q: terminate calls=%d, want %d", tc.state, stub.terminateCalls, wantCalls)
		}
	}
}

func TestTerminateLostVia_TerminateFailureStillReported(t *testing.T) {
	stub := &stubEC2{
		terminateFn: func(*ec2.TerminateInstancesInput) (*ec2.TerminateInstancesOutput, error) {
			return nil, errors.New("boom")
		},
	}
	// The attempt is reported even when AWS errors -- the caller's
	// DB-side cleanup proceeds either way, the error is log-only.
	if !terminateLostVia(context.Background(), stub, "i-lost", deadState{
		StateName: string(ec2types.InstanceStateNameStopped),
	}) {
		t.Fatal("terminate attempt should be reported despite the AWS error")
	}
	if stub.terminateCalls != 1 {
		t.Fatalf("terminate calls=%d, want 1", stub.terminateCalls)
	}
}

// blockingInstanceStore fakes just ListAlive; the embedded nil
// interface panics on any other method, which the empty ListAlive
// result guarantees is never reached.
type blockingInstanceStore struct {
	store.InstanceStore
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func (s *blockingInstanceStore) ListAlive(context.Context) ([]*instance.Instance, error) {
	cur := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		prev := s.maxInFlight.Load()
		if cur <= prev || s.maxInFlight.CompareAndSwap(prev, cur) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	return nil, nil
}

// The background ticker and the manual /api/reconcile endpoint both
// call Tick; overlapping sweeps would duplicate terminate/audit side
// effects, so Tick must serialize.
func TestReaper_Tick_Serialized(t *testing.T) {
	fake := &blockingInstanceStore{}
	r := &Reaper{Runtime: &env.Runtime{Store: &store.Store{Instance: fake}}}

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, _ = r.Tick(context.Background())
		})
	}
	wg.Wait()
	if got := fake.maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent sweeps: want 1, got %d", got)
	}
}

func TestReapInterval_NotPathological(t *testing.T) {
	if ReapInterval > 2*time.Minute {
		t.Fatalf("ReapInterval too long for UI self-heal: %v", ReapInterval)
	}
}
