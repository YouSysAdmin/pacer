// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

package validation

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

// FieldError is the normalized error item the SPA renders next to
// the offending input. JSON-stable; the SPA's fields[].field matches
// the json tag the user actually sent.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Humanize converts validator.ValidationErrors into []FieldError.
// Non-validator errors (typically a JSON-decode failure from
// BindAndValidate) collapse to a single field-less entry so the
// caller can still render something.
func Humanize(err error) []FieldError {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return []FieldError{{Message: err.Error()}}
	}
	out := make([]FieldError, 0, len(ve))
	for _, fe := range ve {
		out = append(out, FieldError{
			Field:   fe.Field(),
			Rule:    fe.Tag(),
			Message: defaultMessage(fe),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// Summary collapses []FieldError into one human line, suitable for
// the response envelope's "error" string when the SPA isn't yet
// consuming the structured "fields" array.
func Summary(fes []FieldError) string {
	if len(fes) == 0 {
		return "validation failed"
	}
	if len(fes) == 1 {
		return fes[0].Message
	}
	parts := make([]string, 0, len(fes))
	for _, fe := range fes {
		parts = append(parts, fe.Message)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func defaultMessage(fe validator.FieldError) string {
	field := fe.Field()
	switch fe.Tag() {
	case "required":
		return field + " is required"
	case "required_if", "required_unless", "required_with", "required_without":
		return field + " is required (conditional rule " + fe.Tag() + "=" + fe.Param() + " not satisfied)"
	case "email":
		return field + " must be a valid email"
	case "uuid", "uuid4", "uuid5":
		return field + " must be a valid UUID"
	case "min":
		return fmt.Sprintf("%s must be at least %s", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, strings.ReplaceAll(fe.Param(), " ", ", "))
	case "gha_safe":
		return field + " uses the reserved gha:* prefix (tool-managed)"
	case "posix_user":
		return field + " must match POSIX user-name charset [a-z_][a-z0-9_-]*"
	case "runner_label":
		return field + " must contain at least one alphanumeric or underscore character"
	case "no_slash_or_space":
		return field + " must not contain slashes or whitespace"
	case "repo_full_name":
		return field + ` must be "owner/name" with non-empty halves`
	case "not_self_hosted":
		return field + ` must not be "self-hosted" (auto-derived)`
	case "runner_label_strict":
		return field + " must be lowercase alphanumeric / underscore / dash, and not start or end with a dash (use the same form your workflows reference in runs-on)"
	default:
		// Fallback: validator's library message with quotes stripped.
		return strings.ReplaceAll(fe.Error(), "\"", "")
	}
}
