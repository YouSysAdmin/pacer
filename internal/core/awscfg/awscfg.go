// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package awscfg loads aws.Config from the SDK's default credential
// chain (instance profile / env / shared credentials file) plus an
// optional named profile override.
// Built once at server startup and stamped on env.Runtime, and per-service
// clients are constructed against it on demand.
package awscfg

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// Load resolves credentials + region.
// When profile is empty the SDK's default chain runs (env vars, then shared file's [default],
// then EC2 instance profile / ECS task role / Pod IRSA).
func Load(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	return cfg, nil
}
