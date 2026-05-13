// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package validation

import "testing"

func TestFriendlyField(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Override map cases: tricky abbreviations + non-obvious names
		// users see by their label, not their json tag.
		{"ami_id", "AMI ID"},
		{"iam_instance_profile", "IAM instance profile name"},
		{"runner_group_id", "Runner group ID"},
		{"org_name", "Org login"},
		{"full_name", "Repository"},
		{"max_concurrent_runners", "Max concurrent runners"},
		{"max_runtime_minutes", "Max runtime"},
		{"runner_user", "Run runner as"},
		// Auto-titlecase fallback for plain snake_case fields.
		{"name", "Name"},
		{"priority", "Priority"},
		{"some_new_field", "Some new field"},
		// Empty / malformed inputs degrade gracefully.
		{"", "This field"},
	}
	for _, c := range cases {
		got := friendlyField(c.in)
		if got != c.want {
			t.Errorf("friendlyField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
