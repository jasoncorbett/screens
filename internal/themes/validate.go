package themes

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ValidationError carries per-field validation messages back to the admin UI
// so that a re-rendered form can show errors next to the offending input.
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
	return "theme validation failed: " + strings.Join(parts, "; ")
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9 _-]{1,64}$`)
	hexRe    = regexp.MustCompile(`^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$`)
	radiusRe = regexp.MustCompile(`^(0|[0-9]+px|[0-9]+(\.[0-9]+)?(rem|em))$`)
)

// validateInput runs every field through its validator and returns a
// non-nil *ValidationError if any field fails. On success it returns a
// normalised copy of the input (lower-cased hex values, trimmed strings).
// Hex values are NOT trimmed; admins who paste leading/trailing whitespace
// into a color field get an explicit error so they fix the underlying
// tooling rather than silently accepting malformed input.
func validateInput(in Input) (Input, error) {
	out := Input{
		Name:           strings.TrimSpace(in.Name),
		ColorBg:        in.ColorBg,
		ColorSurface:   in.ColorSurface,
		ColorBorder:    in.ColorBorder,
		ColorText:      in.ColorText,
		ColorTextMuted: in.ColorTextMuted,
		ColorAccent:    in.ColorAccent,
		FontFamily:     strings.TrimSpace(in.FontFamily),
		FontFamilyMono: strings.TrimSpace(in.FontFamilyMono),
		Radius:         strings.TrimSpace(in.Radius),
	}
	fields := map[string]string{}

	if v, err := validateName(out.Name); err != nil {
		fields["name"] = err.Error()
	} else {
		out.Name = v
	}

	type colorField struct {
		key string
		dst *string
	}
	colors := []colorField{
		{"color_bg", &out.ColorBg},
		{"color_surface", &out.ColorSurface},
		{"color_border", &out.ColorBorder},
		{"color_text", &out.ColorText},
		{"color_text_muted", &out.ColorTextMuted},
		{"color_accent", &out.ColorAccent},
	}
	for _, c := range colors {
		if v, err := validateHex(*c.dst); err != nil {
			fields[c.key] = err.Error()
		} else {
			*c.dst = v
		}
	}

	if v, err := validateFontFamily(out.FontFamily); err != nil {
		fields["font_family"] = err.Error()
	} else {
		out.FontFamily = v
	}

	if out.FontFamilyMono != "" {
		if v, err := validateFontFamily(out.FontFamilyMono); err != nil {
			fields["font_family_mono"] = err.Error()
		} else {
			out.FontFamilyMono = v
		}
	}

	if v, err := validateRadius(out.Radius); err != nil {
		fields["radius"] = err.Error()
	} else {
		out.Radius = v
	}

	if len(fields) > 0 {
		return Input{}, &ValidationError{Fields: fields}
	}
	return out, nil
}

// validateName rejects empty / whitespace-only names and enforces the
// character whitelist plus a 1-64 length cap (encoded in nameRe).
func validateName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !nameRe.MatchString(v) {
		return "", fmt.Errorf("name must be 1-64 characters using letters, digits, spaces, hyphens, or underscores")
	}
	return v, nil
}

// validateHex matches against hexRe and returns the lower-cased form on
// success. Inputs are NOT trimmed; surrounding whitespace fails fast.
func validateHex(v string) (string, error) {
	if !hexRe.MatchString(v) {
		return "", fmt.Errorf("value must be a hex color like #1a2b3c or #abc")
	}
	return strings.ToLower(v), nil
}

// validateRadius matches against radiusRe (e.g. "0", "10px", "0.5rem", "1em").
func validateRadius(v string) (string, error) {
	if !radiusRe.MatchString(v) {
		return "", fmt.Errorf("value must be 0, Npx, N.Nrem, or N.Nem")
	}
	return v, nil
}

// validateFontFamily rejects values containing characters that would break
// out of a CSS declaration: ';', '{', '}', '<', '>', '\\', or any byte below
// 0x20 (control characters including newline, carriage return, tab). Empty
// strings are rejected. Length cap is 256 characters.
func validateFontFamily(v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("font family must not be empty")
	}
	if len(v) > 256 {
		return "", fmt.Errorf("font family must be 256 characters or fewer")
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 0x20 {
			return "", fmt.Errorf("font family must not contain control characters")
		}
		switch c {
		case ';', '{', '}', '<', '>', '\\':
			return "", fmt.Errorf("font family must not contain ; { } < > or backslash")
		}
	}
	return v, nil
}
