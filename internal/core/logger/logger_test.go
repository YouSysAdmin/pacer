// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitLogger_HappyPaths(t *testing.T) {
	for _, tc := range []struct{ level, out, format string }{
		{"info", "stdout", "json"},
		{"DEBUG", "stderr", "text"},
		{"", "", "text"},
	} {
		if _, err := InitLogger(tc.level, tc.out, tc.format, false); err != nil {
			t.Errorf("%+v: unexpected error %v", tc, err)
		}
	}
}

func TestInitLogger_FileSink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pacer.log")
	l, err := InitLogger("info", p, "json", false)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello")
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		t.Fatalf("log file empty or unreadable: %v", err)
	}
}

func TestInitLogger_Rejections(t *testing.T) {
	if _, err := InitLogger("warning", "stdout", "json", false); err == nil {
		t.Error("unknown level should be rejected")
	}
	if _, err := InitLogger("info", "stdout", "yaml", false); err == nil {
		t.Error("unknown format should be rejected")
	}
	// Bad format must fail before the sink is created.
	p := filepath.Join(t.TempDir(), "never.log")
	if _, err := InitLogger("info", p, "yaml", false); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("log file must not be created when format is invalid")
	}
}
