package screens

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/a-h/templ"
	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
	"github.com/jasoncorbett/screens/internal/widget/text"
)

// newTestServiceWithRegistry builds a screens.Service backed by a fresh DB and
// the caller-supplied registry. Mirrors newTestServiceWithText but lets the
// caller plug in a custom registry (e.g. one populated with a misbehaving
// widget that intentionally violates its own ValidateConfig).
func newTestServiceWithRegistry(t *testing.T, registry *widget.Registry) (*Service, string) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	themesSvc := themes.NewService(sqlDB, themes.Config{DefaultName: "default"})
	if err := themesSvc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	def, err := themesSvc.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	svc := NewService(sqlDB, themesSvc, registry)
	return svc, def.ID
}

// TestAdversarial_AddWidget_DefaultFailsOwnValidator pins the contract that
// AddWidget surfaces a defensive error -- NOT a panic, NOT a silent insert --
// when a registered widget's DefaultConfig() returns bytes that its own
// ValidateConfig rejects. The task explicitly demands this round-trip check
// at write time ("default-must-validate"). Without this test a widget author
// could ship a broken default and Screen Display would crash at render.
func TestAdversarial_AddWidget_DefaultFailsOwnValidator(t *testing.T) {
	t.Parallel()
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:          "broken",
		DisplayName:   "Broken",
		Description:   "default fails its own validator",
		New:           func() widget.Widget { return brokenWidget{} },
		DefaultConfig: func() []byte { return []byte(`{"oops":true}`) },
		ValidateConfig: func(raw []byte) (widget.Instance, error) {
			return widget.Instance{}, errors.New("validator: never accepts")
		},
	}); err != nil {
		t.Fatalf("register broken widget: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	_, err := svc.AddWidget(context.Background(), screenID, pageID, "broken")
	if err == nil {
		t.Fatal("AddWidget(broken) returned nil, want validation error")
	}
	if !strings.Contains(err.Error(), "default config failed validation") {
		t.Errorf("AddWidget err = %v, want 'default config failed validation'", err)
	}
	// And no row was inserted.
	rows, lerr := svc.queries.ListWidgetInstancesByPage(context.Background(), pageID)
	if lerr != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", lerr)
	}
	if len(rows) != 0 {
		t.Errorf("widget_instances has %d rows after rejected AddWidget, want 0", len(rows))
	}
}

// brokenWidget is a no-op Widget used by the broken-default test above.
// It satisfies widget.Widget by returning templ.NopComponent.
type brokenWidget struct{}

func (brokenWidget) Render(_ context.Context, _ widget.Instance, _ themes.Theme) templ.Component {
	return templ.NopComponent
}

// TestAdversarial_AddWidget_ConfigBytesStoredVerbatim pins that AddWidget
// stores the exact bytes returned by DefaultConfig() with no JSON re-marshal,
// whitespace munging, or canonical-form rewrite. A regression that round-tripped
// the bytes through json.Unmarshal+json.Marshal would silently re-order keys
// and strip whitespace, breaking byte-comparison consumers (e.g. checksum-based
// caching downstream of GetScreenFull).
func TestAdversarial_AddWidget_ConfigBytesStoredVerbatim(t *testing.T) {
	t.Parallel()
	// Use a config blob with deliberately unusual whitespace and key order.
	verbatim := []byte("  {\n  \"text\"  :   \"verbatim test\"  }  ")
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:           "verbatim",
		DisplayName:    "Verbatim",
		Description:    "preserves bytes",
		New:            func() widget.Widget { return brokenWidget{} },
		DefaultConfig:  func() []byte { return verbatim },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "verbatim"}, nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, "verbatim")
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}
	if string(added.Config) != string(verbatim) {
		t.Errorf("returned Config = %q, want %q (bytes mutated)", added.Config, verbatim)
	}
	row, err := svc.queries.GetWidgetInstanceByID(ctx, db.GetWidgetInstanceByIDParams{ID: added.ID, PageID: pageID})
	if err != nil {
		t.Fatalf("GetWidgetInstanceByID: %v", err)
	}
	if row.Config != string(verbatim) {
		t.Errorf("persisted Config = %q, want %q (bytes mutated in storage)", row.Config, verbatim)
	}
}

// TestAdversarial_AddWidget_EmptyRegistry pins that AddWidget returns
// ErrUnknownWidgetType for any type when the injected registry is empty
// (no widgets registered). A regression that returned a different error
// (e.g. nil-pointer panic on s.widgets.Get) would crash the admin handler.
func TestAdversarial_AddWidget_EmptyRegistry(t *testing.T) {
	t.Parallel()
	svc, themeID := newTestServiceWithRegistry(t, widget.NewRegistry())
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	for _, typ := range []string{"text", "clock", "weather", "", "anything"} {
		_, err := svc.AddWidget(context.Background(), screenID, pageID, typ)
		if !errors.Is(err, ErrUnknownWidgetType) {
			t.Errorf("AddWidget(type=%q) on empty registry = %v, want ErrUnknownWidgetType", typ, err)
		}
	}
}

