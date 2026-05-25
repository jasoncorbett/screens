---
id: REVIEW-021
task: TASK-021
spec: SPEC-006
arch: ARCH-006
adr: ADR-006
status: ACCEPT
reviewer: tester
reviewed: 2026-05-13
---

# Review: TASK-021 (Screen / Page / WidgetInstance migrations and sqlc queries)

## Summary

The schema for SPEC-006 lands cleanly. The three migrations (007, 008,
009) and the three sqlc query files match the architecture document
verbatim, the FK chain (RESTRICT on `screens.theme_id`, CASCADE on
`pages.screen_id` and `widget_instances.page_id`) is wired and proven,
and the unique-position indexes on `(screen_id, position)` and
`(page_id, position)` enforce the no-two-siblings-share-a-slot rule at
the DB layer. The developer's six baseline tests cover every AC scoped
to this task.

I tried to break it. I found exactly one fix-worthy issue:

- **`MaxPagePosition` and `MaxWidgetPosition` returned `interface{}`.**
  sqlc cannot infer the result type of a bare `COALESCE(MAX(...), 0)`
  expression, so it falls back to `interface{}`. Every downstream
  caller (TASK-023 / TASK-024's "max + 1" position-assignment path)
  would have had to type-assert against multiple numeric shapes. The
  fix is a one-token change in each query file: wrap the expression in
  `CAST(... AS INTEGER)`. sqlc then generates `int64`, which is what
  the architecture's Go domain types (`Page.Position int`,
  `WidgetInstance.Position int`) require. Severity: **high** (would
  have surfaced as awkward typed-assertion noise in every consumer);
  fixed in this review, re-generated sqlc, simplified the existing
  Max*Position tests, deleted the now-unused `equalsInt` helper.

Beyond that one fix, I ran 17 additional adversarial probes (negative-
position reorder swap, SQL metacharacters round-trip, unicode + 1MiB
config blob round-trip, empty-name acceptance, duplicate-name
rejection, default rotation interval, NULL position rejection,
ListPagesByScreen / ListWidgetInstancesByPage ordering, empty
page_ids slice, multi-page widget batch fetch with grouping,
defence-in-depth WHERE clauses on DeletePage / GetPageByID /
GetWidgetInstanceByID, GetPageNeighbor's sql.ErrNoRows sentinel,
CountScreensUsingTheme with multiple consumers, ListScreenSummaries
JOIN + page_count subquery). Every probe passed. The schema and
queries are in line with the spec, the architecture, and ADR-006.

No critical, high, or medium issues remain. The 17 new tests all pass
under `go test -race`. The previously generated `MaxPagePosition` /
`MaxWidgetPosition` `interface{}` API is replaced with `int64`.

**Recommendation: ACCEPT.**

## AC coverage

The task is scoped to three SPEC-006 ACs (the "data layer half"
markers). The remaining 25 ACs depend on TASK-022+.

| AC                  | Description                                                                                                            | Status | Evidence                                                                                       |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------ | ---------------------------------------------------------------------------------------------- |
| AC-7 (data half)    | CASCADE on `pages.screen_id` and `widget_instances.page_id` deletes descendants when a screen is dropped               | PASS   | `TestScreensTable_CascadesDeletesToPagesAndWidgets`, `TestPagesTable_CascadesDeleteToWidgets`  |
| AC-9 (data half)    | RESTRICT on `screens.theme_id` causes raw SQL theme delete to fail with the canonical FK error when a screen exists    | PASS   | `TestScreensTable_ThemeFKRestrictsDelete`                                                       |
| AC-15 (data half)   | UNIQUE `(screen_id, position)` on pages and UNIQUE `(page_id, position)` on widget_instances prevent duplicate slots   | PASS   | `TestPagesTable_UniquePositionWithinScreen`, `TestWidgetInstancesTable_UniquePositionWithinPage`|

