---
id: TASK-023
title: "screens.Service Page CRUD + reorder (transactional swap)"
spec: SPEC-006
arch: ARCH-006
status: review
priority: p0
prerequisites: [TASK-022]
skills: [add-store, green-bar]
created: 2026-05-13
author: architect
---

# TASK-023: screens.Service Page CRUD + reorder (transactional swap)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Extend `internal/screens/Service` with the Page-level CRUD methods (`CreatePage`, `GetPageByID`, `UpdatePage`, `DeletePage`) and reorder methods (`MovePageUp`, `MovePageDown`). Reorder uses the transactional negative-position swap pattern to avoid violating the `UNIQUE (screen_id, position)` constraint mid-swap.

This task runs in parallel with TASK-024 (widget instance CRUD). Both depend on TASK-022's service skeleton but are independent of each other.

## Context

- All methods are added to the `*Service` constructed in TASK-022. No new package files; methods live in `internal/screens/service.go`.
- The reorder swap uses the "negative position" idiom: park the target's position to a guaranteed-unique negative value, move the neighbour, then move the target to the neighbour's old position. Inside a single transaction.
- Page IDs are 32-char hex (`auth.GenerateToken[:32]`). The helper `generateID` already lives in this package (added by TASK-022 -- reuse it; do not redefine).
- The architecture document's "Storage > Reorder Implementation (transactional swap)" section pseudo-codes the swap.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/screens/service.go` (output of TASK-022) -- this is where the new methods land.
- `internal/screens/screen.go` -- `Page`, `pageFromRow`.
- `internal/screens/validate.go` -- `validatePageName`.
- `internal/db/pages.sql.go` (output of TASK-021) -- the typed queries you will call: `CreatePage`, `GetPageByID`, `ListPagesByScreen`, `MaxPagePosition`, `UpdatePage`, `DeletePage`, `GetPageNeighbor`, `SetPagePosition`.
- `internal/themes/service.go` -- the `BeginTx` + `WithTx` + `Commit` pattern used in `SetDefault` (mirror this for the reorder transaction).
- `docs/plans/specs/phase-2-display/spec-screen-model.md` -- requirements 10-18; AC-11 through AC-15.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- "Storage > Reorder Implementation (transactional swap)".

## Requirements

### Page CRUD