// TestAdversarial_AddWidget_TypeFuzzing pins that arbitrary unregistered type
// strings (empty, very long, SQL meta-chars, NUL, unicode) ALL resolve to
// ErrUnknownWidgetType -- no panics, no SQL injection through the type field.
// The registry's Get is a case-sensitive map lookup, so none of these match.
func TestAdversarial_AddWidget_TypeFuzzing(t *testing.T) {
	t.Parallel()
	svc, themeID := newTestServiceWithRegistry(t, registryWithText(t))
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	cases := []struct {
		name string
		typ  string
	}{
		{"empty", ""},
		{"NUL", "text\x00"},
		{"NUL only", "\x00"},
		{"unicode", "tex†"},
		{"newline", "text\n"},
		{"uppercase variant", "TEXT"}, // case-sensitive lookup
		{"very long", strings.Repeat("x", 100000)},
		{"sql injection", "text'; DROP TABLE widget_instances;--"},
		{"sql injection 2", "x' OR 1=1 --"},
		{"trailing space", "text "},
		{"leading space", " text"},
		{"angle brackets", "<text>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AddWidget(ctx, screenID, pageID, tc.typ)
			if !errors.Is(err, ErrUnknownWidgetType) {
				t.Errorf("AddWidget(type=%q) = %v, want ErrUnknownWidgetType", tc.typ, err)
			}
		})
	}

	// No rows were inserted by any of the rejections.
	rows, err := svc.queries.ListWidgetInstancesByPage(ctx, pageID)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("widget_instances has %d rows after %d rejections, want 0", len(rows), len(cases))
	}
}

// registryWithText is a tiny helper that returns a fresh registry with the
// text widget registered. Used by tests that need a baseline real widget
// without the full newTestServiceWithText scaffolding.
func registryWithText(t *testing.T) *widget.Registry {
	t.Helper()
	r := widget.NewRegistry()
	if err := r.Register(text.Registration()); err != nil {
		t.Fatalf("register text: %v", err)
	}
	return r
}

