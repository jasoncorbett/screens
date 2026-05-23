package screens

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/themes"
)

// TestAdversarial_ValidateRejectsExoticNameForms enumerates name shapes that
// must NOT pass the regex. These bytes (tab, NUL, ESC, unicode) routinely
// slip past naive trim-then-non-empty validators; the whitelist regex is the
// only line of defence and this test pins that contract.
func TestAdversarial_ValidateRejectsExoticNameForms(t *testing.T) {
	t.Parallel()
	const validTheme = "abcdef1234567890abcdef1234567890"
	cases := []struct {
		name string
		val  string
	}{
		{"tab", "kit\tchen"},
		{"newline", "kit\nchen"},
		{"NUL byte", "kit\x00chen"},
		{"ESC byte", "kit\x1bchen"},
		{"unicode accent", "kíchen"},
		{"angle bracket", "kit<chen"},
		{"single quote", "kit'chen"},
		{"semicolon", "kit;chen"},
		{"slash", "kit/chen"},
		{"backtick", "kit`chen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateScreenInput(ScreenInput{
				Name: tc.val, ThemeID: validTheme, RotationIntervalSeconds: 30,
			})
			if !IsValidationError(err) {
				t.Errorf("validateScreenInput(name=%q) = %v, want *ValidationError", tc.val, err)
			}
		})
	}
}

// TestAdversarial_ValidateRejectsExtremeRotation pins the boundary behaviour
// at the integer extremes. Without this, a refactor that swapped the < / >
// comparators or widened the int type could silently accept absurd values.
func TestAdversarial_ValidateRejectsExtremeRotation(t *testing.T) {
	t.Parallel()
	const validTheme = "abcdef1234567890abcdef1234567890"
	for _, n := range []int{math.MinInt32, math.MaxInt32, math.MaxInt16, -1000000} {
		_, err := validateScreenInput(ScreenInput{
			Name: "kitchen", ThemeID: validTheme, RotationIntervalSeconds: n,
		})
		if !IsValidationError(err) {
			t.Errorf("rotation=%d returned %v, want *ValidationError", n, err)
		}
	}
}

// TestAdversarial_CreateConcurrentDuplicateName fires N goroutines all racing
// to create the same name. The UNIQUE-constraint detection plus the
// single-connection sqlite pool means exactly one wins and the rest see
// ErrDuplicateName. A subtle regression in the substring match (e.g. the
// table name baked into the check) would surface as bare wrapped errors.
func TestAdversarial_CreateConcurrentDuplicateName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateScreen(ctx, ScreenInput{
				Name: "shared", ThemeID: themeID, RotationIntervalSeconds: 30,
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	var successes, duplicates int
	for _, e := range errs {
		switch {
		case e == nil:
			successes++
		case errors.Is(e, ErrDuplicateName):
			duplicates++
		default:
			t.Errorf("unexpected error in concurrent CreateScreen: %v", e)
		}
	}
	if successes != 1 {
		t.Errorf("concurrent CreateScreen: %d successes, want 1", successes)
	}
	if duplicates != N-1 {
		t.Errorf("concurrent CreateScreen: %d ErrDuplicateName, want %d", duplicates, N-1)
	}
}

// TestAdversarial_UpdateScreenSameNameIsNoOp covers the self-rename case:
// renaming a screen to its current name must succeed (the row matches itself,
// not a different row, on UNIQUE). Without this, an admin who tweaks rotation
// without changing the name would get spurious ErrDuplicateName.
func TestAdversarial_UpdateScreenSameNameIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	updated, err := svc.UpdateScreen(ctx, c.ID, ScreenInput{
		Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("UpdateScreen self-rename: %v", err)
	}
	if updated.RotationIntervalSeconds != 60 {
		t.Errorf("RotationIntervalSeconds = %d, want 60", updated.RotationIntervalSeconds)
	}
}

// TestAdversarial_UpdateScreenUnknownTheme verifies UpdateScreen translates a
// missing theme_id to the screens-package ErrThemeNotFound, not the raw
// themes.ErrThemeNotFound. Keeping the package boundary clean is required by
// the spec; without this test a refactor that returned the bare themes error
// would silently re-leak it.
func TestAdversarial_UpdateScreenUnknownTheme(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	c, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	_, err = svc.UpdateScreen(ctx, c.ID, ScreenInput{
		Name: "kitchen", ThemeID: "does-not-exist", RotationIntervalSeconds: 30,
	})
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("UpdateScreen with unknown theme returned %v, want screens.ErrThemeNotFound", err)
	}
}

// TestAdversarial_SQLInjectionInID confirms parameter substitution neutralises
// classic injection probes passed as IDs. The query is sqlc-generated and
// parameterised; this test guards against a future refactor that hand-builds
// SQL via concatenation.
func TestAdversarial_SQLInjectionInID(t *testing.T) {
	t.Parallel()
	svc, sqlDB, _ := newTestService(t)
	ctx := context.Background()

	payloads := []string{
		"'); DROP TABLE screens;--",
		"' OR 1=1 --",
		"x' UNION SELECT * FROM themes --",
	}
	for _, p := range payloads {
		_, err := svc.GetScreenByID(ctx, p)
		if !errors.Is(err, ErrScreenNotFound) {
			t.Errorf("GetScreenByID(%q) = %v, want ErrScreenNotFound", p, err)
		}
	}
	// Sanity check: tables are still intact.
	q := db.New(sqlDB)
	if _, err := q.ListScreens(ctx); err != nil {
		t.Errorf("screens table broken after injection probes: %v", err)
	}
}

