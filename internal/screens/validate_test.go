package screens

import (
	"strings"
	"testing"
)

func TestValidateScreenInput(t *testing.T) {
	t.Parallel()

	const validTheme = "abcdef1234567890abcdef1234567890"
	name64 := strings.Repeat("a", 64)
	name65 := strings.Repeat("a", 65)

	tests := []struct {
		name      string
		in        ScreenInput
		wantOK    bool
		wantField string // expected key in ValidationError.Fields when wantOK is false
	}{
		{"empty name", ScreenInput{Name: "", ThemeID: validTheme, RotationIntervalSeconds: 30}, false, "name"},
		{"whitespace-only name", ScreenInput{Name: "   ", ThemeID: validTheme, RotationIntervalSeconds: 30}, false, "name"},
		{"name with script tag", ScreenInput{Name: "screen<script>", ThemeID: validTheme, RotationIntervalSeconds: 30}, false, "name"},
		{"name 65 chars", ScreenInput{Name: name65, ThemeID: validTheme, RotationIntervalSeconds: 30}, false, "name"},
		{"name 64 chars", ScreenInput{Name: name64, ThemeID: validTheme, RotationIntervalSeconds: 30}, true, ""},
		{"empty theme_id", ScreenInput{Name: "kitchen", ThemeID: "", RotationIntervalSeconds: 30}, false, "theme_id"},
		{"whitespace-only theme_id", ScreenInput{Name: "kitchen", ThemeID: "   ", RotationIntervalSeconds: 30}, false, "theme_id"},
		{"rotation 4", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: 4}, false, "rotation_interval_seconds"},
		{"rotation 5", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: 5}, true, ""},
		{"rotation 3600", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: 3600}, true, ""},
		{"rotation 3601", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: 3601}, false, "rotation_interval_seconds"},
		{"rotation 0", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: 0}, false, "rotation_interval_seconds"},
		{"rotation -1", ScreenInput{Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: -1}, false, "rotation_interval_seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := validateScreenInput(tt.in)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("validateScreenInput(%+v) = %v, want nil error", tt.in, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateScreenInput(%+v) returned nil error, want *ValidationError", tt.in)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("validateScreenInput(%+v) returned %T, want *ValidationError", tt.in, err)
			}
			if ve.Fields[tt.wantField] == "" {
				t.Errorf("Fields[%q] empty in %v", tt.wantField, ve.Fields)
			}
			if out != (ScreenInput{}) {
				t.Errorf("on failure expected zero ScreenInput, got %+v", out)
			}
		})
	}
}

func TestValidateScreenInputNormalises(t *testing.T) {
	t.Parallel()
	out, err := validateScreenInput(ScreenInput{
		Name:                    "  kitchen  ",
		ThemeID:                 "  theme-1  ",
		RotationIntervalSeconds: 30,
	})
	if err != nil {
		t.Fatalf("validateScreenInput: %v", err)
	}
	if out.Name != "kitchen" {
		t.Errorf("Name = %q, want trimmed %q", out.Name, "kitchen")
	}
	if out.ThemeID != "theme-1" {
		t.Errorf("ThemeID = %q, want trimmed %q", out.ThemeID, "theme-1")
	}
}

func TestValidatePageName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty allowed", "", "", false},
		{"whitespace becomes empty", "   ", "", false},
		{"trims valid name", "  clock  ", "clock", false},
		{"rejects script tag", "page<script>", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validatePageName(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validatePageName(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("validatePageName(%q) = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("validatePageName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
