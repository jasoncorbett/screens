---
task: TASK-024
spec: SPEC-006
status: pass
tested-by: tester
date: 2026-05-22
---

# Review: TASK-024 — screens.Service Widget instance CRUD + reorder + registry validation

## Summary

The widget-instance CRUD layer mirrors the page-CRUD layer in shape (same
transactional negative-position swap, same defence-in-depth `GetPageByID`
upstream check, same TOCTOU-acceptance contract on the position arithmetic),
and the registry round-trip on `AddWidget` is correct: a registered widget
whose `DefaultConfig()` fails its own `ValidateConfig` is rejected with a
clear wrapped error and no row is written.

I threw 31 adversarial tests at the implementation across registry
mis-configuration, cross-screen/cross-page authorisation, SQL injection in
every ID slot, concurrent AddWidget, concurrent mixed reorder, closed-DB
panic probes, byte-level config round-trips, unicode and NUL byte config
storage, position-boundary edge cases, and the cascade interaction with the
new AddWidget path. **Everything held up.** No critical, no high, no medium
findings; one low (documented design choice, see below). Recommendation:
**ACCEPT**.

## Acceptance Criteria Coverage

| AC     | Description                                                                                  | Status | Notes                                                                                                                              |
|--------|----------------------------------------------------------------------------------------------|--------|------------------------------------------------------------------------------------------------------------------------------------|
| AC-16  | AddWidget(ctx, screenID, pageID, "text") writes row with type="text", DefaultConfig, pos=max+1 | PASS   | `TestAddWidget_HappyPath`, `TestAdversarial_AddWidget_FirstWidgetOnEmptyPageStartsAtPos1`, `TestAdversarial_AddWidget_ManySequential` |
| AC-17  | AddWidget(..., "nonexistent") returns ErrUnknownWidgetType; no row created                   | PASS   | `TestAddWidget_UnknownType`, `TestAdversarial_AddWidget_EmptyRegistry`, `TestAdversarial_AddWidget_TypeFuzzing`                    |
| AC-18  | DeleteWidget on existing widget returns nil; GetScreenFull no longer includes it             | PASS   | `TestDeleteWidget_HappyPath`, indirectly `TestAdversarial_DeletePage_CascadesAddedWidgets`                                         |
| AC-19  | Widgets at 1 and 2; MoveWidgetUp on pos 2 → pos 2 and 1 swapped                              | PASS   | `TestMoveWidgetUp_Swaps`, `TestMoveWidgetDown_Swaps`, `TestMoveWidget_RoundTrip`                                                  |
| AC-20  | text DefaultConfig → AddWidget → GetScreenFull bytes pass registry.Validate("text", config)  | PASS   | `TestAddWidget_DefaultValidates`, `TestAdversarial_GetScreenFull_WidgetConfigByteForByteRoundTrip`                                |
| AC-26 (widget half) | GetScreenFull on screen+2 pages+3 widgets returns pages and widgets in position order | PASS   | `TestGetScreenFull_IncludesAddedWidgets`, `TestAdversarial_GetScreenFull_MultiPageWidgetsOnPositionOrder`                          |

## Adversarial Findings

### 1. AddWidget TOCTOU race on MaxWidgetPosition+1 — Severity: low (DOCUMENTED, mirrors TASK-023)

`AddWidget` reads `MaxWidgetPosition` and then `INSERT`s outside a
transaction. Under concurrent calls on the same page, two goroutines can
both read `MAX=3` and both try to `INSERT` at position 4; the second loses
to the UNIQUE constraint with a wrapped `UNIQUE constraint failed:
idx_widget_instances_page_position` error.