// TestAdversarial_AddWidget_PageBelongsToDifferentScreen pins the
// cross-screen defence: AddWidget refuses to attach a widget to a page that
// exists but belongs to a different screen. The defence comes from
// GetPageByID's (screenID, pageID) WHERE clause; a regression that dropped
// the screen_id check would let one admin's stale URL plant widgets on
// another screen's pages.
func TestAdversarial_AddWidget_PageBelongsToDifferentScreen(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()

	a, err := svc.CreateScreen(ctx, ScreenInput{Name: "a", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen A: %v", err)
	}
	b, err := svc.CreateScreen(ctx, ScreenInput{Name: "b", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen B: %v", err)
	}
	pageOnA, err := svc.CreatePage(ctx, a.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage on A: %v", err)
	}

	// AddWidget(B, pageOnA, text) must reject -- the page is on A.
	_, err = svc.AddWidget(ctx, b.ID, pageOnA.ID, text.Type)
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("AddWidget(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
	// And no row was inserted on the page.
	rows, err := svc.queries.ListWidgetInstancesByPage(ctx, pageOnA.ID)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("widget_instances on A's page has %d rows after wrong-screen AddWidget, want 0", len(rows))
	}
}

// TestAdversarial_DeleteWidget_OnWrongPage pins the cross-page defence:
// DeleteWidget refuses to delete a widget that exists but belongs to a
// different page. Without the page_id filter on the DELETE WHERE clause,
// one admin URL could nuke a widget on a different page.
func TestAdversarial_DeleteWidget_OnWrongPage(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "k", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	pageA, err := svc.CreatePage(ctx, screen.ID, "a")
	if err != nil {
		t.Fatalf("CreatePage A: %v", err)
	}
	pageB, err := svc.CreatePage(ctx, screen.ID, "b")
	if err != nil {
		t.Fatalf("CreatePage B: %v", err)
	}

	widgetOnA, err := svc.AddWidget(ctx, screen.ID, pageA.ID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget on A: %v", err)
	}

	// DeleteWidget(screen, pageB, widgetOnA) must reject -- widget belongs to A.
	if err := svc.DeleteWidget(ctx, screen.ID, pageB.ID, widgetOnA.ID); !errors.Is(err, ErrWidgetNotFound) {
		t.Errorf("DeleteWidget(pageB, widget-on-A) = %v, want ErrWidgetNotFound", err)
	}
	// Widget on A must still exist.
	if _, err := svc.queries.GetWidgetInstanceByID(ctx,
		db.GetWidgetInstanceByIDParams{ID: widgetOnA.ID, PageID: pageA.ID}); err != nil {
		t.Errorf("widget on A vanished after wrong-page delete attempt: %v", err)
	}
}

// TestAdversarial_DeleteWidget_PageBelongsToDifferentScreen pins that
// DeleteWidget rejects when the page exists but belongs to a different
// screen (the upstream GetPageByID check fires).
func TestAdversarial_DeleteWidget_PageBelongsToDifferentScreen(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()

	a, err := svc.CreateScreen(ctx, ScreenInput{Name: "a", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen A: %v", err)
	}
	b, err := svc.CreateScreen(ctx, ScreenInput{Name: "b", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen B: %v", err)
	}
	pageOnA, err := svc.CreatePage(ctx, a.ID, "p")
	if err != nil {
		t.Fatalf("CreatePage on A: %v", err)
	}
	widget, err := svc.AddWidget(ctx, a.ID, pageOnA.ID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}

	// DeleteWidget(B, pageOnA, widget) -- page belongs to A, not B.
	if err := svc.DeleteWidget(ctx, b.ID, pageOnA.ID, widget.ID); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("DeleteWidget(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
}

// TestAdversarial_MoveWidget_MismatchedIDs runs the full matrix of
// (wrong screen / wrong page / wrong widget) combinations on MoveWidgetUp
// and MoveWidgetDown. Each combination should fire the appropriate error
// without partially mutating state.
func TestAdversarial_MoveWidget_MismatchedIDs(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()

	screenA, err := svc.CreateScreen(ctx, ScreenInput{Name: "a", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen A: %v", err)
	}
	screenB, err := svc.CreateScreen(ctx, ScreenInput{Name: "b", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen B: %v", err)
	}
	pageA, err := svc.CreatePage(ctx, screenA.ID, "p-a")
	if err != nil {
		t.Fatalf("CreatePage A: %v", err)
	}
	pageB, err := svc.CreatePage(ctx, screenB.ID, "p-b")
	if err != nil {
		t.Fatalf("CreatePage B: %v", err)
	}
	wA1, err := svc.AddWidget(ctx, screenA.ID, pageA.ID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget A1: %v", err)
	}
	wA2, err := svc.AddWidget(ctx, screenA.ID, pageA.ID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget A2: %v", err)
	}

	cases := []struct {
		name     string
		screenID string
		pageID   string
		widgetID string
		want     error
	}{
		// wrong-screen, right-page-id, right-widget-id: GetPageByID
		// rejects because page belongs to A, not B.
		{"wrong screen", screenB.ID, pageA.ID, wA1.ID, ErrPageNotFound},
		// right-screen, wrong-page-id, right-widget-id: the page is on the
		// right screen but doesn't match this widget's page; widget lookup
		// inside the tx returns ErrWidgetNotFound.
		{"right-screen, page-on-other-screen", screenA.ID, pageB.ID, wA1.ID, ErrPageNotFound},
		// right-screen, right-page, wrong-widget-id: widget lookup misses.
		{"right-page, unknown widget", screenA.ID, pageA.ID, "no-such-widget", ErrWidgetNotFound},
	}

	for _, tc := range cases {
		t.Run("Up "+tc.name, func(t *testing.T) {
			if err := svc.MoveWidgetUp(ctx, tc.screenID, tc.pageID, tc.widgetID); !errors.Is(err, tc.want) {
				t.Errorf("MoveWidgetUp = %v, want %v", err, tc.want)
			}
		})
		t.Run("Down "+tc.name, func(t *testing.T) {
			if err := svc.MoveWidgetDown(ctx, tc.screenID, tc.pageID, tc.widgetID); !errors.Is(err, tc.want) {
				t.Errorf("MoveWidgetDown = %v, want %v", err, tc.want)
			}
		})
	}

	// After all the rejections, the original widgets are still at positions 1, 2.
	pos := widgetPositions(t, svc, pageA.ID)
	if pos[wA1.ID] != 1 || pos[wA2.ID] != 2 {
		t.Errorf("positions = {wA1:%d, wA2:%d}, want unchanged {1, 2}", pos[wA1.ID], pos[wA2.ID])
	}
}

// TestAdversarial_MoveWidget_CrossPageOnSameScreen pins that move operations
// on one page do not touch widgets on a different page that happens to share
// positions. Two pages, each with 3 widgets at positions 1..3; reordering
// page-A's widgets must not affect page-B's.
func TestAdversarial_MoveWidget_CrossPageOnSameScreen(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "k", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	pageA, err := svc.CreatePage(ctx, screen.ID, "a")
	if err != nil {
		t.Fatalf("CreatePage A: %v", err)
	}
	pageB, err := svc.CreatePage(ctx, screen.ID, "b")
	if err != nil {
		t.Fatalf("CreatePage B: %v", err)
	}

	var aIDs, bIDs []string
	for i := 0; i < 3; i++ {
		w, err := svc.AddWidget(ctx, screen.ID, pageA.ID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget A %d: %v", i, err)
		}
		aIDs = append(aIDs, w.ID)
		w, err = svc.AddWidget(ctx, screen.ID, pageB.ID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget B %d: %v", i, err)
		}
		bIDs = append(bIDs, w.ID)
	}

	// Snapshot page B before doing anything on page A.
	beforeB := widgetPositions(t, svc, pageB.ID)

	// Move page A's middle widget up and down a few times.
	if err := svc.MoveWidgetUp(ctx, screen.ID, pageA.ID, aIDs[1]); err != nil {
		t.Fatalf("MoveWidgetUp A[1]: %v", err)
	}
	if err := svc.MoveWidgetDown(ctx, screen.ID, pageA.ID, aIDs[2]); err != nil {
		t.Fatalf("MoveWidgetDown A[2]: %v", err)
	}

	afterB := widgetPositions(t, svc, pageB.ID)
	for id, pos := range beforeB {
		if afterB[id] != pos {
			t.Errorf("page B widget %s position changed from %d to %d after page A reorders", id, pos, afterB[id])
		}
	}
}

// TestAdversarial_MoveWidget_DenseSequence runs a longer-than-minimum mix of
// moves on 5 widgets and confirms after each step the positions remain dense
// (1..N), unique, and no negative leaks. Verifies the negative-position swap
// idiom completes cleanly under a realistic workload.
func TestAdversarial_MoveWidget_DenseSequence(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	var ids []string
	for i := 0; i < 5; i++ {
		w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget %d: %v", i, err)
		}
		ids = append(ids, w.ID)
	}

	type op struct {
		name string
		fn   func() error
	}
	steps := []op{
		{"down(ids[0])", func() error { return svc.MoveWidgetDown(ctx, screenID, pageID, ids[0]) }},
		{"up(ids[4])", func() error { return svc.MoveWidgetUp(ctx, screenID, pageID, ids[4]) }},
		{"down(ids[2])", func() error { return svc.MoveWidgetDown(ctx, screenID, pageID, ids[2]) }},
		{"up(ids[1])", func() error { return svc.MoveWidgetUp(ctx, screenID, pageID, ids[1]) }},
		{"down(ids[3])", func() error { return svc.MoveWidgetDown(ctx, screenID, pageID, ids[3]) }},
	}
	for i, s := range steps {
		if err := s.fn(); err != nil {
			t.Fatalf("step %d %s: %v", i, s.name, err)
		}
		// No duplicates.
		rows, err := sqlDB.QueryContext(ctx,
			`SELECT page_id, position, COUNT(*) FROM widget_instances GROUP BY page_id, position HAVING COUNT(*) > 1`)
		if err != nil {
			t.Fatalf("step %d duplicate-check: %v", i, err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatalf("step %d %s: duplicate (page_id, position)", i, s.name)
		}
		rows.Close()

		// No negative positions leaked.
		var minPos int64
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT MIN(position) FROM widget_instances WHERE page_id = ?`, pageID).Scan(&minPos); err != nil {
			t.Fatalf("step %d min: %v", i, err)
		}
		if minPos < 1 {
			t.Fatalf("step %d %s: position %d leaked", i, s.name, minPos)
		}
	}
}

// TestAdversarial_MoveWidget_NoOpReleasesLock mirrors the equivalent page
// test: at-top MoveWidgetUp and at-bottom MoveWidgetDown commit cleanly so
// the next read/write succeeds without hanging on a stale write lock.
func TestAdversarial_MoveWidget_NoOpReleasesLock(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	// Top: no-op.
	if err := svc.MoveWidgetUp(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetUp top no-op: %v", err)
	}
	// Bottom: no-op.
	if err := svc.MoveWidgetDown(ctx, screenID, pageID, w2); err != nil {
		t.Fatalf("MoveWidgetDown bottom no-op: %v", err)
	}
	// A subsequent read must succeed.
	if _, err := svc.queries.ListWidgetInstancesByPage(ctx, pageID); err != nil {
		t.Fatalf("ListWidgetInstancesByPage after no-ops: %v", err)
	}
	// A subsequent write must succeed.
	if err := svc.MoveWidgetDown(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("subsequent MoveWidgetDown: %v", err)
	}
}

// TestAdversarial_WidgetReorderClosedDBSurfacesError pins that operating on
// a closed DB returns a non-nil error rather than panicking. swapWidget runs
// GetPageByID before BeginTx; both code paths must surface a wrapped error.
func TestAdversarial_WidgetReorderClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, _ := addTwoWidgets(t, svc, screenID, pageID)

	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MoveWidgetDown panicked after DB close: %v", r)
		}
	}()
	if err := svc.MoveWidgetDown(ctx, screenID, pageID, w1); err == nil {
		t.Error("MoveWidgetDown on closed DB returned nil, want error")
	}
}

// TestAdversarial_AddWidget_ClosedDBSurfacesError pins that AddWidget on a
// closed DB returns an error rather than panicking.
func TestAdversarial_AddWidget_ClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AddWidget panicked after DB close: %v", r)
		}
	}()
	if _, err := svc.AddWidget(ctx, screenID, pageID, text.Type); err == nil {
		t.Error("AddWidget on closed DB returned nil, want error")
	}
}

// TestAdversarial_DeleteWidget_ClosedDBSurfacesError pins that DeleteWidget
// on a closed DB returns an error rather than panicking.
func TestAdversarial_DeleteWidget_ClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	added, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DeleteWidget panicked after DB close: %v", r)
		}
	}()
	if err := svc.DeleteWidget(ctx, screenID, pageID, added.ID); err == nil {
		t.Error("DeleteWidget on closed DB returned nil, want error")
	}
}

// TestAdversarial_ConcurrentAddWidgetOnSamePage pins the documented
// behaviour for concurrent AddWidget calls. Mirrors the page-level test:
// the MaxWidgetPosition+Insert TOCTOU race is acceptable -- under
// contention, some inserts fail with wrapped UNIQUE-constraint errors,
// but at least one succeeds, the final state has unique positions, and
// no panic occurs.
func TestAdversarial_ConcurrentAddWidgetOnSamePage(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.AddWidget(ctx, screenID, pageID, text.Type)
		}(i)
	}
	wg.Wait()

	var ok int
	for i, e := range errs {
		switch {
		case e == nil:
			ok++
		case strings.Contains(e.Error(), "UNIQUE constraint failed"):
			// Documented race-y failure. Acceptable per TASK-024 (same
			// contract as TASK-023's page-level race test).
		default:
			t.Errorf("goroutine %d: unexpected error %v", i, e)
		}
	}
	if ok < 1 {
		t.Fatalf("concurrent AddWidget: %d successes, want at least 1", ok)
	}

	rows, err := sqlDB.QueryContext(ctx,
		`SELECT page_id, position, COUNT(*) FROM widget_instances GROUP BY page_id, position HAVING COUNT(*) > 1`)
	if err != nil {
		t.Fatalf("duplicate-check: %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("concurrent AddWidget produced duplicate (page_id, position)")
	}
	rows.Close()
}

// TestAdversarial_DeletePage_CascadesAddedWidgets pins that DeletePage
// removes widget instances created via the new AddWidget method (not just
// raw-SQL fixtures the TASK-023 test used). FR-21 (CASCADE) must hold
// regardless of how the widget got into the table.
func TestAdversarial_DeletePage_CascadesAddedWidgets(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	// Add three widgets via the service.
	var addedIDs []string
	for i := 0; i < 3; i++ {
		w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget %d: %v", i, err)
		}
		addedIDs = append(addedIDs, w.ID)
	}

	if err := svc.DeletePage(ctx, screenID, pageID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM widget_instances WHERE page_id = ?`, pageID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("widget_instances has %d rows after DeletePage CASCADE, want 0 (added IDs: %v)", count, addedIDs)
	}
}

// TestAdversarial_SQLInjectionInWidgetAndPageIDs probes all widget-targeted
// service entry points with classic SQL-injection strings in the widget id,
// page id, and screen id parameters. Every probe must come back through the
// sqlc parameterised query path -- not by injecting SQL into the table.
func TestAdversarial_SQLInjectionInWidgetAndPageIDs(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}

	payloads := []string{
		"'); DROP TABLE widget_instances;--",
		"' OR 1=1 --",
		"x' UNION SELECT * FROM screens --",
		"'; UPDATE widget_instances SET type='hax' WHERE 1=1; --",
	}
	for _, p := range payloads {
		// As widgetID
		if err := svc.DeleteWidget(ctx, screenID, pageID, p); !errors.Is(err, ErrWidgetNotFound) {
			t.Errorf("DeleteWidget(widgetID=%q) = %v, want ErrWidgetNotFound", p, err)
		}
		if err := svc.MoveWidgetUp(ctx, screenID, pageID, p); !errors.Is(err, ErrWidgetNotFound) {
			t.Errorf("MoveWidgetUp(widgetID=%q) = %v, want ErrWidgetNotFound", p, err)
		}
		// As pageID
		if err := svc.DeleteWidget(ctx, screenID, p, w.ID); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("DeleteWidget(pageID=%q) = %v, want ErrPageNotFound", p, err)
		}
		// As screenID
		if _, err := svc.AddWidget(ctx, p, pageID, text.Type); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("AddWidget(screenID=%q) = %v, want ErrPageNotFound", p, err)
		}
	}

	// Table and row intact.
	if _, err := svc.queries.GetWidgetInstanceByID(ctx,
		db.GetWidgetInstanceByIDParams{ID: w.ID, PageID: pageID}); err != nil {
		t.Errorf("widget vanished after injection probes: %v", err)
	}
	var widgetCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM widget_instances").Scan(&widgetCount); err != nil {
		t.Fatalf("count widgets: %v", err)
	}
	if widgetCount != 1 {
		t.Errorf("widget_instances has %d rows; injection probe wrote unexpected data", widgetCount)
	}
	// And the widget's type is still "text" -- the UPDATE injection didn't land.
	row, err := svc.queries.GetWidgetInstanceByID(ctx,
		db.GetWidgetInstanceByIDParams{ID: w.ID, PageID: pageID})
	if err != nil {
		t.Fatalf("GetWidgetInstanceByID: %v", err)
	}
	if row.Type != text.Type {
		t.Errorf("widget type mutated to %q (UPDATE injection landed)", row.Type)
	}
}

// TestAdversarial_AddWidget_DefaultConfigWithNULBytes pins how the storage
// layer handles config bytes containing NUL (\x00). This is a probe rather
// than a contract -- the architecture spec calls config "TEXT (JSON bytes)",
// and SQLite TEXT columns may or may not preserve NULs depending on driver
// behaviour. The test simply pins whatever the current behaviour is so a
// regression (or a deliberate change to e.g. reject NULs at the validator
// level) is intentional.
func TestAdversarial_AddWidget_DefaultConfigWithNULBytes(t *testing.T) {
	t.Parallel()
	nulConfig := []byte("{\"text\":\"before\x00after\"}")
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:           "nulcfg",
		DisplayName:    "NulCfg",
		Description:    "config with NUL",
		New:            func() widget.Widget { return brokenWidget{} },
		DefaultConfig:  func() []byte { return nulConfig },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "nulcfg"}, nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, "nulcfg")
	if err != nil {
		t.Fatalf("AddWidget with NUL config: %v", err)
	}
	// Whatever the driver does, AddWidget's returned Config must equal what
	// GetScreenFull returns -- the read paths must agree.
	full, err := svc.GetScreenFull(ctx, screenID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages) != 1 || len(full.Pages[0].Widgets) != 1 {
		t.Fatalf("unexpected shape")
	}
	roundTrip := full.Pages[0].Widgets[0].Config
	if string(roundTrip) != string(added.Config) {
		t.Errorf("read paths disagree: AddWidget returned %q, GetScreenFull returned %q",
			added.Config, roundTrip)
	}
}

// TestAdversarial_AddWidget_DefaultConfigBytesAreUnicode pins that widget
// default configs containing unicode (e.g. emoji, accented chars) are stored
// and retrieved byte-for-byte. SQLite TEXT columns are UTF-8 by convention,
// but a regression that re-encoded via templ-safe normalisation or
// string-to-bytes-via-rune-conversion would corrupt the bytes.
func TestAdversarial_AddWidget_DefaultConfigBytesAreUnicode(t *testing.T) {
	t.Parallel()
	unicodeConfig := []byte(`{"text":"héllo 世界 🌍"}`)
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:           "unicode",
		DisplayName:    "Unicode",
		Description:    "stores unicode bytes",
		New:            func() widget.Widget { return brokenWidget{} },
		DefaultConfig:  func() []byte { return unicodeConfig },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "unicode"}, nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	added, err := svc.AddWidget(context.Background(), screenID, pageID, "unicode")
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}
	if string(added.Config) != string(unicodeConfig) {
		t.Errorf("Config = %q, want %q (unicode bytes mutated)", added.Config, unicodeConfig)
	}

	full, err := svc.GetScreenFull(context.Background(), screenID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages) != 1 || len(full.Pages[0].Widgets) != 1 {
		t.Fatalf("unexpected ScreenFull shape: %+v", full)
	}
	got := full.Pages[0].Widgets[0].Config
	if string(got) != string(unicodeConfig) {
		t.Errorf("GetScreenFull Config = %q, want %q (round-trip corrupted unicode)", got, unicodeConfig)
	}
}

