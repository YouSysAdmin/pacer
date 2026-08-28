// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package user

import (
	"testing"

	usermodel "github.com/yousysadmin/pacer/internal/models/user"
	"github.com/yousysadmin/pacer/internal/testutil"
)

func TestUser_Put_DefaultsToRoleUser(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	if err := s.Put(ctx, &usermodel.User{ID: "u1", Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByID(ctx, "u1")
	if err != nil || got == nil {
		t.Fatalf("GetByID: %v %v", got, err)
	}
	if got.Role != usermodel.RoleUser {
		t.Fatalf("want role user, got %q", got.Role)
	}
	if err := s.Put(ctx, &usermodel.User{ID: "u2", Email: "b@example.com", Role: usermodel.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetByID(ctx, "u2"); got.Role != usermodel.RoleAdmin {
		t.Fatalf("explicit admin lost: %q", got.Role)
	}
}

func TestUser_Put_DuplicateEmailRejected(t *testing.T) {
	s := NewStore(testutil.OpenTestDB(t))
	ctx := t.Context()
	if err := s.Put(ctx, &usermodel.User{ID: "u1", Email: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, &usermodel.User{ID: "u2", Email: "a@example.com"}); err == nil {
		t.Fatal("expected unique violation")
	}
}
