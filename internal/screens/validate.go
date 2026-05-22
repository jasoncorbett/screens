package screens

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ValidationError carries per-field validation messages back to the admin UI
// so the redirect flash can name the offending input.
type ValidationError struct {
	Fields map[string]string // field name -> human-readable message
}

// Error returns a deterministic single-line summary of the failed fields.
// Field names are sorted so the message is stable across calls.
func (v *ValidationError) Error() string {
	keys := make([]string, 0, len(v.Fields))
	for k := range v.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, v.Fields[k]))
	}
	return "screen validation failed: " + strings.Join(parts, "; ")
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// nameRe is the whitelist for screen and page names: 1-64 characters drawn
// from letters, digits, spaces, hyphens, and underscores.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,64}$`)

const (
	minRotationSeconds = 5
	maxRotationSeconds = 3600
)

// validateScreenInput validates and normalises the Screen create/update
// payload. Returns a non-nil *ValidationError if any field fails.
func validateScreenInput(in ScreenInput) (ScreenInput, error) {
	out := ScreenInput{
		Name:                    strings.TrimSpace(in.Name),
		ThemeID:                 strings.TrimSpace(in.ThemeID),
		RotationIntervalSeconds: in.RotationIntervalSeconds,
	}
	fields := map[string]string{}

	if !nameRe.MatchString(out.Name) {
		fields["name"] = "name must be 1-64 characters using letters, digits, spaces, hyphens, or underscores"
	}
	if out.ThemeID == "" {
		fields["theme_id"] = "theme is required"
	}
	if out.RotationIntervalSeconds < minRotationSeconds || out.RotationIntervalSeconds > maxRotationSeconds {
		fields["rotation_interval_seconds"] = fmt.Sprintf("must be between %d and %d", minRotationSeconds, maxRotationSeconds)
	}

	if len(fields) > 0 {
		return ScreenInput{}, &ValidationError{Fields: fields}
	}
	return out, nil
}

// validatePageName allows empty (used for unnamed pages) and otherwise
// applies the same regex as screen names. Used by Page CRUD (TASK-023);
// pre-shipped here to keep validation centralised.
func validatePageName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !nameRe.MatchString(v) {
		return "", fmt.Errorf("name must be 1-64 characters using letters, digits, spaces, hyphens, or underscores (or empty)")
	}
	return v, nil
}
