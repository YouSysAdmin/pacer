// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package orchestrator

import (
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestAllocationStrategies pins each preset to the exact AWS enum
// pair the spawn path will send. Adding a new strategy means adding a
// row here -- the table is the contract.
func TestAllocationStrategies(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantOD ec2types.FleetOnDemandAllocationStrategy
		wantSp ec2types.SpotAllocationStrategy
	}{
		{
			name:   "cost (default)",
			in:     "cost",
			wantOD: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			wantSp: ec2types.SpotAllocationStrategyPriceCapacityOptimized,
		},
		{
			name:   "empty falls back to cost",
			in:     "",
			wantOD: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			wantSp: ec2types.SpotAllocationStrategyPriceCapacityOptimized,
		},
		{
			name:   "unknown falls back to cost",
			in:     "wat",
			wantOD: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			wantSp: ec2types.SpotAllocationStrategyPriceCapacityOptimized,
		},
		{
			name:   "lowest_price = pure cheapest",
			in:     "lowest_price",
			wantOD: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			wantSp: ec2types.SpotAllocationStrategyLowestPrice,
		},
		{
			name:   "capacity = deepest spot pool",
			in:     "capacity",
			wantOD: ec2types.FleetOnDemandAllocationStrategyLowestPrice,
			wantSp: ec2types.SpotAllocationStrategyCapacityOptimized,
		},
		{
			name:   "priority = honor list order",
			in:     "priority",
			wantOD: ec2types.FleetOnDemandAllocationStrategyPrioritized,
			wantSp: ec2types.SpotAllocationStrategyCapacityOptimizedPrioritized,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotOD, gotSp := allocationStrategies(c.in)
			if gotOD != c.wantOD {
				t.Errorf("on-demand: want %q, got %q", c.wantOD, gotOD)
			}
			if gotSp != c.wantSp {
				t.Errorf("spot: want %q, got %q", c.wantSp, gotSp)
			}
		})
	}
}

// TestBuildFleetOverrides_PrioritySetOnlyInPriorityMode verifies the
// Priority field is left unset for non-priority strategies (cost,
// lowest_price, capacity) so AWS is free to pick by its own criteria,
// and IS set for priority mode (so the operator's instance_types
// order takes effect).
func TestBuildFleetOverrides_PrioritySetOnlyInPriorityMode(t *testing.T) {
	types := []string{"c6a.large", "c5a.large"}
	subnets := []string{"subnet-1", "subnet-2"}

	t.Run("non-priority leaves Priority nil", func(t *testing.T) {
		got := buildFleetOverrides(types, subnets, false)
		if len(got) != 4 {
			t.Fatalf("expected 4 overrides (2x2), got %d", len(got))
		}
		for i, o := range got {
			if o.Priority != nil {
				t.Errorf("override %d: Priority should be nil, got %v", i, *o.Priority)
			}
		}
	})

	t.Run("priority sets index, shared across subnets", func(t *testing.T) {
		got := buildFleetOverrides(types, subnets, true)
		if len(got) != 4 {
			t.Fatalf("expected 4 overrides, got %d", len(got))
		}
		// types[0] (c6a) overrides land first with Priority=0;
		// types[1] (c5a) overrides land next with Priority=1.
		wantPriorities := []float64{0, 0, 1, 1}
		for i, o := range got {
			if o.Priority == nil {
				t.Fatalf("override %d: Priority should be set, got nil", i)
			}
			if *o.Priority != wantPriorities[i] {
				t.Errorf("override %d: Priority want %v, got %v", i, wantPriorities[i], *o.Priority)
			}
		}
	})
}
