package text

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// TestAdversarial_BoundaryAccept4096 pins the inclusive upper boundary:
// exactly MaxTextLength bytes is accepted. The developer's tooLong test
// covers MaxTextLength+1; this test pins the other side of the cliff so
// a future "off by one" change to validate (e.g., switching `>` to `>=`)
// fails this test rather than silently shrinking the contract.
func TestAdversarial_BoundaryAccept4096(t *testing.T) {
	exact := strings.Repeat("a", MaxTextLength)
	inst, err := validate([]byte(`{"text":"` + exact + `"}`))
	if err != nil {
		t.Fatalf("validate of %d-byte text returned error: %v", MaxTextLength, err)
	}
	if cfg := inst.Config.(Config); len(cfg.Text) != MaxTextLength {
		t.Errorf("len(cfg.Text) = %d, want %d", len(cfg.Text), MaxTextLength)
	}
}

// TestAdversarial_TrimRunsBeforeLengthCheck pins the documented behaviour
// that whitespace is trimmed BEFORE the length check. A 4096-character
// body padded with whitespace is valid even though the raw JSON string
// exceeds MaxTextLength bytes. If a future refactor swaps the order, this
// test fails and forces the spec amendment.
func TestAdversarial_TrimRunsBeforeLengthCheck(t *testing.T) {
	body := strings.Repeat("a", MaxTextLength)
	padded := strings.Repeat(" ", 100) + body + strings.Repeat(" ", 100)
	inst, err := validate([]byte(`{"text":"` + padded + `"}`))
	if err != nil {
		t.Fatalf("padded body should be accepted after trim: %v", err)
	}
	if cfg := inst.Config.(Config); cfg.Text != body {
		t.Errorf("trimmed text mismatch: len=%d", len(cfg.Text))
	}
}

// TestAdversarial_LengthCapIsBytes confirms the implementation's cap is
// byte-based, not rune-based. The task document explicitly endorses
// "the cap is bytes, not lines" -- this test pins that decision so a
// future drift to runes is a deliberate spec change. 1024 four-byte
// emojis = 4096 bytes (accepted); 1025 = 4100 bytes (rejected).
func TestAdversarial_LengthCapIsBytes(t *testing.T) {
	emoji := "\xF0\x9F\x8E\x89" // 4-byte UTF-8 sequence (party popper)
	exact := strings.Repeat(emoji, MaxTextLength/4)
	if utf8.RuneCountInString(exact) != MaxTextLength/4 {
		t.Fatalf("setup: rune count")
	}
	if len(exact) != MaxTextLength {
		t.Fatalf("setup: byte length")
	}
	if _, err := validate([]byte(`{"text":"` + exact + `"}`)); err != nil {
		t.Errorf("%d emojis (%d bytes) should be accepted: %v", MaxTextLength/4, MaxTextLength, err)
	}

	tooMany := strings.Repeat(emoji, MaxTextLength/4+1) // 4100 bytes
	if _, err := validate([]byte(`{"text":"` + tooMany + `"}`)); err == nil {
		t.Errorf("%d emojis (%d bytes) should be rejected", MaxTextLength/4+1, len(tooMany))
	}
}

// TestAdversarial_RejectsMalformedJSONShapes pins all the JSON shape
// variants that should error. Each row is a JSON value the validator
// must NOT accept.
func TestAdversarial_RejectsMalformedJSONShapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"top-level null", `null`},
		{"top-level array", `[]`},
		{"top-level number", `42`},
		{"top-level string", `"hello"`},
		{"empty object (text missing)", `{}`},
		{"text is null", `{"text":null}`},
		{"text is number", `{"text":123}`},
		{"text is array", `{"text":[]}`},
		{"text is bool", `{"text":true}`},
		{"empty input", ``},
		{"trailing comma", `{"text":"x",}`},
		{"unterminated string", `{"text":"hello`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validate([]byte(tt.input))
			if err == nil {
				t.Errorf("validate(%q) returned nil error, want non-nil", tt.input)
			}
		})
	}
}

