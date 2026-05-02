package themes

import "strings"

// CSSVariables returns a :root { ... } CSS block with the theme's values
// written as custom properties. The output is safe to embed in a
// <style>...</style> element provided the input theme was produced via
// Service.Create, Service.Update, Service.GetByID, Service.List, or
// Service.EnsureDefault -- all of which run the field validators.
//
// Output is deterministic: the same Theme value yields the same string
// byte-for-byte.
func (t Theme) CSSVariables() string {
	var b strings.Builder
	b.WriteString(":root {\n")
	b.WriteString("  --bg: ")
	b.WriteString(t.ColorBg)
	b.WriteString(";\n")
	b.WriteString("  --surface: ")
	b.WriteString(t.ColorSurface)
	b.WriteString(";\n")
	b.WriteString("  --border: ")
	b.WriteString(t.ColorBorder)
	b.WriteString(";\n")
	b.WriteString("  --text: ")
	b.WriteString(t.ColorText)
	b.WriteString(";\n")
	b.WriteString("  --text-muted: ")
	b.WriteString(t.ColorTextMuted)
	b.WriteString(";\n")
	b.WriteString("  --accent: ")
	b.WriteString(t.ColorAccent)
	b.WriteString(";\n")
	b.WriteString("  --radius: ")
	b.WriteString(t.Radius)
	b.WriteString(";\n")
	b.WriteString("  --font-family: ")
	b.WriteString(t.FontFamily)
	b.WriteString(";\n")
	if t.FontFamilyMono != "" {
		b.WriteString("  --font-family-mono: ")
		b.WriteString(t.FontFamilyMono)
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
	return b.String()
}
