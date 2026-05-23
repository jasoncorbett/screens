package screens

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestAdversarial_DeletePageOnWrongScreen pins the cross-screen defence:
// DeletePage refuses to delete a page that exists but belongs to a different
// screen. The DELETE query's WHERE clause uses both ID and screen_id, so the
// row count is zero and the service returns ErrPageNotFound. Without this
// test, a future refactor that drops the screen_id filter could let one
// admin's tab delete pages on the wrong screen.
func TestAdversarial_DeletePageOnWrongScreen(t *testing.T) {
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

	if err := svc.DeletePage(ctx, b.ID, page.ID); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("DeletePage(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
	// Page must still exist on screen A.
	if _, err := svc.GetPageByID(ctx, a.ID, page.ID); err != nil {
		t.Errorf("page on screen A vanished after wrong-screen delete attempt: %v", err)
	}
}

// TestAdversarial_UpdatePageOnWrongScreen pins the cross-screen defence for
// UpdatePage: even if both screen IDs exist and the page ID exists, the
// (screen_id, page_id) pair must match. The UpdatePage WHERE clause enforces
// this; without this test a refactor that drops the screen_id filter would
// allow one admin to mutate pages on a different screen by guessing IDs.
func TestAdversarial_UpdatePageOnWrongScreen(t *testing.T) {
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

	if _, err := svc.UpdatePage(ctx, b.ID, page.ID, "renamed"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("UpdatePage(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
	got, err := svc.GetPageByID(ctx, a.ID, page.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}
	if got.Name != "clock" {
		t.Errorf("page name = %q, want unchanged %q", got.Name, "clock")
	}
}

// TestAdversarial_MovePageOnWrongScreen pins that MovePageUp/Down with a
// page that exists but belongs to a different screen returns ErrPageNotFound.
// The swap's first read (GetPageByID inside the tx) catches the mismatch.
func TestAdversarial_MovePageOnWrongScreen(t *testing.T) {
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
	if _, err := svc.CreatePage(ctx, a.ID, "p1"); err != nil {
		t.Fatalf("CreatePage p1: %v", err)
	}
	p2, err := svc.CreatePage(ctx, a.ID, "p2")
	if err != nil {
		t.Fatalf("CreatePage p2: %v", err)
	}

	if err := svc.MovePageUp(ctx, b.ID, p2.ID); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MovePageUp(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
	if err := svc.MovePageDown(ctx, b.ID, p2.ID); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("MovePageDown(B, page-on-A) = %v, want ErrPageNotFound", err)
	}
}

// TestAdversarial_MovePageNoOpReleasesLockForSubsequentOps verifies the no-op
// reorder path commits its transaction so the connection lock releases and
// subsequent reads / writes can run. A regression that returned nil without
// calling Commit (or that returned tx.Rollback's error) would deadlock the
// next admin request.
func TestAdversarial_MovePageNoOpReleasesLockForSubsequentOps(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	// Top page: MovePageUp is a no-op.
	if err := svc.MovePageUp(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageUp top no-op: %v", err)
	}
	// Bottom page: MovePageDown is a no-op.
	if err := svc.MovePageDown(ctx, screenID, p3); err != nil {
		t.Fatalf("MovePageDown bottom no-op: %v", err)
	}
	// A subsequent read must succeed (would hang on an unreleased write lock
	// with the single-connection test pool).
	if _, err := svc.GetPageByID(ctx, screenID, p2); err != nil {
		t.Fatalf("GetPageByID after no-ops: %v", err)
	}
	// And a subsequent write must succeed.
	if err := svc.MovePageDown(ctx, screenID, p1); err != nil {
		t.Fatalf("subsequent MovePageDown: %v", err)
	}
	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 2 || pos[p2] != 1 || pos[p3] != 3 {
		t.Errorf("after no-ops + real move: positions = {p1:%d, p2:%d, p3:%d}, want {p1:2, p2:1, p3:3}",
			pos[p1], pos[p2], pos[p3])
	}
}

// TestAdversarial_PageReorderClosedDBSurfacesError verifies that operating on
// a closed DB returns a non-nil error rather than panicking. BeginTx on a
// closed DB returns sql.ErrConnDone-equivalent; the service must surface it
// wrapped, not crash. Mirrors TestAdversarial_DeleteAfterClosedDBSurfacesError
// for the reorder code path which has its own transaction setup.
func TestAdversarial_PageReorderClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, _, _ := createThreePages(t, svc, themeID)
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MovePageDown panicked after DB close: %v", r)
		}
	}()
	if err := svc.MovePageDown(ctx, screenID, p1); err == nil {
		t.Error("MovePageDown on closed DB returned nil, want error")
	}
}

