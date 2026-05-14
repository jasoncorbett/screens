---
id: TASK-022
title: "screens.Service core: domain types, validation, Screen CRUD, GetScreenFull, themes.ErrThemeInUse"
spec: SPEC-006
arch: ARCH-006
status: ready
priority: p0
prerequisites: [TASK-021]
skills: [add-store, green-bar]
created: 2026-05-13
author: architect
---

# TASK-022: screens.Service core: domain types, validation, Screen CRUD, GetScreenFull, themes.ErrThemeInUse

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Build the core of the `internal/screens/` package: the domain types (`Screen`, `Page`, `WidgetInstance`, `ScreenFull`, `ScreenInput`, `ScreenSummary`, `PageWithWidgets`), the validation logic for screen inputs, the constructor `NewService`, the Screen-level CRUD methods (`CreateScreen`, `GetScreenByID`, `ListScreens`, `UpdateScreen`, `DeleteScreen`), and the `GetScreenFull` aggregated read.

Also extend the existing `internal/themes/Service`: add `ErrThemeInUse` and the FK-violation detection in `Delete`, so the Theme System fails gracefully when a screen references the theme.

Page and widget-instance methods are NOT part of this task -- they ship in TASK-023 and TASK-024 (parallel). Admin handlers are NOT part of this task -- they ship in TASK-025 / TASK-026.

## Context

