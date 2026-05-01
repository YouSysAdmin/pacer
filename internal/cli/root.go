// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package cli wires the cobra commands the pacer binary exposes.
// Today: serve + version. Project / repo management is done via the
// web UI, not here - keep this surface deliberately small.
package cli

import "github.com/spf13/cobra"

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "pacer",
		Short:         "Self-hosted GitHub Actions runner orchestrator for AWS",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().String("config", "", "config file path (yaml); defaults to ./pacer.yaml")
	root.AddCommand(newServeCmd())
	root.AddCommand(newVersionCmd())
	return root
}