// TestAdversarial_GetScreenFull_WidgetConfigByteForByteRoundTrip pins that
// GetScreenFull returns config bytes byte-identical to what AddWidget stored.
// AC-20 (the default-validates property) covers semantic round-trip; this
// covers the byte-level round-trip that downstream cache-keying assumes.
func TestAdversarial_GetScreenFull_WidgetConfigByteForByteRoundTrip(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}

	full, err := svc.GetScreenFull(ctx, screenID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages[0].Widgets) != 1 {
		t.Fatalf("got %d widgets, want 1", len(full.Pages[0].Widgets))
	}
	got := full.Pages[0].Widgets[0].Config
	if string(got) != string(added.Config) {
		t.Errorf("GetScreenFull Config = %q, want %q (byte mismatch with AddWidget return)", got, added.Config)
	}
}

// TestAdversarial_AddWidget_FirstWidgetOnEmptyPageStartsAtPos1 pins the
// boundary case where the page has no existing widgets. MaxWidgetPosition
// returns 0 via COALESCE; the new widget's position is therefore 1. A
// regression that used COALESCE(MAX(position), 1) would land at position 2
// and break the 1-indexed invariant for new pages.
func TestAdversarial_AddWidget_FirstWidgetOnEmptyPageStartsAtPos1(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	w, err := svc.AddWidget(context.Background(), screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}
	if w.Position != 1 {
		t.Errorf("first widget on empty page: Position = %d, want 1", w.Position)
	}
}

