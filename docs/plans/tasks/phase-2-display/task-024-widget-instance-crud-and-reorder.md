---
id: TASK-024
title: "screens.Service widget instance CRUD + reorder + registry validation"
spec: SPEC-006
arch: ARCH-006
status: ready
priority: p0
prerequisites: [TASK-022]
skills: [add-store, green-bar]
created: 2026-05-13
author: architect
---

# TASK-024: screens.Service widget instance CRUD + reorder + registry validation

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Extend `internal/screens/Service` with the WidgetInstance-level operations: `AddWidget` (creates an instance with the widget type's default config), `DeleteWidget`, `MoveWidgetUp`, `MoveWidgetDown`. `AddWidget` uses the `widget.Registry` injected at construction time (in TASK-022's `NewService`) to look up the type's `DefaultConfig` and round-trip it through `ValidateConfig` before persisting -- this enforces the "default-must-validate" property at write time.

Reorder uses the same transactional negative-position swap idiom as Page reorder (TASK-023). The implementation is symmetric; this task may copy the swap helper or extract a shared helper if it reduces duplication.

This task runs in parallel with TASK-023 (page CRUD + reorder). Both depend on TASK-022 but are independent of each other.

## Context

- The `*widget.Registry` was already injected into the `Service` struct by TASK-022's `NewService`. This task is the first consumer of that field.
- `widget.Registry.Get(typeName)` returns the registration and a bool; `widget.Registry.Validate(typeName, raw)` runs the type's validator and returns an `Instance` or an error. This task uses both.
- Widget-instance IDs follow the same 32-char hex idiom as everything else (`auth.GenerateToken[:32]` via `generateID()`).
- The reorder swap pattern is identical to pages: transactional, negative-position parking step.
- The widget-instance config is stored as TEXT (JSON bytes) in the `widget_instances.config` column. sqlc generates `Config string` in `db.WidgetInstance`; convert to `[]byte` in `widgetFromRow` and back at write time.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/screens/service.go` (output of TASK-022; TASK-023 may have added page methods to it -- both that and this task land in the same file).
- `internal/screens/screen.go` -- `WidgetInstance` type and `widgetFromRow` helper.
- `internal/widget/registry.go` -- `Get`, `Validate`, `Default()`.
- `internal/widget/text/text.go` -- the placeholder `text` widget used by tests as the registered widget type.
- `internal/db/widget_instances.sql.go` (output of TASK-021) -- typed queries: `CreateWidgetInstance`, `GetWidgetInstanceByID`, `ListWidgetInstancesByPage`, `ListWidgetInstancesByPageIDs`, `MaxWidgetPosition`, `DeleteWidgetInstance`, `GetWidgetNeighbor`, `SetWidgetPosition`.
- `docs/plans/specs/phase-2-display/spec-screen-model.md` -- requirements 19-27; AC-16 through AC-20.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- "Storage > Reorder Implementation (transactional swap)" (same idiom).

## Requirements

### Widget instance methods

1. `AddWidget(ctx context.Context, screenID, pageID, widgetType string) (WidgetInstance, error)`:
   - Confirm the page exists by calling `GetPageByID(ctx, screenID, pageID)` -- the method from TASK-023. On `ErrPageNotFound`, propagate.
   - Look up the registration: `reg, ok := s.widgets.Get(widgetType)`. If `!ok`, return `ErrUnknownWidgetType`.
   - Get the default config: `raw := reg.DefaultConfig()`.
   - Validate it: `if _, err := reg.ValidateConfig(raw); err != nil { return WidgetInstance{}, fmt.Errorf("widget %q: default config failed validation: %w", widgetType, err) }`. (A registered widget whose default fails validation is a build-time bug, but defensive validation here is cheap and matches the "default-must-validate" property.)
   - Compute the new position: `pos, _ := s.queries.MaxWidgetPosition(ctx, pageID)`; new position is `pos + 1`.
   - Generate an ID via `generateID()`.
   - Call `s.queries.CreateWidgetInstance(ctx, db.CreateWidgetInstanceParams{ID, PageID, Type: widgetType, Config: string(raw), Position})`.
   - Fetch via `GetWidgetInstanceByID` (with both pageID and widgetID) and return.

2. `DeleteWidget(ctx context.Context, screenID, pageID, widgetID string) error`:
   - For defence-in-depth, confirm the page exists via `GetPageByID`. (Catches the case where an admin URL contains a stale page ID.) On `ErrPageNotFound`, propagate.
   - Call `s.queries.DeleteWidgetInstance(ctx, db.DeleteWidgetInstanceParams{ID: widgetID, PageID: pageID})`.
   - Inspect `RowsAffected`. If 0, return `ErrWidgetNotFound`.

3. `MoveWidgetUp(ctx context.Context, screenID, pageID, widgetID string) error`:
   - Confirm the page exists via `GetPageByID` first. On `ErrPageNotFound`, propagate.
   - Begin a transaction; defer rollback; build `qtx`.
   - Look up the target: `target, err := qtx.GetWidgetInstanceByID(ctx, db.GetWidgetInstanceByIDParams{ID: widgetID, PageID: pageID})`. On `sql.ErrNoRows`, return `ErrWidgetNotFound`.
   - Look up the neighbour above: `neighbor, err := qtx.GetWidgetNeighbor(ctx, db.GetWidgetNeighborParams{PageID: pageID, Position: target.Position - 1})`. On `sql.ErrNoRows`, commit and return nil (no-op).
   - Perform the three-step swap:
     1. `qtx.SetWidgetPosition(ctx, ... Position: -target.Position, ID: target.ID, PageID: pageID ...)`.
     2. `qtx.SetWidgetPosition(ctx, ... Position: target.Position, ID: neighbor.ID, PageID: pageID ...)`.
     3. `qtx.SetWidgetPosition(ctx, ... Position: neighbor.Position, ID: target.ID, PageID: pageID ...)`.
   - `tx.Commit()`.

4. `MoveWidgetDown(ctx context.Context, screenID, pageID, widgetID string) error`:
   - Same as MoveWidgetUp but with `target.Position + 1` for the neighbour.

### Optional refactor

5. If the page-reorder swap (`MovePageUp` / `MovePageDown` from TASK-023) and the widget-reorder swap end up being structurally identical, consider extracting a small unexported helper. Sample signature:
   ```go
   // swapPositions swaps two adjacent rows' positions via the
   // negative-position trick inside the given transaction.
   func swapPositions(ctx context.Context, tx *sql.Tx, setPosA, setPosB func(ctx, position int) error, posA, posB int) error
   ```
   This is OPTIONAL. If the parameters and types diverge enough that extraction is uglier than the duplication, skip the helper -- duplication of a ~10-line transactional swap is fine. The architecture document does not commit to a particular helper shape.

### Tests

6. Extend `internal/screens/service_test.go` (or add `internal/screens/widgets_test.go`) with the test cases enumerated in "Test Requirements" below.

7. Tests construct the service with a registry that has the `text` widget registered:
   ```go
   registry := widget.NewRegistry()
   _ = registry.Register(text.Registration())
   svc := screens.NewService(sqlDB, themesSvc, registry)
   ```
   This avoids touching the global `widget.Default()` and gives each test isolated registry state.

8. Use `db.OpenTestDB(t)` and seed the default theme. Create a screen and one page via the service's existing methods (output of TASK-023) so tests have a target page for widget operations.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-16: `AddWidget(ctx, screenID, pageID, "text")` inserts a row with `type="text"`, `config` equal to `text.Registration().DefaultConfig()`, `position = max+1`.
- [ ] AC-17: `AddWidget(ctx, ..., "nonexistent")` returns `ErrUnknownWidgetType`; no row created.
- [ ] AC-18: `DeleteWidget` on an existing widget returns nil; `GetScreenFull` (defined in TASK-022) no longer includes that widget.
- [ ] AC-19: Given widgets at positions 1 and 2 on the same page, `MoveWidgetUp(ctx, ..., position2.ID)` swaps them to positions 2 and 1.
- [ ] AC-20: The `text` widget's `DefaultConfig()` round-tripped through `AddWidget` and then retrieved via `GetScreenFull` produces bytes that pass `widget.NewRegistry()` (with text registered) `Validate("text", config)` without error.

## Skills to Use

- `add-store` -- mirror the patterns from TASK-022 / TASK-023.
- `green-bar` -- run before marking review.

## Test Requirements

1. **TestAddWidget_HappyPath**: register `text` on the registry, AddWidget on a page; assert returned `WidgetInstance.Type == "text"`, `Position == 1`, `Config` parses as `{"text":"Hello, screens"}` (the text widget's `defaultConfig`). Call again; assert `Position == 2`.

2. **TestAddWidget_UnknownType**: `AddWidget(..., "nope")` returns `ErrUnknownWidgetType`.

3. **TestAddWidget_UnknownPage**: `AddWidget(screenID, "nonexistent-page", "text")` returns `ErrPageNotFound`.

4. **TestAddWidget_DefaultValidates**: after AddWidget, fetch the row via `GetWidgetInstanceByID` (or `ListWidgetInstancesByPage`) and call `registry.Validate("text", config)`. Assert nil error. (This verifies AC-20.)

5. **TestDeleteWidget_HappyPath**: add a widget, delete it; subsequent `ListWidgetInstancesByPage` is empty.

6. **TestDeleteWidget_NotFound**: returns `ErrWidgetNotFound`.

7. **TestDeleteWidget_UnknownPage**: returns `ErrPageNotFound` (the upstream page check).

8. **TestMoveWidgetDown_Swaps**:
   - Two widgets at positions 1 and 2 on the same page.
   - `MoveWidgetDown(..., widgetAtPos1.ID)`.
   - Assert positions: widget A at 2, widget B at 1.

9. **TestMoveWidgetUp_Swaps**: symmetric.

10. **TestMoveWidget_AtTop_NoOp** and **TestMoveWidget_AtBottom_NoOp**: returns nil with no row mutations.

11. **TestMoveWidget_UnknownWidget**: returns `ErrWidgetNotFound`.

12. **TestMoveWidget_UnknownPage**: returns `ErrPageNotFound`.

13. **TestMoveWidget_RoundTrip**: swap down then up restores the original order.

14. **TestMoveWidget_NoTransientDuplicates**: after a swap, no two rows share `(page_id, position)`.

15. **TestGetScreenFull_IncludesAddedWidgets** (extends TASK-022's `TestGetScreenFull`): build a screen + page + two widgets via the service's own methods (not raw SQL). Call `GetScreenFull`. Assert the widget config bytes are present in the order their positions imply.

## Definition of Done

- [ ] All four new methods (`AddWidget`, `DeleteWidget`, `MoveWidgetUp`, `MoveWidgetDown`) implemented in `internal/screens/service.go`.
- [ ] All tests pass, including the run with `-race`.
- [ ] green-bar passes (gofmt, vet, build, test).
- [ ] No regressions in TASK-022 / TASK-023 tests.
- [ ] No new third-party dependencies.
- [ ] The registry is consumed via `s.widgets.Get` / the `Registration.DefaultConfig` / `Registration.ValidateConfig` accessors. The implementation does NOT call `widget.Default()` directly -- it uses the registry stored on the `Service` (so tests can inject a clean registry).
