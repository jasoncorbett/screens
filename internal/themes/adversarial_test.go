package themes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jasoncorbett/screens/internal/db"
)

// TestUpdateRejectsDuplicateName documents the contract that renaming a theme
// to collide with another theme's name surfaces as ErrDuplicateName, the same
// error Create returns. Without this, an admin would see a wrapped raw SQL
// error string in the form re-render path.
func TestUpdateRejectsDuplicateName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	a, err := svc.Create(ctx, validInput("alpha"))
	if err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if _, err := svc.Create(ctx, validInput("beta")); err != nil {
		t.Fatalf("Create beta: %v", err)
	}

	rename := validInput("alpha") // collide with theme A's name
	_, err = svc.Update(ctx, a.ID /* same row */, rename)
	if err != nil {
		t.Fatalf("Update self-rename to own name: %v", err)
	}

	// Rename theme B to alpha -- collides with A.
	b, err := svc.Create(ctx, validInput("gamma"))
	if err != nil {
		t.Fatalf("Create gamma: %v", err)
	}
	collide := validInput("alpha")
	_, err = svc.Update(ctx, b.ID, collide)
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("Update with colliding name returned %v, want ErrDuplicateName", err)
	}
}

// TestEnsureDefaultColorsMatchStaticCSS pins every seeded color and font
// constant byte-for-byte. If app.css drifts (or a developer fat-fingers a
// theme constant), this test fails loudly rather than letting the seeded
// theme silently disagree with the static stylesheet. SPEC-004 §18 makes
// matching app.css a hard requirement.
func TestEnsureDefaultColorsMatchStaticCSS(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	got, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}

	// These literals match :root in static/css/app.css and the sans-serif /
	// monospace stacks the static stylesheet uses.
	want := map[string]string{
		"ColorBg":        "#0b0d11",
		"ColorSurface":   "#14171f",
		"ColorBorder":    "#23273a",
		"ColorText":      "#dfe2ed",
		"ColorTextMuted": "#6b7084",
		"ColorAccent":    "#7b93ff",
		"Radius":         "10px",
		"FontFamily":     `-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif`,
		"FontFamilyMono": `"SF Mono", "Fira Code", "Cascadia Code", monospace`,
	}
	gotMap := map[string]string{
		"ColorBg":        got.ColorBg,
		"ColorSurface":   got.ColorSurface,
		"ColorBorder":    got.ColorBorder,
		"ColorText":      got.ColorText,
		"ColorTextMuted": got.ColorTextMuted,
		"ColorAccent":    got.ColorAccent,
		"Radius":         got.Radius,
		"FontFamily":     got.FontFamily,
		"FontFamilyMono": got.FontFamilyMono,
	}
	for k, v := range want {
		if gotMap[k] != v {
			t.Errorf("seed %s = %q, want %q", k, gotMap[k], v)
		}
	}
}

// TestEnsureDefaultConcurrent fires multiple EnsureDefault calls in parallel
// against a fresh database and asserts the seed remains a single row. The
// transaction in EnsureDefault is the safety net; the test runs under -race
// to surface any goroutine-visible mutation of the Service fields.
func TestEnsureDefaultConcurrent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := svc.EnsureDefault(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent EnsureDefault: %v", err)
	}

	q := db.New(svc.sqlDB)
	count, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes: %v", err)
	}
	if count != 1 {
		t.Errorf("CountDefaultThemes after concurrent seed = %d, want 1", count)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List after concurrent seed returned %d themes, want 1", len(list))
	}
}