// TestAdversarial_GetScreenFull_MultiPageWidgetsOnPositionOrder pins that
// GetScreenFull returns widgets in (page, position) order even when widgets
// were created out of order across multiple pages via the service. This is
// the spec's primary read path; an off-by-one in the in-Go partition would
// scramble the render.
func TestAdversarial_GetScreenFull_MultiPageWidgetsOnPositionOrder(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "k", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	pageA, err := svc.CreatePage(ctx, screen.ID, "a")
	if err != nil {
		t.Fatalf("CreatePage A: %v", err)
	}
	pageB, err := svc.CreatePage(ctx, screen.ID, "b")
	if err != nil {
		t.Fatalf("CreatePage B: %v", err)
	}

	// Interleave widget creation across pages.
	type want struct {
		page    string
		pos     int
		widgetN int
	}
	var aWidgets, bWidgets []string
	for i := 0; i < 3; i++ {
		wA, err := svc.AddWidget(ctx, screen.ID, pageA.ID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget A %d: %v", i, err)
		}
		aWidgets = append(aWidgets, wA.ID)
		wB, err := svc.AddWidget(ctx, screen.ID, pageB.ID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget B %d: %v", i, err)
		}
		bWidgets = append(bWidgets, wB.ID)
	}

	// Move some around to make positions non-trivial.
	if err := svc.MoveWidgetDown(ctx, screen.ID, pageA.ID, aWidgets[0]); err != nil {
		t.Fatalf("MoveWidgetDown A[0]: %v", err)
	}

	full, err := svc.GetScreenFull(ctx, screen.ID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(full.Pages))
	}
	// Pages are in position order.
	if full.Pages[0].Page.ID != pageA.ID || full.Pages[1].Page.ID != pageB.ID {
		t.Fatalf("pages out of order: got %q, %q", full.Pages[0].Page.ID, full.Pages[1].Page.ID)
	}
	// Each page's widgets are in position order.
	for pi, pw := range full.Pages {
		for wi := 1; wi < len(pw.Widgets); wi++ {
			if pw.Widgets[wi-1].Position >= pw.Widgets[wi].Position {
				t.Errorf("page %d widgets out of position order at index %d: %d >= %d",
					pi, wi, pw.Widgets[wi-1].Position, pw.Widgets[wi].Position)
			}
		}
	}
	// Page A's widget that was moved down should now be at position 2.
	for _, w := range full.Pages[0].Widgets {
		if w.ID == aWidgets[0] && w.Position != 2 {
			t.Errorf("aWidgets[0] (moved down) at position %d, want 2", w.Position)
		}
	}
	// Page B should be untouched by the page-A move.
	if len(full.Pages[1].Widgets) != 3 {
		t.Errorf("page B has %d widgets, want 3", len(full.Pages[1].Widgets))
	}
}

