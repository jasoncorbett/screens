---
task: TASK-023
spec: SPEC-006
status: pass
tested-by: tester
date: 2026-05-22
---

# Review: TASK-023 — screens.Service Page CRUD + reorder (transactional swap)

## Summary

Implementation matches the architecture's negative-position swap algorithm,
honours the spec's "gaps are acceptable" property, and survives all the
adversarial probes I threw at it (NUL bytes, SQL-injection-shaped IDs,
cross-screen authorisation probes, closed-DB calls, concurrent reorders,
concurrent creates, swap-then-rollback paths). The five page methods plus
the two reorder methods compile, pass the green-bar, and pass `go test -race
./internal/screens/...`.

I found **no critical, no high, two medium and two low** severity findings.
All medium findings are documented design choices (the architecture and task
explicitly accept them); I added pinning tests so any future change is
intentional rather than accidental. Recommendation: **ACCEPT**.

## Acceptance Criteria Coverage

| AC     | Description                                                                       | Status | Notes                                                                                          |
|--------|-----------------------------------------------------------------------------------|--------|------------------------------------------------------------------------------------------------|
| AC-11  | CreatePage(screenID, name) writes row with position = max+1 (1 first)             | PASS   | `TestCreatePage_HappyPath` plus pinned `TestAdversarial_PageNameBoundaryLengths`               |
| AC-12  | DeletePage returns nil for existing page; subsequent GetPageByID = ErrPageNotFound; child widgets cascade-deleted | PASS | `TestDeletePage_HappyPath`, `TestDeletePage_CascadesWidgets`                                   |
| AC-13  | Pages at 1,2,3; MovePageDown on position-1 → positions become 2,1,3               | PASS   | `TestMovePageDown_Swaps`                                                                       |
| AC-14  | MovePageUp on top is no-op nil; MovePageDown on bottom is no-op nil               | PASS   | `TestMovePageUp_AtTop_NoOp`, `TestMovePageDown_AtBottom_NoOp`, `TestAdversarial_MovePageNoOpReleasesLockForSubsequentOps` |
| AC-15  | (page half) No duplicate (screen_id, position) rows after any reorder sequence    | PASS   | `TestMovePage_NoTransientDuplicates`, `TestAdversarial_PagePositionsUniqueAfterMixedReorders` |

## Adversarial Findings

### 1. CreatePage TOCTOU race surfaces as bare UNIQUE-constraint errors -- Severity: medium (DOCUMENTED, by design)

`CreatePage` reads `MaxPagePosition` and then `INSERT`s outside a transaction.
Under concurrent calls on the same screen (production case with
`MaxOpenConns > 1`), goroutines can interleave and collide on the
`(screen_id, position)` UNIQUE constraint. The failing goroutines receive a
wrapped raw error string containing `UNIQUE constraint failed: pages.screen_id,
pages.position`.

I reproduced this even under the single-connection test pool because
goroutines still interleave between `MaxPagePosition` and `CreatePage`:

```
goroutine 0 err: create page: constraint failed: UNIQUE constraint failed: pages.screen_id, pages.position (2067)
goroutine 1 err: create page: constraint failed: UNIQUE constraint failed: pages.screen_id, pages.position (2067)
goroutine 2 err: <nil>
goroutine 3 err: <nil>
```

This is **explicitly accepted by TASK-023's Requirements section 1**:
> "The DB-layer UNIQUE constraint on (screen_id, position) should never trip
> because we just looked up the max; if it does (race-y boot), wrap and
> return the error verbatim."

It is also accepted by the architecture (no transaction around
`MaxPagePosition + Insert`). The task is in scope; the race acceptance is
the explicit contract.

- **Reproduction**: `TestAdversarial_ConcurrentCreatePageOnSameScreen` (N=8
  concurrent CreatePage calls).
- **Suggested fix** (out of scope for TASK-023, but worth filing for a
  later cleanup): wrap `MaxPagePosition + INSERT` in a `BEGIN IMMEDIATE`
  transaction; or use a single SQL statement
  `INSERT INTO pages ... SELECT ?, ?, ?, COALESCE(MAX(position), 0) + 1 FROM pages WHERE screen_id = ?`,
  which removes the race entirely. If kept as-is, the wrapped error should
  be translated to a typed `ErrPageCreateConflict` so admin handlers can
  retry rather than surfacing a raw SQLite string in the UI.
