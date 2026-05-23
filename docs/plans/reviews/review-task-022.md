---
task: TASK-022
spec: SPEC-006
reviewer: tester
date: 2026-05-22
recommendation: ACCEPT
---

# Review: TASK-022 — screens.Service core + themes.ErrThemeInUse

## Acceptance Criteria

| AC     | Status | Evidence |
|--------|--------|----------|
| AC-1 (service half)  | PASS | `TestCreateScreen_HappyPath` persists row with matching ThemeID, returns Screen. |
| AC-2  | PASS | `TestCreateScreen_RejectsValidationErrors/empty name` returns `*ValidationError` with `name` field. |
| AC-3  | PASS | `TestCreateScreen_RejectsValidationErrors/name with angle bracket` returns `*ValidationError`. |
| AC-4  | PASS | `TestCreateScreen_RejectsDuplicateName` returns `ErrDuplicateName`. |
| AC-5  | PASS | `TestCreateScreen_RejectsValidationErrors/rotation 4` and `/rotation 3601` reject; rotation 5 / 3600 succeed via `TestValidateScreenInput`. |
| AC-6  | PASS | `TestCreateScreen_RejectsUnknownThemeID` returns the screens-package `ErrThemeNotFound`. |
| AC-7 (service half) | PASS | `TestDeleteScreen_HappyPath` confirms CASCADE removes the page and widget rows. |
| AC-9  | PASS | `TestDelete_RejectsThemeInUse` in `internal/themes/service_test.go` returns `ErrThemeInUse` and the row remains. |
| AC-26 | PASS | `TestGetScreenFull_AggregatesPagesAndWidgets` returns 2 pages in position order, 3 widgets total, theme attached. |

## Adversarial Findings

I tried hard to break the implementation. Nothing rises above informational. The implementation is solid; below is the full list of probes I ran and the (passing) tests I committed that document each contract.

### Inputs I tried

- **Empty / whitespace / 65-char / unicode / tab / NUL / ESC / angle-bracket / quote / semicolon / slash / backtick name** — all rejected by `validateScreenInput` via the ASCII whitelist regex. Locked in by `TestAdversarial_ValidateRejectsExoticNameForms` (table-driven, 10 cases).
- **1 MB name string** — rejected (regex caps at 64 chars). Verified in probing.
- **`math.MaxInt32` / `math.MinInt32` / large negative rotation** — rejected. Locked in by `TestAdversarial_ValidateRejectsExtremeRotation`.
- **Whitespace-only `theme_id`** — rejected (`TestValidateScreenInput/whitespace-only theme_id`).
- **Leading/trailing whitespace around a valid name** — accepted and normalised (`validateScreenInput` trims).
- **64-character name** — accepted; 65-char rejected (boundary tests already present in `validate_test.go`).
- **SQL injection in `id`** — neutralised by parameterised query; tables intact. Locked in by `TestAdversarial_SQLInjectionInID` (three classic payloads).
- **SQL meta-chars in widget config JSON** — round-tripped intact through `GetScreenFull` (probed; covered indirectly by ordering test).
- **Widget config with NUL bytes / invalid UTF-8** — preserved as raw bytes; no panic, no truncation.

### Concurrency

- **`go test -race ./internal/screens/... ./internal/themes/...`** — clean.
- **N=8 goroutines racing to `CreateScreen` the same name** — exactly one wins, the other 7 see `ErrDuplicateName` (no bare wrapped errors, no panics). Locked in by `TestAdversarial_CreateConcurrentDuplicateName`.
- The `*db.Queries` and `*sql.DB` are shared between goroutines but the single-connection sqlite pool serialises writes; no observed race conditions.

### Error paths

- **`GetScreenByID` with non-existent id** — `ErrScreenNotFound` (covered by existing tests).
- **`UpdateScreen` with non-existent id** — `ErrScreenNotFound` (existing test).
- **`UpdateScreen` with unknown theme_id** — translated to screens-package `ErrThemeNotFound`, not the raw themes error. Locked in by `TestAdversarial_UpdateScreenUnknownTheme`.
- **`UpdateScreen` to the same name (self-rename)** — succeeds. The UNIQUE constraint excludes the row from matching itself. Locked in by `TestAdversarial_UpdateScreenSameNameIsNoOp`.
- **Operating on a closed `*sql.DB`** — returns an error; does not panic. Locked in by `TestAdversarial_DeleteAfterClosedDBSurfacesError`.
- **Dangling `theme_id` (forced via `PRAGMA foreign_keys = OFF`)** — `GetScreenFull` surfaces the "startup-invariant violation" error wrapping `themes.ErrThemeNotFound`. Locked in by `TestAdversarial_GetScreenFullDanglingTheme`, including the `errors.Is(..., themes.ErrThemeNotFound)` chain assertion.
- **Malformed timestamp in DB row** — `screenFromRow` returns an error, does not panic. Locked in by `TestAdversarial_ScreenFromRowMalformedTimestamp`.

