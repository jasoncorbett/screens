package db

import (
	"context"
	"strings"
	"testing"
)

// insertTestScreen seeds a screen row pointing at the given theme. Tests pass
// id / name / theme_id; everything else has a sensible default.
func insertTestScreen(ctx context.Context, t *testing.T, q *Queries, id, name, themeID string) {
	t.Helper()
	if err := q.CreateScreen(ctx, CreateScreenParams{
		ID:                      id,
		Name:                    name,
		ThemeID:                 themeID,
		RotationIntervalSeconds: 30,
	}); err != nil {
		t.Fatalf("CreateScreen(id=%s, name=%s, theme=%s): %v", id, name, themeID, err)
	}
}

// insertTestPage seeds a page row on the given screen at the given position.
func insertTestPage(ctx context.Context, t *testing.T, q *Queries, id, screenID, name string, position int64) {
	t.Helper()
	if err := q.CreatePage(ctx, CreatePageParams{
		ID:       id,
		ScreenID: screenID,
		Name:     name,
		Position: position,
	}); err != nil {
		t.Fatalf("CreatePage(id=%s, screen=%s, position=%d): %v", id, screenID, position, err)
	}
}

// insertTestWidget seeds a widget_instance row on the given page.
func insertTestWidget(ctx context.Context, t *testing.T, q *Queries, id, pageID, widgetType, config string, position int64) {
	t.Helper()
	if err := q.CreateWidgetInstance(ctx, CreateWidgetInstanceParams{
		ID:       id,
		PageID:   pageID,
		Type:     widgetType,
		Config:   config,
		Position: position,
	}); err != nil {
		t.Fatalf("CreateWidgetInstance(id=%s, page=%s, position=%d): %v", id, pageID, position, err)
	}
}

// TestScreenModelTables_ExistAfterMigration verifies that migrations 007 / 008 /
// 009 actually run and produce the three expected tables. Without this the
// foreign-key tests below would fail with confusing "no such table" errors
// rather than a clear "the migration didn't apply" failure.
func TestScreenModelTables_ExistAfterMigration(t *testing.T) {
	database := OpenTestDB(t)

	tables := []string{"screens", "pages", "widget_instances"}
	for _, table := range tables {
		var got string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&got)
		if err != nil {
			t.Errorf("table %q not created by migration: %v", table, err)
			continue
		}
		if got != table {
			t.Errorf("table name = %q, want %q", got, table)
		}
	}
}

// TestScreensTable_ThemeFKRequiresExistingTheme verifies the
// `theme_id REFERENCES themes(id)` constraint on the screens table rejects an
// INSERT pointing at a non-existent theme. This is the FK definition that
// makes the RESTRICT-on-delete behaviour meaningful.
func TestScreensTable_ThemeFKRequiresExistingTheme(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreateScreen(ctx, CreateScreenParams{
		ID:                      "screen-orphan",
		Name:                    "orphan",
		ThemeID:                 "no-such-theme",
		RotationIntervalSeconds: 30,
	})
	if err == nil {
		t.Fatal("expected FOREIGN KEY violation for missing theme_id, got nil")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("expected FOREIGN KEY error, got: %v", err)
	}
}

// TestPagesTable_ScreenFKRequiresExistingScreen verifies the
// `screen_id REFERENCES screens(id)` constraint rejects an orphan page.
func TestPagesTable_ScreenFKRequiresExistingScreen(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreatePage(ctx, CreatePageParams{
		ID:       "page-orphan",
		ScreenID: "no-such-screen",
		Name:     "orphan",
		Position: 1,
	})
	if err == nil {
		t.Fatal("expected FOREIGN KEY violation for missing screen_id, got nil")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("expected FOREIGN KEY error, got: %v", err)
	}
}

// TestWidgetInstancesTable_PageFKRequiresExistingPage verifies the
// `page_id REFERENCES pages(id)` constraint rejects an orphan widget instance.
func TestWidgetInstancesTable_PageFKRequiresExistingPage(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreateWidgetInstance(ctx, CreateWidgetInstanceParams{
		ID:       "widget-orphan",
		PageID:   "no-such-page",
		Type:     "text",
		Config:   "{}",
		Position: 1,
	})
	if err == nil {
		t.Fatal("expected FOREIGN KEY violation for missing page_id, got nil")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("expected FOREIGN KEY error, got: %v", err)
	}
}

