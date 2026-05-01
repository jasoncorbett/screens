package themes

import (
	"strings"
	"testing"
)

// sampleTheme returns a fully-populated valid Theme for CSS rendering tests.
func sampleTheme() Theme {
	return Theme{
		ID:             "abc123",
		Name:           "default",
		IsDefault:      true,
		ColorBg:        "#0b0d11",
		ColorSurface:   "#14171f",
		ColorBorder:    "#23273a",
		ColorText:      "#dfe2ed",
		ColorTextMuted: "#6b7084",
		ColorAccent:    "#7b93ff",
		FontFamily:     `system-ui, sans-serif`,
		FontFamilyMono: `"SF Mono", monospace`,
		Radius:         "10px",
	}
}

func TestCSSVariablesDeterministic(t *testing.T) {
	t.Parallel()

	theme := sampleTheme()
	a := theme.CSSVariables()
	b := theme.CSSVariables()
	if a != b {
		t.Errorf("CSSVariables() not deterministic:\nfirst:  %q\nsecond: %q", a, b)
	}
}

func TestCSSVariablesContainsAllRequiredDeclarations(t *testing.T) {
	t.Parallel()

	theme := sampleTheme()
	out := theme.CSSVariables()

	required := []string{
		":root {",
		"--bg: #0b0d11;",
		"--surface: #14171f;",
		"--border: #23273a;",
		"--text: #dfe2ed;",
		"--text-muted: #6b7084;",
		"--accent: #7b93ff;",
		"--radius: 10px;",
		"--font-family: system-ui, sans-serif;",
		`--font-family-mono: "SF Mono", monospace;`,
	}
	for _, sub := range required {
		if !strings.Contains(out, sub) {
			t.Errorf("CSSVariables() missing %q\nfull output:\n%s", sub, out)
		}
	}
}

func TestCSSVariablesAccentSpecific(t *testing.T) {
	t.Parallel()

	theme := sampleTheme()
	theme.ColorAccent = "#7b93ff"
	out := theme.CSSVariables()
	if !strings.Contains(out, "--accent: #7b93ff;") {
		t.Errorf("CSSVariables() missing --accent: #7b93ff;\noutput:\n%s", out)
	}
}

func TestCSSVariablesMonoOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	theme := sampleTheme()
	theme.FontFamilyMono = ""
	out := theme.CSSVariables()
	if strings.Contains(out, "--font-family-mono") {
		t.Errorf("CSSVariables() included --font-family-mono with empty value:\n%s", out)
	}
}

func TestCSSVariablesMonoIncludedWhenSet(t *testing.T) {
	t.Parallel()

	theme := sampleTheme()
	theme.FontFamilyMono = "monospace"
	out := theme.CSSVariables()
	if !strings.Contains(out, "--font-family-mono: monospace;") {
		t.Errorf("CSSVariables() missing --font-family-mono: monospace; for set value:\n%s", out)
	}
}

// TestCSSVariablesNoBreakoutCharacters guards the contract that, for a
// theme that has cleared validation, CSSVariables cannot emit characters
// that would close the surrounding <style> tag. The validators are what
// guarantee this; this test ensures a future format refactor does not drop
// the property without a deliberate decision.
func TestCSSVariablesNoBreakoutCharacters(t *testing.T) {
	t.Parallel()

	out := sampleTheme().CSSVariables()
	for _, banned := range []string{"<", ">", "</style>"} {
		if strings.Contains(out, banned) {
			t.Errorf("CSSVariables() contains banned substring %q\noutput:\n%s", banned, out)
		}
	}
}