// TestAdversarial_AddWidget_ManySequential pins that AddWidget produces a
// dense 1..N position sequence under purely-sequential calls (no
// concurrency). This is the happy path for the typical admin workflow
// ("click 'add widget' 10 times"); a regression in MaxWidgetPosition+1
// arithmetic would surface as gaps.
func TestAdversarial_AddWidget_ManySequential(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	const N = 10
	for i := 1; i <= N; i++ {
		w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget #%d: %v", i, err)
		}
		if w.Position != i {
			t.Errorf("AddWidget #%d: Position = %d, want %d", i, w.Position, i)
		}
	}

	rows, err := svc.queries.ListWidgetInstancesByPage(ctx, pageID)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	if len(rows) != N {
		t.Fatalf("got %d widgets, want %d", len(rows), N)
	}
	for i, r := range rows {
		if int(r.Position) != i+1 {
			t.Errorf("row %d position = %d, want %d", i, r.Position, i+1)
		}
	}
}

// TestAdversarial_RegistrationDuplicateRejected confirms the registry's own
// guarantee that registering two widgets with the same Type is rejected. The
// task posed this as a registry-behavioural question; the registry returns an
// error on duplicate (it does NOT silently overwrite), so AddWidget always
// resolves to the first registration. This pins the contract end-to-end:
// register text twice → second Register fails → AddWidget(text) still works
// and returns the original DefaultConfig bytes.
func TestAdversarial_RegistrationDuplicateRejected(t *testing.T) {
	t.Parallel()
	registry := widget.NewRegistry()
	if err := registry.Register(text.Registration()); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// Second registration with the same Type must fail.
	if err := registry.Register(text.Registration()); err == nil {
		t.Error("second Register(text) returned nil, want duplicate-type error")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("second Register err = %v, want 'already registered'", err)
	}
	// AddWidget through this registry must still work, using the first registration.
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w, err := svc.AddWidget(context.Background(), screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget after dup-register attempt: %v", err)
	}
	if !strings.Contains(string(w.Config), "Hello, screens") {
		t.Errorf("Config = %q, want first DefaultConfig (Hello, screens)", w.Config)
	}
}

