// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package env

import (
	"log/slog"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/yousysadmin/pacer/internal/core/ghapp"
	"github.com/yousysadmin/pacer/internal/core/ghrunner"
	pacoidc "github.com/yousysadmin/pacer/internal/core/oidc"
	"github.com/yousysadmin/pacer/internal/core/pricing"
	"github.com/yousysadmin/pacer/internal/database"
	"github.com/yousysadmin/pacer/internal/domain/store"
)

// Runtime is the server-scoped bag of dependencies. Built once in
// cli/serve.go and handed to every domain Handler so it can reach
// config, the logger, the DB, the aggregated Store, and AWS / GitHub
// / OIDC clients.
type Runtime struct {
	Config        *Config
	Log           *slog.Logger
	DB            database.Database
	Store         *store.Store
	EC2           *ec2.Client
	IAM           *iam.Client // nil when aws.disabled is true; used by ec2lt to validate instance profiles at pool save
	GHApp         *ghapp.Client
	Pricing       *pricing.Fetcher   // nil when aws.disabled is true
	RunnerVersion *ghrunner.Resolver // nil when github.disabled is true (no spawns)
	OIDC          *pacoidc.Provider  // nil when auth.oidc.enabled is false
	// BootstrapAPIToken is the secret the in-instance bootstrap script
	// presents as `Authorization: Bearer <token>` when calling
	// /api/runner/bootstrap. Loaded from the settings table at startup
	// (auto-generated on first start) and refreshed in place when the
	// operator rotates via the Settings UI. atomic.Value so the
	// orchestrator + bootstrap-endpoint readers don't take a lock per
	// request.
	BootstrapAPIToken atomic.Value // string
}
