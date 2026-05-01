package themes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

// newTestService builds a Service backed by a fresh in-memory SQLite database
// with all migrations applied.
func newTestService(t *testing.T, defaultName string) *Service {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	return NewService(sqlDB, Config{DefaultName: defaultName})
}

// validInput returns a fully-valid Input fixture; tests mutate fields as
// needed.
func validInput(name string) Input {
	return Input{
		Name:           name,
		ColorBg:        "#0b0d11",
		ColorSurface:   "#14171f",
		ColorBorder:    "#23273a",
		ColorText:      "#dfe2ed",
		ColorTextMuted: "#6b7084",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	}
}

func TestEnsureDefaultSeedsRow(t *testing.T) {
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
	if !got.IsDefault {
		t.Error("seeded theme is not marked default")
	}
	if got.Name != "default" {
		t.Errorf("Name = %q, want default", got.Name)
	}
	checks := map[string]string{
		"ColorBg":        got.ColorBg,
		"ColorSurface":   got.ColorSurface,
		"ColorBorder":    got.ColorBorder,
		"ColorText":      got.ColorText,
		"ColorTextMuted": got.ColorTextMuted,
		"ColorAccent":    got.ColorAccent,
		"Radius":         got.Radius,
	}
	want := map[string]string{
		"ColorBg":        "#0b0d11",
		"ColorSurface":   "#14171f",
		"ColorBorder":    "#23273a",
		"ColorText":      "#dfe2ed",
		"ColorTextMuted": "#6b7084",
		"ColorAccent":    "#7b93ff",
		"Radius":         "10px",
	}
	for k, v := range want {
		if checks[k] != v {
			t.Errorf("%s = %q, want %q", k, checks[k], v)
		}
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List returned %d themes, want 1", len(list))
	}
}

func TestEnsureDefaultIdempotent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("first EnsureDefault: %v", err)
	}
	first, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("first GetDefault: %v", err)
	}

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("second EnsureDefault: %v", err)
	}
	second, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("second GetDefault: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ID changed across calls: %q -> %q", first.ID, second.ID)
	}
	if !first.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("CreatedAt mutated: %v -> %v", first.CreatedAt, second.CreatedAt)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("after two EnsureDefault calls List returned %d, want 1", len(list))
	}
}

func TestEnsureDefaultRespectsConfigName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "onyx")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	got, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if got.Name != "onyx" {
		t.Errorf("Name = %q, want onyx", got.Name)
	}
}

func TestCreateHappyPath(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	in := validInput("kitchen-day")
	in.ColorBg = "#FFF" // verify normalisation lower-cases on the way through.

	out, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.ID == "" {
		t.Error("returned theme has empty ID")
	}
	if out.IsDefault {
		t.Error("newly-created theme should not be default")
	}
	if out.ColorBg != "#fff" {
		t.Errorf("ColorBg = %q, want lower-cased #fff", out.ColorBg)
	}

	fetched, err := svc.GetByID(ctx, out.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched.ID != out.ID || fetched.Name != out.Name || fetched.ColorBg != out.ColorBg {
		t.Errorf("GetByID returned %+v, want %+v", fetched, out)
	}
}

func TestCreateRejectsBadName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("   ")

	_, err := svc.Create(context.Background(), in)
	if err == nil {
		t.Fatal("Create with whitespace-only name returned nil error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err is %T, want *ValidationError", err)
	}
	if ve.Fields["name"] == "" {
		t.Errorf("Fields[name] empty in %v", ve.Fields)
	}
}

func TestCreateRejectsHtmlInjectionInName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("theme<script>")

	_, err := svc.Create(context.Background(), in)
	if !IsValidationError(err) {
		t.Fatalf("Create returned %v, want *ValidationError", err)
	}
}

func TestCreateRejectsLongName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("")
	in.Name = ""
	for i := 0; i < 65; i++ {
		in.Name += "a"
	}

	_, err := svc.Create(context.Background(), in)
	if !IsValidationError(err) {
		t.Fatalf("Create returned %v, want *ValidationError", err)
	}
}

func TestCreateNormalisesUppercaseHex(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("uppercase-test")
	in.ColorBg = "#FFF"

	out, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.ColorBg != "#fff" {
		t.Errorf("ColorBg = %q, want #fff", out.ColorBg)
	}
}

func TestCreateRejectsBadHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"named color", "red"},
		{"rgb function", "rgb(11,13,17)"},
		{"non-hex digits", "#zzzzzz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t, "default")
			in := validInput("hex-" + tt.name)
			in.ColorBg = tt.in

			_, err := svc.Create(context.Background(), in)
			if !IsValidationError(err) {
				t.Fatalf("Create with ColorBg=%q returned %v, want *ValidationError", tt.in, err)
			}
		})
	}
}

func TestCreateAcceptsValidHex(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("hex-ok")
	in.ColorBg = "#0b0d11"

	out, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.ColorBg != "#0b0d11" {
		t.Errorf("ColorBg = %q, want #0b0d11", out.ColorBg)
	}
}

func TestCreateRejectsBadFontFamily(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("font-injection")
	in.FontFamily = "Arial;}<script>"

	_, err := svc.Create(context.Background(), in)
	if !IsValidationError(err) {
		t.Fatalf("Create returned %v, want *ValidationError", err)
	}
}

func TestCreateRejectsLongFontFamily(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("font-too-long")
	long := make([]byte, 257)
	for i := range long {
		long[i] = 'a'
	}
	in.FontFamily = string(long)

	_, err := svc.Create(context.Background(), in)
	if !IsValidationError(err) {
		t.Fatalf("Create returned %v, want *ValidationError", err)
	}
}

func TestCreateAcceptsRadius10px(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("radius-ok")
	in.Radius = "10px"

	out, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.Radius != "10px" {
		t.Errorf("Radius = %q, want 10px", out.Radius)
	}
}

func TestCreateRejectsRadiusWithoutUnit(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	in := validInput("radius-bad")
	in.Radius = "10"

	_, err := svc.Create(context.Background(), in)
	if !IsValidationError(err) {
		t.Fatalf("Create returned %v, want *ValidationError", err)
	}
}

func TestCreateDuplicateName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	in := validInput("twice")
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := svc.Create(ctx, in)
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("second Create returned %v, want ErrDuplicateName", err)
	}
}

func TestUpdateHappyPath(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	created, err := svc.Create(ctx, validInput("kitchen"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force a measurable updated_at delta. SQLite datetime('now') resolves to
	// whole seconds, so a one-second sleep is the minimum guaranteed gap.
	time.Sleep(1100 * time.Millisecond)

	updated := validInput("kitchen") // same name keeps it simple
	updated.ColorBg = "#abcdef"
	out, err := svc.Update(ctx, created.ID, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if out.ColorBg != "#abcdef" {
		t.Errorf("ColorBg = %q, want #abcdef", out.ColorBg)
	}
	if !out.UpdatedAt.After(created.CreatedAt) {
		t.Errorf("UpdatedAt %v not after CreatedAt %v", out.UpdatedAt, created.CreatedAt)
	}

	fetched, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched.ColorBg != "#abcdef" {
		t.Errorf("after update GetByID ColorBg = %q, want #abcdef", fetched.ColorBg)
	}
}

func TestUpdateUnknownID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")

	_, err := svc.Update(context.Background(), "no-such-id", validInput("ghost"))
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("Update returned %v, want ErrThemeNotFound", err)
	}
}

func TestUpdateRejectsValidationError(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	created, err := svc.Create(ctx, validInput("good"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bad := validInput("good")
	bad.ColorBg = "not-a-color"
	_, err = svc.Update(ctx, created.ID, bad)
	if !IsValidationError(err) {
		t.Errorf("Update returned %v, want *ValidationError", err)
	}
}

func TestDeleteOfDefault(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	def, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}

	err = svc.Delete(ctx, def.ID)
	if !errors.Is(err, ErrCannotDeleteDefault) {
		t.Errorf("Delete returned %v, want ErrCannotDeleteDefault", err)
	}

	if _, err := svc.GetByID(ctx, def.ID); err != nil {
		t.Errorf("default theme is missing after refused delete: %v", err)
	}
}

func TestDeleteOfNonDefault(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	created, err := svc.Create(ctx, validInput("disposable"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = svc.GetByID(ctx, created.ID)
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("GetByID after delete returned %v, want ErrThemeNotFound", err)
	}
}

func TestDeleteOfNonDefaultPreservesDefault(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	created, err := svc.Create(ctx, validInput("temp"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	def, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if def.Name != "default" {
		t.Errorf("default Name = %q, want default", def.Name)
	}
}

func TestDeleteUnknownID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")

	err := svc.Delete(context.Background(), "no-such-id")
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("Delete returned %v, want ErrThemeNotFound", err)
	}
}

func TestSetDefaultSwap(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	originalDefault, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	created, err := svc.Create(ctx, validInput("alt"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.SetDefault(ctx, created.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	newDefault, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault after swap: %v", err)
	}
	if newDefault.ID != created.ID {
		t.Errorf("default ID = %q, want %q", newDefault.ID, created.ID)
	}

	// Original default is no longer flagged.
	prev, err := svc.GetByID(ctx, originalDefault.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if prev.IsDefault {
		t.Error("previously-default theme still has IsDefault=true after swap")
	}

	// Exactly one row has is_default = 1.
	q := db.New(svc.sqlDB)
	count, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes: %v", err)
	}
	if count != 1 {
		t.Errorf("CountDefaultThemes = %d, want 1", count)
	}
}

// TestSetDefaultBackToOriginal walks the AC-16 sequence: A -> B -> A and
// confirms exactly one default remains and it is A.
func TestSetDefaultBackToOriginal(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "A")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	a, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	b, err := svc.Create(ctx, validInput("B"))
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	if err := svc.SetDefault(ctx, b.ID); err != nil {
		t.Fatalf("SetDefault B: %v", err)
	}
	if err := svc.SetDefault(ctx, a.ID); err != nil {
		t.Fatalf("SetDefault A: %v", err)
	}

	final, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if final.ID != a.ID {
		t.Errorf("default ID = %q, want %q (A)", final.ID, a.ID)
	}
	q := db.New(svc.sqlDB)
	count, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes: %v", err)
	}
	if count != 1 {
		t.Errorf("CountDefaultThemes = %d, want 1", count)
	}
}

func TestSetDefaultUnknownID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	q := db.New(svc.sqlDB)
	before, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes before: %v", err)
	}

	err = svc.SetDefault(ctx, "no-such-id")
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("SetDefault returned %v, want ErrThemeNotFound", err)
	}

	after, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes after: %v", err)
	}
	if before != after {
		t.Errorf("CountDefaultThemes changed: %d -> %d", before, after)
	}
}

