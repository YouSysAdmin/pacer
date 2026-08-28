// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package auth

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/yousysadmin/pacer/internal/core/env"
	pacoidc "github.com/yousysadmin/pacer/internal/core/oidc"
	"github.com/yousysadmin/pacer/internal/domain/store"
	"github.com/yousysadmin/pacer/internal/domain/user"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
	"github.com/yousysadmin/pacer/internal/testutil"
)

func TestSessionTTL_Fallbacks(t *testing.T) {
	rt := &env.Runtime{Config: &env.Config{}}
	if sessionTTL(rt).Hours() != 12 {
		t.Fatal("empty -> 12h")
	}
	rt.Config.Auth.SessionTTL = "garbage"
	if sessionTTL(rt).Hours() != 12 {
		t.Fatal("garbage -> 12h")
	}
	rt.Config.Auth.SessionTTL = "30m"
	if sessionTTL(rt).Minutes() != 30 {
		t.Fatal("30m honored")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("abc", 5) != "abc" || truncate("abcdef", 3) != "abc..." {
		t.Fatal("truncate")
	}
}

func TestFindOrCreateOIDCUser_UnverifiedEmailNeverLinksOrDuplicates(t *testing.T) {
	db := testutil.OpenTestDB(t)
	users := user.NewStore(db)
	if err := users.Put(t.Context(), &usermodel.User{ID: "local-admin", Email: "ops@example.com", Role: usermodel.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Runtime: &env.Runtime{Config: &env.Config{}, Store: &store.Store{User: users}}}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	var gotErr error
	var gotUser *usermodel.User
	app.Get("/", func(c *fiber.Ctx) error {
		gotUser, gotErr = h.findOrCreateOIDCUser(c, &pacoidc.Claims{Subject: "sub-1", Email: "OPS@example.com", EmailVerified: false})
		return c.SendStatus(200)
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil), -1); err != nil {
		t.Fatal(err)
	}
	if gotErr == nil || gotUser != nil {
		t.Fatalf("unverified email must be refused, got user=%v err=%v", gotUser, gotErr)
	}
	if !strings.Contains(gotErr.Error(), "verified") {
		t.Fatalf("error should explain the refusal: %v", gotErr)
	}
	u, _ := users.Get(t.Context(), "ops@example.com")
	if u.OIDCSubject != "" {
		t.Fatal("local account must not be linked")
	}
	if n, _ := users.Count(t.Context()); n != 1 {
		t.Fatalf("no duplicate user row expected, got %d", n)
	}
}
