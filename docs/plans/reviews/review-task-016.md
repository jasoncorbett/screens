---
id: REVIEW-016
task: TASK-016
spec: SPEC-004
arch: ARCH-004
status: ACCEPT
reviewer: tester
reviewed: 2026-04-30
---

# Review: TASK-016 (Theme Config, Migration, and sqlc Queries)

## Summary

The implementation is a clean, narrowly-scoped data-layer foundation. The new
`ThemeConfig` sub-struct mirrors the existing `AuthConfig` / `DBConfig`
shape; migration `006_create-themes.sql` matches the architecture doc
verbatim and applies cleanly; the partial unique index `themes_one_default`
correctly enforces "at most one default" at the schema level; and all ten
sqlc queries listed in the task are present, parameterised, and behave
exactly the way TASK-017's service layer will need them to.

I tried hard to break this and the SQL surface holds up: parameterised
inserts shrug off classic injection payloads, the partial unique index
turns a missed transaction into a hard error rather than silent corruption,
`UpdateTheme` does not touch `is_default`, `DeleteTheme` correctly refuses
to delete the default row, and `SetDefaultTheme` returns
`RowsAffected = 0` when no row matches the id.

One **low-severity defense-in-depth gap** in the config validator was
fixed in this review (whitespace-only `THEME_DEFAULT_NAME` previously
slipped through the bare `== ""` check). All other adversarial probes
came back clean.

**Recommendation: ACCEPT** with the whitespace validator hardening
applied.

## AC coverage

| AC                              | Description                                                                  | Status | Evidence                                                                                |
| ------------------------------- | ---------------------------------------------------------------------------- | ------ | --------------------------------------------------------------------------------------- |
| AC-32 (task scope)              | THEME_DEFAULT_NAME unset -> parsed config equals "default"                  | PASS   | `TestLoadThemeDefaultName/default_when_unset`                                            |
| AC schema prereq                | `themes` table exists with all documented columns                            | PASS   | `TestThemesTable_ExistsAfterMigration` + `TestThemesTable_DefaultColumnDefaults`         |
| AC schema prereq                | `themes_one_default` partial unique index exists                             | PASS   | `TestThemesTable_ExistsAfterMigration` (sqlite_master query)                            |
| AC schema prereq                | OpenTestDB(t) accepts the documented INSERT/UPDATE/DELETE shapes             | PASS   | `TestUpdateTheme_PreservesIsDefault`, `TestDeleteTheme_RowsAffectedDistinguishesDefault` |
| Validation contract             | Empty `Theme.DefaultName` -> Validate() error mentioning THEME_DEFAULT_NAME  | PASS   | `TestValidateThemeDefaultName/empty_rejected`                                            |
| Schema invariant                | Two rows with `is_default = 1` -> UNIQUE constraint violation on the second  | PASS   | `TestThemesTable_PartialUniqueDefaultRejectsSecond`                                      |

All task-scoped ACs pass.

## Adversarial findings

### Findings that surfaced an issue

