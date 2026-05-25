package screens

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// newTestService builds a screens.Service backed by a fresh in-memory SQLite
// database with all migrations applied and the default theme seeded. It
// returns the service, the raw DB handle (for raw-SQL fixture inserts), and
// the seeded default theme's ID.
func newTestService(t *testing.T) (*Service, *sql.DB, string) {
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
	svc := NewService(sqlDB, themesSvc, widget.NewRegistry())
	return svc, sqlDB, def.ID
}

// insertScreen inserts a screen row directly via SQL, bypassing the service.
func insertScreen(t *testing.T, sqlDB *sql.DB, id, name, themeID string, rotation int) {
	t.Helper()
	_, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO screens (id, name, theme_id, rotation_interval_seconds) VALUES (?, ?, ?, ?)`,
		id, name, themeID, rotation)
	if err != nil {
		t.Fatalf("insert screen %q: %v", name, err)
	}
}

// insertPage inserts a page row directly via SQL.
func insertPage(t *testing.T, sqlDB *sql.DB, id, screenID, name string, position int) {
	t.Helper()
	_, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO pages (id, screen_id, name, position) VALUES (?, ?, ?, ?)`,
		id, screenID, name, position)
	if err != nil {
		t.Fatalf("insert page %q: %v", id, err)
	}
}