// TestAdversarial_NilBytesRejected confirms the validator does not panic
// on nil input and produces the expected JSON-error path.
func TestAdversarial_NilBytesRejected(t *testing.T) {
	_, err := validate(nil)
	if err == nil {
		t.Error("nil bytes should be rejected")
	}
}

// TestAdversarial_UnknownExtraFieldsAccepted confirms json.Unmarshal's
// default ignore-unknown-fields behaviour applies. Future Phase 3
// widgets that add fields to their JSON schema get forward
// compatibility for free under this contract.
func TestAdversarial_UnknownExtraFieldsAccepted(t *testing.T) {
	inst, err := validate([]byte(`{"text":"hello","foo":"bar","__meta":{"v":1}}`))
	if err != nil {
		t.Fatalf("extra fields should be ignored: %v", err)
	}
	if cfg := inst.Config.(Config); cfg.Text != "hello" {
		t.Errorf("Text = %q, want %q", cfg.Text, "hello")
	}
}

// TestAdversarial_OneMegBlobRejectedByCap pins that a multi-megabyte
// payload doesn't slip past the cap. JSON Unmarshal allocates the full
// string before validation, so the per-page cost is bounded by whatever
// upstream caller already received the bytes; the validator's job is
// to reject before the bad value reaches the renderer.
func TestAdversarial_OneMegBlobRejectedByCap(t *testing.T) {
	bigText := strings.Repeat("x", 1024*1024)
	raw := []byte(`{"text":"` + bigText + `"}`)
	if _, err := validate(raw); err == nil {
		t.Error("1MB text should be rejected by length cap")
	}
}

// TestAdversarial_HTMLEntitiesAreEscaped pins that templ's automatic
// HTML escape applies to every angle-bracket / quote / ampersand
// combination a malicious admin could store in the text field. The
// renderer is the second line of defence; the first is the validator,
// which only enforces length / non-empty -- it does NOT strip HTML.
func TestAdversarial_HTMLEntitiesAreEscaped(t *testing.T) {
	cases := []struct {
		input      string
		mustEscape []string // substrings that MUST appear in escaped form
		mustNotRaw []string // raw substrings that MUST NOT appear
	}{
		{
			input:      `<script>alert(1)</script>`,
			mustEscape: []string{"&lt;script&gt;", "&lt;/script&gt;"},
			mustNotRaw: []string{"<script>", "</script>"},
		},
		{
			input:      `<img src=x onerror=alert(1)>`,
			mustEscape: []string{"&lt;img"},
			mustNotRaw: []string{"<img "},
		},
		{
			input:      `"><svg/onload=alert(1)>`,
			mustEscape: []string{"&#34;&gt;"},
			mustNotRaw: []string{`"><svg`},
		},
		{
			input:      `&amp;`,
			mustEscape: []string{"&amp;amp;"}, // pre-escaped entity gets re-escaped
			mustNotRaw: nil,
		},
		{
			input:      `</div>`,
			mustEscape: []string{"&lt;/div&gt;"},
			mustNotRaw: []string{"</div></div>"}, // outer </div> would close the widget div early
		},
	}
	w := Registration().New()
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			c := w.Render(context.Background(),
				widget.Instance{Type: Type, Config: Config{Text: tc.input}},
				themes.Theme{},
			)
			var buf bytes.Buffer
			if err := c.Render(context.Background(), &buf); err != nil {
				t.Fatalf("render: %v", err)
			}
			out := buf.String()
			for _, want := range tc.mustEscape {
				if !strings.Contains(out, want) {
					t.Errorf("output missing escaped %q; got %q", want, out)
				}
			}
			for _, bad := range tc.mustNotRaw {
				if strings.Contains(out, bad) {
					t.Errorf("output contains raw %q; got %q", bad, out)
				}
			}
		})
	}
}

