package text

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

func TestValidateAccepts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantText string
	}{
		{name: "plain text", input: `{"text":"hello"}`, wantText: "hello"},
		{name: "trims surrounding whitespace", input: `{"text":"  hello  "}`, wantText: "hello"},
		{name: "preserves embedded newlines", input: `{"text":"line\nbreak"}`, wantText: "line\nbreak"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := validate([]byte(tt.input))
			if err != nil {
				t.Fatalf("validate(%q) returned error: %v", tt.input, err)
			}
			if inst.Type != Type {
				t.Errorf("inst.Type = %q, want %q", inst.Type, Type)
			}
			cfg, ok := inst.Config.(Config)
			if !ok {
				t.Fatalf("inst.Config has type %T, want Config", inst.Config)
			}
			if cfg.Text != tt.wantText {
				t.Errorf("cfg.Text = %q, want %q", cfg.Text, tt.wantText)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	tooLong := strings.Repeat("a", MaxTextLength+1)

	tests := []struct {
		name      string
		input     string
		wantInErr string
	}{
		{name: "not JSON", input: "not-json", wantInErr: "JSON"},
		{name: "empty string", input: `{"text":""}`, wantInErr: "empty"},
		{name: "whitespace-only", input: `{"text":"   "}`, wantInErr: "empty"},
		{name: "exceeds length cap", input: `{"text":"` + tooLong + `"}`, wantInErr: "4096"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validate([]byte(tt.input))
			if err == nil {
				t.Fatalf("validate(%q) returned nil error, want non-nil", tt.input)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestDefaultConfigValidates(t *testing.T) {
	raw := defaultConfig()
	inst, err := validate(raw)
	if err != nil {
		t.Fatalf("validate(defaultConfig()) returned error: %v", err)
	}
	if inst.Type != Type {
		t.Errorf("inst.Type = %q, want %q", inst.Type, Type)
	}
	cfg, ok := inst.Config.(Config)
	if !ok {
		t.Fatalf("inst.Config has type %T, want Config", inst.Config)
	}
	if cfg.Text == "" {
		t.Error("cfg.Text from default is empty, want non-empty")
	}
}

func TestRenderContainsText(t *testing.T) {
	reg := Registration()
	w := reg.New()
	component := w.Render(context.Background(),
		widget.Instance{Type: Type, Config: Config{Text: "hello"}},
		themes.Theme{},
	)
	if component == nil {
		t.Fatal("Render returned nil component")
	}
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("component.Render returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("rendered output %q does not contain %q", buf.String(), "hello")
	}
}

func TestRenderHTMLEscapes(t *testing.T) {
	reg := Registration()
	w := reg.New()
	component := w.Render(context.Background(),
		widget.Instance{Type: Type, Config: Config{Text: "<script>alert('x')</script>"}},
		themes.Theme{},
	)
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("component.Render returned error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>") {
		t.Errorf("rendered output contains raw <script>: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("rendered output does not contain HTML-escaped &lt;script&gt;: %q", out)
	}
}

func TestRenderNoInlineStyle(t *testing.T) {
	reg := Registration()
	w := reg.New()
	component := w.Render(context.Background(),
		widget.Instance{Type: Type, Config: Config{Text: "hello"}},
		themes.Theme{},
	)
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("component.Render returned error: %v", err)
	}
	if strings.Contains(buf.String(), `style="`) {
		t.Errorf("rendered output contains inline style attribute: %q", buf.String())
	}
}

func TestRenderThroughRegistry(t *testing.T) {
	r := widget.NewRegistry()
	if err := r.Register(Registration()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	component, err := r.Render(context.Background(), Type, []byte(`{"text":"hi"}`), themes.Theme{})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if component == nil {
		t.Fatal("Render returned nil component")
	}
	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("component.Render returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "hi") {
		t.Errorf("rendered output %q does not contain %q", buf.String(), "hi")
	}
}

func TestDefaultContainsTextAfterImport(t *testing.T) {
	// This test file lives in package text, so the package's init()
	// has run and registered the widget with widget.Default().
	reg, ok := widget.Default().Get(Type)
	if !ok {
		t.Fatalf("widget.Default().Get(%q) returned ok=false", Type)
	}
	if reg.Type != Type {
		t.Errorf("reg.Type = %q, want %q", reg.Type, Type)
	}
	if reg.DisplayName != "Text" {
		t.Errorf("reg.DisplayName = %q, want %q", reg.DisplayName, "Text")
	}
	if reg.Description == "" {
		t.Error("reg.Description is empty, want non-empty")
	}
	if reg.New == nil {
		t.Error("reg.New is nil")
	}
	if reg.DefaultConfig == nil {
		t.Error("reg.DefaultConfig is nil")
	}
	if reg.ValidateConfig == nil {
		t.Error("reg.ValidateConfig is nil")
	}
}