// TestAdversarial_GetScreenFullDanglingTheme exercises the
// startup-invariant-violation branch in GetScreenFull. The ON DELETE RESTRICT
// FK makes this unreachable through the service surface, but a direct SQLite
// edit (or a future migration bug) could produce a screen referencing a
// missing theme. The service must surface a helpful error rather than panic
// or return a bare sql.ErrNoRows.
func TestAdversarial_GetScreenFullDanglingTheme(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "s-1", "kitchen", themeID, 30)
	// Disable FK to simulate a corrupted DB / direct edit; the spec calls
	// this a "startup-invariant violation" and the service must surface it.
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable FK: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM themes WHERE id = ?", themeID); err != nil {
		t.Fatalf("force delete theme: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("re-enable FK: %v", err)
	}

	_, err := svc.GetScreenFull(ctx, "s-1")
	if err == nil {
		t.Fatal("GetScreenFull on dangling theme returned nil, want error")
	}
	if !strings.Contains(err.Error(), "missing theme") {
		t.Errorf("error %q does not mention 'missing theme' (startup-invariant message lost)", err)
	}
	// errors.Is should still walk to themes.ErrThemeNotFound (it is wrapped).
	if !errors.Is(err, themes.ErrThemeNotFound) {
		t.Errorf("error chain does not unwrap to themes.ErrThemeNotFound: %v", err)
	}
}

// TestAdversarial_GetScreenFullPagesAndWidgetsOrdering inserts pages and
// widgets out of position order to confirm the ORDER BY clauses (and the in-Go
// group-by) preserve the position-order contract that Screen Display relies
// on. Out-of-order inserts force the test to fail if a future change drops
// the ORDER BY or sorts by ID instead.
func TestAdversarial_GetScreenFullPagesAndWidgetsOrdering(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "s-1", "kitchen", themeID, 30)
	// Pages inserted at positions 3, 1, 2 (id ordering deliberately reversed).
	insertPage(t, sqlDB, "p-z", "s-1", "", 3)
	insertPage(t, sqlDB, "p-y", "s-1", "", 1)
	insertPage(t, sqlDB, "p-x", "s-1", "", 2)
	// Widgets on p-x inserted out of order at positions 2, 1.
	insertWidget(t, sqlDB, "w-second", "p-x", "t", "{}", 2)
	insertWidget(t, sqlDB, "w-first", "p-x", "t", "{}", 1)

	full, err := svc.GetScreenFull(ctx, "s-1")
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	gotPages := []string{full.Pages[0].Page.ID, full.Pages[1].Page.ID, full.Pages[2].Page.ID}
	wantPages := []string{"p-y", "p-x", "p-z"}
	for i := range wantPages {
		if gotPages[i] != wantPages[i] {
			t.Errorf("page[%d].ID = %q, want %q (pages out of position order)", i, gotPages[i], wantPages[i])
		}
	}
	// Widgets on p-x are at index 1 of full.Pages
	gotWidgets := []string{full.Pages[1].Widgets[0].ID, full.Pages[1].Widgets[1].ID}
	wantWidgets := []string{"w-first", "w-second"}
	for i := range wantWidgets {
		if gotWidgets[i] != wantWidgets[i] {
			t.Errorf("widget[%d].ID = %q, want %q (widgets out of position order)", i, gotWidgets[i], wantWidgets[i])
		}
	}
}

// TestAdversarial_GetScreenFullPageWithoutWidgetsHasNonNilSlice asserts the
// invariant that every PageWithWidgets.Widgets slice is non-nil even when the
// page has no widget rows. Screen Display iterates this slice; a nil here
// would still work in range loops, but JSON-marshalled responses would render
// as `null` instead of `[]` and templ templates would short-circuit on
// `widgets == nil` versus the intended empty check.
func TestAdversarial_GetScreenFullPageWithoutWidgetsHasNonNilSlice(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "s-1", "kitchen", themeID, 30)
	insertPage(t, sqlDB, "p-empty", "s-1", "", 1)
	insertPage(t, sqlDB, "p-with", "s-1", "", 2)
	insertWidget(t, sqlDB, "w-1", "p-with", "t", "{}", 1)

	full, err := svc.GetScreenFull(ctx, "s-1")
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if full.Pages[0].Widgets == nil {
		t.Error("page-with-no-widgets has nil Widgets slice; want non-nil empty slice")
	}
	if len(full.Pages[0].Widgets) != 0 {
		t.Errorf("page-empty has %d widgets, want 0", len(full.Pages[0].Widgets))
	}
}

// TestAdversarial_ScreenFromRowMalformedTimestamp confirms the row->domain
// converter returns an error rather than panicking on a parse failure. The
// service contract is "errors all the way down, never panic"; this test pins
// that contract for the timestamp branch where a corrupted DB write (or a
// SQLite version that emits a different datetime format) would otherwise
// crash the request path.
func TestAdversarial_ScreenFromRowMalformedTimestamp(t *testing.T) {
	t.Parallel()
	row := db.Screen{
		ID: "x", Name: "n", ThemeID: "t",
		RotationIntervalSeconds: 30,
		CreatedAt:               "definitely-not-a-timestamp",
		UpdatedAt:               "2024-01-15 13:45:00",
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("screenFromRow panicked: %v", r)
		}
	}()
	if _, err := screenFromRow(row); err == nil {
		t.Error("screenFromRow accepted a malformed CreatedAt without error")
	}
}

// TestAdversarial_DeleteAfterClosedDBSurfacesError verifies that operating on
// a closed DB handle returns a non-nil error rather than panicking. The
// service must remain crash-free even when callers misuse its lifecycle.
func TestAdversarial_DeleteAfterClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, _ := newTestService(t)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DeleteScreen panicked after DB close: %v", r)
		}
	}()
	err := svc.DeleteScreen(context.Background(), "any-id")
	if err == nil {
		t.Error("DeleteScreen on closed DB returned nil, want error")
	}
}