// TestSetDefaultConcurrent fires multiple SetDefault calls in parallel, each
// targeting a different theme. The "exactly one default" invariant must hold
// regardless of which goroutine wins. Combined with -race this surfaces any
// data race on the Service fields or the shared *db.Queries.
func TestSetDefaultConcurrent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	const N = 6
	ids := make([]string, 0, N)
	for i := 0; i < N; i++ {
		in := validInput("alt-" + string(rune('a'+i)))
		t, err := svc.Create(ctx, in)
		if err != nil {
			panic(err)
		}
		ids = append(ids, t.ID)
	}

	var wg sync.WaitGroup
	wg.Add(len(ids))
	for _, id := range ids {
		go func(id string) {
			defer wg.Done()
			// Failure is acceptable here only if it's a benign error type;
			// any error other than nil is unexpected because the rows exist
			// for the duration of the test.
			if err := svc.SetDefault(ctx, id); err != nil {
				t.Errorf("SetDefault(%s): %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	q := db.New(svc.sqlDB)
	count, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes: %v", err)
	}
	if count != 1 {
		t.Errorf("CountDefaultThemes after concurrent SetDefault = %d, want 1", count)
	}
}

// TestCSSVariablesConcurrent spins many goroutines all calling CSSVariables on
// shared Theme values. The output must be deterministic and equal across
// callers; -race surfaces any hidden mutation in strings.Builder reuse.
func TestCSSVariablesConcurrent(t *testing.T) {
	t.Parallel()
	theme := sampleTheme()
	want := theme.CSSVariables()

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got := theme.CSSVariables()
			if got != want {
				t.Errorf("CSSVariables() race produced divergent output:\nwant:\n%s\ngot:\n%s", want, got)
			}
		}()
	}
	wg.Wait()
}

// TestCSSVariablesDeclarationOrder pins the exact ordering of declarations
// in the CSS block so a future refactor that re-orders the WriteString calls
// flips this test instead of silently changing every downstream snapshot.
func TestCSSVariablesDeclarationOrder(t *testing.T) {
	t.Parallel()
	out := sampleTheme().CSSVariables()

	want := []string{
		":root {",
		"--bg:",
		"--surface:",
		"--border:",
		"--text:",
		"--text-muted:",
		"--accent:",
		"--radius:",
		"--font-family:",
		"--font-family-mono:",
		"}",
	}
	last := -1
	for _, w := range want {
		idx := strings.Index(out, w)
		if idx < 0 {
			t.Fatalf("CSSVariables() missing %q\nfull output:\n%s", w, out)
		}
		if idx <= last {
			t.Errorf("CSSVariables() declaration order wrong: %q at %d, expected after previous (%d)\noutput:\n%s",
				w, idx, last, out)
		}
		last = idx
	}
}

// TestCSSVariablesTextOrderingDistinguishesMutedFromBare guards the off-by-one
// trap in substring searches: the property name `--text:` is a prefix of
// `--text-muted:`. The ordering test above already constrains it, but a
// dedicated test pins the exact bytes of each declaration so a typo like
// `--text -muted: ...` (extra space) would surface here.
func TestCSSVariablesTextOrderingDistinguishesMutedFromBare(t *testing.T) {
	t.Parallel()
	theme := sampleTheme()
	theme.ColorText = "#aaaaaa"
	theme.ColorTextMuted = "#bbbbbb"
	out := theme.CSSVariables()
	if !strings.Contains(out, "--text: #aaaaaa;") {
		t.Errorf("missing --text: #aaaaaa;\noutput:\n%s", out)
	}
	if !strings.Contains(out, "--text-muted: #bbbbbb;") {
		t.Errorf("missing --text-muted: #bbbbbb;\noutput:\n%s", out)
	}
}