// TestScreensTable_ThemeFKRestrictsDelete verifies that the
// `ON DELETE RESTRICT` clause on screens.theme_id causes a raw SQL theme
// delete to fail with the SQLite FK violation error when at least one screen
// references the theme, and that BOTH rows survive the failed delete. This is
// the SPEC-006 AC-9 (data layer half).
func TestScreensTable_ThemeFKRestrictsDelete(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-used", "used", 0)
	insertTestScreen(ctx, t, q, "screen-using", "using", "theme-used")

	// A raw DELETE bypasses any application-level guard. We expect the FK
	// constraint to surface as an error string containing the SQLite
	// canonical "FOREIGN KEY constraint failed" phrase.
	_, err := database.ExecContext(ctx, "DELETE FROM themes WHERE id = ?", "theme-used")
	if err == nil {
		t.Fatal("expected FOREIGN KEY violation deleting in-use theme, got nil")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("expected FOREIGN KEY error, got: %v", err)
	}

	// The theme row must still exist (RESTRICT, not CASCADE / SET NULL).
	var themeCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM themes WHERE id = ?", "theme-used").Scan(&themeCount); err != nil {
		t.Fatalf("count themes: %v", err)
	}
	if themeCount != 1 {
		t.Errorf("themes row count after rejected delete = %d, want 1", themeCount)
	}

	// And the screen row must still point at it (i.e. RESTRICT did not SET NULL).
	var screenCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM screens WHERE theme_id = ?", "theme-used").Scan(&screenCount); err != nil {
		t.Fatalf("count screens: %v", err)
	}
	if screenCount != 1 {
		t.Errorf("screens referencing the theme = %d, want 1", screenCount)
	}
}

// TestScreensTable_CascadesDeletesToPagesAndWidgets verifies the CASCADE chain
// on the FKs: deleting a screen drops its pages, which in turn drops their
// widget_instances. This is SPEC-006 AC-7 (data layer half).
func TestScreensTable_CascadesDeletesToPagesAndWidgets(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-c", "cascade-theme", 0)
	insertTestScreen(ctx, t, q, "screen-c", "cascade-screen", "theme-c")
	insertTestPage(ctx, t, q, "page-c", "screen-c", "page-one", 1)
	insertTestWidget(ctx, t, q, "widget-c", "page-c", "text", "{}", 1)

	// Delete the top of the chain.
	if _, err := q.DeleteScreen(ctx, "screen-c"); err != nil {
		t.Fatalf("DeleteScreen: %v", err)
	}

	// The page must be gone (CASCADE from screens -> pages).
	var pageCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM pages WHERE id = ?", "page-c").Scan(&pageCount); err != nil {
		t.Fatalf("count pages: %v", err)
	}
	if pageCount != 0 {
		t.Errorf("page row count after screen delete = %d, want 0", pageCount)
	}

	// The widget instance must be gone (CASCADE from pages -> widget_instances).
	var widgetCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM widget_instances WHERE id = ?", "widget-c").Scan(&widgetCount); err != nil {
		t.Fatalf("count widget_instances: %v", err)
	}
	if widgetCount != 0 {
		t.Errorf("widget_instance row count after screen delete = %d, want 0", widgetCount)
	}

	// And the theme survives (it was not the deleted parent).
	var themeCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM themes WHERE id = ?", "theme-c").Scan(&themeCount); err != nil {
		t.Fatalf("count themes: %v", err)
	}
	if themeCount != 1 {
		t.Errorf("theme row count after screen delete = %d, want 1", themeCount)
	}
}