// TestAdversarial_AddWidget_AfterDelete_PositionContinuesFromMax pins that
// AddWidget after DeleteWidget uses MAX(position)+1, leaving a gap rather
// than reusing the deleted slot. Mirrors the page-level "delete creates a
// gap" contract from TASK-023's adversarial tests.
func TestAdversarial_AddWidget_AfterDelete_PositionContinuesFromMax(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)
	_ = w1

	// Delete w2 (position 2). Add a new widget. The new widget gets position 3
	// (= MAX(1) + 1 after delete? actually MAX = 1 then +1 = 2). The spec's
	// "MAX+1" is on the surviving rows; with only w1 left the new one is at 2.
	if err := svc.DeleteWidget(ctx, screenID, pageID, w2); err != nil {
		t.Fatalf("DeleteWidget: %v", err)
	}
	w3, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget after delete: %v", err)
	}
	if w3.Position != 2 {
		t.Errorf("AddWidget after delete: position = %d, want 2 (MAX(1)+1 with only w1 surviving)", w3.Position)
	}

	// Now delete w1 (position 1), leaving only w3 at position 2. Add another
	// widget. The new widget gets position 3 (= MAX(2) + 1) -- this is the
	// gap-creating contract: position 1 is NOT reused.
	if err := svc.DeleteWidget(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("DeleteWidget w1: %v", err)
	}
	w4, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget after w1 delete: %v", err)
	}
	if w4.Position != 3 {
		t.Errorf("AddWidget after gap delete: position = %d, want 3 (MAX(2)+1)", w4.Position)
	}
}

// TestAdversarial_AddWidget_NilDefaultConfigPanicsOrErrors pins what happens
// when DefaultConfig() returns a nil byte slice. The registry's ValidateConfig
// would receive nil; text/json.Unmarshal of nil returns "unexpected end of
// JSON input". AddWidget should surface that as "default config failed
// validation" rather than panicking.
func TestAdversarial_AddWidget_NilDefaultConfig(t *testing.T) {
	t.Parallel()
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:           "niloid",
		DisplayName:    "Nil",
		Description:    "returns nil default",
		New:            func() widget.Widget { return brokenWidget{} },
		DefaultConfig:  func() []byte { return nil },
		ValidateConfig: text.Registration().ValidateConfig, // uses json.Unmarshal, rejects nil bytes
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	_, err := svc.AddWidget(context.Background(), screenID, pageID, "niloid")
	if err == nil {
		t.Fatal("AddWidget(nil-default) returned nil, want validation error")
	}
	if !strings.Contains(err.Error(), "default config failed validation") {
		t.Errorf("err = %v, want 'default config failed validation'", err)
	}
}

// TestAdversarial_AddWidget_EmptyByteDefault_StoresEmptyString pins that a
// widget whose DefaultConfig returns []byte{} but whose ValidateConfig
// accepts empty bytes is stored as an empty string (NOT NULL). The schema
// declares config TEXT NOT NULL.
func TestAdversarial_AddWidget_EmptyByteDefault(t *testing.T) {
	t.Parallel()
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:           "emptyaccept",
		DisplayName:    "EmptyAccept",
		Description:    "accepts empty bytes",
		New:            func() widget.Widget { return brokenWidget{} },
		DefaultConfig:  func() []byte { return []byte{} },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "emptyaccept"}, nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, "emptyaccept")
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}
	if len(added.Config) != 0 {
		t.Errorf("Config = %q, want empty bytes", added.Config)
	}
	// Confirm it round-trips via GetScreenFull too.
	full, err := svc.GetScreenFull(ctx, screenID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages) != 1 || len(full.Pages[0].Widgets) != 1 {
		t.Fatalf("unexpected shape: %+v", full)
	}
	if len(full.Pages[0].Widgets[0].Config) != 0 {
		t.Errorf("GetScreenFull Config = %q, want empty bytes", full.Pages[0].Widgets[0].Config)
	}
}