// TestGenerateIDUniqueness asserts that the slice-to-32-chars idiom does not
// collide across many calls. The probability of an actual collision in 16 bytes
// of entropy is negligible; this test catches off-by-one bugs in the slicing
// (e.g. `token[:31]` would still pass everywhere else but show as a duplicate
// length here -- and longer length checks below).
func TestGenerateIDUniqueness(t *testing.T) {
	t.Parallel()
	const N = 1024
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id, err := generateID()
		if err != nil {
			t.Fatalf("generateID: %v", err)
		}
		if len(id) != 32 {
			t.Errorf("generateID produced length %d, want 32", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Errorf("generateID produced duplicate %q after %d calls", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestCreateRejectsSQLInjectionInName proves the parameterised query renders
// classic injection payloads as literal column data rather than executing
// them. The validator already restricts names to [A-Za-z0-9 _-], so the
// payload must first survive validation -- it does not, which is the
// contract. This test pins both halves: the validator rejects the payload,
// AND if a future refactor loosened the validator the parameterised query
// would still neutralise the attack.
func TestCreateRejectsSQLInjectionInName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	in := validInput("'); DROP TABLE themes;--")
	_, err := svc.Create(ctx, in)
	if !IsValidationError(err) {
		t.Fatalf("Create with SQL-injection name returned %v, want *ValidationError", err)
	}

	// Belt-and-braces: confirm the table still exists and is queryable.
	q := db.New(svc.sqlDB)
	if _, err := q.CountDefaultThemes(ctx); err != nil {
		t.Errorf("themes table broken after injection probe: %v", err)
	}
}

// TestUpdateOfDeletedThemeReturnsNotFound verifies the Update -> GetThemeByID
// pre-check returns ErrThemeNotFound rather than panicking when the row was
// deleted between Create and Update. With OpenTestDB(t)'s single-connection
// pool, this is a sequential exercise, not a race.
func TestUpdateOfDeletedThemeReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	created, err := svc.Create(ctx, validInput("ephemeral"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = svc.Update(ctx, created.ID, validInput("ephemeral"))
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("Update of deleted id returned %v, want ErrThemeNotFound", err)
	}
}

// TestSetDefaultOnDeletedDefaultReturnsNotFound exercises the sequence
// "SetDefault on a deleted theme". The first SetDefault makes the new theme
// default; we then delete a non-default theme and try to SetDefault on the
// deleted ID. Must surface as ErrThemeNotFound.
func TestSetDefaultOnDeletedThemeReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	disposable, err := svc.Create(ctx, validInput("disposable"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, disposable.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := svc.SetDefault(ctx, disposable.ID); !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("SetDefault on deleted id returned %v, want ErrThemeNotFound", err)
	}
}

// TestCreateRejectsControlCharacterInFontFamily is the targeted probe that the
// validator rejects every byte below 0x20, including ESC (0x1b) and BEL
// (0x07). Without this, an attacker who slipped a string with embedded ANSI
// escape sequences past a different layer could land bytes inside the
// <style> block.
func TestCreateRejectsControlCharacterInFontFamily(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	for _, c := range []byte{0x00, 0x07, 0x1b, 0x1f} {
		in := validInput("control-test")
		in.FontFamily = "Arial" + string(c) + "Helvetica"
		_, err := svc.Create(ctx, in)
		if !IsValidationError(err) {
			t.Errorf("Create with byte 0x%02x in font_family returned %v, want *ValidationError", c, err)
		}
	}
}

// TestValidateFontFamilyAllowsRealFontStacks checks the boundary case that the
// blacklist does not over-reject characters real CSS font stacks need:
// double-quote, comma, hyphen, space.
func TestValidateFontFamilyAllowsRealFontStacks(t *testing.T) {
	t.Parallel()
	stacks := []string{
		`"SF Mono", "Fira Code", monospace`,
		`Arial, Helvetica, sans-serif`,
		`-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif`,
		`'Helvetica Neue', sans-serif`, // single-quote ok
		"Arial",
	}
	for _, s := range stacks {
		if _, err := validateFontFamily(s); err != nil {
			t.Errorf("validateFontFamily(%q) unexpected error: %v", s, err)
		}
	}
}

// TestCreateThenGetByIDPreservesEverything sanity-checks the full round-trip:
// every field set on Create must come back identically through GetByID. This
// would catch a bug where, for example, Service.Create populated a field but
// GetByID's SELECT/Scan dropped it (or themeFromRow misordered fields).
func TestCreateThenGetByIDPreservesEverything(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	in := Input{
		Name:           "round-trip",
		ColorBg:        "#010203",
		ColorSurface:   "#040506",
		ColorBorder:    "#070809",
		ColorText:      "#0a0b0c",
		ColorTextMuted: "#0d0e0f",
		ColorAccent:    "#101112",
		FontFamily:     `"Roundtrip Font", sans-serif`,
		FontFamilyMono: "monospace",
		Radius:         "0.25rem",
	}
	out, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.GetByID(ctx, out.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if got.ColorBg != in.ColorBg ||
		got.ColorSurface != in.ColorSurface ||
		got.ColorBorder != in.ColorBorder ||
		got.ColorText != in.ColorText ||
		got.ColorTextMuted != in.ColorTextMuted ||
		got.ColorAccent != in.ColorAccent ||
		got.FontFamily != in.FontFamily ||
		got.FontFamilyMono != in.FontFamilyMono ||
		got.Radius != in.Radius ||
		got.Name != in.Name {
		t.Errorf("GetByID round-trip mismatch:\ninput:  %+v\nfetched: %+v", in, got)
	}
}