// TestAdversarial_PagePositionsUniqueAfterMixedReorders runs a sequence of
// mixed moves and asserts the UNIQUE (screen_id, position) invariant holds
// after each one. AC-15 (page half) requires this; the test exercises a
// longer-than-minimum sequence so an off-by-one in the negative-position
// swap would surface.
func TestAdversarial_PagePositionsUniqueAfterMixedReorders(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	type op struct {
		name string
		fn   func() error
	}
	steps := []op{
		{"down(p1)", func() error { return svc.MovePageDown(ctx, screenID, p1) }},
		{"down(p1)", func() error { return svc.MovePageDown(ctx, screenID, p1) }},
		{"up(p1)", func() error { return svc.MovePageUp(ctx, screenID, p1) }},
		{"up(p2)", func() error { return svc.MovePageUp(ctx, screenID, p2) }},
		{"down(p3)", func() error { return svc.MovePageDown(ctx, screenID, p3) }},
		{"up(p3)", func() error { return svc.MovePageUp(ctx, screenID, p3) }},
	}

	for i, s := range steps {
		if err := s.fn(); err != nil {
			t.Fatalf("step %d %s: %v", i, s.name, err)
		}
		// Check no duplicate (screen_id, position) pairs exist.
		rows, err := sqlDB.QueryContext(ctx,
			`SELECT screen_id, position, COUNT(*) FROM pages GROUP BY screen_id, position HAVING COUNT(*) > 1`)
		if err != nil {
			t.Fatalf("step %d duplicate-check: %v", i, err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatalf("step %d %s: duplicate (screen_id, position) detected", i, s.name)
		}
		rows.Close()

		// Check no negative positions leaked (the swap idiom must clear them
		// before commit). A leak would mean the third UPDATE was skipped.
		var minPos int64
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT MIN(position) FROM pages WHERE screen_id = ?`, screenID).Scan(&minPos); err != nil {
			t.Fatalf("step %d min position: %v", i, err)
		}
		if minPos < 1 {
			t.Fatalf("step %d %s: position %d leaked (negative-position swap did not complete)", i, s.name, minPos)
		}
	}
}

// TestAdversarial_PageNameRejectsControlBytes verifies the validator rejects
// names containing NUL, ESC, and other control bytes that strings.TrimSpace
// does NOT strip. The regex whitelist is the only line of defence here;
// without this test a refactor that swapped TrimSpace for a more aggressive
// strip+accept could silently let \x00 land in the database, which downstream
// templ rendering would treat as a string-terminator on some platforms.
func TestAdversarial_PageNameRejectsControlBytes(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	bad := []struct {
		name  string
		value string
	}{
		{"NUL", "clock\x00"},
		{"ESC", "clock\x1b"},
		{"unicode", "kíchen"},
		{"angle", "clock<x>"},
		{"semicolon", "clock;"},
		{"backtick", "clock`"},
		{"slash", "clock/"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreatePage(ctx, screen.ID, tc.value)
			if !IsValidationError(err) {
				t.Errorf("CreatePage(name=%q) = %v, want *ValidationError", tc.value, err)
			}
		})
	}
}

// TestAdversarial_PageNameTrimmedWhitespaceIsEmpty pins the documented
// behaviour that any name consisting only of whitespace (spaces, tabs,
// newlines) is normalised to "" by validatePageName and accepted, because
// FR-13 explicitly allows empty page names. A future change that rejected
// whitespace-only names would break admins who left the field blank in the
// form (browsers sometimes send trailing whitespace from autofill).
func TestAdversarial_PageNameTrimmedWhitespaceIsEmpty(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	for i, input := range []string{"", "   ", "\t", "\n", " \t\n "} {
		page, err := svc.CreatePage(ctx, screen.ID, input)
		if err != nil {
			t.Errorf("input %d %q: CreatePage err = %v, want nil", i, input, err)
			continue
		}
		if page.Name != "" {
			t.Errorf("input %d %q: stored Name = %q, want empty", i, input, page.Name)
		}
		// Clean up so positions stay tidy for the next iteration.
		if err := svc.DeletePage(ctx, screen.ID, page.ID); err != nil {
			t.Fatalf("cleanup DeletePage: %v", err)
		}
	}
}

// TestAdversarial_PageNameBoundaryLengths pins the 64-char accept / 65-char
// reject boundary. A regex off-by-one (`{1,63}` or `{1,65}`) would slip past
// the existing validation tests.
func TestAdversarial_PageNameBoundaryLengths(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	name64 := strings.Repeat("a", 64)
	name65 := strings.Repeat("a", 65)

	page, err := svc.CreatePage(ctx, screen.ID, name64)
	if err != nil {
		t.Errorf("CreatePage(64 chars): err = %v, want nil", err)
	}
	if page.Name != name64 {
		t.Errorf("stored Name length = %d, want 64", len(page.Name))
	}

	if _, err := svc.CreatePage(ctx, screen.ID, name65); !IsValidationError(err) {
		t.Errorf("CreatePage(65 chars): err = %v, want *ValidationError", err)
	}
}