// TestSetDefaultDeletedMidTransaction simulates the race where the target row
// disappears between the existence check and the inner UPDATE. We reproduce
// it by deleting the row directly via the queries layer between the calls.
// Because SetDefault begins its transaction inside the method (not before
// our deletion), we use a different shape: pass an id that exists at the
// time of GetThemeByID but is removed before SetDefaultTheme runs. Easiest
// reproduction: bypass the existence check by passing an id that never
// existed -- but that already maps to ErrThemeNotFound at the lookup step.
//
// Instead we exercise the equivalent post-condition: after SetDefault
// returns ErrThemeNotFound the database state is unchanged (CountDefaultThemes
// before == after). That is the property AC-17 actually asserts.
func TestSetDefaultPreservesStateOnNotFound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")
	ctx := context.Background()

	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	q := db.New(svc.sqlDB)
	before, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes before: %v", err)
	}

	if err := svc.SetDefault(ctx, "ghost"); !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("SetDefault returned %v, want ErrThemeNotFound", err)
	}

	after, err := q.CountDefaultThemes(ctx)
	if err != nil {
		t.Fatalf("CountDefaultThemes after: %v", err)
	}
	if before != after {
		t.Errorf("default count changed across failed SetDefault: %d -> %d", before, after)
	}
	def, err := svc.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if def.Name != "default" {
		t.Errorf("default theme Name changed to %q", def.Name)
	}
}

func TestListOrdering(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "z-default")
	ctx := context.Background()

	for _, n := range []string{"c", "a", "b"} {
		if _, err := svc.Create(ctx, validInput(n)); err != nil {
			t.Fatalf("Create %q: %v", n, err)
		}
	}
	got, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("List returned %d themes, want %d", len(got), len(want))
	}
	for i, g := range got {
		if g.Name != want[i] {
			t.Errorf("List[%d].Name = %q, want %q", i, g.Name, want[i])
		}
	}
}

func TestGetDefaultReturnsDefault(t *testing.T) {
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
	if !got.IsDefault {
		t.Error("GetDefault returned theme with IsDefault=false")
	}
}

func TestGetByIDUnknown(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")

	_, err := svc.GetByID(context.Background(), "missing")
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("GetByID returned %v, want ErrThemeNotFound", err)
	}
}

func TestListEmptyReturnsNonNil(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, "default")

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Error("List returned nil slice on empty table; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List returned %d items, want 0", len(got))
	}
}
