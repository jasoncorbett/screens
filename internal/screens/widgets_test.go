package screens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
	"github.com/jasoncorbett/screens/internal/widget/text"
)

// newTestServiceWithText builds a screens.Service whose injected registry
// has the placeholder text widget registered. Widget tests need a registry
// with at least one known type; the default newTestService() helper uses an
// empty registry, which is correct for screen / page tests but useless here.
// Returns the service, raw DB handle, default theme ID, and the registry so
// individual tests can call Validate against it for the AC-20 round-trip
// check.
func newTestServiceWithText(t *testing.T) (*Service, *sql.DB, string, *widget.Registry) {
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
	registry := widget.NewRegistry()
	if err := registry.Register(text.Registration()); err != nil {
		t.Fatalf("register text widget: %v", err)
	}
	svc := NewService(sqlDB, themesSvc, registry)
	return svc, sqlDB, def.ID, registry
}

// createScreenWithPage creates a screen and one page on it, returning the
// IDs. Used by widget tests that need a target page.
func createScreenWithPage(t *testing.T, svc *Service, themeID string) (screenID, pageID string) {
	t.Helper()
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	page, err := svc.CreatePage(ctx, screen.ID, "main")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return screen.ID, page.ID
}

// addTwoWidgets adds two text widgets to the given page via the service and
// returns their IDs in position order (w1 at position 1, w2 at position 2).
func addTwoWidgets(t *testing.T, svc *Service, screenID, pageID string) (w1, w2 string) {
	t.Helper()
	ctx := context.Background()
	first, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget 1: %v", err)
	}
	second, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget 2: %v", err)
	}
	return first.ID, second.ID
}

// widgetPositions returns the ID -> position mapping for the named page.
func widgetPositions(t *testing.T, svc *Service, pageID string) map[string]int {
	t.Helper()
	rows, err := svc.queries.ListWidgetInstancesByPage(context.Background(), pageID)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ID] = int(r.Position)
	}
	return out
}

func TestAddWidget_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	first, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget 1: %v", err)
	}
	if first.Type != text.Type {
		t.Errorf("Type = %q, want %q", first.Type, text.Type)
	}
	if first.Position != 1 {
		t.Errorf("Position = %d, want 1", first.Position)
	}
	if first.ID == "" {
		t.Error("returned widget has empty ID")
	}
	if first.PageID != pageID {
		t.Errorf("PageID = %q, want %q", first.PageID, pageID)
	}

	// The default config should round-trip to the documented JSON shape.
	var cfg text.Config
	if err := json.Unmarshal(first.Config, &cfg); err != nil {
		t.Fatalf("unmarshal first.Config: %v", err)
	}
	if cfg.Text != "Hello, screens" {
		t.Errorf("default text = %q, want %q", cfg.Text, "Hello, screens")
	}

	second, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget 2: %v", err)
	}
	if second.Position != 2 {
		t.Errorf("second Position = %d, want 2", second.Position)
	}
}