// TestAdversarial_NewReturnsSingleton pins the architecture's commitment
// that the text widget reuses a single stateless implementation. If a
// future change accidentally allocates per-call, this test catches it.
func TestAdversarial_NewReturnsSingleton(t *testing.T) {
	reg := Registration()
	a := reg.New().(*widgetImpl)
	b := reg.New().(*widgetImpl)
	if a != b {
		t.Errorf("Registration().New() returned distinct pointers: %p vs %p", a, b)
	}
}

// TestAdversarial_ConcurrentRenderRaceFree exercises the singleton under
// concurrent Render calls. With -race enabled, this is the canary that
// pins "the implementation is stateless".
func TestAdversarial_ConcurrentRenderRaceFree(t *testing.T) {
	w := Registration().New()
	const G, N = 32, 50
	var wg sync.WaitGroup
	wg.Add(G)
	for i := 0; i < G; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < N; j++ {
				c := w.Render(context.Background(),
					widget.Instance{Type: Type, Config: Config{Text: "hello"}},
					themes.Theme{},
				)
				var buf bytes.Buffer
				if err := c.Render(context.Background(), &buf); err != nil {
					t.Errorf("render: %v", err)
					return
				}
				if !strings.Contains(buf.String(), "hello") {
					t.Errorf("missing body in output")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestAdversarial_RenderWithMistypedConfigDoesNotPanic exercises the
// defensive `cfg, _ := instance.Config.(Config)` branch. The architecture
// notes the validator guarantees the type, but the comma-ok keeps Render
// from panicking if a caller bypasses the validator. The output is an
// empty themed div -- visible-but-harmless.
func TestAdversarial_RenderWithMistypedConfigDoesNotPanic(t *testing.T) {
	w := Registration().New()
	c := w.Render(context.Background(),
		widget.Instance{Type: Type, Config: "not a Config"},
		themes.Theme{},
	)
	if c == nil {
		t.Fatal("Render returned nil component")
	}
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="widget widget-text"`) {
		t.Errorf("output missing widget-text class: %q", out)
	}
	if !strings.Contains(out, `<div class="widget widget-text"></div>`) {
		t.Errorf("output should be empty themed div, got %q", out)
	}
}

// TestAdversarial_RenderProducesExactCSSClasses pins the exact class
// attribute. Screen Display will style with `widget` and `widget-text`
// hooks; a typo here would leave the placeholder unstyled across every
// theme without any obvious test failure elsewhere.
func TestAdversarial_RenderProducesExactCSSClasses(t *testing.T) {
	w := Registration().New()
	c := w.Render(context.Background(),
		widget.Instance{Type: Type, Config: Config{Text: "x"}},
		themes.Theme{},
	)
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="widget widget-text"`) {
		t.Errorf("missing 'widget widget-text' class: %q", out)
	}
	if !strings.HasPrefix(out, `<div class="widget widget-text">`) {
		t.Errorf("output should start with <div class=\"widget widget-text\">, got %q", out)
	}
	if !strings.HasSuffix(out, `</div>`) {
		t.Errorf("output should end with </div>, got %q", out)
	}
}

// TestAdversarial_DefaultRegistrationFieldsMatch pins that the
// Registration value living inside widget.Default() (after init() ran)
// is byte-for-byte the value Registration() returns. If a future
// refactor splits the global registration from the test-time helper,
// this test catches the drift.
func TestAdversarial_DefaultRegistrationFieldsMatch(t *testing.T) {
	got, ok := widget.Default().Get(Type)
	if !ok {
		t.Fatalf("widget.Default().Get(%q) = false", Type)
	}
	want := Registration()
	if got.Type != want.Type {
		t.Errorf("Type: got %q, want %q", got.Type, want.Type)
	}
	if got.DisplayName != want.DisplayName {
		t.Errorf("DisplayName: got %q, want %q", got.DisplayName, want.DisplayName)
	}
	if got.Description != want.Description {
		t.Errorf("Description: got %q, want %q", got.Description, want.Description)
	}
	if got.New == nil || got.DefaultConfig == nil || got.ValidateConfig == nil {
		t.Errorf("Registration in Default() has nil function field: %+v", got)
	}
}