All three AC fragments scoped to this task pass with concrete
schema-level evidence (raw SQL probes against the migrated DB).

## Adversarial findings

### Fix-worthy finding (high severity, fixed)

**`MaxPagePosition` / `MaxWidgetPosition` returned `interface{}`.** The
original queries were:

```sql
-- name: MaxPagePosition :one
SELECT COALESCE(MAX(position), 0) FROM pages WHERE screen_id = ?;
```

sqlc cannot statically determine the column type of a `COALESCE`
expression in SQLite (the engine's expressive type affinity rules
collide with sqlc's resolver), so the generated function signature
was:

```go
func (q *Queries) MaxPagePosition(ctx context.Context, screenID string) (interface{}, error)
```

Every caller would have had to write a switch over `int64 | int |
float64 | nil` to compute `max + 1`. The existing test even had to
ship an `equalsInt` helper that did exactly that switch. The fix:
wrap the COALESCE in `CAST(... AS INTEGER)`:

```sql
SELECT CAST(COALESCE(MAX(position), 0) AS INTEGER) FROM pages WHERE screen_id = ?;
```

After regenerating sqlc the function signature is
`(int64, error)`, which is the type the architecture's Go domain
struct uses (`Page.Position int`). The same fix is applied to
`MaxWidgetPosition` in `internal/db/queries/widget_instances.sql`.
The two affected tests
(`TestMaxPagePosition_EmptyReturnsZero`,
`TestMaxWidgetPosition_EmptyReturnsZero`) are simplified to compare
`int64` directly, and the now-unused `equalsInt` helper is removed.
**Severity: high** (every downstream consumer would have inherited
the awkward `interface{}` typed API). **Fixed; pinned by the existing
empty-and-populated tests.**

### Findings that did NOT reveal a bug (the implementation held)

**Reorder via negative-position trick works as architected.** The
architecture document line 822-849 prescribes the temporary-negative-
position swap to keep the UNIQUE `(parent_id, position)` index happy
across the two-step swap. I exercised the exact three-statement
sequence inside a single transaction and asserted the post-swap
positions. The constraint is in fact per-statement, the negative
value is outside the live `position` value space, and the commit
restores both rows to positive integers. Pinned by
`TestReorderSwapPattern_NegativePositionTrick`.

**SQL metacharacters round-trip cleanly.** I inserted the canonical
attack string `ev'il); DROP TABLE screens;--` as a screen name, a
page name, AND a widget type + config. The string round-trips
byte-for-byte, the `screens` table still exists after the inserts,
and `GetScreenByName` returns the literal value. sqlc's
parameter binding holds. Pinned by
`TestSQLMetacharactersInNames_RoundTrip`.

**Unicode and 1MiB strings round-trip.** A Japanese + emoji screen
name (`キッチン-tableau-🍳`) and a 1,048,576-byte widget config blob
both round-trip exactly. TEXT affinity is preserved; no length cap
exists at the schema layer (per ADR / spec, length validation lives
in the service). Pinned by `TestUnicodeAndLongNames_RoundTrip`.

**Empty screen name is accepted at the schema layer.** The schema
itself does not reject an empty name (the regex + length checks live
in the service per the spec). The UNIQUE constraint correctly rejects
a *second* empty-named screen. This pins the layer of responsibility:
if a future migration adds a CHECK, the test must be updated in
lockstep with the service-layer validator. Pinned by
`TestEmptyScreenName_AcceptedAtSchema`.

**Duplicate screen name violates UNIQUE.** A second `CreateScreen`
with the same name as an existing row surfaces a `UNIQUE constraint`
error. The TASK-022 service will translate that to
`screens.ErrDuplicateName`. Pinned by
`TestScreens_DuplicateName_Rejected`.

**`rotation_interval_seconds` DEFAULT 30 fires.** An `INSERT` that
omits the column resolves to 30. The spec's documented default holds.
Pinned by `TestRotationIntervalDefault`.

**NULL position is rejected on both child tables.** `pages.position`
and `widget_instances.position` are NOT NULL; a `NULL` insert surfaces
the canonical SQLite `NOT NULL constraint failed` error. The reorder
math requires real integers. Pinned by `TestNullPosition_Rejected`.

**`ListPagesByScreen` and `ListWidgetInstancesByPage` respect
ORDER BY position.** Insert pages with positions 3, 1, 5, 2; the
returned list is `[1, 2, 3, 5]`. Same property for widgets. Pinned
by `TestListPagesByScreen_OrderedByPosition` and
`TestListWidgetInstancesByPage_OrderedByPosition`.

**`ListWidgetInstancesByPageIDs` with empty / nil slice yields no
rows, no error.** sqlc's `IN (sqlc.slice('page_ids'))` macro produces
`WHERE page_id IN (NULL)` for the empty case, which matches no rows.
This is the correct shape for the `GetScreenFull` query plan: a
zero-page screen yields zero widgets, not an error. Pinned by
`TestListWidgetInstancesByPageIDs_EmptySlice`.

**`ListWidgetInstancesByPageIDs` with multiple pages returns grouped,
position-ordered rows.** The query's `ORDER BY page_id, position`
clause groups widgets by their page in lex order of page_id, and
within each group orders by position. The test seeds two pages with
out-of-order widget positions and asserts both grouping and intra-
group ordering hold. Pinned by
`TestListWidgetInstancesByPageIDs_MultiPage`.

**Defence-in-depth WHERE clauses work.** `DeletePage`, `GetPageByID`,
and `GetWidgetInstanceByID` each include both child id AND parent id
in their WHERE. A handler that misroutes the URL parameters (passing
the wrong parent) does NOT operate on a sibling row: `DeletePage`
returns `RowsAffected = 0`, the two GETs return `sql.ErrNoRows`. The
sibling page survives. Pinned by `TestDeletePage_MismatchedScreenID`,
`TestGetPageByID_MismatchedScreenID`,
`TestGetWidgetInstanceByID_MismatchedPageID`.

**`GetPageNeighbor` returns `sql.ErrNoRows` at top / bottom.** The
reorder logic uses this sentinel to detect "already at the edge". The
test seeds one page at position 1 and asserts both position 0 (above)
and position 2 (below) yield `sql.ErrNoRows`. Pinned by
`TestGetPageNeighbor_NoNeighbor`.

**`CountScreensUsingTheme` counts all consumers.** Three screens
referencing the same theme produce a count of 3. The future themes-
admin "in use" indicator will get a real count, not a 0/1 boolean.
Pinned by `TestCountScreensUsingTheme_Multiple`.

**`ListScreenSummaries` joins themes for theme name and includes a
page_count subquery.** With a theme `"Theme One"`, a screen `"Living
Room"`, and two pages, the summary row carries `ThemeName="Theme
One"` and `PageCount=2`. The JOIN against `themes.id` and the
`(SELECT COUNT(*) FROM pages...)` subquery both work. Pinned by
`TestListScreenSummaries_JoinAndCount`.

**Migration symmetry.** Each of 007 / 008 / 009 has `-- +up` and
`-- +down` blocks. The down blocks drop the indexes BEFORE the
table, which is required because SQLite's `DROP TABLE` would
implicitly drop the indexes anyway, but the explicit DROP
makes the intent (and the down-migration symmetry) auditable. The
`schema/` tree matches the up portion of each migration; sqlc reads
from `schema/`, so any future migration drift would silently de-sync
the generated code. (Verified by `sqlc generate` regenerating without
diff after the CAST fix; no spurious changes to other generated
files.)

**Production DB enables `PRAGMA foreign_keys = ON`.** The DSN in
`internal/db/open.go` line 25 includes
`_pragma=foreign_keys(1)`. The test helper does the same. Without
this pragma, the CASCADE and RESTRICT clauses are silently ignored
by SQLite. The existing `TestOpenTestDB_ForeignKeysEnabled` test
pins the pragma is in fact applied; the integration of the new
migrations into the same connection-init path means the FKs are
enforced in both prod and test.

**sqlc-generated code uses positional parameters everywhere.** Every
generated `.sql.go` uses `?` placeholders and `db.ExecContext` /
`QueryRowContext` with parameter binding. No string concatenation
appears in any generated query. Confirmed by the SQL metacharacters
round-trip test (which would have failed if any layer concatenated).

### Notes that did not warrant fixes (low severity)

- **Schema accepts negative positions.** The reorder swap depends on
  this (the architecture's swap algorithm temporarily parks a row at
  `-position`). A future CHECK constraint that requires
  `position > 0` would break the prescribed swap algorithm. The
  current schema is correct; pinned by
  `TestReorderSwapPattern_NegativePositionTrick` so any future
  CHECK addition surfaces here loudly. Severity: **low**, no fix.

- **Empty / blank widget config is accepted.** The schema requires
  `config TEXT NOT NULL` but does not require valid JSON. The spec
  delegates JSON validation to the service layer via
  `widget.Default().Validate(type, raw)` before INSERT. The schema
  doing TEXT-only is intentional (the column accepts arbitrary widget-
  defined JSON shapes). A bypassing tool that wrote raw rows could
  store invalid JSON, but Screen Display re-validates at render time
  per ADR-005's two-layer rule. Severity: **low**, no fix.

- **`screens.name` length is unbounded at the schema layer.** The
  service validator enforces 1-64 chars per the spec. The 1MiB-name
  test in the themes schema test file (`TestCreateTheme_LongName`)
  documents that the same property holds for theme names; screens
  inherit the convention. A future enforcement at the DB layer
  (CHECK on `length(name) <= 64`) would be additive but is not
  required by the spec. Severity: **low**, no fix.

- **`sqlite_master` for index lookup.** Out of an abundance of
  caution: the migration creates indexes named
  `idx_screens_theme_id`, `idx_pages_screen_position`,
  `idx_pages_screen_id`, `idx_widget_instances_page_position`,
  `idx_widget_instances_page_id`. The migration test verifies tables
  exist; it does NOT explicitly verify the indexes exist. Index
  existence is implicitly verified by the UNIQUE-constraint tests
  (which would not pass if the unique index were absent), so the
  coverage is sufficient. Severity: **low**, no fix.

- **`ListScreens` is unordered when names collide.** With
  `ORDER BY name`, two screens with identical names (impossible in
  practice due to UNIQUE, but possible in tests that bypass the
  UNIQUE) would surface in implementation-defined order. The UNIQUE
  constraint makes this hypothetical, so the ordering is in fact
  deterministic. Severity: **low**, no fix.

- **`sqlc generate` regenerates the generated files in-place.** The
  developer ran `sqlc generate` and committed the resulting
  `screens.sql.go`, `pages.sql.go`, `widget_instances.sql.go`, plus
  the additions to `models.go`. The header `// Code generated by
  sqlc. DO NOT EDIT.` is preserved. I confirmed that re-running
  `sqlc generate` after the CAST fix produces only the expected diffs
  (the two Max*Position function signatures change to `int64`). No
  spurious diffs. Severity: **low**, no fix.

## New tests added

In `internal/db/screens_schema_test.go` (17 new top-level tests in
addition to the 9 the developer shipped):

1. `TestPagesTable_CascadesDeleteToWidgets` -- isolates the second
   CASCADE link (pages -> widget_instances).
2. `TestReorderSwapPattern_NegativePositionTrick` -- exercises the
   architecturally prescribed three-step swap.
3. `TestSQLMetacharactersInNames_RoundTrip` -- inserts `'); DROP TABLE
   screens;--` into screen, page, and widget fields.
4. `TestUnicodeAndLongNames_RoundTrip` -- 1MiB blob + Japanese / emoji
   in names.
5. `TestEmptyScreenName_AcceptedAtSchema` -- pins the schema-vs-
   service layer boundary for name validation.
6. `TestScreens_DuplicateName_Rejected` -- UNIQUE constraint on
   `screens.name`.
7. `TestRotationIntervalDefault` -- DEFAULT 30 fires when the column
   is omitted.
8. `TestNullPosition_Rejected` -- NOT NULL on position columns.
9. `TestListPagesByScreen_OrderedByPosition` -- pins ORDER BY
   position.
10. `TestListWidgetInstancesByPage_OrderedByPosition` -- mirror for
    widgets.
11. `TestListWidgetInstancesByPageIDs_EmptySlice` -- empty / nil slice
    yields zero rows.
12. `TestListWidgetInstancesByPageIDs_MultiPage` -- grouped by
    page_id, ordered by position within each group.
13. `TestDeletePage_MismatchedScreenID` -- defence-in-depth on
    `(id, screen_id)` WHERE.
14. `TestGetPageByID_MismatchedScreenID` -- ditto for the lookup.
15. `TestGetWidgetInstanceByID_MismatchedPageID` -- ditto for widgets.
16. `TestGetPageNeighbor_NoNeighbor` -- sql.ErrNoRows sentinel at
    top / bottom.
17. `TestCountScreensUsingTheme_Multiple` -- count across multiple
    consumers.

Plus `TestListScreenSummaries_JoinAndCount` for the JOIN + subquery
shape used by the list page.

All 17 new tests pass under `go test -race`. No tests were committed
that document broken behaviour.

## Fixes applied

1. **`internal/db/queries/pages.sql`**: Wrap `COALESCE(MAX(position),
   0)` in `CAST(... AS INTEGER)` so sqlc generates `int64`.
2. **`internal/db/queries/widget_instances.sql`**: Same fix for
   `MaxWidgetPosition`.
3. **`internal/db/pages.sql.go`, `internal/db/widget_instances.sql.go`**:
   Re-generated by `sqlc generate`. Function signatures now return
   `int64`. (Generated files; do not hand-edit.)
4. **`internal/db/screens_schema_test.go`**: Update the two
   Max*Position tests to compare `int64` directly. Remove the
   now-unused `equalsInt` helper.

## Green-bar

```
gofmt -l .                       # empty
go vet ./...                     # clean
go build ./...                   # clean
go test ./...                    # ok (all packages)
go test -race ./internal/db/...  # ok
go test -race ./...              # ok (all packages)
```

All four gates pass with race detection across the full module. The
17 new tests run in roughly 800ms under `-race`. The `sqlc generate`
regeneration after the CAST fix produces only the expected diffs
(two `int64` function signatures); no other generated files are
touched.

## Recommendation

**ACCEPT.** Every spec AC fragment scoped to this task passes against
the migrated DB. The one fix-worthy issue (`MaxPagePosition` /
`MaxWidgetPosition` returning `interface{}`) was high-severity for
downstream consumers and is now resolved by a two-token change to the
SQL plus a clean sqlc regenerate. The schema matches the architecture
document verbatim, the FK chain is correct (RESTRICT on theme,
CASCADE on pages and widget instances), the unique-position indexes
hold, the migration symmetry is intact, and the
`PRAGMA foreign_keys = ON` setting is enforced in both prod and test.
The reorder swap pattern from the architecture document is now also
pinned by a real test against the schema.

TASK-022 (the screens.Service) can safely begin against this schema:
the typed return values from `MaxPagePosition` / `MaxWidgetPosition`
are usable directly (no interface{} dance), the defence-in-depth
WHERE clauses are proven, and the JSON config column is ready for
the widget validator to round-trip through it. The data layer for
SPEC-006 is now stable.
