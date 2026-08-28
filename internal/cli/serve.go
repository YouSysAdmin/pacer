// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/spf13/cobra"

	"github.com/yousysadmin/pacer/internal/core/authenticator"
	"github.com/yousysadmin/pacer/internal/core/awscfg"
	"github.com/yousysadmin/pacer/internal/core/awspreflight"
	"github.com/yousysadmin/pacer/internal/core/env"
	"github.com/yousysadmin/pacer/internal/core/ghapp"
	"github.com/yousysadmin/pacer/internal/core/ghrunner"
	"github.com/yousysadmin/pacer/internal/core/health"
	"github.com/yousysadmin/pacer/internal/core/logger"
	pacoidc "github.com/yousysadmin/pacer/internal/core/oidc"
	corepricing "github.com/yousysadmin/pacer/internal/core/pricing"
	"github.com/yousysadmin/pacer/internal/core/validation"
	"github.com/yousysadmin/pacer/internal/database/sqlite"
	"github.com/yousysadmin/pacer/internal/domain/settings"
	usermodel "github.com/yousysadmin/pacer/internal/models/user"
	"github.com/yousysadmin/pacer/internal/orchestrator"
	"github.com/yousysadmin/pacer/internal/server"

	"github.com/google/uuid"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the API server, orchestrator, and reaper",
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	cfg, err := env.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}

	log, err := logger.InitLogger(cfg.Logging.Level, cfg.Logging.Output, cfg.Logging.Format, cfg.Logging.Color)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	// Wire the JSON-body validator + custom rules before any handler
	// can run. BindAndValidate panics if Init hasn't run, so this
	// must precede server.New.
	validation.Init()

	db, err := sqlite.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// aws.disabled flips off all AWS integration for local UI dev.
	// awscfg.Load + ec2.NewFromConfig are skipped. Runtime.EC2 stays
	// nil and the pool handler short-circuits ec2lt.CreateOrUpdate
	// (stamping a placeholder LT id).
	// Pair with github.disabled for full UI-only dev.
	var (
		ec2Client    *ec2.Client
		iamClient    *iam.Client
		priceFetcher *corepricing.Fetcher
	)
	if !cfg.AWS.Disabled {
		awsCfg, err := awscfg.Load(context.Background(), cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("aws config: %w", err)
		}
		ec2Client = ec2.NewFromConfig(awsCfg)

		// TODO: AWS Pricing/IAM API always requires a "global" region - us-east-1
		// in the future it may be necessary to implement automatic region
		// selection to support CN etc.
		// IAM
		iamClient = iam.NewFromConfig(awsCfg)
		// Pricing API
		pricingCfg := awsCfg.Copy()
		pricingCfg.Region = "us-east-1"
		priceFetcher = corepricing.New(ec2Client, pricing.NewFromConfig(pricingCfg), cfg.AWS.Region)
	} else {
		log.Warn("AWS integration disabled (aws.disabled=true): launch templates won't be materialized; pool create/update stamps a placeholder LT id; instance pricing will not be stamped")
	}

	// github.disabled flips the tool into UI-only mode: skip loading
	// the App private key, leave Runtime.GHApp nil, and don't start
	// the orchestrator + reaper (both would call AWS / GitHub).
	// routes.go skips webhook + runner route registration when
	// GHApp is nil.
	var (
		ghClient  *ghapp.Client
		runnerRes *ghrunner.Resolver
	)
	if !cfg.GitHub.Disabled {
		ghClient, err = ghapp.New(cfg.GitHub.AppID, cfg.GitHub.PrivateKeyPath)
		if err != nil {
			return fmt.Errorf("github app: %w", err)
		}
		// Cache the latest actions/runner version up front so the
		// first spawn doesn't hit the GitHub API. A failed first fetch
		// keeps the resolver alive so the background loop can recover
		// without a restart. Until then user-data uses its default.
		runnerRes = ghrunner.New(context.Background())
	} else {
		log.Warn("GitHub integration disabled (github.disabled=true): webhook + runner endpoints inactive, orchestrator + reaper not started -- UI-only mode")
	}

	// OIDC discovery happens at startup so issuer-side typos / outages
	// surface here instead of on the first SSO attempt.
	var oidcProvider *pacoidc.Provider
	if !cfg.Auth.Disabled && cfg.Auth.OIDC.Enabled {
		requireVerified := true
		if cfg.Auth.OIDC.RequireEmailVerified != nil {
			requireVerified = *cfg.Auth.OIDC.RequireEmailVerified
		}
		oidcProvider, err = pacoidc.New(context.Background(), pacoidc.Config{
			Issuer:               cfg.Auth.OIDC.Issuer,
			ClientID:             cfg.Auth.OIDC.ClientID,
			ClientSecret:         cfg.Auth.OIDC.ClientSecret,
			RedirectURL:          cfg.Auth.OIDC.RedirectURL,
			Scopes:               cfg.Auth.OIDC.Scopes,
			RequireEmailVerified: requireVerified,
			AllowedDomains:       cfg.Auth.OIDC.AllowedDomains,
			AllowedEmails:        cfg.Auth.OIDC.AllowedEmails,
			GroupsClaim:          cfg.Auth.OIDC.GroupsClaim,
			AllowedGroups:        cfg.Auth.OIDC.AllowedGroups,
		})
		if err != nil {
			return fmt.Errorf("oidc init: %w", err)
		}
		log.Info("auth: oidc enabled", "issuer", cfg.Auth.OIDC.Issuer, "client_id", cfg.Auth.OIDC.ClientID)
	}

	rt := &env.Runtime{
		Config:        cfg,
		Log:           log,
		DB:            db,
		Store:         sqlite.BindStore(db),
		EC2:           ec2Client,
		IAM:           iamClient,
		GHApp:         ghClient,
		Pricing:       priceFetcher,
		RunnerVersion: runnerRes,
		OIDC:          oidcProvider,
		Health:        health.New(),
	}

	// AWS preflight: exercise the reaper's IAM perms via EC2 DryRun
	// so missing permissions surface as a UI banner BEFORE any
	// orphan instance accumulates. Skipped in UI-only dev. Result
	// failures land on rt.Health but do NOT abort startup -- an
	// operator with intentionally trimmed perms keeps a usable
	// console. The banner makes the cost explicit.
	if !cfg.AWS.Disabled && ec2Client != nil {
		results := awspreflight.Run(context.Background(), ec2Client, rt.Health)
		awspreflight.LogResults(log, results)
	}

	// Auth bootstrap: when local login is enabled and there's no
	// user yet, mint one with a random password and log the
	// plaintext to stderr ONCE.
	// Operator copies it. Subsequent starts skip the mint.
	if !cfg.Auth.Disabled && cfg.Auth.Local.Enabled {
		if err := bootstrapUser(context.Background(), rt); err != nil {
			return fmt.Errorf("auth bootstrap: %w", err)
		}
	}

	// Bootstrap API token: lives in the settings table, auto-generated
	// on first start. Loaded into Runtime.BootstrapAPIToken (atomic.Value)
	// so the bootstrap endpoint + LT-materialize have it ready.
	// Rotatable later via the Settings UI.
	if err := settings.EnsureBootstrapToken(context.Background(), rt); err != nil {
		return fmt.Errorf("settings bootstrap: %w", err)
	}

	// Background workers run under a cancellable context so SIGINT
	// stops them cleanly before HTTP shutdown.
	bgCtx, cancelBG := context.WithCancel(context.Background())
	defer cancelBG()

	var bgWG sync.WaitGroup
	if !cfg.GitHub.Disabled {
		if runnerRes != nil {
			runnerRes.Start(bgCtx)
		}
		orch := orchestrator.New(rt)
		reaper := orchestrator.NewReaper(rt)
		// Expose the reaper through Runtime so the /api/reconcile
		// endpoint can trigger an immediate Tick instead of waiting
		// for the next 60s window. Must happen after Reaper has its
		// own Runtime pointer wired by NewReaper to avoid a half-
		// constructed cycle.
		rt.Reaper = reaper
		bgWG.Go(func() { orch.Run(bgCtx) })
		bgWG.Go(func() { reaper.Run(bgCtx) })
	}
	// Prune webhook_deliveries on a daily cadence even in UI-only
	// dev so the table doesn't grow during ad-hoc curl tests against
	// /api/webhook. Cheap one-statement DELETE.
	pruner := orchestrator.NewPruner(rt)
	bgWG.Go(func() { pruner.Run(bgCtx) })

	srv, err := server.New(server.Options{Runtime: rt})
	if err != nil {
		return fmt.Errorf("server init: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case s := <-sigCh:
		log.Info("shutdown requested", "signal", s.String())
		cancelBG()
		// Wait for orchestrator + reaper to finish their current
		// tick before tearing down HTTP. An in-flight spawn (pricing
		// API + CreateFleet + audit writes) can take several seconds.
		// Rushing past it would leave half-stamped jobs.
		bgWG.Wait()
		return srv.Shutdown()
	case err := <-errCh:
		cancelBG()
		bgWG.Wait()
		return err
	}
}