**F1 (low) — `THEME_DEFAULT_NAME` validator accepts whitespace-only values.**
Before this review, `Config.Validate()` only checked
`c.Theme.DefaultName == ""`. The values `" "`, `"\t"`, `"\n"`, `"   "`, and
`" \t "` all passed validation, even though SPEC-004 §35 says "Validation
MUST reject empty values." A whitespace-only theme name flows verbatim
into the seed query (TASK-017's `EnsureDefault`) and becomes the stored
name on the seeded row, where it is impossible to display, search for, or
distinguish from another whitespace-named theme.

This is admin-controlled config, so the threat model is "operator
fat-fingers `THEME_DEFAULT_NAME=  `" rather than an attacker. Even so,
catching it at the validator is exactly the right layer; the existing
`DEVICE_COOKIE_NAME` validator already follows this pattern.

- Severity: **low** (configuration footgun; admin-only attack surface;
  no impact on existing data).
- Reproduction: before the fix, `Config{Theme: ThemeConfig{DefaultName:
  "  "}}.Validate()` returns `nil`. After the fix it returns
  `THEME_DEFAULT_NAME must not be empty`.
- Fix: in `internal/config/config.go::Validate`, change
  `c.Theme.DefaultName == ""` to `strings.TrimSpace(c.Theme.DefaultName) == ""`.
- Tests added: `TestValidateThemeDefaultName` is now table-driven and
  exercises empty, single space, multi-space, tab, newline, tab+space,
  the literal `"default"`, names with embedded spaces, and unicode names.

### Findings that did NOT reveal a bug (the implementation held)

**SQL injection via theme name.** Inserting a name of
`ev'il); DROP TABLE themes;--` round-trips literally and the table is
not dropped. `TestCreateTheme_RejectsSQLMetacharactersAsLiteralValues`
inserts the payload, fetches it back via `GetThemeByName`, and runs
`CountDefaultThemes` afterwards to prove the table still exists.

**`UpdateTheme` silently demoting the default.** The architecture doc
explicitly says UpdateTheme "Does NOT touch is_default". I exercised the
generated query against a row with `is_default = 1` and verified the
flag is preserved while every other column updates.
`TestUpdateTheme_PreservesIsDefault` pins the contract.

**`DeleteTheme` returning the wrong RowsAffected.** The service layer in
TASK-017 will use `DeleteTheme`'s `RowsAffected` to distinguish "row was
the default" from "row was deleted". I verified all three branches in
`TestDeleteTheme_RowsAffectedDistinguishesDefault`:
- delete the default row -> `RowsAffected = 0`, row still exists;
- delete a non-default row -> `RowsAffected = 1`, row gone;
- delete a missing id -> `RowsAffected = 0`, no error.

**`SetDefaultTheme` flipping a flag while another row already has it.**
Without `ClearDefaultTheme` first, `SetDefaultTheme` correctly hits the
partial unique index and fails with a `UNIQUE constraint` error
(`TestSetDefaultTheme_WithoutClearViolatesIndex`). This means a buggy
caller that skips the transactional clear-then-set pattern gets a
hard SQL error rather than two silently coexisting defaults.

**`SetDefaultTheme` on a nonexistent id.** Returns
`RowsAffected = 0` and no error -- this is how TASK-017 will detect
"id not found" (`TestSetDefaultTheme_NoSuchID`).

**`SetDefaultTheme` on the row that is already default.** Idempotent
no-op: returns `RowsAffected = 1` (because the row matched the WHERE),
no constraint violation. (Verified during exploratory probing; not
pinned with a dedicated test because the behaviour is what every
`UPDATE WHERE id=?` shape returns -- there is no specific contract to
guard.)

**`Clear + Set` happy path.** Verified end-to-end in
`TestClearAndSetDefault_HappyPathSwap`: old default goes to 0, new
default goes to 1, `CountDefaultThemes` reports exactly 1.

**Migration `+down` mirrors the schema.** The `+down` block does
`DROP INDEX IF EXISTS themes_one_default;` followed by
`DROP TABLE IF EXISTS themes;` -- the correct order (drop the index
first, then the table that owns it) and uses `IF EXISTS` so a partial
prior teardown does not abort the rest. The current migration runner
does not invoke `+down` automatically (rollbacks are a manual operator
action), so this is the documented manual-rollback shape; it is correct
in form and order.

**Schema/migration mirror parity.** `internal/db/schema/006_create-themes.sql`
contains exactly the `+up` SQL from the migration (sans the `-- +up`
marker), as the existing convention requires. sqlc reads this file to
type-check the queries; the type-checked code compiles cleanly, which
is itself proof of parity.

**Migration runner shared state under `-race`.** `go test -race ./...`
passes. The migration runner uses an exclusive transaction per
migration and `OpenTestDB` pins `MaxOpenConns(1)` so the test pool can
never race against itself; nothing about the new migration changes
that profile.

**`Config.String()` does not leak the theme name as a "secret".** The
default theme name is intentionally not a secret. I verified
`TestConfigStringIncludesTheme` confirms it surfaces as
`Theme{DefaultName:midnight}`, alongside the existing
`GoogleClientSecret:REDACTED` invariant. The `String()` format string
is parseable and predictable.

**Long theme names.** A 1MB name is accepted by SQLite and round-trips
through `GetThemeByName` with the full byte length preserved
(`TestCreateTheme_LongName`). The schema does not constrain length,
which matches the architecture's "application-layer validation is the
single source of truth" stance for TASK-017.

**Empty strings in NOT NULL TEXT columns.** SQLite accepts `''` in
`NOT NULL TEXT` columns; only NULL is rejected
(`TestCreateTheme_NotNullIsDefault` covers the NULL branch). The empty
string is therefore an application-layer concern (validators in
TASK-017), not a schema concern.

**Default values for unspecified columns.** Inserting only the
strictly-required columns produces `is_default = 0`, `font_family_mono = ''`,
and non-empty `created_at` / `updated_at`. `TestThemesTable_DefaultColumnDefaults`
pins the seed-friendly defaults so a future migration that broke them
(e.g. typo on `DEFAULT 0` -> `DEFAULT NULL`) would surface immediately.

**Partial unique index across UPDATE.** Already covered indirectly:
- `TestUpdateTheme_PreservesIsDefault` proves `UpdateTheme` cannot
  cause the swap (it does not touch is_default).
- `TestSetDefaultTheme_WithoutClearViolatesIndex` proves any UPDATE
  that does try to set is_default = 1 on a second row hits the index.
- `TestClearAndSetDefault_HappyPathSwap` proves the documented
  clear-then-set sequence walks through the index without violation.

**Nonexistent id reads.** `GetThemeByID(ctx, "missing")` returns
`sql.ErrNoRows`, which is the standard sqlc `:one` failure mode and
what TASK-017's service layer will expect.

### Notes that did not warrant fixes

- The README description for `THEME_DEFAULT_NAME` says "auto-seeded
  default theme on first startup", which is forward-looking (the seed
  ships in TASK-017). This is fine; the table entry tells the operator
  what the variable will mean once the feature is fully wired.
