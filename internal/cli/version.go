// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/pacer/pkg"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the binary version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s\n", pkg.AppName, pkg.Version)
		},
	}
}