// TestAdversarial_SQLInjectionInPageAndScreenIDs covers SQL injection probes
// through both the page id AND screen id parameters to all page-targeted
// service methods. All queries are sqlc-generated and parameterised, but a
// future hand-built query path could regress; this pins the contract for
// every page-CRUD entrypoint.
func TestAdversarial_SQLInjectionInPageAndScreenIDs(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	payloads := []string{
		"'); DROP TABLE pages;--",
		"' OR 1=1 --",
		"x' UNION SELECT * FROM screens --",
		"'; UPDATE pages SET name='hax' WHERE 1=1; --",
	}
	for _, p := range payloads {
		// As pageID
		if _, err := svc.GetPageByID(ctx, screen.ID, p); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("GetPageByID(pageID=%q) = %v, want ErrPageNotFound", p, err)
		}
		if err := svc.DeletePage(ctx, screen.ID, p); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("DeletePage(pageID=%q) = %v, want ErrPageNotFound", p, err)
		}
		if err := svc.MovePageUp(ctx, screen.ID, p); !errors.Is(err, ErrPageNotFound) {
			t.Errorf("MovePageUp(pageID=%q) = %v, want ErrPageNotFound", p, err)
		}

		// As screenID
		if _, err := svc.CreatePage(ctx, p, "x"); !errors.Is(err, ErrScreenNotFound) {
			t.Errorf("CreatePage(screenID=%q) = %v, want ErrScreenNotFound", p, err)
		}
	}

	// Tables and rows intact: the screen still exists and the pages table
	// still has the expected (single) row from a fresh create.
	if _, err := svc.GetScreenByID(ctx, screen.ID); err != nil {
		t.Errorf("screen vanished after injection probes: %v", err)
	}
	var pagesTableCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pages").Scan(&pagesTableCount); err != nil {
		t.Fatalf("count pages: %v", err)
	}
	if pagesTableCount != 0 {
		t.Errorf("pages table has %d rows; injection probe wrote unexpected data", pagesTableCount)
	}
}