func TestAddWidget_UnknownType(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	_, err := svc.AddWidget(context.Background(), screenID, pageID, "nope")
	if !errors.Is(err, ErrUnknownWidgetType) {
		t.Fatalf("AddWidget returned %v, want ErrUnknownWidgetType", err)
	}

	// No row should have been created.
	var count int
	row := sqlDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM widget_instances WHERE page_id = ?`, pageID)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count widget_instances: %v", err)
	}
	if count != 0 {
		t.Errorf("widget_instances has %d rows after rejected AddWidget, want 0", count)
	}
}

func TestAddWidget_UnknownPage(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	_, err = svc.AddWidget(ctx, screen.ID, "nonexistent-page", text.Type)
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("AddWidget returned %v, want ErrPageNotFound", err)
	}
}

func TestAddWidget_DefaultValidates(t *testing.T) {
	t.Parallel()
	svc, _, themeID, registry := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}

	row, err := svc.queries.GetWidgetInstanceByID(ctx, db.GetWidgetInstanceByIDParams{
		ID: added.ID, PageID: pageID,
	})
	if err != nil {
		t.Fatalf("GetWidgetInstanceByID: %v", err)
	}
	if _, err := registry.Validate(text.Type, []byte(row.Config)); err != nil {
		t.Errorf("registry.Validate persisted default config: %v", err)
	}
}

func TestDeleteWidget_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	added, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget: %v", err)
	}

	if err := svc.DeleteWidget(ctx, screenID, pageID, added.ID); err != nil {
		t.Fatalf("DeleteWidget: %v", err)
	}

	rows, err := svc.queries.ListWidgetInstancesByPage(ctx, pageID)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("page has %d widgets after delete, want 0", len(rows))
	}
}

func TestDeleteWidget_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)

	err := svc.DeleteWidget(context.Background(), screenID, pageID, "no-such-widget")
	if !errors.Is(err, ErrWidgetNotFound) {
		t.Errorf("DeleteWidget returned %v, want ErrWidgetNotFound", err)
	}
}

func TestDeleteWidget_UnknownPage(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	err = svc.DeleteWidget(ctx, screen.ID, "nonexistent-page", "anything")
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("DeleteWidget returned %v, want ErrPageNotFound", err)
	}
}

func TestMoveWidgetDown_Swaps(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	if err := svc.MoveWidgetDown(context.Background(), screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetDown: %v", err)
	}

	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 2 || pos[w2] != 1 {
		t.Errorf("after MoveWidgetDown(w1): positions = {w1:%d, w2:%d}, want {w1:2, w2:1}",
			pos[w1], pos[w2])
	}
}

func TestMoveWidgetUp_Swaps(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	if err := svc.MoveWidgetUp(context.Background(), screenID, pageID, w2); err != nil {
		t.Fatalf("MoveWidgetUp: %v", err)
	}

	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 2 || pos[w2] != 1 {
		t.Errorf("after MoveWidgetUp(w2): positions = {w1:%d, w2:%d}, want {w1:2, w2:1}",
			pos[w1], pos[w2])
	}
}

func TestMoveWidget_AtTop_NoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	if err := svc.MoveWidgetUp(context.Background(), screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetUp at top: %v", err)
	}

	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 1 || pos[w2] != 2 {
		t.Errorf("after MoveWidgetUp(w1 at top): positions = {w1:%d, w2:%d}, want unchanged {1,2}",
			pos[w1], pos[w2])
	}
}

func TestMoveWidget_AtBottom_NoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	if err := svc.MoveWidgetDown(context.Background(), screenID, pageID, w2); err != nil {
		t.Fatalf("MoveWidgetDown at bottom: %v", err)
	}

	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 1 || pos[w2] != 2 {
		t.Errorf("after MoveWidgetDown(w2 at bottom): positions = {w1:%d, w2:%d}, want unchanged {1,2}",
			pos[w1], pos[w2])
	}
}

func TestMoveWidget_UnknownWidget(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	ctx := context.Background()

	if err := svc.MoveWidgetUp(ctx, screenID, pageID, "no-such-widget"); !errors.Is(err, ErrWidgetNotFound) {
		t.Errorf("MoveWidgetUp returned %v, want ErrWidgetNotFound", err)
	}
	if err := svc.MoveWidgetDown(ctx, screenID, pageID, "no-such-widget"); !errors.Is(err, ErrWidgetNotFound) {
		t.Errorf("MoveWidgetDown returned %v, want ErrWidgetNotFound", err)
	}
}

func TestMoveWidget_UnknownPage(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	if err := svc.MoveWidgetUp(ctx, screen.ID, "nonexistent-page", "anything"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MoveWidgetUp returned %v, want ErrPageNotFound", err)
	}
	if err := svc.MoveWidgetDown(ctx, screen.ID, "nonexistent-page", "anything"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MoveWidgetDown returned %v, want ErrPageNotFound", err)
	}
}

func TestMoveWidget_RoundTrip(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)
	ctx := context.Background()

	if err := svc.MoveWidgetDown(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetDown: %v", err)
	}
	if err := svc.MoveWidgetUp(ctx, screenID, pageID, w1); err != nil {
		t.Fatalf("MoveWidgetUp: %v", err)
	}

	pos := widgetPositions(t, svc, pageID)
	if pos[w1] != 1 || pos[w2] != 2 {
		t.Errorf("after round trip: positions = {w1:%d, w2:%d}, want {w1:1, w2:2}",
			pos[w1], pos[w2])
	}
}

// TestMoveWidget_NoTransientDuplicates verifies the
// UNIQUE(page_id, position) invariant holds after each swap. Performs two
// sequential MoveWidgetDown calls and asserts no duplicate (page_id,
// position) rows exist after either.
func TestMoveWidget_NoTransientDuplicates(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, _ := addTwoWidgets(t, svc, screenID, pageID)
	ctx := context.Background()

	// Add a third widget so there's a meaningful sequence to walk.
	third, err := svc.AddWidget(ctx, screenID, pageID, text.Type)
	if err != nil {
		t.Fatalf("AddWidget 3: %v", err)
	}
	_ = third

	for i := 0; i < 2; i++ {
		if err := svc.MoveWidgetDown(ctx, screenID, pageID, w1); err != nil {
			t.Fatalf("MoveWidgetDown iteration %d: %v", i, err)
		}

		rows, err := sqlDB.QueryContext(ctx,
			`SELECT page_id, position, COUNT(*) FROM widget_instances GROUP BY page_id, position HAVING COUNT(*) > 1`)
		if err != nil {
			t.Fatalf("duplicate-check query iteration %d: %v", i, err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatalf("iteration %d: duplicate (page_id, position) detected", i)
		}
		rows.Close()
	}
}

// TestGetScreenFull_IncludesAddedWidgets builds a screen + page + two
// widgets entirely via the service (no raw SQL) and confirms GetScreenFull
// returns the widget configs in position order. Complements
// TestGetScreenFull_AggregatesPagesAndWidgets which uses raw-SQL fixtures.
func TestGetScreenFull_IncludesAddedWidgets(t *testing.T) {
	t.Parallel()
	svc, _, themeID, _ := newTestServiceWithText(t)
	screenID, pageID := createScreenWithPage(t, svc, themeID)
	w1, w2 := addTwoWidgets(t, svc, screenID, pageID)

	full, err := svc.GetScreenFull(context.Background(), screenID)
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if len(full.Pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(full.Pages))
	}
	widgets := full.Pages[0].Widgets
	if len(widgets) != 2 {
		t.Fatalf("got %d widgets, want 2", len(widgets))
	}
	if widgets[0].ID != w1 || widgets[1].ID != w2 {
		t.Errorf("widgets out of position order: got %q, %q want %q, %q",
			widgets[0].ID, widgets[1].ID, w1, w2)
	}
	// Both widgets came from the same DefaultConfig so their bytes match.
	for i, w := range widgets {
		var cfg text.Config
		if err := json.Unmarshal(w.Config, &cfg); err != nil {
			t.Fatalf("unmarshal widget %d config: %v", i, err)
		}
		if cfg.Text != "Hello, screens" {
			t.Errorf("widget %d Config.Text = %q, want %q", i, cfg.Text, "Hello, screens")
		}
	}
}
