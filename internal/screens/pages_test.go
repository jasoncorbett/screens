package screens

import (
	"context"
	"errors"
	"testing"
)

func TestCreatePage_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	first, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage 1: %v", err)
	}
	if first.Position != 1 {
		t.Errorf("first page Position = %d, want 1", first.Position)
	}
	if first.Name != "clock" {
		t.Errorf("first page Name = %q, want clock", first.Name)
	}
	if first.ScreenID != screen.ID {
		t.Errorf("first page ScreenID = %q, want %q", first.ScreenID, screen.ID)
	}
	if first.ID == "" {
		t.Error("first page has empty ID")
	}

	second, err := svc.CreatePage(ctx, screen.ID, "weather")
	if err != nil {
		t.Fatalf("CreatePage 2: %v", err)
	}
	if second.Position != 2 {
		t.Errorf("second page Position = %d, want 2", second.Position)
	}
}

func TestCreatePage_EmptyName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	page, err := svc.CreatePage(ctx, screen.ID, "")
	if err != nil {
		t.Fatalf("CreatePage with empty name: %v", err)
	}
	if page.Position != 1 {
		t.Errorf("Position = %d, want 1", page.Position)
	}
	if page.Name != "" {
		t.Errorf("Name = %q, want empty", page.Name)
	}
}

func TestCreatePage_InvalidName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	_, err = svc.CreatePage(ctx, screen.ID, "page<script>")
	if !IsValidationError(err) {
		t.Errorf("CreatePage with invalid name returned %v, want *ValidationError", err)
	}
}

func TestCreatePage_UnknownScreen(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	_, err := svc.CreatePage(context.Background(), "nonexistent", "x")
	if !errors.Is(err, ErrScreenNotFound) {
		t.Errorf("CreatePage returned %v, want ErrScreenNotFound", err)
	}
}

func TestGetPageByID_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	created, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	got, err := svc.GetPageByID(ctx, screen.ID, created.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}
	if got.ID != created.ID || got.Name != "clock" || got.Position != 1 || got.ScreenID != screen.ID {
		t.Errorf("got %+v, want id=%q name=clock position=1 screenID=%q",
			got, created.ID, screen.ID)
	}
}

func TestGetPageByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	_, err = svc.GetPageByID(ctx, screen.ID, "no-such-page")
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("GetPageByID returned %v, want ErrPageNotFound", err)
	}
}

func TestGetPageByID_WrongScreen(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	a, err := svc.CreateScreen(ctx, ScreenInput{Name: "screen-a", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen A: %v", err)
	}
	b, err := svc.CreateScreen(ctx, ScreenInput{Name: "screen-b", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen B: %v", err)
	}
	page, err := svc.CreatePage(ctx, a.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	_, err = svc.GetPageByID(ctx, b.ID, page.ID)
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("GetPageByID with wrong screen returned %v, want ErrPageNotFound", err)
	}
}

func TestUpdatePage_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	page, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	updated, err := svc.UpdatePage(ctx, screen.ID, page.ID, "weather")
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if updated.Name != "weather" {
		t.Errorf("Name = %q, want weather", updated.Name)
	}

	fetched, err := svc.GetPageByID(ctx, screen.ID, page.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}
	if fetched.Name != "weather" {
		t.Errorf("persisted Name = %q, want weather", fetched.Name)
	}
}

func TestUpdatePage_InvalidName(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	page, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	_, err = svc.UpdatePage(ctx, screen.ID, page.ID, "name<script>")
	if !IsValidationError(err) {
		t.Errorf("UpdatePage with invalid name returned %v, want *ValidationError", err)
	}
}

func TestUpdatePage_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	_, err = svc.UpdatePage(ctx, screen.ID, "no-such-page", "weather")
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("UpdatePage returned %v, want ErrPageNotFound", err)
	}
}

func TestDeletePage_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	page, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	if err := svc.DeletePage(ctx, screen.ID, page.ID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	_, err = svc.GetPageByID(ctx, screen.ID, page.ID)
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("GetPageByID after DeletePage returned %v, want ErrPageNotFound", err)
	}
}