// TestAdversarial_AddWidget_ManyWidgetsCorrectPositions creates 25 widgets
// in a loop and verifies the position field grows monotonically. This is a
// regression guard for MaxWidgetPosition+1 arithmetic going wrong at scale.
func TestAdversarial_AddWidget_ManyWidgets(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	const N = 25
	positions := make(map[int]bool, N)
	for i := 0; i < N; i++ {
		w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget #%d: %v", i, err)
		}
		if positions[w.Position] {
			t.Fatalf("AddWidget #%d duplicated position %d", i, w.Position)
		}
		positions[w.Position] = true
	}
	if len(positions) != N {
		t.Errorf("got %d unique positions, want %d", len(positions), N)
	}
	// All positions are dense 1..N.
	for i := 1; i <= N; i++ {
		if !positions[i] {
			t.Errorf("missing position %d in dense 1..%d", i, N)
		}
	}
}

// TestAdversarial_MoveWidget_AcrossGapIsNoOp mirrors the page-level
// MovePageAcrossGapIsNoOp test. After deleting a middle widget, the
// neighbour-by-exact-position lookup finds nothing, so MoveWidgetUp/Down on
// adjacent widgets becomes a no-op. We pin this UX behaviour so any future
// change to use "next-smaller / next-larger" lookups is intentional.
func TestAdversarial_MoveWidget_AcrossGapIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)
	w3row, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget w3: %v", err)
	}
	w3 := w3row.ID

	// Delete middle, leaving {w1:1, w3:3}, gap at 2.
	if err := svc.DeleteWidget(ctx, screenID, pageID, w2); err != nil {
		t.Fatalf("DeleteWidget w2: %v", err)
	}
	// MoveWidgetUp(w3) looks for neighbour at position 2 → no row → no-op.
	if err := svc.MoveWidgetUp(ctx, screenID, pageID, w3); err != nil {
		t.Fatalf("MoveWidgetUp across gap: %v", err)
	}
	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 1 || pos[w3] != 3 {
		t.Errorf("after MoveWidgetUp(w3) across gap: {w1:%d, w3:%d}, want unchanged {1,3}",
			pos[w1], pos[w3])
	}
	// MoveWidgetDown(w1) likewise.
	if err := svc.MoveWidgetDown(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetDown across gap: %v", err)
	}
	pos = widgetPositions(t, svc, pageID)
	if pos[w1] != 1 || pos[w3] != 3 {
		t.Errorf("after MoveWidgetDown(w1) across gap: {w1:%d, w3:%d}, want unchanged {1,3}",
			pos[w1], pos[w3])
	}
}

// TestAdversarial_ConcurrentMixedReorder runs concurrent move operations on
// the same page and asserts no deadlock, no race detector violations, and
// no UNIQUE constraint duplicates remain. Uses a small number of goroutines
// against the single-connection test DB; mostly checks that the
// transaction-locking pattern is sound (no missing Commit, no shared mutable
// state being raced on).
func TestAdversarial_ConcurrentMixedReorder(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	// 4 widgets at positions 1..4.
	var ids []string
	for i := 0; i < 4; i++ {
		w, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
		if err != nil {
			t.Fatalf("AddWidget %d: %v", i, err)
		}
		ids = append(ids, w.ID)
	}

	const goroutines = 4
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for it := 0; it < 5; it++ {
				switch (g + it) % 4 {
				case 0:
					_ = svc.MoveWidgetDown(ctx, screenID, pageID, ids[0])
				case 1:
					_ = svc.MoveWidgetUp(ctx, screenID, pageID, ids[3])
				case 2:
					_ = svc.MoveWidgetUp(ctx, screenID, pageID, ids[2])
				case 3:
					_ = svc.MoveWidgetDown(ctx, screenID, pageID, ids[1])
				}
			}
		}(g)
	}
	wg.Wait()

	// No duplicates, no negatives.
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT page_id, position, COUNT(*) FROM widget_instances GROUP BY page_id, position HAVING COUNT(*) > 1`)
	if err != nil {
		t.Fatalf("duplicate-check: %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("duplicate (page_id, position) after concurrent reorders")
	}
	rows.Close()

	var minPos int64
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT MIN(position) FROM widget_instances WHERE page_id = ?`, pageID).Scan(&minPos); err != nil {
		t.Fatalf("min: %v", err)
	}
	if minPos < 1 {
		t.Errorf("negative position %d leaked after concurrent reorders", minPos)
	}
}

// TestAdversarial_AddWidget_DefaultConfigInvokedEveryCall pins that AddWidget
// calls DefaultConfig() fresh on every invocation rather than caching the
// first result. This matters because some widget types may want to embed a
// timestamp or seed in the default (none today, but the contract should
// allow it). A cached-once implementation would silently break that future.
func TestAdversarial_AddWidget_DefaultConfigInvokedEveryCall(t *testing.T) {
	t.Parallel()
	var calls int
	var mu sync.Mutex
	registry := widget.NewRegistry()
	if err := registry.Register(widget.Registration{
		Type:        "counter",
		DisplayName: "Counter",
		Description: "counts DefaultConfig invocations",
		New:         func() widget.Widget { return brokenWidget{} },
		DefaultConfig: func() []byte {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return []byte(fmt.Sprintf(`{"call":%d}`, calls))
		},
		ValidateConfig: func(raw []byte) (widget.Instance, error) {
			return widget.Instance{Type: "counter"}, nil
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc, themeID := newTestServiceWithRegistry(t, registry)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.AddWidget(ctx, screenID, pageID, "counter"); err != nil {
			t.Fatalf("AddWidget %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("DefaultConfig invoked %d times across 3 AddWidget calls; want 3", calls)
	}
}