1. `CreatePage(ctx context.Context, screenID, name string) (Page, error)`:
   - Validate the name with `validatePageName(name)`. On failure, return the error directly (it is `*ValidationError`-compatible OR a plain error from the helper -- use the helper's signature). For consistency with the rest of the service, wrap into a `*ValidationError{Fields: map[string]string{"name": err.Error()}}` if the helper returns a plain error.
   - Confirm the screen exists via `s.queries.GetScreenByID(ctx, screenID)`. On `sql.ErrNoRows`, return `ErrScreenNotFound`.
   - Compute the new position: `pos, err := s.queries.MaxPagePosition(ctx, screenID)`; new page's position is `pos + 1`. (sqlc returns `int64`; cast to `int`.)
   - Generate an ID with the existing `generateID()` helper.
   - Call `s.queries.CreatePage(ctx, db.CreatePageParams{...})`. The DB-layer UNIQUE constraint on `(screen_id, position)` should never trip because we just looked up the max; if it does (race-y boot), wrap and return the error verbatim.
   - Fetch the row back with `GetPageByID` (using the screen + page IDs) and return.

2. `GetPageByID(ctx context.Context, screenID, pageID string) (Page, error)`:
   - Call `s.queries.GetPageByID(ctx, db.GetPageByIDParams{ID: pageID, ScreenID: screenID})`. The query's WHERE clause requires both to match.
   - On `sql.ErrNoRows`, return `ErrPageNotFound`. (This conflates "page does not exist" with "page exists but belongs to a different screen" -- intentional, since both are 404-equivalent to an admin browsing a wrong URL.)
   - Convert via `pageFromRow` and return.

3. `UpdatePage(ctx context.Context, screenID, pageID, name string) (Page, error)`:
   - Validate the name via `validatePageName`. Wrap into `*ValidationError` on failure as in `CreatePage`.
   - Confirm the page exists via `GetPageByID(ctx, screenID, pageID)`. On `ErrPageNotFound`, propagate.
   - Call `s.queries.UpdatePage(ctx, db.UpdatePageParams{Name: name, ID: pageID, ScreenID: screenID})`.
   - Fetch and return the updated row via `GetPageByID`.

4. `DeletePage(ctx context.Context, screenID, pageID string) error`:
   - Call `s.queries.DeletePage(ctx, db.DeletePageParams{ID: pageID, ScreenID: screenID})`.
   - Check `RowsAffected`. If 0, return `ErrPageNotFound`.
   - The DB-layer CASCADE handles widget instance cleanup -- no application code needed.

### Reorder

5. `MovePageUp(ctx context.Context, screenID, pageID string) error`:
   - Begin a transaction: `tx, err := s.sqlDB.BeginTx(ctx, nil)`. Defer `tx.Rollback()`. (Idempotent; ignored after Commit.)
   - Build a `qtx := s.queries.WithTx(tx)`.
   - Look up the target: `target, err := qtx.GetPageByID(ctx, db.GetPageByIDParams{ID: pageID, ScreenID: screenID})`. On `sql.ErrNoRows`, return `ErrPageNotFound`.
   - Look up the neighbour above: `neighbor, err := qtx.GetPageNeighbor(ctx, db.GetPageNeighborParams{ScreenID: screenID, Position: target.Position - 1})`. On `sql.ErrNoRows`, return nil (no-op: already at top); commit the (empty) transaction first.
   - Perform the negative-position swap:
     1. `qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: -target.Position, ID: target.ID, ScreenID: screenID})` -- park target at `-position`.
     2. `qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: target.Position, ID: neighbor.ID, ScreenID: screenID})` -- move neighbour to target's old position.
     3. `qtx.SetPagePosition(ctx, db.SetPagePositionParams{Position: neighbor.Position, ID: target.ID, ScreenID: screenID})` -- move target to neighbour's old position.
   - `tx.Commit()`.
   - Each `SetPagePosition` error is wrapped and returned, triggering the deferred rollback.

6. `MovePageDown(ctx context.Context, screenID, pageID string) error`:
   - Same shape as MovePageUp, but the neighbour lookup uses `target.Position + 1`.

### Tests

7. Extend `internal/screens/service_test.go` (or add `internal/screens/pages_test.go` if file size is becoming unwieldy) with the tests enumerated in "Test Requirements" below.

8. Tests use `db.OpenTestDB(t)` and the same service construction pattern as TASK-022 (themes service + empty widget registry). Seed a default theme via `themesSvc.EnsureDefault(ctx)`. Create a screen via `svc.CreateScreen(...)` so tests can target the screen by ID.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-11: `CreatePage(ctx, screenID, "clock")` inserts a row with `screen_id=screenID`, `name="clock"`, `position = max+1` (1 on first page).
- [ ] AC-12 (service half): `DeletePage` returns nil for an existing page; subsequent `GetPageByID` returns `ErrPageNotFound`. Child widget instances (created via raw SQL) are also gone (DB-layer cascade).
- [ ] AC-13: Given pages at positions 1, 2, 3, `MovePageDown` on position 1 results in the pages at positions 2, 1, 3.
- [ ] AC-14: `MovePageUp` on the top page returns nil with no row mutations. `MovePageDown` on the bottom page returns nil with no row mutations.
- [ ] AC-15 (page half): After any sequence of reorder operations, `SELECT screen_id, position, COUNT(*) FROM pages GROUP BY screen_id, position HAVING COUNT(*) > 1` returns zero rows (uniqueness preserved).

## Skills to Use

- `add-store` -- pattern is the same as theme service methods.
- `green-bar` -- run before marking review.

## Test Requirements

1. **TestCreatePage_HappyPath**: create a screen, then `CreatePage(screenID, "clock")`. Assert returned `Page.Position == 1`, `Page.Name == "clock"`. Call again, assert `Position == 2`.

2. **TestCreatePage_EmptyName**: `CreatePage(screenID, "")` succeeds (empty name is allowed); `Position == 1`. The returned page's `Name` is `""`.

3. **TestCreatePage_InvalidName**: `CreatePage(screenID, "page<script>")` returns a `*ValidationError`.

4. **TestCreatePage_UnknownScreen**: `CreatePage("nonexistent", "x")` returns `ErrScreenNotFound`.

5. **TestGetPageByID_HappyPath**: create a page, retrieve it; assert fields match.

6. **TestGetPageByID_NotFound**: returns `ErrPageNotFound`.

7. **TestGetPageByID_WrongScreen**: create page on screen A; call `GetPageByID(B.ID, page.ID)` -- returns `ErrPageNotFound` (the query's WHERE clause requires both to match).

8. **TestUpdatePage_HappyPath**: update name; assert the new name is persisted.

9. **TestUpdatePage_InvalidName**: returns `*ValidationError`.

10. **TestUpdatePage_NotFound**: returns `ErrPageNotFound`.

11. **TestDeletePage_HappyPath**: create page, delete it; subsequent `GetPageByID` returns `ErrPageNotFound`.

12. **TestDeletePage_CascadesWidgets**: create a page, manually insert a widget_instance via raw SQL (this task does not own widget CRUD; TASK-024 does), call `DeletePage`. Assert the widget_instance row is gone (DB-layer cascade).

13. **TestDeletePage_NotFound**: returns `ErrPageNotFound`.

14. **TestMovePageDown_Swaps**:
    - Create three pages: P1 (position 1), P2 (position 2), P3 (position 3).
    - Call `MovePageDown(screenID, P1.ID)`.
    - Assert positions: P1 at 2, P2 at 1, P3 at 3 (via direct DB query or `ListPagesByScreen`).

15. **TestMovePageUp_Swaps**:
    - Same setup. Call `MovePageUp(screenID, P3.ID)`.
    - Assert: P1 at 1, P2 at 3, P3 at 2.

16. **TestMovePageUp_AtTop_NoOp**:
    - Three pages. Call `MovePageUp(screenID, P1.ID)` (P1 is at position 1).
    - Assert nil error and positions unchanged: P1=1, P2=2, P3=3.

17. **TestMovePageDown_AtBottom_NoOp**:
    - Three pages. Call `MovePageDown(screenID, P3.ID)`.
    - Assert nil error and positions unchanged.

18. **TestMovePage_UnknownPage**: returns `ErrPageNotFound`.

19. **TestMovePage_NoTransientDuplicates**: a swap must not leave duplicate `(screen_id, position)` rows visible to a concurrent reader. Test by running two `MovePageDown` calls sequentially on a 3-page screen and asserting `SELECT screen_id, position, COUNT(*) FROM pages GROUP BY 1,2 HAVING COUNT(*)>1` always returns zero rows.

20. **TestMovePage_RoundTripsCorrectly**:
    - Three pages.
    - MovePageDown(P1), MovePageDown(P1 -- now at position 2 -> 3), MovePageUp(P1) twice should return to the original layout.
    - Asserts the swap idiom is fully reversible.

## Definition of Done

- [ ] All five new methods (`CreatePage`, `GetPageByID`, `UpdatePage`, `DeletePage`, `MovePageUp`, `MovePageDown`) implemented in `internal/screens/service.go`.
- [ ] All tests above pass.
- [ ] green-bar passes (gofmt, vet, build, test). Run `go test -race ./internal/screens/...` since the service uses transactions.
- [ ] No regressions in TASK-022 tests.
- [ ] No new third-party dependencies.
- [ ] The reorder swap uses the negative-position-trick inside a single transaction; no version of the swap mutates positions outside a transaction.
