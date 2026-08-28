// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package validation

import (
	"strings"

	"github.com/go-playground/validator/v10"
)

// registerCustom wires pacer-specific validators onto v. Each rule
// has a short, stable tag name (matches what defaultMessage knows
// about) and operates on a string field unless noted.
func registerCustom(v *validator.Validate) {
	// gha_safe rejects strings (or string map-keys when used with
	// dive,keys) that fall in the gha:* tool-managed namespace. The
	// taxonomy doc (CLAUDE.md "Tag taxonomy") explains why this is
	// reserved: the orchestrator stamps gha:* last, and an
	// operator-supplied tag with the same prefix would shadow it.
	_ = v.RegisterValidation("gha_safe", func(fl validator.FieldLevel) bool {
		s := strings.TrimSpace(fl.Field().String())
		return !strings.HasPrefix(strings.ToLower(s), "gha:")
	})

	// posix_user matches ^[a-z_][a-z0-9_-]*$ -- belt-and-braces
	// against shell metacharacters slipping through user-data into
	// the sudo command line. Empty string passes (the field is
	// optional in the pool DTO; required-ness is a separate tag).
	_ = v.RegisterValidation("posix_user", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == "" {
			return true
		}
		for i, r := range s {
			ok := false
			switch {
			case r >= 'a' && r <= 'z':
				ok = true
			case r == '_':
				ok = true
			case i > 0 && (r >= '0' && r <= '9' || r == '-'):
				ok = true
			}
			if !ok {
				return false
			}
		}
		return true
	})

	// runner_label requires the string to contain at least one
	// alphanumeric or underscore (i.e. SanitizeLabel(s) != ""). Used
	// on pool.Name and on each entry in pool.ExtraLabels so a label
	// of "---" or " " can't slip through.
	//
	// The check is inlined (rather than calling pool.SanitizeLabel)
	// to avoid an import cycle: pool imports validation indirectly
	// via env.Runtime, and validation must stay leaf-y.
	_ = v.RegisterValidation("runner_label", func(fl validator.FieldLevel) bool {
		s := strings.ToLower(fl.Field().String())
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
				return true
			}
		}
		return false
	})

	// no_slash_or_space rejects '/', spaces, and tabs -- used on the
	// project.org_name field where the input must be a bare GitHub
	// org login, not "github.com/foo" or "  foo  ".
	_ = v.RegisterValidation("no_slash_or_space", func(fl validator.FieldLevel) bool {
		return !strings.ContainsAny(fl.Field().String(), "/ \t")
	})

	// repo_full_name verifies "<owner>/<name>" with non-empty halves.
	// The repo Bind handler splits on '/' downstream; we want the
	// shape error here so the SPA can highlight the field.
	_ = v.RegisterValidation("repo_full_name", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if strings.Count(s, "/") != 1 || strings.ContainsAny(s, " \t") {
			return false
		}
		owner, name, _ := strings.Cut(s, "/")
		return owner != "" && name != ""
	})

	// not_self_hosted rejects strings that sanitize to "self-hosted".
	// Used on pool.extra_labels entries so an operator can't shadow
	// the auto-derived label that every spawn carries.
	_ = v.RegisterValidation("not_self_hosted", func(fl validator.FieldLevel) bool {
		return sanitizeLabel(fl.Field().String()) != "self-hosted"
	})

	// runner_label_strict requires the input to ALREADY equal the
	// canonical SanitizeLabel form. Used on pool.Name so the
	// displayed name, the persisted row, the runner label, and the
	// `runs-on:` value in workflows all match. Without this, "#433"
	// would persist literally but register as "433", and the
	// operator wouldn't notice until a workflow's runs-on missed.
	//
	// For ExtraLabels, the looser runner_label rule still applies --
	// auxiliary tags can sanitize-mangle (e.g. "Production GPU"
	// becomes "production-gpu") because they aren't the primary
	// match key. Pool name is the primary key, so it gets the
	// strict rule.
	_ = v.RegisterValidation("runner_label_strict", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == "" {
			return false
		}
		return s == sanitizeLabel(s)
	})
}

// sanitizeLabel mirrors pool.SanitizeLabel. Duplicated to keep the
// validation package leaf-y (importing pool here would create an
// import cycle once pool depends on validation transitively through
// env.Runtime).
func sanitizeLabel(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