- The pattern to mirror is `internal/themes/service.go` plus `internal/themes/validate.go` plus `internal/themes/theme.go`. The Screen Model service has the same shape: a `Service` struct holding `*sql.DB` and `*db.Queries`, a constructor, typed errors as package-level vars, validation as a separate file, domain-type conversion via `*FromRow` helpers.
- The service has TWO extra dependencies beyond the DB: `*themes.Service` (so we can validate `theme_id` references before INSERT) and `*widget.Registry` (used later for AddWidget; pass it through `NewService` now so we do not change the constructor signature in TASK-024).
- ID generation reuses `auth.GenerateToken[:32]`. The themes package has a private `generateID` helper that wraps it; this task can either copy that helper into the screens package or extract it into a shared `internal/auth` helper. Copying is acceptable (one line); the existing precedent does it.
- The architecture document's "Component Design > Key Interfaces and Functions" section commits the method signatures. Do not deviate from those signatures without an ADR update.
- `GetScreenFull` does three queries in sequence: `GetScreenByID` (then look up the theme via `themes.Service.GetByID`), `ListPagesByScreen`, `ListWidgetInstancesByPageIDs`. Group the widgets by page_id in Go after fetching them in one batch.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/themes/service.go` -- the closest existing pattern; mirror this in shape.
- `internal/themes/theme.go` -- domain type + FromRow helper pattern.
- `internal/themes/validate.go` -- validation + `ValidationError` pattern.
- `internal/themes/service_test.go` -- the test patterns to mirror.
- `internal/auth/auth.go` -- look for `GenerateToken` (and how themes copies the ID generation idiom).
- `internal/db/screens.sql.go` (newly generated in TASK-021) -- the typed query functions you will call.
- `internal/db/pages.sql.go` -- you need `ListPagesByScreen` for `GetScreenFull`.
- `internal/db/widget_instances.sql.go` -- you need `ListWidgetInstancesByPageIDs` for `GetScreenFull`.
- `internal/db/testhelper.go` -- `OpenTestDB` for tests.
- `docs/plans/specs/phase-2-display/spec-screen-model.md` -- requirements 1-9, 35-38; AC-1 through AC-10, AC-26.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- sections "Data Model > Go Domain Types", "Component Design > Key Interfaces and Functions > internal/screens/service.go", "Storage > GetScreenFull Query Plan", "Storage > Theme.Delete change".
- `docs/plans/architecture/decisions/adr-006-screen-model.md` -- "Theme FK: ON DELETE RESTRICT" and "Pre-shipped `GetScreenFull` for Screen Display".

## Requirements

### Package skeleton

1. Create `internal/screens/` package with the following files:
   - `screen.go` -- domain type definitions (Screen, Page, WidgetInstance, ScreenFull, ScreenSummary, ScreenInput, PageWithWidgets) + `screenFromRow`, `pageFromRow`, `widgetFromRow` helpers.
   - `service.go` -- error variables, `Service` struct, `NewService`, Screen CRUD methods, `GetScreenFull`.
   - `validate.go` -- `ValidationError`, `IsValidationError`, `validateScreenInput`, `validatePageName`, `nameRe`, `minRotationSeconds` / `maxRotationSeconds` constants.

2. Each file is `package screens`. The package doc comment lives at the top of `screen.go` and is one paragraph describing the package's role (mirror `themes.theme.go`'s top-of-file doc).

### Domain types

3. In `screen.go`, define:
   ```go
   type Screen struct {
       ID                      string
       Name                    string
       ThemeID                 string
       RotationIntervalSeconds int
       CreatedAt               time.Time
       UpdatedAt               time.Time
   }

   type ScreenSummary struct {
       Screen
       ThemeName string
       PageCount int
   }

   type Page struct {
       ID        string
       ScreenID  string
       Name      string
       Position  int
       CreatedAt time.Time
       UpdatedAt time.Time
   }

   type WidgetInstance struct {
       ID        string
       PageID    string
       Type      string
       Config    []byte
       Position  int
       CreatedAt time.Time
       UpdatedAt time.Time
   }

   type PageWithWidgets struct {
       Page    Page
       Widgets []WidgetInstance
   }

   type ScreenFull struct {
       Screen Screen
       Theme  themes.Theme
       Pages  []PageWithWidgets
   }

   type ScreenInput struct {
       Name                    string
       ThemeID                 string
       RotationIntervalSeconds int
   }
   ```

4. Implement `screenFromRow(row db.Screen) (Screen, error)`. The sqlc-generated `db.Screen.RotationIntervalSeconds` is `int64`; convert to `int`. Timestamps are TEXT in the `2006-01-02 15:04:05` format; parse with `time.Parse` (mirror `themeFromRow`).

5. Implement `pageFromRow(row db.Page) (Page, error)` and `widgetFromRow(row db.WidgetInstance) (WidgetInstance, error)` similarly.

### Errors and validation

6. In `service.go`, declare the following error variables at package scope:
   - `ErrScreenNotFound`
   - `ErrPageNotFound`
   - `ErrWidgetNotFound`
   - `ErrUnknownWidgetType`
   - `ErrThemeNotFound` (a screens-package-local error returned by `CreateScreen` / `UpdateScreen` when the requested theme_id does not exist; this is intentionally distinct from `themes.ErrThemeNotFound` for package boundary cleanliness)
   - `ErrDuplicateName`

7. In `validate.go`, implement:
   - The `ValidationError` type with `Fields map[string]string`, `Error() string` (mirror themes), `IsValidationError(err)`.
   - `nameRe = regexp.MustCompile(\`^[A-Za-z0-9 _-]{1,64}$\`)`.
   - `minRotationSeconds = 5`, `maxRotationSeconds = 3600`.
   - `validateScreenInput(in ScreenInput) (ScreenInput, error)` that:
     - Trims whitespace from `Name` and `ThemeID`.
     - Fails `name` if `!nameRe.MatchString(name)`.
     - Fails `theme_id` if it is empty after trim.
     - Fails `rotation_interval_seconds` if outside the [5, 3600] range.
     - Returns `(ScreenInput{}, &ValidationError{...})` on any failures.
     - Returns the normalised input on success.
   - `validatePageName(v string) (string, error)` that allows empty (returns "", nil) and otherwise applies `nameRe`. (Used in TASK-023; pre-shipped here to keep validation centralised.)

### Service constructor

8. In `service.go`:
   ```go
   type Service struct {
       sqlDB   *sql.DB
       queries *db.Queries
       themes  *themes.Service
       widgets *widget.Registry
   }

   func NewService(sqlDB *sql.DB, themesSvc *themes.Service, widgets *widget.Registry) *Service {
       return &Service{
           sqlDB:   sqlDB,
           queries: db.New(sqlDB),
           themes:  themesSvc,
           widgets: widgets,
       }
   }
   ```

   The `*widget.Registry` field is stored now even though this task does not use it -- TASK-024 will. Keeping the constructor stable means TASK-024 changes only its own files.

### Screen CRUD methods

9. Implement `CreateScreen(ctx context.Context, in ScreenInput) (Screen, error)`:
   - Call `validateScreenInput(in)`. On `*ValidationError`, return it.
   - Call `s.themes.GetByID(ctx, clean.ThemeID)`. If it returns `themes.ErrThemeNotFound`, return the screens-package-local `ErrThemeNotFound`. Any other error: wrap and return.
   - Generate a fresh ID (via `auth.GenerateToken[:32]` -- copy the `generateID` helper from themes into the screens package).
   - Call `s.queries.CreateScreen(ctx, db.CreateScreenParams{...})` with the validated input.
   - If the error is a UNIQUE-constraint violation on `screens.name`, return `ErrDuplicateName`. Reuse the same `strings.Contains(err.Error(), "UNIQUE constraint failed: screens.name")` idiom that themes uses.
   - On other errors: wrap and return.
   - On success: fetch the row back with `GetScreenByID`, convert via `screenFromRow`, return.

10. Implement `GetScreenByID(ctx context.Context, id string) (Screen, error)`:
    - Call `s.queries.GetScreenByID(ctx, id)`.
    - On `sql.ErrNoRows`, return `ErrScreenNotFound`.
    - On success: `screenFromRow` and return.

11. Implement `ListScreens(ctx context.Context) ([]ScreenSummary, error)`:
    - Call `s.queries.ListScreenSummaries(ctx)`.
    - Convert each row to a `ScreenSummary` (`Screen` embedded + `ThemeName` + `PageCount`).
    - Note: `db.ListScreenSummariesRow` is a sqlc-generated struct distinct from `db.Screen`; map its fields carefully (the timestamp parse logic from `screenFromRow` applies; PageCount is `int64` from `COUNT(*)`).
    - Return an empty (non-nil) slice when no rows exist.

12. Implement `UpdateScreen(ctx context.Context, id string, in ScreenInput) (Screen, error)`:
    - Call `validateScreenInput(in)`. Return `*ValidationError` on failure.
    - Call `s.themes.GetByID(ctx, clean.ThemeID)`. Translate `themes.ErrThemeNotFound` to the local `ErrThemeNotFound`.
    - Confirm the screen exists via `s.queries.GetScreenByID(ctx, id)`. On `sql.ErrNoRows`, return `ErrScreenNotFound`.
    - Call `s.queries.UpdateScreen`. Detect UNIQUE-constraint violation on name; return `ErrDuplicateName`.
    - Fetch and return the updated row.

13. Implement `DeleteScreen(ctx context.Context, id string) error`:
    - Call `s.queries.DeleteScreen(ctx, id)`.
    - Inspect `RowsAffected`. If 0, return `ErrScreenNotFound`.
    - Note: CASCADE handles child cleanup; no application code is needed for that.

### GetScreenFull

14. Implement `GetScreenFull(ctx context.Context, id string) (ScreenFull, error)`:
    - Step A: `GetScreenByID(ctx, id)`. Propagate `ErrScreenNotFound`.
    - Step B: `s.themes.GetByID(ctx, screen.ThemeID)`. If it returns `themes.ErrThemeNotFound`, return a wrapped error indicating a startup-invariant violation (this should be unreachable given the RESTRICT FK, but defensive).
    - Step C: `s.queries.ListPagesByScreen(ctx, id)`. Convert each via `pageFromRow`.
    - Step D: if the page list is empty, return `ScreenFull{Screen, Theme, Pages: []PageWithWidgets{}}`.
    - Step E: build `[]string` of page IDs, call `s.queries.ListWidgetInstancesByPageIDs(ctx, pageIDs)`.
    - Step F: group widgets by page_id in a `map[string][]WidgetInstance`. Iterate pages in position order, attaching each page's widget list (or an empty slice if none).
    - Step G: return the assembled `ScreenFull`.
    - Total queries: at most 3 (screen, pages, widgets); plus a fourth to the themes service for the theme row.

### Theme System change: ErrThemeInUse

15. In `internal/themes/service.go`:
    - Add `var ErrThemeInUse = errors.New("theme in use by one or more screens")` at package scope (near the existing error vars).
    - Add an unexported helper `isForeignKeyViolation(err error) bool` that returns `err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")`. Mirror the placement of `isUniqueNameViolation`.
    - In the `Delete` method, between the `s.queries.DeleteTheme(ctx, id)` call and the `RowsAffected` check, add:
      ```go
      if err != nil {
          if isForeignKeyViolation(err) {
              return ErrThemeInUse
          }
          return fmt.Errorf("delete theme: %w", err)
      }
      ```
      (Replace the existing `if err != nil { return fmt.Errorf("delete theme: %w", err) }` block with the version above.)
    - Order matters: the existing pre-check (`if row.IsDefault == 1 { return ErrCannotDeleteDefault }`) fires first; the FK detection fires only on actual SQL errors.

16. Add no new public surface to `themes.Service` beyond the new `ErrThemeInUse` variable.

### Tests

17. Create `internal/screens/service_test.go` with table-driven tests covering Screen CRUD. Use `db.OpenTestDB(t)` for an isolated DB per test. Construct the service via `screens.NewService(sqlDB, themes.NewService(sqlDB, themes.Config{DefaultName: "default"}), widget.NewRegistry())` -- the registry can be empty for this task's tests. Run the themes service's `EnsureDefault(ctx)` after construction so the default theme exists and can be referenced by created screens.

    Required tests (see "Test Requirements" below for the full list).

18. Create `internal/screens/validate_test.go` with table-driven tests for `validateScreenInput`:
    - Empty name rejected.
    - Whitespace-only name rejected.
    - Name with `<script>` rejected.
    - Name with 65 chars rejected.
    - Name with 64 chars accepted.
    - Empty theme_id rejected.
    - Rotation 4 rejected; 5 accepted; 3600 accepted; 3601 rejected.

19. Extend `internal/themes/service_test.go` with a new test case `TestDelete_RejectsThemeInUse`:
    - Set up the test DB with the default theme seeded.
    - Insert a fake `screens` row referencing the default theme via raw SQL `INSERT INTO screens (id, name, theme_id, rotation_interval_seconds) VALUES (?, 'kitchen', ?, 30)`. (Raw SQL is fine for this test; we are exercising the FK constraint, not the screens service.)
    - Call `themesSvc.Delete(ctx, defaultID)`. The default theme has `is_default = 1` so the default-check fires first; create a NON-default theme too and have the screen reference IT, then attempt to delete that non-default theme.
    - Assert `errors.Is(err, themes.ErrThemeInUse)`.
    - Assert the theme row is still present.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-1 (service half): `CreateScreen` with a valid input persists a row whose `ThemeID` matches the input and whose `Position` / cascade behaviour is correct.
- [ ] AC-2: `CreateScreen` with empty name returns a `*ValidationError` with `name` in `Fields`.
- [ ] AC-3: `CreateScreen` with `name=screen<script>` returns a `*ValidationError`.
- [ ] AC-4: `CreateScreen` with a duplicate name returns `ErrDuplicateName`.
- [ ] AC-5: `CreateScreen` with `rotation_interval_seconds=4` or `=3601` returns a `*ValidationError`; `=5` and `=3600` succeed.
- [ ] AC-6: `CreateScreen` with `theme_id=does-not-exist` returns the screens-package `ErrThemeNotFound`.
- [ ] AC-7 (service half): `DeleteScreen` on an existing screen returns nil and -- via the DB-layer CASCADE configured in TASK-021 -- removes child pages and widget instances.
- [ ] AC-9: `themes.Service.Delete` on a theme referenced by at least one screen returns `themes.ErrThemeInUse` and does NOT delete the row.
- [ ] AC-26: `GetScreenFull` on a Screen with 2 pages and 3 widgets total returns a `ScreenFull` with the screen, both pages in position order, each page's widgets in position order, and the theme.

## Skills to Use

- `add-store` -- mirror the existing `internal/themes/service.go` pattern.
- `green-bar` -- run before marking review.

## Test Requirements

Tests live in `internal/screens/service_test.go` and `internal/screens/validate_test.go`. Tests follow `.claude/rules/testing.md`. Use `t.Helper()` in setup helpers and table-driven tests where practical.

1. **TestCreateScreen_HappyPath**: Create with valid input; assert the row is in the DB with the correct values; assert returned `Screen` matches.

2. **TestCreateScreen_RejectsValidationErrors** (table-driven): empty name, whitespace-only name, name with `<`, name 65 chars long, empty theme_id, rotation 4, rotation 3601, rotation 0, rotation -1.

3. **TestCreateScreen_RejectsUnknownThemeID**: returns `ErrThemeNotFound`.

4. **TestCreateScreen_RejectsDuplicateName**: returns `ErrDuplicateName`.

5. **TestGetScreenByID_NotFound**: returns `ErrScreenNotFound`.

6. **TestListScreens_ReturnsPageCountAndThemeName**: create one theme + one screen + two pages (via raw SQL since the page methods are in TASK-023). Assert `ListScreens()[0].PageCount == 2` and `ThemeName` matches.

7. **TestUpdateScreen_HappyPath**: Update valid; assert columns change and `updated_at` advances.

8. **TestUpdateScreen_NotFound**: returns `ErrScreenNotFound`.

9. **TestUpdateScreen_RejectsValidation**: returns `*ValidationError`.

10. **TestUpdateScreen_RejectsDuplicateName**: create A and B, try to rename B to A, expect `ErrDuplicateName`.

11. **TestDeleteScreen_HappyPath**: insert a screen and one page (raw SQL) and one widget_instance (raw SQL), call `DeleteScreen`, assert all three rows are gone.

12. **TestDeleteScreen_NotFound**: returns `ErrScreenNotFound`.

13. **TestGetScreenFull_AggregatesPagesAndWidgets**: build a screen + 2 pages + 3 widgets (1 on page A, 2 on page B) via raw SQL. Call `GetScreenFull`. Assert the returned tree has the right shape: 2 pages in position order, page A has 1 widget, page B has 2 widgets in position order, the theme name matches the seeded default.

14. **TestGetScreenFull_NoPages**: A screen with zero pages returns `Pages: []PageWithWidgets{}` (non-nil, empty).

15. **TestGetScreenFull_NotFound**: returns `ErrScreenNotFound`.

16. **TestValidateScreenInput** (in `validate_test.go`, table-driven): the cases enumerated in requirement 18.

17. **TestDelete_RejectsThemeInUse** (in `internal/themes/service_test.go`):
    - Seed a theme, insert a screen referencing it via raw SQL.
    - Call `themesSvc.Delete(ctx, themeID)`.
    - Assert `errors.Is(err, themes.ErrThemeInUse)`.
    - Assert the row is still present (`GetByID` succeeds).

18. (Optional) **TestDelete_RejectsDefaultBeforeFK**: a screen referencing the default theme; calling `Delete(defaultID)` should return `ErrCannotDeleteDefault` (the default check fires first). This verifies error ordering.

## Definition of Done

- [ ] `internal/screens/screen.go`, `service.go`, `validate.go` all created with the methods and types specified.
- [ ] `internal/themes/service.go` extended with `ErrThemeInUse` + FK-violation detection in `Delete`.
- [ ] All tests in `internal/screens/service_test.go`, `internal/screens/validate_test.go`, and the new theme test pass.
- [ ] green-bar passes (gofmt, vet, build, test). Run `go test -race ./internal/screens/...` and `go test -race ./internal/themes/...` since the service touches `*sql.DB`.
- [ ] No new third-party dependencies.
- [ ] Existing theme tests still pass (the additive change must not regress).
- [ ] Service method signatures match the architecture document exactly.
