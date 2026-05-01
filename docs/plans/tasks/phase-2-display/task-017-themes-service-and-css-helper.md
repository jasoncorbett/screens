---
id: TASK-017
title: "Themes service, validators, default seed, and CSS variables helper"
spec: SPEC-004
arch: ARCH-004
status: ready
priority: p0
prerequisites: [TASK-016]
skills: [add-store, green-bar]
created: 2026-04-30
author: architect
---

# TASK-017: Themes service, validators, default seed, and CSS variables helper

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Build the domain-level Theme System on top of the schema and queries delivered in TASK-016. This task ships:

1. A new `internal/themes/` package with a `Theme` type, an `Input` type for create / update inputs, and a `Service` exposing CRUD methods (`Create`, `GetByID`, `List`, `GetDefault`, `Update`, `Delete`, `SetDefault`) plus an idempotent `EnsureDefault` for startup seeding.
2. Strict server-side validators (`validateName`, `validateHex`, `validateFontFamily`, `validateRadius`) that close the CSS-injection hole described in ADR-004.
3. The pure-function `Theme.CSSVariables()` helper that returns a `:root { ... }` CSS block for downstream Screen Display to embed in a `<style>` tag.

This task touches NO HTTP code and NO templ files. It is the testable domain core; TASK-018 mounts it onto the admin UI.

## Context

