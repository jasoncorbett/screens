package db

import (
	"context"
	"database/sql"
	"errors"
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
// the result as "max + 1" even when the screen has no pages yet. The CAST in
// the query forces sqlc to generate an int64 return type rather than the
// awkward interface{} default for COALESCE expressions.
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
	if got != 0 {
		t.Errorf("MaxPagePosition(empty) = %d, want 0", got)
	}

	// After inserting a page at position 3 the max becomes 3.
	insertTestPage(ctx, t, q, "page-mp", "screen-empty", "", 3)
	got, err = q.MaxPagePosition(ctx, "screen-empty")
	if err != nil {
		t.Fatalf("MaxPagePosition(populated): %v", err)
	}
	if got != 3 {
		t.Errorf("MaxPagePosition(populated) = %d, want 3", got)
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
	if got != 0 {
		t.Errorf("MaxWidgetPosition(empty) = %d, want 0", got)
	}

	insertTestWidget(ctx, t, q, "widget-mw", "page-mw", "text", "{}", 7)
	got, err = q.MaxWidgetPosition(ctx, "page-mw")
	if err != nil {
		t.Fatalf("MaxWidgetPosition(populated): %v", err)
	}
	if got != 7 {
		t.Errorf("MaxWidgetPosition(populated) = %d, want 7", got)
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

// TestPagesTable_CascadesDeleteToWidgets verifies the second link of the
// CASCADE chain directly: a DeletePage drops the page's widget instances.
// The combined chain (screen -> page -> widget) is covered above; this isolates
// the pages -> widget_instances FK so a regression in only that link surfaces
// here rather than only via the longer chain test.
func TestPagesTable_CascadesDeleteToWidgets(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-pcw", "pcw", 0)
	insertTestScreen(ctx, t, q, "screen-pcw", "pcw", "theme-pcw")
	insertTestPage(ctx, t, q, "page-pcw", "screen-pcw", "p", 1)
	insertTestWidget(ctx, t, q, "widget-pcw-1", "page-pcw", "text", "{}", 1)
	insertTestWidget(ctx, t, q, "widget-pcw-2", "page-pcw", "text", "{\"a\":1}", 2)

	if _, err := q.DeletePage(ctx, DeletePageParams{ID: "page-pcw", ScreenID: "screen-pcw"}); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	var widgets int
	if err := database.QueryRow("SELECT COUNT(*) FROM widget_instances WHERE page_id = ?", "page-pcw").Scan(&widgets); err != nil {
		t.Fatalf("count widgets: %v", err)
	}
	if widgets != 0 {
		t.Errorf("widgets after page delete = %d, want 0 (CASCADE failed)", widgets)
	}
}

// TestReorderSwapPattern_NegativePositionTrick verifies that the architecturally
// prescribed reorder-swap pattern works as documented: park the target at a
// negative position, move the neighbor into its slot, then move the target to
// the neighbor's old slot. The negative position step is the only way to keep
// SQLite happy with the UNIQUE(parent_id, position) index across the swap,
// because SQLite checks unique constraints per-statement (not at COMMIT). If
// the schema gains a CHECK(position > 0) someday this test will fail loudly
// and the reorder strategy will need to change.
func TestReorderSwapPattern_NegativePositionTrick(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-rs", "rs", 0)
	insertTestScreen(ctx, t, q, "screen-rs", "rs", "theme-rs")
	insertTestPage(ctx, t, q, "page-rs-1", "screen-rs", "one", 1)
	insertTestPage(ctx, t, q, "page-rs-2", "screen-rs", "two", 2)

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	qtx := q.WithTx(tx)

	// Park page-rs-1 at -1 (frees position 1).
	if err := qtx.SetPagePosition(ctx, SetPagePositionParams{Position: -1, ID: "page-rs-1", ScreenID: "screen-rs"}); err != nil {
		t.Fatalf("SetPagePosition park: %v", err)
	}
	// Move page-rs-2 into position 1.
	if err := qtx.SetPagePosition(ctx, SetPagePositionParams{Position: 1, ID: "page-rs-2", ScreenID: "screen-rs"}); err != nil {
		t.Fatalf("SetPagePosition neighbor up: %v", err)
	}
	// Restore page-rs-1 to position 2.
	if err := qtx.SetPagePosition(ctx, SetPagePositionParams{Position: 2, ID: "page-rs-1", ScreenID: "screen-rs"}); err != nil {
		t.Fatalf("SetPagePosition target down: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	pages, err := q.ListPagesByScreen(ctx, "screen-rs")
	if err != nil {
		t.Fatalf("ListPagesByScreen: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("ListPagesByScreen len = %d, want 2", len(pages))
	}
	// ORDER BY position; after swap the order is page-rs-2 (pos 1) then page-rs-1 (pos 2).
	if pages[0].ID != "page-rs-2" || pages[0].Position != 1 {
		t.Errorf("after swap, pages[0] = (id=%s, pos=%d), want (page-rs-2, 1)", pages[0].ID, pages[0].Position)
	}
	if pages[1].ID != "page-rs-1" || pages[1].Position != 2 {
		t.Errorf("after swap, pages[1] = (id=%s, pos=%d), want (page-rs-1, 2)", pages[1].ID, pages[1].Position)
	}
}

// TestSQLMetacharactersInNames_RoundTrip confirms that parameter binding in the
// sqlc-generated code stores SQL-meaningful sequences (quote, semicolon, DROP
// TABLE) as literal text in screens, pages, and widget_instances. If any layer
// switched to string concatenation this test would either drop a table or
// corrupt the round-trip value.
func TestSQLMetacharactersInNames_RoundTrip(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	evil := "ev'il); DROP TABLE screens;--"
	insertTestTheme(ctx, t, q, "theme-sql", "th", 0)

	insertTestScreen(ctx, t, q, "screen-sql", evil, "theme-sql")
	insertTestPage(ctx, t, q, "page-sql", "screen-sql", evil, 1)
	insertTestWidget(ctx, t, q, "widget-sql", "page-sql", evil, evil, 1)

	gotScreen, err := q.GetScreenByName(ctx, evil)
	if err != nil {
		t.Fatalf("GetScreenByName(metachar): %v", err)
	}
	if gotScreen.Name != evil {
		t.Errorf("screen name round-trip: got %q, want %q", gotScreen.Name, evil)
	}

	gotPage, err := q.GetPageByID(ctx, GetPageByIDParams{ID: "page-sql", ScreenID: "screen-sql"})
	if err != nil {
		t.Fatalf("GetPageByID(metachar): %v", err)
	}
	if gotPage.Name != evil {
		t.Errorf("page name round-trip: got %q, want %q", gotPage.Name, evil)
	}

	gotWidget, err := q.GetWidgetInstanceByID(ctx, GetWidgetInstanceByIDParams{ID: "widget-sql", PageID: "page-sql"})
	if err != nil {
		t.Fatalf("GetWidgetInstanceByID(metachar): %v", err)
	}
	if gotWidget.Type != evil || gotWidget.Config != evil {
		t.Errorf("widget type/config round-trip: type=%q config=%q, want both %q", gotWidget.Type, gotWidget.Config, evil)
	}

	// Sanity: the screens table still exists (no injection executed).
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM screens").Scan(&n); err != nil {
		t.Fatalf("count screens after metachar inserts: %v -- table may have been dropped", err)
	}
}

// TestUnicodeAndLongNames_RoundTrip verifies that names containing multi-byte
// UTF-8 (Japanese, emoji) and very long strings round-trip through screens,
// pages, and widget_instances without loss. The schema columns are TEXT so
// this should hold; the test pins the behaviour so a future migration that
// added a NUMERIC affinity or a length limit would surface here.
func TestUnicodeAndLongNames_RoundTrip(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	unicodeName := "キッチン-tableau-🍳"
	longName := strings.Repeat("X", 1024*1024) // 1 MiB

	insertTestTheme(ctx, t, q, "theme-u", "u", 0)
	insertTestScreen(ctx, t, q, "screen-u", unicodeName, "theme-u")
	insertTestPage(ctx, t, q, "page-u", "screen-u", unicodeName, 1)
	insertTestWidget(ctx, t, q, "widget-u", "page-u", "text", longName, 1)

	gotScreen, err := q.GetScreenByID(ctx, "screen-u")
	if err != nil {
		t.Fatalf("GetScreenByID(unicode): %v", err)
	}
	if gotScreen.Name != unicodeName {
		t.Errorf("unicode screen name round-trip: got %q, want %q", gotScreen.Name, unicodeName)
	}

	gotPage, err := q.GetPageByID(ctx, GetPageByIDParams{ID: "page-u", ScreenID: "screen-u"})
	if err != nil {
		t.Fatalf("GetPageByID(unicode): %v", err)
	}
	if gotPage.Name != unicodeName {
		t.Errorf("unicode page name round-trip: got %q, want %q", gotPage.Name, unicodeName)
	}

	gotWidget, err := q.GetWidgetInstanceByID(ctx, GetWidgetInstanceByIDParams{ID: "widget-u", PageID: "page-u"})
	if err != nil {
		t.Fatalf("GetWidgetInstanceByID(long config): %v", err)
	}
	if len(gotWidget.Config) != len(longName) {
		t.Errorf("long config round-trip length: got %d, want %d", len(gotWidget.Config), len(longName))
	}
}

// TestEmptyScreenName_AcceptedAtSchema documents the architectural decision
// that input validation (1-64 chars, regex) lives in the service layer rather
// than the schema. The UNIQUE constraint is enforced (so the schema rejects a
// second empty-named screen) but an empty name is otherwise valid SQL. If a
// future migration adds a CHECK constraint, this test will fail and the
// schema layer must be updated coherently with the service layer.
func TestEmptyScreenName_AcceptedAtSchema(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-e", "e", 0)
	if err := q.CreateScreen(ctx, CreateScreenParams{
		ID: "screen-empty-name", Name: "", ThemeID: "theme-e", RotationIntervalSeconds: 30,
	}); err != nil {
		t.Fatalf("schema rejected empty name; if this is intentional the service-layer test must be updated: %v", err)
	}

	// A second empty-name insert must be rejected by the UNIQUE constraint.
	err := q.CreateScreen(ctx, CreateScreenParams{
		ID: "screen-empty-name-2", Name: "", ThemeID: "theme-e", RotationIntervalSeconds: 30,
	})
	if err == nil {
		t.Fatal("two screens with the same empty name should violate UNIQUE; got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected UNIQUE constraint error, got: %v", err)
	}
}

// TestScreens_DuplicateName_Rejected verifies the UNIQUE constraint on
// screens.name surfaces as a UNIQUE-constraint error. The Theme System uses
// the same pattern (`themes.name UNIQUE`) and the Screens service in
// TASK-022 will translate this error into ErrDuplicateName.
func TestScreens_DuplicateName_Rejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-dup", "dup", 0)
	insertTestScreen(ctx, t, q, "screen-dup-1", "shared-name", "theme-dup")

	err := q.CreateScreen(ctx, CreateScreenParams{
		ID:                      "screen-dup-2",
		Name:                    "shared-name",
		ThemeID:                 "theme-dup",
		RotationIntervalSeconds: 30,
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate screen name, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("expected UNIQUE error, got: %v", err)
	}
}

// TestRotationIntervalDefault verifies the schema's DEFAULT 30 on
// screens.rotation_interval_seconds. The spec says this is the documented
// default for new screens. If a future migration changes the default to a
// different value, the service-layer constants must change in lockstep.
func TestRotationIntervalDefault(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-rid", "rid", 0)
	// INSERT without rotation_interval_seconds: relies on DEFAULT 30.
	if _, err := database.ExecContext(ctx,
		"INSERT INTO screens (id, name, theme_id) VALUES (?, ?, ?)",
		"screen-rid", "rid-screen", "theme-rid",
	); err != nil {
		t.Fatalf("INSERT relying on rotation default: %v", err)
	}

	got, err := q.GetScreenByID(ctx, "screen-rid")
	if err != nil {
		t.Fatalf("GetScreenByID: %v", err)
	}
	if got.RotationIntervalSeconds != 30 {
		t.Errorf("default rotation_interval_seconds = %d, want 30", got.RotationIntervalSeconds)
	}
}

// TestNullPosition_Rejected verifies that NOT NULL on pages.position and
// widget_instances.position is in force. The reorder code path relies on
// position being a real integer (negative or positive); NULL would break the
// swap math silently.
func TestNullPosition_Rejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-np", "np", 0)
	insertTestScreen(ctx, t, q, "screen-np", "np", "theme-np")

	_, err := database.ExecContext(ctx,
		"INSERT INTO pages (id, screen_id, name, position) VALUES (?, ?, ?, NULL)",
		"page-np", "screen-np", "")
	if err == nil {
		t.Fatal("expected NOT NULL violation for pages.position=NULL, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "NOT NULL") {
		t.Errorf("expected NOT NULL error for pages.position, got: %v", err)
	}

	insertTestPage(ctx, t, q, "page-np-ok", "screen-np", "", 1)
	_, err = database.ExecContext(ctx,
		"INSERT INTO widget_instances (id, page_id, type, config, position) VALUES (?, ?, ?, ?, NULL)",
		"widget-np", "page-np-ok", "text", "{}")
	if err == nil {
		t.Fatal("expected NOT NULL violation for widget_instances.position=NULL, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "NOT NULL") {
		t.Errorf("expected NOT NULL error for widget_instances.position, got: %v", err)
	}
}

// TestListPagesByScreen_OrderedByPosition verifies the ORDER BY position clause
// is honoured. The service layer (TASK-022) and the renderer downstream rely
// on this ordering; a regression would surface as widgets rendering in
// arbitrary order.
func TestListPagesByScreen_OrderedByPosition(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-lp", "lp", 0)
	insertTestScreen(ctx, t, q, "screen-lp", "lp", "theme-lp")

	// Insert in non-position order: pos 3, 1, 5, 2 (with gaps).
	insertTestPage(ctx, t, q, "page-lp-c", "screen-lp", "third", 3)
	insertTestPage(ctx, t, q, "page-lp-a", "screen-lp", "first", 1)
	insertTestPage(ctx, t, q, "page-lp-e", "screen-lp", "fifth", 5)
	insertTestPage(ctx, t, q, "page-lp-b", "screen-lp", "second", 2)

	pages, err := q.ListPagesByScreen(ctx, "screen-lp")
	if err != nil {
		t.Fatalf("ListPagesByScreen: %v", err)
	}

	wantOrder := []int64{1, 2, 3, 5}
	if len(pages) != len(wantOrder) {
		t.Fatalf("ListPagesByScreen len = %d, want %d", len(pages), len(wantOrder))
	}
	for i, p := range pages {
		if p.Position != wantOrder[i] {
			t.Errorf("pages[%d].Position = %d, want %d", i, p.Position, wantOrder[i])
		}
	}
}

// TestListWidgetInstancesByPage_OrderedByPosition mirrors the page test for the
// widget list. The renderer iterates widgets in position order; this guards
// against an accidental ORDER BY removal.
func TestListWidgetInstancesByPage_OrderedByPosition(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-lw", "lw", 0)
	insertTestScreen(ctx, t, q, "screen-lw", "lw", "theme-lw")
	insertTestPage(ctx, t, q, "page-lw", "screen-lw", "", 1)

	insertTestWidget(ctx, t, q, "w-3", "page-lw", "text", "{}", 3)
	insertTestWidget(ctx, t, q, "w-1", "page-lw", "text", "{}", 1)
	insertTestWidget(ctx, t, q, "w-2", "page-lw", "text", "{}", 2)

	widgets, err := q.ListWidgetInstancesByPage(ctx, "page-lw")
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPage: %v", err)
	}
	wantIDs := []string{"w-1", "w-2", "w-3"}
	if len(widgets) != len(wantIDs) {
		t.Fatalf("len = %d, want %d", len(widgets), len(wantIDs))
	}
	for i, w := range widgets {
		if w.ID != wantIDs[i] {
			t.Errorf("widgets[%d].ID = %q, want %q (ORDER BY position lost?)", i, w.ID, wantIDs[i])
		}
	}
}

// TestListWidgetInstancesByPageIDs_EmptySlice verifies the sqlc IN-clause
// generator handles an empty page_ids slice by producing a WHERE that matches
// no rows (rather than crashing or returning all widgets). GetScreenFull may
// pass an empty pageIDs slice when the screen has no pages; the contract is
// that no widgets come back, with no error.
func TestListWidgetInstancesByPageIDs_EmptySlice(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-es", "es", 0)
	insertTestScreen(ctx, t, q, "screen-es", "es", "theme-es")
	insertTestPage(ctx, t, q, "page-es", "screen-es", "", 1)
	insertTestWidget(ctx, t, q, "widget-es", "page-es", "text", "{}", 1)

	got, err := q.ListWidgetInstancesByPageIDs(ctx, nil)
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPageIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListWidgetInstancesByPageIDs(nil) returned %d rows, want 0", len(got))
	}

	got, err = q.ListWidgetInstancesByPageIDs(ctx, []string{})
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPageIDs(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListWidgetInstancesByPageIDs(empty) returned %d rows, want 0", len(got))
	}
}

// TestListWidgetInstancesByPageIDs_MultiPage verifies that the batch fetch
// returns widgets across multiple pages, grouped by page_id, ordered by
// position within each page. This is the GetScreenFull query plan in
// TASK-022's renderer hot path; a regression would cause N+1 fallback at
// best, or scrambled widget ordering at worst.
func TestListWidgetInstancesByPageIDs_MultiPage(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-mp", "mp", 0)
	insertTestScreen(ctx, t, q, "screen-mp", "mp", "theme-mp")
	insertTestPage(ctx, t, q, "page-A", "screen-mp", "A", 1)
	insertTestPage(ctx, t, q, "page-B", "screen-mp", "B", 2)

	insertTestWidget(ctx, t, q, "w-A-2", "page-A", "text", "{}", 2)
	insertTestWidget(ctx, t, q, "w-A-1", "page-A", "text", "{}", 1)
	insertTestWidget(ctx, t, q, "w-B-1", "page-B", "text", "{}", 1)

	got, err := q.ListWidgetInstancesByPageIDs(ctx, []string{"page-A", "page-B"})
	if err != nil {
		t.Fatalf("ListWidgetInstancesByPageIDs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d widgets, want 3", len(got))
	}

	// Group by page_id then check ordering within each group.
	byPage := map[string][]WidgetInstance{}
	for _, w := range got {
		byPage[w.PageID] = append(byPage[w.PageID], w)
	}
	if len(byPage["page-A"]) != 2 {
		t.Errorf("page-A widget count = %d, want 2", len(byPage["page-A"]))
	}
	if len(byPage["page-B"]) != 1 {
		t.Errorf("page-B widget count = %d, want 1", len(byPage["page-B"]))
	}
	if len(byPage["page-A"]) >= 2 && byPage["page-A"][0].ID != "w-A-1" {
		t.Errorf("page-A widgets not ordered by position: first=%q want w-A-1", byPage["page-A"][0].ID)
	}
}

// TestDeletePage_MismatchedScreenID verifies the defence-in-depth WHERE clause
// on DeletePage (id = ? AND screen_id = ?). A handler that misroutes the
// screen / page IDs cannot accidentally delete a page belonging to a different
// screen; the DELETE just affects zero rows.
func TestDeletePage_MismatchedScreenID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-md", "md", 0)
	insertTestScreen(ctx, t, q, "screen-A", "md-A", "theme-md")
	insertTestScreen(ctx, t, q, "screen-B", "md-B", "theme-md")
	insertTestPage(ctx, t, q, "page-A", "screen-A", "", 1)

	// Try to delete page-A while passing screen-B; defence-in-depth must reject.
	res, err := q.DeletePage(ctx, DeletePageParams{ID: "page-A", ScreenID: "screen-B"})
	if err != nil {
		t.Fatalf("DeletePage(mismatch): %v", err)
	}
	rows, _ := res.RowsAffected()
	if rows != 0 {
		t.Errorf("DeletePage(mismatched screen) RowsAffected = %d, want 0", rows)
	}

	// Page must still exist.
	if _, err := q.GetPageByID(ctx, GetPageByIDParams{ID: "page-A", ScreenID: "screen-A"}); err != nil {
		t.Errorf("page-A unexpectedly deleted: %v", err)
	}
}

// TestGetPageByID_MismatchedScreenID verifies the WHERE includes both id and
// screen_id, so a page lookup with the wrong screen returns sql.ErrNoRows
// rather than the page row.
func TestGetPageByID_MismatchedScreenID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-gm", "gm", 0)
	insertTestScreen(ctx, t, q, "screen-A", "gm-A", "theme-gm")
	insertTestScreen(ctx, t, q, "screen-B", "gm-B", "theme-gm")
	insertTestPage(ctx, t, q, "page-A", "screen-A", "", 1)

	_, err := q.GetPageByID(ctx, GetPageByIDParams{ID: "page-A", ScreenID: "screen-B"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetPageByID(mismatch) error = %v, want sql.ErrNoRows", err)
	}
}

// TestGetWidgetInstanceByID_MismatchedPageID is the widget equivalent: a query
// for a widget that exists but on a different page must yield no rows.
func TestGetWidgetInstanceByID_MismatchedPageID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-wm", "wm", 0)
	insertTestScreen(ctx, t, q, "screen-wm", "wm", "theme-wm")
	insertTestPage(ctx, t, q, "page-X", "screen-wm", "", 1)
	insertTestPage(ctx, t, q, "page-Y", "screen-wm", "", 2)
	insertTestWidget(ctx, t, q, "widget-X", "page-X", "text", "{}", 1)

	_, err := q.GetWidgetInstanceByID(ctx, GetWidgetInstanceByIDParams{ID: "widget-X", PageID: "page-Y"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetWidgetInstanceByID(mismatch) error = %v, want sql.ErrNoRows", err)
	}
}

// TestGetPageNeighbor_NoNeighbor verifies the sentinel sql.ErrNoRows result
// from GetPageNeighbor when no row exists at the requested (screen_id,
// position). The reorder logic uses this signal to detect "already at the
// top/bottom".
func TestGetPageNeighbor_NoNeighbor(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-gn", "gn", 0)
	insertTestScreen(ctx, t, q, "screen-gn", "gn", "theme-gn")
	insertTestPage(ctx, t, q, "only-page", "screen-gn", "", 1)

	// Asking for position 0 (above the only page) yields no rows.
	_, err := q.GetPageNeighbor(ctx, GetPageNeighborParams{ScreenID: "screen-gn", Position: 0})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetPageNeighbor(pos 0) = %v, want sql.ErrNoRows", err)
	}
	// Asking for position 2 (below the only page) yields no rows.
	_, err = q.GetPageNeighbor(ctx, GetPageNeighborParams{ScreenID: "screen-gn", Position: 2})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetPageNeighbor(pos 2) = %v, want sql.ErrNoRows", err)
	}
}

// TestCountScreensUsingTheme_Multiple verifies the count includes every screen
// referencing the theme. This is the query a future themes-admin "in use"
// indicator will consume.
func TestCountScreensUsingTheme_Multiple(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-many", "many", 0)
	insertTestScreen(ctx, t, q, "screen-1", "one", "theme-many")
	insertTestScreen(ctx, t, q, "screen-2", "two", "theme-many")
	insertTestScreen(ctx, t, q, "screen-3", "three", "theme-many")

	got, err := q.CountScreensUsingTheme(ctx, "theme-many")
	if err != nil {
		t.Fatalf("CountScreensUsingTheme: %v", err)
	}
	if got != 3 {
		t.Errorf("CountScreensUsingTheme = %d, want 3", got)
	}
}

// TestListScreenSummaries_JoinAndCount verifies the summary query joins themes
// for the theme name and uses a subquery for the page count. A regression in
// the JOIN (wrong column, missing themes row) would surface here, as would a
// regression in the page_count subquery.
func TestListScreenSummaries_JoinAndCount(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-ls", "Theme One", 0)
	insertTestScreen(ctx, t, q, "screen-ls", "Living Room", "theme-ls")
	insertTestPage(ctx, t, q, "page-ls-1", "screen-ls", "", 1)
	insertTestPage(ctx, t, q, "page-ls-2", "screen-ls", "", 2)

	summaries, err := q.ListScreenSummaries(ctx)
	if err != nil {
		t.Fatalf("ListScreenSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len = %d, want 1", len(summaries))
	}
	s := summaries[0]
	if s.ThemeName != "Theme One" {
		t.Errorf("ThemeName = %q, want %q", s.ThemeName, "Theme One")
	}
	if s.PageCount != 2 {
		t.Errorf("PageCount = %d, want 2", s.PageCount)
	}
}