// insertWidget inserts a widget_instance row directly via SQL.
func insertWidget(t *testing.T, sqlDB *sql.DB, id, pageID, typ, config string, position int) {
	t.Helper()
	_, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO widget_instances (id, page_id, type, config, position) VALUES (?, ?, ?, ?, ?)`,
		id, pageID, typ, config, position)
	if err != nil {
		t.Fatalf("insert widget %q: %v", id, err)
	}
}

func TestCreateScreen_HappyPath(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	got, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 45})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	if got.ID == "" {
		t.Error("returned screen has empty ID")
	}
	if got.Name != "kitchen" {
		t.Errorf("Name = %q, want kitchen", got.Name)
	}
	if got.ThemeID != themeID {
		t.Errorf("ThemeID = %q, want %q", got.ThemeID, themeID)
	}
	if got.RotationIntervalSeconds != 45 {
		t.Errorf("RotationIntervalSeconds = %d, want 45", got.RotationIntervalSeconds)
	}

	// Confirm the row really landed in the database.
	var (
		name     string
		theme    string
		rotation int
	)
	row := sqlDB.QueryRowContext(ctx,
		`SELECT name, theme_id, rotation_interval_seconds FROM screens WHERE id = ?`, got.ID)
	if err := row.Scan(&name, &theme, &rotation); err != nil {
		t.Fatalf("scan persisted row: %v", err)
	}
	if name != "kitchen" || theme != themeID || rotation != 45 {
		t.Errorf("persisted row = (%q, %q, %d), want (kitchen, %q, 45)", name, theme, rotation, themeID)
	}
}

func TestCreateScreen_RejectsValidationErrors(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	longName := ""
	for i := 0; i < 65; i++ {
		longName += "a"
	}

	tests := []struct {
		name string
		in   ScreenInput
	}{
		{"empty name", ScreenInput{Name: "", ThemeID: themeID, RotationIntervalSeconds: 30}},
		{"whitespace name", ScreenInput{Name: "   ", ThemeID: themeID, RotationIntervalSeconds: 30}},
		{"name with angle bracket", ScreenInput{Name: "screen<x", ThemeID: themeID, RotationIntervalSeconds: 30}},
		{"name 65 chars", ScreenInput{Name: longName, ThemeID: themeID, RotationIntervalSeconds: 30}},
		{"empty theme_id", ScreenInput{Name: "kitchen", ThemeID: "", RotationIntervalSeconds: 30}},
		{"rotation 4", ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 4}},
		{"rotation 3601", ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 3601}},
		{"rotation 0", ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 0}},
		{"rotation -1", ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateScreen(ctx, tt.in)
			if !IsValidationError(err) {
				t.Fatalf("CreateScreen(%+v) returned %v, want *ValidationError", tt.in, err)
			}
		})
	}
}

func TestCreateScreen_RejectsUnknownThemeID(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	_, err := svc.CreateScreen(context.Background(), ScreenInput{
		Name: "kitchen", ThemeID: "does-not-exist", RotationIntervalSeconds: 30,
	})
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("CreateScreen returned %v, want ErrThemeNotFound", err)
	}
}

func TestCreateScreen_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	in := ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30}
	if _, err := svc.CreateScreen(ctx, in); err != nil {
		t.Fatalf("first CreateScreen: %v", err)
	}
	_, err := svc.CreateScreen(ctx, in)
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("second CreateScreen returned %v, want ErrDuplicateName", err)
	}
}

func TestGetScreenByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	_, err := svc.GetScreenByID(context.Background(), "no-such-id")
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("GetScreenByID returned %v, want ErrScreenNotFound", err)
	}
}

func TestListScreens_ReturnsPageCountAndThemeName(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "screen-1", "kitchen", themeID, 30)
	insertPage(t, sqlDB, "page-1", "screen-1", "a", 1)
	insertPage(t, sqlDB, "page-2", "screen-1", "b", 2)

	got, err := svc.ListScreens(ctx)
	if err != nil {
		t.Fatalf("ListScreens: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListScreens returned %d rows, want 1", len(got))
	}
	if got[0].PageCount != 2 {
		t.Errorf("PageCount = %d, want 2", got[0].PageCount)
	}
	if got[0].ThemeName != "default" {
		t.Errorf("ThemeName = %q, want default", got[0].ThemeName)
	}
	if got[0].Name != "kitchen" {
		t.Errorf("Name = %q, want kitchen", got[0].Name)
	}
}

func TestListScreens_EmptyReturnsNonNil(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	got, err := svc.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("ListScreens: %v", err)
	}
	if got == nil {
		t.Error("ListScreens returned nil slice on empty table; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("ListScreens returned %d rows, want 0", len(got))
	}
}

func TestUpdateScreen_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	// SQLite datetime('now') resolves to whole seconds; sleep past the boundary.
	time.Sleep(1100 * time.Millisecond)

	updated, err := svc.UpdateScreen(ctx, created.ID, ScreenInput{
		Name: "living-room", ThemeID: themeID, RotationIntervalSeconds: 90,
	})
	if err != nil {
		t.Fatalf("UpdateScreen: %v", err)
	}
	if updated.Name != "living-room" {
		t.Errorf("Name = %q, want living-room", updated.Name)
	}
	if updated.RotationIntervalSeconds != 90 {
		t.Errorf("RotationIntervalSeconds = %d, want 90", updated.RotationIntervalSeconds)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt %v not after original %v", updated.UpdatedAt, created.UpdatedAt)
	}

	fetched, err := svc.GetScreenByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetScreenByID: %v", err)
	}
	if fetched.Name != "living-room" || fetched.RotationIntervalSeconds != 90 {
		t.Errorf("persisted screen = (%q, %d), want (living-room, 90)", fetched.Name, fetched.RotationIntervalSeconds)
	}
}

func TestUpdateScreen_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)

	_, err := svc.UpdateScreen(context.Background(), "no-such-id", ScreenInput{
		Name: "ghost", ThemeID: themeID, RotationIntervalSeconds: 30,
	})
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("UpdateScreen returned %v, want ErrScreenNotFound", err)
	}
}

func TestUpdateScreen_RejectsValidation(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	_, err = svc.UpdateScreen(ctx, created.ID, ScreenInput{Name: "", ThemeID: themeID, RotationIntervalSeconds: 30})
	if !IsValidationError(err) {
		t.Errorf("UpdateScreen returned %v, want *ValidationError", err)
	}
}

func TestUpdateScreen_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateScreen(ctx, ScreenInput{Name: "screen-a", ThemeID: themeID, RotationIntervalSeconds: 30}); err != nil {
		t.Fatalf("CreateScreen A: %v", err)
	}
	b, err := svc.CreateScreen(ctx, ScreenInput{Name: "screen-b", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen B: %v", err)
	}

	_, err = svc.UpdateScreen(ctx, b.ID, ScreenInput{Name: "screen-a", ThemeID: themeID, RotationIntervalSeconds: 30})
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("UpdateScreen returned %v, want ErrDuplicateName", err)
	}
}

func TestDeleteScreen_HappyPath(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "screen-1", "kitchen", themeID, 30)
	insertPage(t, sqlDB, "page-1", "screen-1", "a", 1)
	insertWidget(t, sqlDB, "widget-1", "page-1", "clock", "{}", 1)

	if err := svc.DeleteScreen(ctx, "screen-1"); err != nil {
		t.Fatalf("DeleteScreen: %v", err)
	}

	for _, tc := range []struct {
		table, id string
	}{
		{"screens", "screen-1"},
		{"pages", "page-1"},
		{"widget_instances", "widget-1"},
	} {
		var count int
		row := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tc.table+" WHERE id = ?", tc.id)
		if err := row.Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.table, err)
		}
		if count != 0 {
			t.Errorf("%s row %q still present after DeleteScreen (CASCADE failed)", tc.table, tc.id)
		}
	}
}

func TestDeleteScreen_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	err := svc.DeleteScreen(context.Background(), "no-such-id")
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("DeleteScreen returned %v, want ErrScreenNotFound", err)
	}
}

func TestGetScreenFull_AggregatesPagesAndWidgets(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "screen-1", "kitchen", themeID, 30)
	insertPage(t, sqlDB, "page-a", "screen-1", "first", 1)
	insertPage(t, sqlDB, "page-b", "screen-1", "second", 2)
	insertWidget(t, sqlDB, "w-a1", "page-a", "clock", "{}", 1)
	insertWidget(t, sqlDB, "w-b1", "page-b", "weather", `{"city":"x"}`, 1)
	insertWidget(t, sqlDB, "w-b2", "page-b", "calendar", "{}", 2)

	full, err := svc.GetScreenFull(ctx, "screen-1")
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if full.Screen.ID != "screen-1" {
		t.Errorf("Screen.ID = %q, want screen-1", full.Screen.ID)
	}
	if full.Theme.Name != "default" {
		t.Errorf("Theme.Name = %q, want default", full.Theme.Name)
	}
	if len(full.Pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(full.Pages))
	}
	if full.Pages[0].Page.ID != "page-a" || full.Pages[1].Page.ID != "page-b" {
		t.Errorf("pages out of position order: %q, %q", full.Pages[0].Page.ID, full.Pages[1].Page.ID)
	}
	if len(full.Pages[0].Widgets) != 1 {
		t.Errorf("page-a got %d widgets, want 1", len(full.Pages[0].Widgets))
	}
	if len(full.Pages[1].Widgets) != 2 {
		t.Fatalf("page-b got %d widgets, want 2", len(full.Pages[1].Widgets))
	}
	if full.Pages[1].Widgets[0].ID != "w-b1" || full.Pages[1].Widgets[1].ID != "w-b2" {
		t.Errorf("page-b widgets out of position order: %q, %q",
			full.Pages[1].Widgets[0].ID, full.Pages[1].Widgets[1].ID)
	}
	if string(full.Pages[1].Widgets[0].Config) != `{"city":"x"}` {
		t.Errorf("widget config = %q, want %q", full.Pages[1].Widgets[0].Config, `{"city":"x"}`)
	}
}

func TestGetScreenFull_NoPages(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	insertScreen(t, sqlDB, "screen-1", "kitchen", themeID, 30)

	full, err := svc.GetScreenFull(ctx, "screen-1")
	if err != nil {
		t.Fatalf("GetScreenFull: %v", err)
	}
	if full.Pages == nil {
		t.Error("Pages is nil; want non-nil empty slice")
	}
	if len(full.Pages) != 0 {
		t.Errorf("Pages has %d entries, want 0", len(full.Pages))
	}
}

func TestGetScreenFull_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	_, err := svc.GetScreenFull(context.Background(), "no-such-id")
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("GetScreenFull returned %v, want ErrScreenNotFound", err)
	}
}