- **Action taken**: added a pinning test (`TestAdversarial_ConcurrentCreatePageOnSameScreen`)
  that asserts "at least one wins, no duplicates after, no panic". A future
  fix would intentionally need to update this test, which is the right shape
  for a future architectural change.

### 2. MovePageUp / MovePageDown silently no-ops across a position gap -- Severity: medium (DOCUMENTED, by design)

The architecture's `GetPageNeighbor` query is parameterised on the *exact*
neighbour position (`target.Position ± 1`). If positions have a gap (e.g. an
admin deleted a middle page), the neighbour at the immediate ±1 position is
absent and `swapPage` returns nil with no row mutation -- the page does NOT
move past the gap to the next-occupied position.

Reproduction:

```
after create: {p1:1, p2:2, p3:3}
after delete p2 (pos=2): {p1:1, p3:3}
after create p4 (pos=4): {p1:1, p3:3, p4:4}    -- CreatePage uses MAX+1, leaves gap at 2
after MovePageUp p3:    {p1:1, p3:3, p4:4}    -- no-op: no row at position 2
after MovePageDown p1:  {p1:1, p3:3, p4:4}    -- no-op: no row at position 2
```

This is the documented behaviour per the architecture's "Reorder
Implementation" section and per SPEC-006 FR-15 ("Gaps in positions are
acceptable"). However, the resulting **UX** is awkward: an admin who
deletes a middle page then tries to reorder adjacent pages will see "move
up / move down" do nothing and have no obvious recourse.

- **Reproduction**: `TestAdversarial_DeleteCreatesGapButPositionsRemainUnique`
  and `TestAdversarial_MovePageAcrossGapIsNoOp`.
- **Suggested fix** (out of scope for TASK-023): change
  `GetPageNeighbor` to lookup by `MIN(position) WHERE position > target.Position`
  (for MovePageDown) and `MAX(position) WHERE position < target.Position`
  (for MovePageUp). This makes reorder traverse gaps, which is the
  intuitive UX. The negative-position swap still works because the new
  neighbour's position is still unique. This is a tiny SQL change and a
  larger UX win; worth a follow-up spec / task if Phase-2 admin testing
  surfaces complaints.
- **Action taken**: added a pinning test (`TestAdversarial_MovePageAcrossGapIsNoOp`)
  that documents the current behaviour, so any future change has to
  intentionally update the expectation.

### 3. CreatePage with whitespace-only name silently stores empty string -- Severity: low (per spec)

`validatePageName` trims whitespace; a name of `" "`, `"\t"`, `"\n"`, or
`" \t\n "` becomes `""` and the page is created with an empty name. This
matches SPEC-006 FR-13 (page names MAY be empty) but is the kind of
"silent normalisation" that occasionally surprises admins who think they
typed something.

- **Reproduction**: `TestAdversarial_PageNameTrimmedWhitespaceIsEmpty`.
- **No fix needed**: spec explicitly allows empty names.
- **Action taken**: pinning test documents the trim-to-empty contract so
  it's not changed by accident.

### 4. CreatePage wraps unknown screen FK error with the screen lookup, not the insert -- Severity: low

`CreatePage` does an explicit `GetScreenByID` lookup before computing the
position, returning `ErrScreenNotFound` cleanly. Good. If someone manages
to race a screen delete in between the lookup and the insert, the insert
would surface a raw `FOREIGN KEY constraint failed` error wrapped in
`fmt.Errorf("create page: %w", err)`. The lookup-then-insert pattern keeps
the happy-path error typed but leaves a tiny window for a raw-error leak.

In practice this is a screen-delete-races-page-create scenario that the
spec does not address; the wrapped error contains no secrets and is safe
to surface to admins. **No fix needed.**

## New Tests Added

`internal/screens/pages_adversarial_test.go` (13 tests, all PASS):

1. `TestAdversarial_DeletePageOnWrongScreen` -- cross-screen defence on DELETE.
2. `TestAdversarial_UpdatePageOnWrongScreen` -- cross-screen defence on UPDATE.
3. `TestAdversarial_MovePageOnWrongScreen` -- cross-screen defence on reorder.
4. `TestAdversarial_MovePageNoOpReleasesLockForSubsequentOps` -- verifies the
   no-op reorder path commits the tx (a missing Commit would deadlock under
   the single-connection test pool).
5. `TestAdversarial_PageReorderClosedDBSurfacesError` -- verifies
   MovePageDown on a closed DB returns error, no panic (covers the BeginTx
   error path that the existing closed-DB test for DeleteScreen does not).