// bootstrapUser inserts the first operator user when the users table is empty.
// Generates a 16-char random password, hashes it, and
// LOGS THE PLAINTEXT ONCE so the operator can copy it.
// Subsequent starts find the user and no-op.
func bootstrapUser(ctx context.Context, rt *env.Runtime) error {
	count, err := rt.Store.User.Count(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	plain, err := authenticator.GeneratePassword()
	if err != nil {
		return fmt.Errorf("generate password: %w", err)
	}
	hash, err := authenticator.HashPassword(plain)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	u := &usermodel.User{
		ID:           uuid.NewString(),
		Email:        rt.Config.Auth.Local.Email,
		PasswordHash: hash,
		Role:         usermodel.RoleAdmin,
		SuperUser:    true,
	}
	if err := rt.Store.User.Put(ctx, u); err != nil {
		return fmt.Errorf("put user: %w", err)
	}
	fmt.Fprintf(os.Stderr,
		"\n========================================================\n"+
			"AUTH BOOTSTRAP: created user %s\n"+
			"  password: %s\n"+
			"  (this is the only time the password is shown)\n"+
			"========================================================\n\n",
		u.Email, plain)
	rt.Log.Info("auth: bootstrap user created", "email", u.Email, "user_id", u.ID)
	return nil
}
