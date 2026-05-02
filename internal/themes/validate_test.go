package themes

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantErr        bool
		wantNormalised string
	}{
		{"empty", "", true, ""},
		{"whitespace only", "   ", true, ""},
		{"valid simple", "my-theme", false, "my-theme"},
		{"valid with space", "theme 1", false, "theme 1"},
		{"valid single char", "a", false, "a"},
		{"valid 64 chars", strings.Repeat("a", 64), false, strings.Repeat("a", 64)},
		{"invalid 65 chars", strings.Repeat("a", 65), true, ""},
		{"invalid html-ish", "theme<script>", true, ""},
		{"invalid semicolon", "theme;name", true, ""},
		{"valid trims surrounding whitespace", "  hello  ", false, "hello"},
		{"valid underscore", "my_theme_2", false, "my_theme_2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateName(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateName(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.wantNormalised {
				t.Errorf("validateName(%q) = %q, want %q", tt.input, got, tt.wantNormalised)
			}
		})
	}
}

func TestValidateHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantErr        bool
		wantNormalised string
	}{
		{"three-digit lower", "#000", false, "#000"},
		{"six-digit lower", "#abcdef", false, "#abcdef"},
		{"three-digit upper normalised", "#ABC", false, "#abc"},
		{"six-digit mixed normalised", "#AbCdEf", false, "#abcdef"},
		{"named color rejected", "red", true, ""},
		{"rgb function rejected", "rgb(0,0,0)", true, ""},
		{"non-hex chars rejected", "#zzzzzz", true, ""},
		{"seven digits rejected", "#1234567", true, ""},
		{"empty rejected", "", true, ""},
		{"trailing space not trimmed", "#fff ", true, ""},
		{"leading space not trimmed", " #fff", true, ""},
		{"missing hash rejected", "abcdef", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateHex(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateHex(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateHex(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.wantNormalised {
				t.Errorf("validateHex(%q) = %q, want %q", tt.input, got, tt.wantNormalised)
			}
		})
	}
}

func TestValidateRadius(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string
	}{
		{"zero", "0", false, "0"},
		{"px", "10px", false, "10px"},
		{"rem fractional", "0.5rem", false, "0.5rem"},
		{"em integer", "1em", false, "1em"},
		{"large px", "999px", false, "999px"},
		{"unitless number", "10", true, ""},
		{"unsupported unit", "10pt", true, ""},
		{"negative", "-1px", true, ""},
		{"empty", "", true, ""},
		{"trailing junk", "10px;", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateRadius(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateRadius(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateRadius(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("validateRadius(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateFontFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"system-ui", "system-ui", false},
		{"comma-separated quoted", `"SF Mono", monospace`, false},
		{"complex stack", `-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif`, false},
		{"semicolon and brace", "Arial; }", true},
		{"angle bracket open", "<script>", true},
		{"newline rejected", "Foo\nBar", true},
		{"carriage return rejected", "Foo\rBar", true},
		{"tab rejected", "Foo\tBar", true},
		{"empty rejected", "", true},
		{"too long", strings.Repeat("a", 257), true},
		{"backslash rejected", `Arial\`, true},
		{"close brace rejected", "Arial}", true},
		{"open brace rejected", "Arial{", true},
		{"close angle rejected", "Arial>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateFontFamily(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateFontFamily(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateFontFamily(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.input {
				t.Errorf("validateFontFamily(%q) = %q, want unchanged %q", tt.input, got, tt.input)
			}
		})
	}
}

// TestValidateInputAccumulatesErrors verifies the headline UX property: when
// multiple fields are bad, the returned ValidationError carries all of them
// at once so the form can surface every issue in a single render.
func TestValidateInputAccumulatesErrors(t *testing.T) {
	t.Parallel()

	in := Input{
		Name:           "",
		ColorBg:        "red",
		ColorSurface:   "#zzz",
		ColorBorder:    "#000",
		ColorText:      "#000",
		ColorTextMuted: "#000",
		ColorAccent:    "#000",
		FontFamily:     "<script>",
		Radius:         "abc",
	}
	_, err := validateInput(in)
	if err == nil {
		t.Fatal("validateInput returned nil error for all-bad input")
	}
	if !IsValidationError(err) {
		t.Fatalf("IsValidationError = false, want true")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err is not *ValidationError: %T", err)
	}
	for _, key := range []string{"name", "color_bg", "color_surface", "font_family", "radius"} {
		if ve.Fields[key] == "" {
			t.Errorf("ValidationError missing field %q (got fields: %v)", key, ve.Fields)
		}
	}
}

// TestValidateInputNormalises checks that hex values are lower-cased and the
// trim-eligible string fields have surrounding whitespace stripped on success.
func TestValidateInputNormalises(t *testing.T) {
	t.Parallel()

	in := Input{
		Name:           "  My Theme  ",
		ColorBg:        "#FFF",
		ColorSurface:   "#ABCDEF",
		ColorBorder:    "#000",
		ColorText:      "#FFF",
		ColorTextMuted: "#000",
		ColorAccent:    "#7B93FF",
		FontFamily:     "  system-ui  ",
		FontFamilyMono: "  monospace  ",
		Radius:         "  10px  ",
	}
	out, err := validateInput(in)
	if err != nil {
		t.Fatalf("validateInput unexpected error: %v", err)
	}
	if out.Name != "My Theme" {
		t.Errorf("Name = %q, want %q", out.Name, "My Theme")
	}
	if out.ColorBg != "#fff" {
		t.Errorf("ColorBg = %q, want %q", out.ColorBg, "#fff")
	}
	if out.ColorSurface != "#abcdef" {
		t.Errorf("ColorSurface = %q, want %q", out.ColorSurface, "#abcdef")
	}
	if out.ColorAccent != "#7b93ff" {
		t.Errorf("ColorAccent = %q, want %q", out.ColorAccent, "#7b93ff")
	}
	if out.FontFamily != "system-ui" {
		t.Errorf("FontFamily = %q, want %q", out.FontFamily, "system-ui")
	}
	if out.FontFamilyMono != "monospace" {
		t.Errorf("FontFamilyMono = %q, want %q", out.FontFamilyMono, "monospace")
	}
	if out.Radius != "10px" {
		t.Errorf("Radius = %q, want %q", out.Radius, "10px")
	}
}

// TestValidateInputAllowsEmptyMono verifies that font_family_mono is the only
// optional field; it must accept an empty string and skip validation entirely.
func TestValidateInputAllowsEmptyMono(t *testing.T) {
	t.Parallel()

	in := Input{
		Name:           "ok",
		ColorBg:        "#000",
		ColorSurface:   "#000",
		ColorBorder:    "#000",
		ColorText:      "#000",
		ColorTextMuted: "#000",
		ColorAccent:    "#000",
		FontFamily:     "system-ui",
		FontFamilyMono: "",
		Radius:         "10px",
	}
	out, err := validateInput(in)
	if err != nil {
		t.Fatalf("validateInput unexpected error: %v", err)
	}
	if out.FontFamilyMono != "" {
		t.Errorf("FontFamilyMono = %q, want empty", out.FontFamilyMono)
	}
}

// TestValidationErrorMessageStable asserts the Error() string is deterministic
// (sorted keys) so log output and snapshot tests do not flake on map ordering.
func TestValidationErrorMessageStable(t *testing.T) {
	t.Parallel()

	ve := &ValidationError{
		Fields: map[string]string{
			"radius":  "bad",
			"name":    "bad",
			"color_x": "bad",
		},
	}
	got := ve.Error()
	if !strings.HasPrefix(got, "theme validation failed: ") {
		t.Errorf("Error() prefix wrong: %q", got)
	}
	// Sorted order: color_x, name, radius.
	wantSubstr := "color_x: bad; name: bad; radius: bad"
	if !strings.Contains(got, wantSubstr) {
		t.Errorf("Error() = %q, want substring %q", got, wantSubstr)
	}
}
