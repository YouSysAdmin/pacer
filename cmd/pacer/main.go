// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package main

import (
	"log/slog"
	"os"

	"github.com/yousysadmin/pacer/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		slog.Error("command failed", "err", err)
		os.Exit(1)
	}
}