func TestDeletePage_CascadesWidgets(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	page, err := svc.CreatePage(ctx, screen.ID, "clock")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	insertWidget(t, sqlDB, "widget-1", page.ID, "clock", "{}", 1)

	if err := svc.DeletePage(ctx, screen.ID, page.ID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	var count int
	row := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM widget_instances WHERE id = ?`, "widget-1")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count widget_instances: %v", err)
	}
	if count != 0 {
		t.Errorf("widget_instances row remains after DeletePage; CASCADE failed")
	}
}

func TestDeletePage_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	err = svc.DeletePage(ctx, screen.ID, "no-such-page")
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("DeletePage returned %v, want ErrPageNotFound", err)
	}
}

// pagePositions returns the ID -> position mapping for the named screen.
// Helper for reorder assertions.
func pagePositions(t *testing.T, svc *Service, screenID string) map[string]int {
	t.Helper()
	rows, err := svc.queries.ListPagesByScreen(context.Background(), screenID)
	if err != nil {
		t.Fatalf("ListPagesByScreen: %v", err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ID] = int(r.Position)
	}
	return out
}

// createThreePages creates a screen with three pages and returns the screen
// ID plus the page IDs in position order (P1 at 1, P2 at 2, P3 at 3).
func createThreePages(t *testing.T, svc *Service, themeID string) (screenID, p1, p2, p3 string) {
	t.Helper()
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	first, err := svc.CreatePage(ctx, screen.ID, "p1")
	if err != nil {
		t.Fatalf("CreatePage 1: %v", err)
	}
	second, err := svc.CreatePage(ctx, screen.ID, "p2")
	if err != nil {
		t.Fatalf("CreatePage 2: %v", err)
	}
	third, err := svc.CreatePage(ctx, screen.ID, "p3")
	if err != nil {
		t.Fatalf("CreatePage 3: %v", err)
	}
	return screen.ID, first.ID, second.ID, third.ID
}

func TestMovePageDown_Swaps(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	if err := svc.MovePageDown(context.Background(), screenID, p1); err != nil {
		t.Fatalf("MovePageDown: %v", err)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 2 || pos[p2] != 1 || pos[p3] != 3 {
		t.Errorf("after MovePageDown(P1): positions = {p1:%d, p2:%d, p3:%d}, want {p1:2, p2:1, p3:3}",
			pos[p1], pos[p2], pos[p3])
	}
}

func TestMovePageUp_Swaps(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	if err := svc.MovePageUp(context.Background(), screenID, p3); err != nil {
		t.Fatalf("MovePageUp: %v", err)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p2] != 3 || pos[p3] != 2 {
		t.Errorf("after MovePageUp(P3): positions = {p1:%d, p2:%d, p3:%d}, want {p1:1, p2:3, p3:2}",
			pos[p1], pos[p2], pos[p3])
	}
}

func TestMovePageUp_AtTop_NoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	if err := svc.MovePageUp(context.Background(), screenID, p1); err != nil {
		t.Fatalf("MovePageUp at top: %v", err)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p2] != 2 || pos[p3] != 3 {
		t.Errorf("after MovePageUp(P1 at top): positions = {p1:%d, p2:%d, p3:%d}, want unchanged {1,2,3}",
			pos[p1], pos[p2], pos[p3])
	}
}

func TestMovePageDown_AtBottom_NoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	if err := svc.MovePageDown(context.Background(), screenID, p3); err != nil {
		t.Fatalf("MovePageDown at bottom: %v", err)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p2] != 2 || pos[p3] != 3 {
		t.Errorf("after MovePageDown(P3 at bottom): positions = {p1:%d, p2:%d, p3:%d}, want unchanged {1,2,3}",
			pos[p1], pos[p2], pos[p3])
	}
}

func TestMovePage_UnknownPage(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()

	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	if err := svc.MovePageUp(ctx, screen.ID, "no-such-page"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MovePageUp returned %v, want ErrPageNotFound", err)
	}
	if err := svc.MovePageDown(ctx, screen.ID, "no-such-page"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MovePageDown returned %v, want ErrPageNotFound", err)
	}
}

// TestMovePage_NoTransientDuplicates verifies that after each swap the
// (screen_id, position) uniqueness invariant holds. Runs two sequential
// MovePageDown calls and asserts no duplicate positions exist after either.
func TestMovePage_NoTransientDuplicates(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, _, _ := createThreePages(t, svc, themeID)

	for i := 0; i < 2; i++ {
		if err := svc.MovePageDown(ctx, screenID, p1); err != nil {
			t.Fatalf("MovePageDown iteration %d: %v", i, err)
		}

		rows, err := sqlDB.QueryContext(ctx,
			`SELECT screen_id, position, COUNT(*) FROM pages GROUP BY screen_id, position HAVING COUNT(*) > 1`)
		if err != nil {
			t.Fatalf("duplicate-check query iteration %d: %v", i, err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatalf("iteration %d: duplicate (screen_id, position) detected", i)
		}
		rows.Close()
	}
}

// TestMovePage_RoundTripsCorrectly walks a page from position 1 to 3 and
// back to 1 via MovePageDown/Up and asserts the original layout returns.
func TestMovePage_RoundTripsCorrectly(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	// P1: 1 -> 2 -> 3
	if err := svc.MovePageDown(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageDown 1: %v", err)
	}
	if err := svc.MovePageDown(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageDown 2: %v", err)
	}
	// P1: 3 -> 2 -> 1
	if err := svc.MovePageUp(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageUp 1: %v", err)
	}
	if err := svc.MovePageUp(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageUp 2: %v", err)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p2] != 2 || pos[p3] != 3 {
		t.Errorf("after round trip: positions = {p1:%d, p2:%d, p3:%d}, want {p1:1, p2:2, p3:3}",
			pos[p1], pos[p2], pos[p3])
	}
}
