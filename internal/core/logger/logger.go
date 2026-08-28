// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package logger builds the process-wide slog handler.
// Levels: debug/info/warn/error.
// Format: text (with optional ANSI color) or json.
// Sink: stdout, stderr, or an absolute file path.
// InitLogger is called once from cli/serve.go and installs the handler as the
// slog default. Everything else just calls slog.Info / slog.Error.
package logger

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ANSI color codes.
var (
	colorDebug = "\033[36m" // Cyan
	colorInfo  = "\033[32m" // Green
	colorWarn  = "\033[33m" // Yellow
	colorError = "\033[31m" // Red
	colorReset = "\033[0m"
)

// InitLogger initializes slog with level, output, format, and optional coloring.
//
// levelStr: "DEBUG", "INFO"(default), "WARN", "ERROR"
// outputDest: "stdout" or file path
// format: "text" or "json"
// color: enable coloring for text format
func InitLogger(levelStr, outputDest, format string, color bool) (*slog.Logger, error) {
	if format == "" {
		format = "text"
	}

	// Determine log level
	var level slog.Level
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	case "":
		level = slog.LevelInfo
	default:
		return nil, fmt.Errorf("invalid log level: %s", levelStr)
	}

	// Reject a bad format before touching the filesystem so an error
	// path never leaves a log file open.
	format = strings.ToLower(format)
	if format != "json" && format != "text" {
		return nil, fmt.Errorf("invalid log format: %s", format)
	}

	// Determine output destination
	var output *os.File
	switch {
	case outputDest == "" || strings.EqualFold(outputDest, "stdout"):
		output = os.Stdout
	case strings.EqualFold(outputDest, "stderr"):
		output = os.Stderr
	default:
		var err error
		output, err = os.OpenFile(outputDest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler

	switch format {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	default:
		if color {
			handler = &ColorHandler{
				output: output,
				level:  level,
			}
		} else {
			handler = slog.NewTextHandler(output, opts)
		}
	}

	logger := slog.New(handler)
	// if format == "json" {
	//	logger = logger.With("app", pkg.AppName, "version", pkg.Version)
	//}

	// set as default logger
	slog.SetDefault(logger)

	return logger, nil
}

// ColorHandler renders log entries with colored level prefixes.
type ColorHandler struct {
	output *os.File
	level  slog.Level
	attrs  []slog.Attr
	group  string
}

// Enabled reports whether the level is enabled.
func (c *ColorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= c.level
}

// Handle writes the log entry with colored prefix.
func (c *ColorHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// Select color based on level
	var color string
	switch r.Level {
	case slog.LevelDebug:
		color = colorDebug
	case slog.LevelInfo:
		color = colorInfo
	case slog.LevelWarn:
		color = colorWarn
	case slog.LevelError:
		color = colorError
	default:
		color = colorReset
	}

	// Timestamp
	buf.WriteString(r.Time.Format("2006-01-02T15:04:05.000Z07:00"))
	buf.WriteString(" ")

	// Colored level
	buf.WriteString(color)
	buf.WriteString("[")
	buf.WriteString(strings.ToUpper(r.Level.String()))
	buf.WriteString("]")
	buf.WriteString(colorReset)
	buf.WriteString(" ")

	// Message
	buf.WriteString(r.Message)

	// Static attributes (WithAttrs)
	for _, a := range c.attrs {
		a.Value = a.Value.Resolve()
		writeAttr(&buf, c.attrKey(a), a.Value)
	}

	// Record attributes
	r.Attrs(func(a slog.Attr) bool {
		a.Value = a.Value.Resolve()
		writeAttr(&buf, c.attrKey(a), a.Value)
		return true
	})

	buf.WriteString("\n")
	_, err := c.output.Write(buf.Bytes())
	return err
}

// WithAttrs returns a new handler with additional attributes.
func (c *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Copy current handler
	newHandler := *c

	// Append new attrs
	newHandler.attrs = append(append([]slog.Attr{}, c.attrs...), attrs...)
	return &newHandler
}

// WithGroup returns a new handler with a group prefix.
func (c *ColorHandler) WithGroup(name string) slog.Handler {
	newHandler := *c
	if c.group != "" {
		newHandler.group = c.group + "." + name
	} else {
		newHandler.group = name
	}
	return &newHandler
}

// attrKey formats the attribute key with the group prefix.
func (c *ColorHandler) attrKey(a slog.Attr) string {
	if c.group != "" {
		return c.group + "." + a.Key
	}
	return a.Key
}

func writeAttr(buf *bytes.Buffer, key string, v slog.Value) {
	switch v.Kind() {
	case slog.KindGroup:
		for _, ga := range v.Group() {
			ga.Value = ga.Value.Resolve()
			fmt.Fprintf(buf, " %s.%s=%v", key, ga.Key, ga.Value.Any())
		}
	default:
		fmt.Fprintf(buf, " %s=%v", key, v.Any())
	}
}
