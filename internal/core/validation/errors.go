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
	field := friendlyField(fe.Field())
	switch fe.Tag() {
	case "required":
		return field + " is required"
	case "required_if", "required_unless", "required_with", "required_without":
		return field + " is required when other fields change"
	case "email":
		return field + " must be a valid email"
	case "uuid", "uuid4", "uuid5":
		return field + " must be a valid UUID"
	case "min":
		// Numeric vs string min/max are surfaced with the same tag
		// in validator/v10; the parameter is unitless. Keep the
		// message neutral ("at least N") so it reads both for
		// counts ("at least 1") and lengths ("at least 1 character").
		return fmt.Sprintf("%s must be at least %s", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, strings.ReplaceAll(fe.Param(), " ", ", "))
	case "gha_safe":
		return field + " uses the reserved \"gha:\" prefix; pick a different prefix"
	case "posix_user":
		return field + " must use only lowercase letters, digits, underscore, or dash, and may not start with a digit or dash"
	case "runner_label":
		return field + " must contain at least one letter, digit, or underscore"
	case "no_slash_or_space":
		return field + " must not contain slashes or spaces"
	case "repo_full_name":
		return field + ` must be in "owner/name" form (e.g. octocat/hello-world)`
	case "not_self_hosted":
		return field + ` must not be "self-hosted" -- that label is added automatically`
	case "runner_label_strict":
		return field + " must use only lowercase letters, digits, underscore, or dash, and not start or end with a dash"
	default:
		// Fallback: validator's library message with quotes stripped.
		return strings.ReplaceAll(fe.Error(), "\"", "")
	}
}

// friendlyLabels overrides the auto-titlecase for json tags that
// expand to unidiomatic English ("Ami Id" -> "AMI ID", "Iam Instance
// Profile" -> "IAM instance profile name"). Add an entry here when a
// new field's auto-rendered form reads awkwardly in error messages.
var friendlyLabels = map[string]string{
	"ami_id":                 "AMI ID",
	"subnet_ids":             "Subnet IDs",
	"security_group_ids":     "Security group IDs",
	"iam_instance_profile":   "IAM instance profile name",
	"runner_group_id":        "Runner group ID",
	"org_name":               "Org login",
	"full_name":              "Repository",
	"project_id":             "Project",
	"max_concurrent_runners": "Max concurrent runners",
	"max_runtime_minutes":    "Max runtime",
	"root_volume_gb":         "Root volume GB",
	"user_data_extra":        "Extra user-data",
	"extra_labels":           "Extra runner labels",
	"runner_user":            "Run runner as",
	"runner_version":         "Runner version",
	"instance_types":         "Instance types",
	"spawn_method":           "Spawn method",
	"allocation_strategy":    "Allocation strategy",
	"is_default":             "Default pool",
}

// friendlyField converts a json tag name to a human-readable label
// suitable for an end-user-facing error message. Known cases come
// from the friendlyLabels map; everything else falls through to a
// sentence-cased version with underscores replaced by spaces
// ("max_concurrent_runners" -> "Max concurrent runners").
//
// The structured FieldError envelope keeps the original json tag in
// FieldError.Field so the SPA can still route the error to the
// matching input -- only the Message is humanized.
func friendlyField(jsonField string) string {
	if jsonField == "" {
		return "This field"
	}
	if v, ok := friendlyLabels[jsonField]; ok {
		return v
	}
	parts := strings.Split(jsonField, "_")
	for i, p := range parts {
		if i == 0 && len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