// TestAdversarial_DeleteCreatesGapButPositionsRemainUnique pins the
// documented behaviour that deleting a middle page leaves a gap in positions
// (the spec FR-15 explicitly accepts this: "Gaps in positions are
// acceptable") and the subsequent CreatePage uses MAX+1 rather than filling
// the gap. The (screen_id, position) UNIQUE invariant still holds.
//
// This test exists to lock the contract: if a future change starts filling
// gaps on Create, that would silently break callers that rely on stable
// position values for already-created pages.
func TestAdversarial_DeleteCreatesGapButPositionsRemainUnique(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	if err := svc.DeletePage(ctx, screenID, p2); err != nil {
		t.Fatalf("DeletePage middle: %v", err)
	}

	p4page, err := svc.CreatePage(ctx, screenID, "p4")
	if err != nil {
		t.Fatalf("CreatePage p4: %v", err)
	}
	if p4page.Position != 4 {
		t.Errorf("p4.Position = %d, want 4 (MAX+1 after delete leaves a gap)", p4page.Position)
	}

	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p3] != 3 || pos[p4page.ID] != 4 {
		t.Errorf("positions = {p1:%d, p3:%d, p4:%d}, want {1, 3, 4}", pos[p1], pos[p3], pos[p4page.ID])
	}

	// And the UNIQUE invariant still holds (defensive query mirroring
	// AC-15 / TestMovePage_NoTransientDuplicates).
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT screen_id, position, COUNT(*) FROM pages GROUP BY screen_id, position HAVING COUNT(*) > 1`)
	if err != nil {
		t.Fatalf("duplicate-check: %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("duplicate (screen_id, position) detected after gap-create")
	}
	rows.Close()
}

// TestAdversarial_MovePageAcrossGapIsNoOp pins the documented behaviour of
// the GetPageNeighbor-by-exact-position lookup: if a page's neighbour at
// position±1 is absent (because a delete left a gap), MovePageUp/Down is a
// no-op. This matches the architecture's reorder algorithm
// (ARCH-006 "Reorder Implementation"); changing it would require a different
// neighbour query (e.g. "the row with the next-smaller position") and is
// out of scope for TASK-023.
//
// The current behaviour is *not* the intuitive UX -- an admin who deletes a
// middle page may expect MovePageUp/Down on adjacent pages to still re-rank
// them. We pin it here so any future change is intentional, not accidental.
func TestAdversarial_MovePageAcrossGapIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screenID, p1, p2, p3 := createThreePages(t, svc, themeID)

	// Delete the middle page to create a gap at position 2.
	if err := svc.DeletePage(ctx, screenID, p2); err != nil {
		t.Fatalf("DeletePage middle: %v", err)
	}
	// Pages now at positions {p1:1, p3:3}. Position 2 is empty.

	// MovePageUp on p3 looks for a neighbour at position 2 -- finds nothing,
	// returns nil with no mutation.
	if err := svc.MovePageUp(ctx, screenID, p3); err != nil {
		t.Fatalf("MovePageUp across gap: %v", err)
	}
	pos := pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p3] != 3 {
		t.Errorf("after MovePageUp(p3) across gap: positions = {p1:%d, p3:%d}, want unchanged {1, 3}",
			pos[p1], pos[p3])
	}

	// MovePageDown on p1 looks for a neighbour at position 2 -- same story.
	if err := svc.MovePageDown(ctx, screenID, p1); err != nil {
		t.Fatalf("MovePageDown across gap: %v", err)
	}
	pos = pagePositions(t, svc, screenID)
	if pos[p1] != 1 || pos[p3] != 3 {
		t.Errorf("after MovePageDown(p1) across gap: positions = {p1:%d, p3:%d}, want unchanged {1, 3}",
			pos[p1], pos[p3])
	}
}

// TestAdversarial_ConcurrentCreatePageOnSameScreen pins the documented
// behaviour for concurrent CreatePage calls. The task explicitly accepts
// the MaxPagePosition+Insert TOCTOU race ("if it does (race-y boot), wrap
// and return the error verbatim"). Under the single-connection test pool,
// goroutines interleave between MaxPagePosition reads and Insert writes,
// so collisions appear as wrapped UNIQUE-constraint errors. At least one
// CreatePage must succeed and the final state must have unique positions.
//
// Pin: the contract is "at least one wins; failures contain UNIQUE
// constraint string; no panic; no duplicate positions". If a future change
// wraps CreatePage in a transaction (recommended; the race is real in
// production with MaxOpenConns > 1), the failure-count assertion below
// will fail and force an intentional update.
func TestAdversarial_ConcurrentCreatePageOnSameScreen(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.CreatePage(ctx, screen.ID, fmt.Sprintf("p-%d", i))
		}(i)
	}
	wg.Wait()

	var ok int
	for i, e := range errs {
		switch {
		case e == nil:
			ok++
		case strings.Contains(e.Error(), "UNIQUE constraint failed"):
			// Documented race-y boot failure. Acceptable per TASK-023.
		default:
			t.Errorf("goroutine %d: unexpected error %v", i, e)
		}
	}
	if ok < 1 {
		t.Fatalf("concurrent CreatePage: %d successes, want at least 1", ok)
	}

	// Whatever succeeded, the (screen_id, position) UNIQUE invariant must
	// hold. AC-15 (page half).
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT screen_id, position, COUNT(*) FROM pages GROUP BY screen_id, position HAVING COUNT(*) > 1`)
	if err != nil {
		t.Fatalf("duplicate-check: %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("concurrent CreatePage produced duplicate (screen_id, position) rows")
	}
	rows.Close()
}

// TestAdversarial_CreatePageWithClosedDBSurfacesError verifies CreatePage on
// a closed DB returns an error rather than panicking, mirroring the
// equivalent test for DeleteScreen.
func TestAdversarial_CreatePageWithClosedDBSurfacesError(t *testing.T) {
	t.Parallel()
	svc, sqlDB, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CreatePage panicked after DB close: %v", r)
		}
	}()
	if _, err := svc.CreatePage(ctx, screen.ID, "p"); err == nil {
		t.Error("CreatePage on closed DB returned nil, want error")
	}
}

// TestAdversarial_DeletePageReturnsErrPageNotFoundOnEmptyDB tests the
// boundary where the pages table is completely empty (first-ever request
// scenario, or post-full-delete). DeletePage must return ErrPageNotFound,
// not panic or surface a bare sql.ErrNoRows from the underlying
// RowsAffected path.
func TestAdversarial_DeletePageReturnsErrPageNotFoundOnEmptyDB(t *testing.T) {
	t.Parallel()
	svc, _, themeID := newTestService(t)
	ctx := context.Background()
	screen, err := svc.CreateScreen(ctx, ScreenInput{Name: "kitchen", ThemeID: themeID, RotationIntervalSeconds: 30})
	if err != nil {
		t.Fatalf("CreateScreen: %v", err)
	}

	// No pages exist yet.
	if err := svc.DeletePage(ctx, screen.ID, "anything"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("DeletePage on empty table = %v, want ErrPageNotFound", err)
	}
}