- The new `ThemeConfig` struct has only one field today
  (`DefaultName`). The architecture doc anticipates this and the
  sub-struct shape matches `AuthConfig` / `DBConfig`, leaving room
  for future theme-related config (e.g. a cache duration) without
  another README churn.
- `Config.String()` prints the theme block at the end of its single
  format string. This is the same pattern the existing config uses
  for every other domain; no change needed.

## New tests added

In `internal/config/config_test.go`:

- `TestValidateThemeDefaultName` is now table-driven (was a single case).
  New cases: single space, multi-space, tab, newline, tab+space, literal
  `"default"`, name with spaces, unicode name. Locks in the
  whitespace-only fix from F1.

In `internal/db/themes_schema_test.go`:

- `insertTestTheme` -- helper used by the new tests.
- `TestUpdateTheme_PreservesIsDefault` -- pins the architectural contract
  that UpdateTheme does not touch `is_default`. Catches a regression
  where someone adds `is_default = ?` to the UPDATE.
- `TestDeleteTheme_RowsAffectedDistinguishesDefault` -- table-style
  walk of the three DeleteTheme branches (default, non-default,
  missing). This is the contract TASK-017's `Service.Delete` relies on.
- `TestSetDefaultTheme_NoSuchID` -- proves the missing-id branch
  surfaces as `RowsAffected = 0`, not as an error.
- `TestSetDefaultTheme_WithoutClearViolatesIndex` -- proves the
  partial unique index turns a missed `ClearDefault` into a hard
  SQL error rather than two coexisting defaults. Documents the
  belt-and-braces story.
- `TestClearAndSetDefault_HappyPathSwap` -- end-to-end Clear + Set
  pair, asserts `CountDefaultThemes == 1` afterwards.
- `TestCreateTheme_RejectsSQLMetacharactersAsLiteralValues` --
  classic SQL injection probe; the parameterised query stores the
  metacharacters verbatim and the table survives.
- `TestCreateTheme_LongName` -- 1MB name round-trips through
  `GetThemeByName`. Documents that length enforcement is an
  application-layer concern.
- `TestCreateTheme_NotNullIsDefault` -- explicit `NULL` `is_default`
  rejected by the NOT NULL constraint. Without this guarantee the
  partial unique index's `WHERE is_default = 1` predicate would
  not cover NULL rows.
- `TestThemesTable_DefaultColumnDefaults` -- inserting only the
  required columns produces `is_default = 0`, `font_family_mono = ''`,
  and non-empty timestamps.

## Fixes applied

- **`internal/config/config.go::Validate`** -- reject
  whitespace-only `THEME_DEFAULT_NAME` values to close the
  "operator types `   `" footgun. The check now uses
  `strings.TrimSpace(c.Theme.DefaultName) == ""` instead of the bare
  `== ""`.

No source changes were required to the migration, the schema mirror,
the sqlc query file, or the generated code.

## Green-bar

```
gofmt -l .             # empty
go vet ./...           # clean
go build ./...         # clean
go test ./...          # ok
go test -race ./...    # ok
```

## Recommendation

**ACCEPT.** The data-layer foundation is correct and solid:

- the partial unique index turns "two defaults" into a hard error;
- `UpdateTheme` does not silently demote the default;
- `DeleteTheme` and `SetDefaultTheme` return the right
  `RowsAffected` shapes for TASK-017's service layer to interpret;
- the parameterised queries are immune to classic SQL injection;
- the schema mirror and migration are byte-identical aside from
  the `-- +up` marker.

The single low-severity gap (whitespace-only `THEME_DEFAULT_NAME`)
is fixed in this review.