6. `TestAdversarial_PagePositionsUniqueAfterMixedReorders` -- a six-step
   reorder sequence; asserts no duplicate (screen_id, position) rows AND
   no negative positions leaked after each step (a missed third UPDATE in
   the swap would leak a -N position).
7. `TestAdversarial_PageNameRejectsControlBytes` -- table-driven fuzz on
   NUL, ESC, unicode accents, angle brackets, slashes, backticks.
8. `TestAdversarial_PageNameTrimmedWhitespaceIsEmpty` -- pins the
   normalisation contract for whitespace-only names.
9. `TestAdversarial_PageNameBoundaryLengths` -- 64 chars accepted, 65 chars
   rejected with `*ValidationError`.
10. `TestAdversarial_SQLInjectionInPageAndScreenIDs` -- four injection
    payloads through all four page-CRUD entrypoints, asserting the
    parameterised queries neutralise them and the `pages` table count is
    unchanged.
11. `TestAdversarial_DeleteCreatesGapButPositionsRemainUnique` -- pins the
    "MAX+1 leaves gaps; uniqueness preserved" contract.
12. `TestAdversarial_MovePageAcrossGapIsNoOp` -- pins the "neighbour
    lookup is exact-position-only" contract.
13. `TestAdversarial_ConcurrentCreatePageOnSameScreen` -- pins the
    documented MaxPosition+Insert TOCTOU race shape (at least one wins,
    no duplicates after).
14. `TestAdversarial_CreatePageWithClosedDBSurfacesError` -- closed-DB
    behaviour for CreatePage (the existing closed-DB test only covered
    DeleteScreen; the early-lookup paths through GetScreenByID, MaxPagePosition,
    and the insert all need to fail cleanly).
15. `TestAdversarial_DeletePageReturnsErrPageNotFoundOnEmptyDB` -- empty
    pages table boundary.

## Green Bar

- `gofmt -l .` -- PASS (empty output)
- `go vet ./...` -- PASS (no findings)
- `go build ./...` -- PASS
- `go test ./...` -- PASS (all packages)
- `go test -race ./internal/screens/...` -- PASS (no races detected, full
  package including the new concurrent tests)

## Test Results

```
$ go test ./...
ok      github.com/jasoncorbett/screens/api
ok      github.com/jasoncorbett/screens/internal/auth
ok      github.com/jasoncorbett/screens/internal/config
ok      github.com/jasoncorbett/screens/internal/db
ok      github.com/jasoncorbett/screens/internal/middleware
ok      github.com/jasoncorbett/screens/internal/screens
ok      github.com/jasoncorbett/screens/internal/themes
ok      github.com/jasoncorbett/screens/internal/widget
ok      github.com/jasoncorbett/screens/internal/widget/text
ok      github.com/jasoncorbett/screens/views

$ go test -race ./internal/screens/...
ok      github.com/jasoncorbett/screens/internal/screens        3.234s
```

## Recommendation

**ACCEPT**.

All five required methods (`CreatePage`, `GetPageByID`, `UpdatePage`,
`DeletePage`, `MovePageUp`, `MovePageDown`) are implemented and behave per
the spec. The transactional negative-position swap is correctly implemented
and survives mixed-reorder sequences without leaking negative or duplicate
positions. Cross-screen defence-in-depth is wired through every entrypoint.
SQL injection is structurally impossible (sqlc parameterisation throughout).
All medium-severity findings are pre-documented design choices (the task
text explicitly accepts the CreatePage race; the architecture explicitly
defines the exact-position neighbour lookup); the new pinning tests will
flag any unintentional drift.

## Follow-Up Tasks Needed (optional / out-of-scope)

- **Optional follow-up**: wrap `CreatePage` in a `BEGIN IMMEDIATE`
  transaction (or use a `INSERT ... SELECT MAX+1 FROM pages WHERE screen_id`
  pattern) to eliminate the documented TOCTOU race in production
  (`MaxOpenConns > 1`). Would also remove the raw SQLite error string from
  the admin-visible error path. Same change applies to TASK-024
  (`AddWidget` has the identical `MaxWidgetPosition + INSERT` pattern).
- **Optional follow-up**: change `GetPageNeighbor` to a
  `MIN/MAX(position) WHERE position </> target.Position` lookup so reorder
  traverses gaps left by deletes -- the documented "Move up does nothing
  because the previous page was deleted" UX is unintuitive.

Neither blocks acceptance of TASK-023; both are recommendations for a
later iteration.