- The package shape mirrors `internal/auth/`: a `Service` with a `*sql.DB` plus a sqlc-generated `*db.Queries`, constructor `NewService(sqlDB *sql.DB, cfg Config) *Service`, exported domain types (`Theme`, `Input`, `Config`), and exported sentinel errors.
- The `auth.GenerateToken[:32]` primitive is reused for theme IDs (16 bytes of entropy, hex-encoded). Look at `internal/auth/session.go` for `GenerateToken`. To use it without an import cycle: import `github.com/jasoncorbett/screens/internal/auth` -- the `internal/themes` package is a leaf package and can depend on `auth` for the token primitive (just like `views/` does). If a cleaner surface is preferred, the `auth.GenerateToken` function may be moved to a smaller `internal/idgen/` package; that refactor is OUT OF SCOPE for this task. Reuse what exists.
- The seeded default theme's color values come from `static/css/app.css` (look at the `:root { ... }` block: `--bg: #0b0d11`, `--surface: #14171f`, `--border: #23273a`, `--text: #dfe2ed`, `--text-muted: #6b7084`, `--accent: #7b93ff`, `--radius: 10px`). Encode these as Go constants in `service.go`. The font family for the default seed is the same one `static/css/app.css` uses on `body`: `-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif`. The mono font for the default seed is `"SF Mono", "Fira Code", "Cascadia Code", monospace` (from the `.version-badge` rule).
- All timestamp parsing uses `time.Parse("2006-01-02 15:04:05", row.CreatedAt)` -- match the existing `auth.userFromRow` / `auth.deviceFromRow` pattern.
- `IsDefault int64` is the SQLite-friendly bool. `dev := row.IsDefault == 1` -- match the existing `auth.userFromRow` pattern (`Active: row.Active == 1`).
- The `CSSVariables()` helper is a pure function. It does NOT need a database. It does NOT need a context. It does NOT panic on input. It returns deterministic output for a given Theme value.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/auth/auth.go` -- mirror `Service` shape, constructor pattern, transaction usage in `DeactivateUser`.
- `internal/auth/session.go` -- `GenerateToken`, `HashToken` -- reuse `GenerateToken` for IDs.
- `internal/auth/device.go` -- mirror `deviceFromRow` for `themeFromRow`.
- `internal/auth/user.go` -- mirror `userFromRow` for the `IsDefault int64` -> `bool` translation.
- `internal/db/models.go` -- the sqlc-generated `Theme` struct (post TASK-016).
- `internal/db/themes.sql.go` -- the sqlc-generated query methods (post TASK-016). Read this to understand the exact method signatures available.
- `static/css/app.css` -- the source of truth for the seeded default theme's color values.
- `docs/plans/architecture/phase-2-display/arch-theme-system.md` -- "Component Design > Key Interfaces and Functions" and "Storage > sqlc Queries" sections.
- `docs/plans/specs/phase-2-display/spec-theme-system.md` -- "Functional Requirements > Color Palette / Fonts / Spacing-Radius / Default Theme / CSS Variable Rendering Helper" sections.

## Requirements

### Package layout

1. Create `internal/themes/theme.go` with:
   - The `Theme` struct (14 fields per the architecture doc).
   - The `Input` struct for create / update inputs (10 user-supplied fields: `Name`, six color fields, `FontFamily`, `FontFamilyMono`, `Radius`).
   - A `themeFromRow(row db.Theme) (Theme, error)` package-private helper that translates the sqlc row to the domain type. Mirror `auth.deviceFromRow`. Translate `IsDefault int64 -> bool` (`dev.IsDefault = row.IsDefault == 1`). Parse `CreatedAt` and `UpdatedAt` from the `2006-01-02 15:04:05` TEXT format.

2. Create `internal/themes/validate.go` with:
   - `var nameRe = regexp.MustCompile(\`^[A-Za-z0-9 _-]{1,64}$\`)`
   - `var hexRe = regexp.MustCompile(\`^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$\`)`
   - `var radiusRe = regexp.MustCompile(\`^(0|[0-9]+px|[0-9]+(\.[0-9]+)?(rem|em))$\`)`
   - The `ValidationError` struct with `Fields map[string]string`, an `Error()` method, and the `IsValidationError(err error) bool` helper.
   - A `validateInput(in Input) (Input, error)` function that:
     - Trims whitespace from `Name`, `FontFamily`, `FontFamilyMono`, and `Radius`. (Hex values are NOT trimmed by validateInput; an admin who pasted `  #ffffff` should get an explicit error so they fix the underlying tooling. Trim ONLY the four non-hex string fields.)
     - Runs each field through its validator.
     - Lowercases hex values on success.
     - Returns the normalised `Input` and a `*ValidationError` if any field failed (the error contains every failed field, not just the first -- this is the key UX property).
   - A `validateFontFamily(v string) (string, error)` helper that rejects `;`, `{`, `}`, `<`, `>`, `\`, and any byte `< 0x20` (control characters including `\n`, `\r`, `\t`). Length cap 256 chars. Empty string rejected. Returns the trimmed-but-otherwise-unchanged value on success.
   - A `validateHex(v string) (string, error)` helper that matches against `hexRe` and returns `strings.ToLower(v)` on success.
   - A `validateRadius(v string) (string, error)` helper that matches against `radiusRe` and returns the trimmed value unchanged.
   - A `validateName(v string) (string, error)` helper that matches against `nameRe` (which already enforces the 1-64 length cap and the character whitelist) on the trimmed value.
   - The `FontFamilyMono` field follows the same validator as `FontFamily` BUT empty string is allowed (the validator only runs when the value is non-empty).

3. Create `internal/themes/css.go` with the exported method:
   ```go
   func (t Theme) CSSVariables() string
   ```
   - Output is a `:root { ... }` block with declarations for `--bg`, `--surface`, `--border`, `--text`, `--text-muted`, `--accent`, `--radius`, `--font-family`, plus `--font-family-mono` (only when `t.FontFamilyMono != ""`).
   - Output is deterministic: same Theme yields identical bytes.
   - Uses `strings.Builder`. No reflection, no `text/template`, no allocations beyond the builder.
   - Does NOT escape its inputs. The contract is: only call this on a Theme that came out of `Service.Create`, `Service.Update`, `Service.GetByID`, `Service.List`, or `Service.EnsureDefault` -- all of which run validation. The function is a pure formatter.

4. Create `internal/themes/service.go` with:
   - The exported sentinel errors: `ErrThemeNotFound`, `ErrCannotDeleteDefault`, `ErrDuplicateName`.
   - The exported `Config` struct with one field: `DefaultName string`.
   - The `Service` struct (private fields: `sqlDB *sql.DB`, `queries *db.Queries`, `config Config`).
   - The `NewService(sqlDB *sql.DB, cfg Config) *Service` constructor.
   - All methods listed below. Method signatures match the architecture doc verbatim.

### Service methods

5. `EnsureDefault(ctx context.Context) error`:
   - Calls `queries.CountDefaultThemes(ctx)`. If the count is non-zero, return nil (idempotent).
   - If the count is zero:
     - Generate a fresh theme ID via `auth.GenerateToken()` and slice the first 32 chars (mirror the existing `internal/auth.generateID` pattern -- 16 bytes of entropy is plenty).
     - Call `queries.CreateTheme(ctx, db.CreateThemeParams{...})` with the seeded default values (the constants documented in Context above) and `IsDefault: 1`, `Name: s.config.DefaultName`.
     - Return any DB error.
   - Wrap the count + insert in a single transaction (`s.sqlDB.BeginTx`) to avoid a TOCTOU window where two simultaneous boots could both insert. The transaction is short -- one SELECT and at most one INSERT.
   - On a UNIQUE constraint violation during INSERT (race lost), the error is fine to return verbatim; main.go will log.fatal. In practice no production setup boots two processes against the same database file, but the transaction is still the right shape.

6. `Create(ctx context.Context, in Input) (Theme, error)`:
   - Calls `validateInput(in)`. On `*ValidationError`, return `Theme{}, &validationError`.
   - Generates a fresh ID via the same primitive as EnsureDefault.
   - Calls `queries.CreateTheme(ctx, ...)` with `IsDefault: 0`.
   - On a UNIQUE constraint violation on the `name` column, return `Theme{}, ErrDuplicateName`. (Detect by checking the error string for "UNIQUE constraint failed: themes.name" -- match the existing error-detection idiom; if the existing codebase uses `sqlite3.Error` type assertion, mirror that.)
   - On success, calls `queries.GetThemeByID(ctx, id)` and returns the populated `Theme`. (The sqlc-generated `CreateTheme :exec` does not return the row, so a follow-up SELECT is required to populate `created_at`. Mirror `auth.Service.CreateDevice`.)

7. `GetByID(ctx context.Context, id string) (Theme, error)`:
   - Calls `queries.GetThemeByID(ctx, id)`.
   - On `sql.ErrNoRows`, return `Theme{}, ErrThemeNotFound`.
   - On success, run through `themeFromRow` and return.

8. `List(ctx context.Context) ([]Theme, error)`:
   - Calls `queries.ListThemes(ctx)`.
   - Translates each row through `themeFromRow`.
   - Returns the slice (empty slice, never nil, on no rows -- mirror `auth.Service.ListDevices`).

9. `GetDefault(ctx context.Context) (Theme, error)`:
   - Calls `queries.GetDefaultTheme(ctx)`.
   - On `sql.ErrNoRows`, return `Theme{}, ErrThemeNotFound`. (After EnsureDefault has run, this should never happen -- treat it as a startup-invariant violation. Returning the error is the right shape.)
   - On success, returns the theme.

10. `Update(ctx context.Context, id string, in Input) (Theme, error)`:
    - Calls `validateInput(in)` first. On `*ValidationError`, returns `Theme{}, &validationError`.
    - Calls `queries.GetThemeByID(ctx, id)` to confirm the theme exists. On `sql.ErrNoRows`, returns `Theme{}, ErrThemeNotFound`.
    - Calls `queries.UpdateTheme(ctx, ...)` with the validated values.
    - On a UNIQUE constraint violation on `name`, returns `Theme{}, ErrDuplicateName`.
    - On success, re-fetches and returns the updated theme.

11. `Delete(ctx context.Context, id string) error`:
    - Calls `queries.GetThemeByID(ctx, id)`. On `sql.ErrNoRows`, returns `ErrThemeNotFound`. On success, check if `row.IsDefault == 1`; if so, return `ErrCannotDeleteDefault`.
    - Calls `queries.DeleteTheme(ctx, id)`. The query's WHERE clause already filters out default rows; the application-layer check above is the primary defence and provides the clear error type. The SQL clause is belt-and-suspenders.
    - Checks `RowsAffected`: if 0 (and the GetThemeByID returned non-default), some race or schema issue happened -- return a generic error wrapping the result. In practice this branch is unreachable.

12. `SetDefault(ctx context.Context, id string) error`:
    - Calls `queries.GetThemeByID(ctx, id)` first to detect non-existent IDs cleanly. On `sql.ErrNoRows`, returns `ErrThemeNotFound`.
    - Begins a transaction (`s.sqlDB.BeginTx`).
    - Inside the transaction (using `s.queries.WithTx(tx)`): calls `ClearDefaultTheme`, then `SetDefaultTheme(id)`.
    - Checks `SetDefaultTheme`'s `RowsAffected`. If 0, the theme was deleted between the lookup and the update; treat as `ErrThemeNotFound` (rollback first; the deferred Rollback handles this).
    - Commits the transaction.
    - The partial unique index `themes_one_default` is the second line of defence: if the transaction were ever interrupted in a way that left two rows both with `is_default = 1`, the index would have failed the second UPDATE first. Either way, the invariant is preserved.

### CSS Variables helper

13. Implement `(t Theme) CSSVariables() string` per the architecture doc. The output format is:
    ```
    :root {
      --bg: <ColorBg>;
      --surface: <ColorSurface>;
      --border: <ColorBorder>;
      --text: <ColorText>;
      --text-muted: <ColorTextMuted>;
      --accent: <ColorAccent>;
      --radius: <Radius>;
      --font-family: <FontFamily>;
      [--font-family-mono: <FontFamilyMono>;]   <-- only if non-empty
    }
    ```
    - Two-space indentation inside the block.
    - Trailing newline after each declaration.
    - Trailing newline after the closing brace.

## Acceptance Criteria

From SPEC-004:

- [ ] AC-1: Calling `Service.EnsureDefault(ctx)` against an empty `themes` table inserts exactly one row whose color values match the constants in `static/css/app.css` (`#0b0d11`, `#14171f`, `#23273a`, `#dfe2ed`, `#6b7084`, `#7b93ff`, `10px`) and `IsDefault == 1`.
- [ ] AC-2: Calling `Service.EnsureDefault(ctx)` twice in a row leaves the table with exactly one default theme and does not mutate the existing row.
- [ ] AC-3 (service half): `Service.Create` with `Input{Name: "  ", ...}` returns a `*ValidationError` whose `Fields["name"]` is non-empty.
- [ ] AC-4 (service half): `Service.Create` with `Input{Name: "theme<script>", ...}` returns a `*ValidationError` whose `Fields["name"]` is non-empty.
- [ ] AC-5 (service half): `Service.Create` with a 65-character name returns `*ValidationError`.
- [ ] AC-6: Two `Service.Create` calls with the same name return `ErrDuplicateName` on the second call.
- [ ] AC-7: `Service.Create` with `ColorBg: "#0b0d11"` succeeds and the returned theme's `ColorBg == "#0b0d11"`.
- [ ] AC-8: `Service.Create` with `ColorBg: "#FFF"` succeeds and the returned theme's `ColorBg == "#fff"` (lowercased).
- [ ] AC-9: `Service.Create` with `ColorBg: "red"` returns `*ValidationError`.
- [ ] AC-10: `Service.Create` with `ColorBg: "rgb(11,13,17)"` returns `*ValidationError`.
- [ ] AC-11: `Service.Create` with `ColorBg: "#zzzzzz"` returns `*ValidationError`.
- [ ] AC-12: `Service.Create` with `FontFamily: "Arial;}<script>"` returns `*ValidationError`.
- [ ] AC-13: `Service.Create` with a 257-character `FontFamily` returns `*ValidationError`.
- [ ] AC-14: `Service.Create` with `Radius: "10px"` succeeds and the returned theme's `Radius == "10px"`.
- [ ] AC-15: `Service.Create` with `Radius: "10"` returns `*ValidationError`.
- [ ] AC-16: After `Service.Create(theme=A)` then `Service.SetDefault(B.ID)` then `Service.SetDefault(A.ID)`, exactly one theme has `IsDefault == 1` and it is `A`.
- [ ] AC-17: When `Service.SetDefault` is called and the inner `SetDefaultTheme` UPDATE affects zero rows (theme deleted mid-transaction), the function returns `ErrThemeNotFound` and the database state is unchanged (verified by querying `CountDefaultThemes` before and after).
- [ ] AC-18: `Service.Delete(defaultThemeID)` returns `ErrCannotDeleteDefault` and the row is still present.
- [ ] AC-19: `Service.Delete(nonDefaultThemeID)` removes the row and a subsequent `Service.GetDefault(ctx)` still returns the original default theme.
- [ ] AC-20: After AC-16, `SELECT COUNT(*) FROM themes WHERE is_default = 1` returns `1`.
- [ ] AC-28: `Theme.CSSVariables()` contains substrings `:root {`, `--bg:`, `--surface:`, `--border:`, `--text:`, `--text-muted:`, `--accent:`, `--radius:`, and `--font-family:`.
- [ ] AC-29: Calling `Theme.CSSVariables()` twice returns byte-identical strings.
- [ ] AC-30: When `t.ColorAccent == "#7b93ff"`, then `Theme.CSSVariables()` contains the literal substring `--accent: #7b93ff;`.
- [ ] AC-31: `Theme.CSSVariables()` does NOT contain `<`, `>`, or the substring `</style>` (verified by `strings.Contains` checks on the output).
- [ ] AC-33: When `Service.EnsureDefault(ctx)` is called with `Config{DefaultName: "onyx"}` against an empty table, the resulting row's name is `"onyx"`.

## Skills to Use

- `add-store` -- mirror `internal/auth/auth.go` for the service shape and transaction patterns.
- `green-bar` -- run before marking complete.

## Test Requirements

Use `db.OpenTestDB(t)` to construct a fresh in-memory database with all migrations applied. Build a `Service` directly in each test:

```go
func newTestService(t *testing.T, defaultName string) *themes.Service {
    t.Helper()
    sqlDB := db.OpenTestDB(t)
    return themes.NewService(sqlDB, themes.Config{DefaultName: defaultName})
}
```

1. **Validators (`validate_test.go`)**: a single table-driven test per validator. Each row carries `name`, `input`, `wantErr bool`, and an optional `wantNormalised`. Cover:
   - Names: empty, whitespace-only, valid `"my-theme"`, valid `"theme 1"`, valid `"a"`, 64 chars valid, 65 chars invalid, `"theme<script>"` invalid.
   - Hex: `"#000"` valid, `"#abcdef"` valid, `"#ABC"` valid (and normalised to `"#abc"`), `"red"` invalid, `"rgb(0,0,0)"` invalid, `"#zzzzzz"` invalid, `"#1234567"` invalid, `""` invalid, `"#fff "` (trailing space) invalid (we do NOT trim hex).
   - Radius: `"0"` valid, `"10px"` valid, `"0.5rem"` valid, `"1em"` valid, `"10"` invalid, `"10pt"` invalid, `"-1px"` invalid, `""` invalid.
   - Font family: `"system-ui"` valid, `"\"SF Mono\", monospace"` valid, `"Arial; }"` invalid, `"<script>"` invalid (the `<` is rejected), `"Foo\nBar"` invalid, `""` invalid, 257-char string invalid, `"Arial\\"` invalid (backslash rejected).

2. **CSS rendering (`css_test.go`)**:
   - **Determinism**: build a `Theme` with hard-coded values, call `CSSVariables()` twice, assert `out1 == out2`.
   - **Required declarations**: assert the output contains every property listed in AC-28 followed by `:` and the corresponding theme value.
   - **Mono font omitted when empty**: build a Theme with `FontFamilyMono == ""`, assert `!strings.Contains(out, "--font-family-mono")`.
   - **Mono font included when non-empty**: build a Theme with `FontFamilyMono == "monospace"`, assert `strings.Contains(out, "--font-family-mono: monospace;")`.
   - **No HTML break-out characters**: build a Theme (any valid one), assert the output contains none of `<`, `>`, `</style>`. (The strict validators are what guarantee this; the test guards against a future refactor that drops the validators or changes the CSS format.)
   - **Substring of one specific declaration**: build a theme with `ColorAccent == "#7b93ff"`, assert `strings.Contains(out, "--accent: #7b93ff;")`.

3. **Service CRUD (`service_test.go`)**:
   - **EnsureDefault on empty DB**: assert exactly one row exists, `IsDefault == true`, `Name == cfg.DefaultName`, color values match the documented constants.
   - **EnsureDefault is idempotent**: call twice, assert one row, same values, same `created_at` (i.e., the second call did not mutate).
   - **EnsureDefault respects DefaultName**: build with `Config{DefaultName: "onyx"}`, assert `theme.Name == "onyx"`.
   - **Create happy path**: pass a fully-valid `Input`, assert returned `Theme.ID` is non-empty, `IsDefault == false`, all color values match (lowercased where applicable), and `Service.GetByID(returnedID)` returns the same theme.
   - **Create rejects bad name / hex / radius / font**: one test per category showing the returned error is `*ValidationError` with the relevant field populated.
   - **Create rejects duplicate name**: create theme `A`, create theme `A` again, assert second call returns `ErrDuplicateName`.
   - **Update happy path**: create, then update with new color values, assert `GetByID` returns the new values and `UpdatedAt > CreatedAt`.
   - **Update of unknown ID**: assert returns `ErrThemeNotFound`.
   - **Update rejects validation errors**: same shape as Create.
   - **Delete of default**: try to delete the EnsureDefault'd row, assert `ErrCannotDeleteDefault`, assert the row is still present.
   - **Delete of non-default**: create a non-default theme, delete it, assert `GetByID` returns `ErrThemeNotFound`.
   - **SetDefault swap**: EnsureDefault'd row is `A`. Create `B`. Call `SetDefault(B.ID)`. Assert `B.IsDefault == true` and `A.IsDefault == false`. Assert `CountDefaultThemes` returns 1.
   - **SetDefault unknown ID**: assert returns `ErrThemeNotFound` and no rows are mutated.
   - **List ordering**: create themes named `c`, `a`, `b`, assert `List` returns them in order `a`, `b`, `c`.
   - **GetDefault returns the default**: assert the returned theme has `IsDefault == true`.

4. **No HTTP tests in this task.** Service tests use the service directly. Handler tests live in TASK-018.

5. Tests follow `.claude/rules/testing.md`. Use table-driven tests where the variations are mostly inputs. Mark independent subtests with `t.Parallel()` when safe (the service tests share a database per call to `db.OpenTestDB(t)` so each top-level test is independently parallel-safe).

## Definition of Done

- [ ] `internal/themes/theme.go`, `validate.go`, `css.go`, `service.go` created.
- [ ] All ten service methods (`EnsureDefault`, `Create`, `GetByID`, `List`, `GetDefault`, `Update`, `Delete`, `SetDefault`) implemented per the architecture doc.
- [ ] Validators exported via `*ValidationError` with per-field messages.
- [ ] `Theme.CSSVariables()` produces deterministic, validation-safe output.
- [ ] All acceptance criteria tests pass.
- [ ] green-bar passes (gofmt, vet, build, test). Run `go test -race ./internal/themes/...` since `SetDefault` uses transactions and `EnsureDefault` is invoked at startup.
- [ ] No new third-party dependencies.
- [ ] No HTTP handlers, no templ files, no `views/` changes.