### Theme FK delete precedence

- **Non-default theme referenced by a screen → `Delete`** — returns `ErrThemeInUse`, theme row preserved. Covered by `TestDelete_RejectsThemeInUse`.
- **Default theme that is also referenced → `Delete`** — `ErrCannotDeleteDefault` fires first (the application-layer is_default check precedes the DELETE call, so the FK trap never trips). Covered by `TestDelete_RejectsDefaultBeforeFK`.

### GetScreenFull ordering & shape

- **Pages and widgets inserted in reverse-position order** — service returns them in position order via the `ORDER BY` clauses plus the in-Go map merge. Locked in by `TestAdversarial_GetScreenFullPagesAndWidgetsOrdering` (3 pages reversed, widgets within a page reversed).
- **Page with zero widgets** — `Widgets` slice is non-nil (empty), not `nil`. Locked in by `TestAdversarial_GetScreenFullPageWithoutWidgetsHasNonNilSlice`.
- **Screen with zero pages** — `Pages` slice is non-nil (empty), not `nil`. Covered by the existing `TestGetScreenFull_NoPages`.
- **Empty page-IDs branch is short-circuited** — the service does not call `ListWidgetInstancesByPageIDs([])`; verified by reading the code path and by the no-pages test.

### Security review

- All SQL is sqlc-generated and parameterised. No string concatenation.
- The `nameRe` regex is the only writeable string going into the `name` column; the whitelist rejects every control byte and unicode codepoint.
- Error messages do not leak DB internals to admin callers (they wrap a context message via `fmt.Errorf` with `%w`; the underlying driver string surfaces only if the caller chooses to render it).
- The widget config column accepts arbitrary bytes by design (per-widget validators own the shape in TASK-024); raw bytes round-trip safely.
- The dangling-theme branch in `GetScreenFull` leaks the screen ID and theme ID in the error string. Acceptable: this path is admin-context only and the IDs are opaque hex strings.

### Low-severity observations (no fix required)

- **`UpdateScreen` re-fetches via `GetScreenByID` after the UPDATE** — one extra round-trip per update. Acceptable for the admin path's call volume; mirrors the themes `Create` / `Update` pattern.
- **The two `isUniqueNameViolation` helpers (one in `themes`, one in `screens`) are byte-identical except for the hard-coded table name.** A shared helper in `internal/db` could DRY this. Not in scope for this task.
- **Validation order in `UpdateScreen` runs input validation before the screen-exists check.** So an unauthenticated caller with a junk ID and junk inputs sees a `*ValidationError`, not `ErrScreenNotFound`. This is conventional and matches the themes service; calling it out only for completeness.

## New Tests Written

Added `internal/screens/adversarial_test.go` with 11 test functions (one is table-driven with 10 sub-cases). Coverage:

1. `TestAdversarial_ValidateRejectsExoticNameForms` — 10 byte-level / unicode probes the whitelist must reject.
2. `TestAdversarial_ValidateRejectsExtremeRotation` — `math.MinInt32`, `math.MaxInt32`, etc.
3. `TestAdversarial_CreateConcurrentDuplicateName` — N=8 racing creators; exactly one wins.
4. `TestAdversarial_UpdateScreenSameNameIsNoOp` — self-rename succeeds.
5. `TestAdversarial_UpdateScreenUnknownTheme` — UpdateScreen translates to package-local `ErrThemeNotFound`.
6. `TestAdversarial_SQLInjectionInID` — three classic injection payloads neutralised.
7. `TestAdversarial_GetScreenFullDanglingTheme` — startup-invariant branch surfaces a helpful error chain.
8. `TestAdversarial_GetScreenFullPagesAndWidgetsOrdering` — reverse-order inserts still come back in position order.
9. `TestAdversarial_GetScreenFullPageWithoutWidgetsHasNonNilSlice` — empty page has non-nil `Widgets`.
10. `TestAdversarial_ScreenFromRowMalformedTimestamp` — returns error, does not panic.
11. `TestAdversarial_DeleteAfterClosedDBSurfacesError` — closed DB surfaces error without panic.

No source-code bugs were found that required a fix.

## Green-bar Results

```
gofmt -l .                              -> empty
go vet ./...                            -> clean
go build ./...                          -> clean
go test ./...                           -> all packages OK
go test -race ./internal/screens/... \
        ./internal/themes/...           -> OK
```

## Recommendation

**ACCEPT.** The implementation is well-scoped, faithfully follows the architecture document, mirrors the themes-service idioms, and survives every adversarial probe I tried (input fuzzing, concurrency, SQL injection, error paths, FK precedence, ordering, malformed data, closed DB). The 11 new adversarial tests lock in contracts that a future refactor could otherwise quietly break.