This is the same TOCTOU pattern the task explicitly accepted for the page
layer (TASK-023's `TestAdversarial_ConcurrentCreatePageOnSameScreen`). The
contract is:

- At least one concurrent `AddWidget` succeeds.
- Failures carry a `UNIQUE constraint failed` substring (not a panic, not a
  bare driver error).
- The final state has no duplicate `(page_id, position)` rows.

`TestAdversarial_ConcurrentAddWidgetOnSamePage` pins all three. A future
refactor that wraps the read+insert in a transaction (recommended at higher
connection counts) will turn this from "some fail" to "all succeed in
position 1..N"; that test's `ok < 1` assertion is the only thing that needs
updating, and the change is intentional.

### What I tried that did NOT find a bug

- Registry with a widget whose `DefaultConfig` returns bytes its own
  `ValidateConfig` rejects → `AddWidget` returns wrapped error "default
  config failed validation", no row inserted
  (`TestAdversarial_AddWidget_DefaultFailsOwnValidator`).
- Registry with a widget returning `nil` from `DefaultConfig` → JSON-based
  validators reject nil bytes → same defensive error, no panic
  (`TestAdversarial_AddWidget_NilDefaultConfig`).
- Registry with a widget returning `[]byte{}` and a permissive validator →
  stored as empty string, round-trips through `GetScreenFull` cleanly
  (`TestAdversarial_AddWidget_EmptyByteDefault`).
- Default config bytes containing NUL (\x00) → read paths agree on whatever
  the driver does; `AddWidget` and `GetScreenFull` return the same bytes
  (`TestAdversarial_AddWidget_DefaultConfigWithNULBytes`).
- Default config bytes containing unicode (emoji, accents) → byte-identical
  round-trip through SQLite (`TestAdversarial_AddWidget_DefaultConfigBytesAreUnicode`).
- Whitespace and key-order preserved verbatim in storage (no JSON re-marshal)
  (`TestAdversarial_AddWidget_ConfigBytesStoredVerbatim`).
- Empty registry → `AddWidget` of any type returns `ErrUnknownWidgetType`,
  no panic on nil-map lookup (`TestAdversarial_AddWidget_EmptyRegistry`).
- Widget type fuzzing: empty string, NUL, unicode, uppercase, 100k chars,
  SQL meta-chars, whitespace-wrapped → all → `ErrUnknownWidgetType`
  (`TestAdversarial_AddWidget_TypeFuzzing`).
- Duplicate registration of the same `Type` is rejected by the registry
  with an error containing "already registered"; the first registration
  wins; `AddWidget` continues to resolve to the first
  (`TestAdversarial_RegistrationDuplicateRejected`).
- AddWidget where the page belongs to a different screen →
  `ErrPageNotFound`, no row inserted on either page
  (`TestAdversarial_AddWidget_PageBelongsToDifferentScreen`).
- DeleteWidget where the widget exists but on a different page →
  `ErrWidgetNotFound`, widget unchanged on its real page
  (`TestAdversarial_DeleteWidget_OnWrongPage`).
- DeleteWidget where the page belongs to a different screen →
  `ErrPageNotFound` from the upstream `GetPageByID` check
  (`TestAdversarial_DeleteWidget_PageBelongsToDifferentScreen`).
- MoveWidget mismatched-ID matrix: wrong screen / wrong page / wrong
  widget combinations all fire the appropriate error and leave positions
  unchanged (`TestAdversarial_MoveWidget_MismatchedIDs`).
- Moves on page A do not perturb widgets on page B even when both pages
  share position numbers (`TestAdversarial_MoveWidget_CrossPageOnSameScreen`).
- Five-widget dense reorder sequence: no duplicates, no negative position
  leaks, no off-by-one (`TestAdversarial_MoveWidget_DenseSequence`).
- At-top `MoveWidgetUp` and at-bottom `MoveWidgetDown` no-op paths commit
  the (empty) transaction cleanly; subsequent reads/writes succeed without
  hanging on a stale write lock (`TestAdversarial_MoveWidget_NoOpReleasesLock`).
- Move across a gap (left by a delete) is a no-op rather than skip-swap;
  pinned as the current behaviour
  (`TestAdversarial_MoveWidget_AcrossGapIsNoOp`).
- AddWidget after DeleteWidget continues from `MAX(position)+1` (creating
  gaps, never reusing slots)
  (`TestAdversarial_AddWidget_AfterDelete_PositionContinuesFromMax`).
- `AddWidget` / `DeleteWidget` / `MoveWidgetUp/Down` on a closed DB return
  wrapped errors, no panic (`TestAdversarial_AddWidget_ClosedDBSurfacesError`,
  `TestAdversarial_DeleteWidget_ClosedDBSurfacesError`,
  `TestAdversarial_WidgetReorderClosedDBSurfacesError`).
- SQL-injection probes in `widgetID`, `pageID`, `screenID` parameters of
  every widget-targeted entry point → table intact, no rows mutated, the
  widget's `type` column unchanged
  (`TestAdversarial_SQLInjectionInWidgetAndPageIDs`).
- `DeletePage` cascade-deletes widget instances that were created via the
  new `AddWidget` method (not just raw-SQL fixtures)
  (`TestAdversarial_DeletePage_CascadesAddedWidgets`).
- `GetScreenFull` returns widget config bytes byte-identical to what
  `AddWidget` returned (cache-keying invariant)
  (`TestAdversarial_GetScreenFull_WidgetConfigByteForByteRoundTrip`).
- `GetScreenFull` returns pages and widgets in position order even when
  widgets were interleaved across pages during creation and then reordered
  (`TestAdversarial_GetScreenFull_MultiPageWidgetsOnPositionOrder`).
- Concurrent `MoveWidgetUp/Down` from 4 goroutines × 5 iterations each:
  no race detector hits, no deadlock, no duplicates, no negative leaks
  (`TestAdversarial_ConcurrentMixedReorder`).
- 25 sequential `AddWidget` calls produce a dense 1..25 position sequence
  (`TestAdversarial_AddWidget_ManyWidgets`).
- `DefaultConfig` is invoked fresh on every `AddWidget` call (no caching
  that would break a widget type that wanted to embed e.g. a timestamp)
  (`TestAdversarial_AddWidget_DefaultConfigInvokedEveryCall`).

## New Tests Written

All in `/Users/jcorbett/dev/screens/internal/screens/widgets_adversarial_test.go`
(31 top-level tests, several with subtests):

- Registry mis-configuration: `TestAdversarial_AddWidget_DefaultFailsOwnValidator`, `TestAdversarial_AddWidget_NilDefaultConfig`, `TestAdversarial_AddWidget_EmptyByteDefault`, `TestAdversarial_AddWidget_EmptyRegistry`, `TestAdversarial_RegistrationDuplicateRejected`, `TestAdversarial_AddWidget_DefaultConfigInvokedEveryCall`.
- Config byte preservation: `TestAdversarial_AddWidget_ConfigBytesStoredVerbatim`, `TestAdversarial_AddWidget_DefaultConfigWithNULBytes`, `TestAdversarial_AddWidget_DefaultConfigBytesAreUnicode`, `TestAdversarial_GetScreenFull_WidgetConfigByteForByteRoundTrip`.
- Widget type fuzzing: `TestAdversarial_AddWidget_TypeFuzzing` (11 subcases including SQL injection, NUL, 100k char, case-sensitivity, whitespace).
- Cross-screen / cross-page authorisation: `TestAdversarial_AddWidget_PageBelongsToDifferentScreen`, `TestAdversarial_DeleteWidget_OnWrongPage`, `TestAdversarial_DeleteWidget_PageBelongsToDifferentScreen`, `TestAdversarial_MoveWidget_MismatchedIDs` (3×2 subcases), `TestAdversarial_MoveWidget_CrossPageOnSameScreen`.
- Position-arithmetic / reorder algorithm: `TestAdversarial_MoveWidget_DenseSequence`, `TestAdversarial_MoveWidget_NoOpReleasesLock`, `TestAdversarial_MoveWidget_AcrossGapIsNoOp`, `TestAdversarial_AddWidget_FirstWidgetOnEmptyPageStartsAtPos1`, `TestAdversarial_AddWidget_ManySequential`, `TestAdversarial_AddWidget_ManyWidgets`, `TestAdversarial_AddWidget_AfterDelete_PositionContinuesFromMax`.
- GetScreenFull integration via service-only construction: `TestAdversarial_GetScreenFull_MultiPageWidgetsOnPositionOrder`.
- Cascade: `TestAdversarial_DeletePage_CascadesAddedWidgets`.
- SQL injection: `TestAdversarial_SQLInjectionInWidgetAndPageIDs`.
- Concurrency: `TestAdversarial_ConcurrentAddWidgetOnSamePage`, `TestAdversarial_ConcurrentMixedReorder`.
- Closed-DB resilience: `TestAdversarial_AddWidget_ClosedDBSurfacesError`, `TestAdversarial_DeleteWidget_ClosedDBSurfacesError`, `TestAdversarial_WidgetReorderClosedDBSurfacesError`.

Each test fits the project's "earn their existence" testing rule: each one
either exercises a contract the next refactor could plausibly break, or
pins a documented design choice (gap-creating delete, TOCTOU acceptance,
no-op-across-gap) so a future change is intentional.

## Green-Bar Results

```
$ gofmt -l .          # (no output)
$ go vet ./...        # (no output)
$ go build ./...      # (no output)
$ go test ./...       # all packages OK
$ go test -race ./... # all packages OK
```

The full `go test -race ./internal/screens/...` run completes in ~3.0
seconds. The concurrent tests (`TestAdversarial_ConcurrentAddWidgetOnSamePage`,
`TestAdversarial_ConcurrentMixedReorder`, plus the
`TestAdversarial_ConcurrentCreatePageOnSameScreen` from TASK-023) all
clear the race detector.

## Recommendation

**ACCEPT.** The implementation matches the spec, the architecture, and the
defensive-programming patterns established by TASK-023. All acceptance
criteria pass with multiple test paths covering each. No critical, high, or
medium findings; one low finding (the documented TOCTOU on `AddWidget`) is
pinned by an adversarial test so a future refactor is intentional.