// TestPagesTable_UniquePositionWithinScreen verifies the
// UNIQUE(screen_id, position) index rejects two pages sharing a position in
// the same screen, but allows the same position number across different
// screens (the constraint is scoped to the parent). SPEC-006 AC-15 part 1.
func TestPagesTable_UniquePositionWithinScreen(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-pp", "pp", 0)
	insertTestScreen(ctx, t, q, "screen-A", "A", "theme-pp")
	insertTestScreen(ctx, t, q, "screen-B", "B", "theme-pp")

	// First page at position 1 succeeds.
	insertTestPage(ctx, t, q, "page-A1", "screen-A", "", 1)

	// Second page at position 1 in the same screen MUST fail.
	err := q.CreatePage(ctx, CreatePageParams{
		ID:       "page-A1-dup",
		ScreenID: "screen-A",
		Name:     "",
		Position: 1,
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate (screen_id, position), got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected UNIQUE constraint error, got: %v", err)
	}

	// A page at the same position in a DIFFERENT screen MUST succeed: the
	// unique index is scoped per screen.
	if err := q.CreatePage(ctx, CreatePageParams{
		ID:       "page-B1",
		ScreenID: "screen-B",
		Name:     "",
		Position: 1,
	}); err != nil {
		t.Errorf("position 1 in a different screen should be allowed, got: %v", err)
	}
}

// TestWidgetInstancesTable_UniquePositionWithinPage mirrors the page test:
// (page_id, position) is unique per parent. SPEC-006 AC-15 part 2.
func TestWidgetInstancesTable_UniquePositionWithinPage(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-wp", "wp", 0)
	insertTestScreen(ctx, t, q, "screen-wp", "wp-screen", "theme-wp")
	insertTestPage(ctx, t, q, "page-X", "screen-wp", "", 1)
	insertTestPage(ctx, t, q, "page-Y", "screen-wp", "", 2)

	insertTestWidget(ctx, t, q, "widget-X1", "page-X", "text", "{}", 1)

	// Duplicate (page_id, position) MUST fail.
	err := q.CreateWidgetInstance(ctx, CreateWidgetInstanceParams{
		ID:       "widget-X1-dup",
		PageID:   "page-X",
		Type:     "text",
		Config:   "{}",
		Position: 1,
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate (page_id, position), got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected UNIQUE constraint error, got: %v", err)
	}

	// Same position number on a different page MUST succeed.
	if err := q.CreateWidgetInstance(ctx, CreateWidgetInstanceParams{
		ID:       "widget-Y1",
		PageID:   "page-Y",
		Type:     "text",
		Config:   "{}",
		Position: 1,
	}); err != nil {
		t.Errorf("position 1 on a different page should be allowed, got: %v", err)
	}
}

// TestMaxPagePosition_EmptyReturnsZero verifies the COALESCE(MAX(position), 0)
// in the MaxPagePosition query so callers (TASK-023's CreatePage path) can use
// the result as "max + 1" even when the screen has no pages yet.
func TestMaxPagePosition_EmptyReturnsZero(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-mp", "mp", 0)
	insertTestScreen(ctx, t, q, "screen-empty", "empty", "theme-mp")

	got, err := q.MaxPagePosition(ctx, "screen-empty")
	if err != nil {
		t.Fatalf("MaxPagePosition(empty screen): %v", err)
	}
	if !equalsInt(got, 0) {
		t.Errorf("MaxPagePosition(empty) = %v (%T), want 0", got, got)
	}

	// After inserting a page at position 3 the max becomes 3.
	insertTestPage(ctx, t, q, "page-mp", "screen-empty", "", 3)
	got, err = q.MaxPagePosition(ctx, "screen-empty")
	if err != nil {
		t.Fatalf("MaxPagePosition(populated): %v", err)
	}
	if !equalsInt(got, 3) {
		t.Errorf("MaxPagePosition(populated) = %v (%T), want 3", got, got)
	}
}

// TestMaxWidgetPosition_EmptyReturnsZero mirrors the page test: empty page
// yields 0, populated page yields the actual MAX.
func TestMaxWidgetPosition_EmptyReturnsZero(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-mw", "mw", 0)
	insertTestScreen(ctx, t, q, "screen-mw", "mw", "theme-mw")
	insertTestPage(ctx, t, q, "page-mw", "screen-mw", "", 1)

	got, err := q.MaxWidgetPosition(ctx, "page-mw")
	if err != nil {
		t.Fatalf("MaxWidgetPosition(empty page): %v", err)
	}
	if !equalsInt(got, 0) {
		t.Errorf("MaxWidgetPosition(empty) = %v (%T), want 0", got, got)
	}

	insertTestWidget(ctx, t, q, "widget-mw", "page-mw", "text", "{}", 7)
	got, err = q.MaxWidgetPosition(ctx, "page-mw")
	if err != nil {
		t.Fatalf("MaxWidgetPosition(populated): %v", err)
	}
	if !equalsInt(got, 7) {
		t.Errorf("MaxWidgetPosition(populated) = %v (%T), want 7", got, got)
	}
}

// TestCountScreensUsingTheme_Counts verifies the simple FK-usage count query
// the Theme System will consume to surface "in use" status in the admin UI.
func TestCountScreensUsingTheme_Counts(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-c0", "unused", 0)
	insertTestTheme(ctx, t, q, "theme-c1", "used", 0)
	insertTestScreen(ctx, t, q, "screen-c1", "consumer", "theme-c1")

	got, err := q.CountScreensUsingTheme(ctx, "theme-c0")
	if err != nil {
		t.Fatalf("CountScreensUsingTheme(unused): %v", err)
	}
	if got != 0 {
		t.Errorf("CountScreensUsingTheme(unused) = %d, want 0", got)
	}

	got, err = q.CountScreensUsingTheme(ctx, "theme-c1")
	if err != nil {
		t.Fatalf("CountScreensUsingTheme(used): %v", err)
	}
	if got != 1 {
		t.Errorf("CountScreensUsingTheme(used) = %d, want 1", got)
	}
}

// equalsInt normalises the interface{} returned by sqlc's COALESCE(MAX(...))
// queries (the SQLite driver may return int64 or, depending on driver version,
// a different numeric type) and compares against the expected integer value.
func equalsInt(got interface{}, want int64) bool {
	switch v := got.(type) {
	case int64:
		return v == want
	case int:
		return int64(v) == want
	case float64:
		return int64(v) == want
	default:
		return false
	}
}
