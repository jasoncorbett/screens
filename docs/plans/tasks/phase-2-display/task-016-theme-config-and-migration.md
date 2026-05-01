---
id: TASK-016
title: "Theme config, migration, and sqlc queries"
spec: SPEC-004
arch: ARCH-004
status: done
priority: p0
prerequisites: []
skills: [add-config, add-migration, add-store, green-bar]
created: 2026-04-30
author: architect
---

# TASK-016: Theme config, migration, and sqlc queries

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Lay the data-layer foundation for the Theme System: add the single config knob the rest of the spec needs (`THEME_DEFAULT_NAME`), create the `themes` table migration with the partial unique index that enforces "exactly one default theme", and add the sqlc queries so that subsequent tasks can use type-safe Go for theme CRUD. This task touches no Go HTTP code and no service logic -- it is the bottom of the dependency chain so every later task can assume the schema and queries exist.

## Context

- Existing config sub-structs: `internal/config/config.go` already has `HTTPConfig`, `LogConfig`, `DBConfig`, and `AuthConfig`. This task introduces a new `ThemeConfig` sub-struct (per ADR-004's "cleaner domain boundary" rationale) -- mirror the shape of the existing sub-structs.
- Existing migrations: `001_initial.sql` through `005_create-devices.sql`. The next number is `006`. The migration runner (`internal/db/migrate.go`) parses `-- +up` and `-- +down` markers; mirror that exact format.
- Existing sqlc setup: `sqlc.yaml` at repo root, queries in `internal/db/queries/*.sql`, generated code emitted into `internal/db/`. Models for `users`, `sessions`, `invitations`, `devices` already exist in `internal/db/models.go`. The new `Theme` struct will join them after `sqlc generate` runs.
- The schema uses TEXT-as-ISO8601 timestamps and `INTEGER NOT NULL DEFAULT 0` booleans (matching the existing `users.active` idiom).
- The partial unique index `themes_one_default` is the architectural lynchpin that prevents "two defaults at once" -- it is essential, not optional. SQLite supports partial unique indexes natively (this project is SQLite-only by ADR-001).
- This task does NOT seed the default theme. The seed happens in TASK-017 inside `Service.EnsureDefault`. This task only ships the empty schema.

### Files to Read Before Starting

- `.claude/rules/config.md`
- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/config/config.go` -- existing sub-struct pattern (especially `AuthConfig` and `DBConfig`); add `ThemeConfig` mirroring this style.
- `internal/db/migrate.go` -- understand the `-- +up` / `-- +down` parser before writing the migration file.
- `internal/db/migrations/005_create-devices.sql` -- the most recent migration; copy its formatting and the index-naming convention.
- `internal/db/queries/devices.sql` -- mirror sqlc annotation style (`:exec`, `:one`, `:many`, `:execresult`).
- `internal/db/queries/users.sql` -- additional reference for query style, especially the `UPDATE ... :exec` shape.
- `sqlc.yaml`
- `docs/plans/architecture/phase-2-display/arch-theme-system.md` -- "Storage" section (the SQL is provided verbatim there).
- `docs/plans/specs/phase-2-display/spec-theme-system.md` -- "Functional Requirements > Theme Records" and "Default Theme" sections.

## Requirements

### Configuration

1. Add a `ThemeConfig` sub-struct to `internal/config/config.go`:
   ```go
   type ThemeConfig struct {
       DefaultName string
   }
   ```
2. Add a `Theme ThemeConfig` field to the top-level `Config` struct (place it after `Auth`, mirroring the alphabetic-by-domain ordering).
3. Parse `THEME_DEFAULT_NAME` in `Load()` with default `"default"`:
   ```go
   Theme: ThemeConfig{
       DefaultName: env("THEME_DEFAULT_NAME", "default"),
   },
   ```
4. Add validation in `Config.Validate()`: `Theme.DefaultName` MUST NOT be empty. Append `"THEME_DEFAULT_NAME must not be empty"` to the `errs` slice on failure.
5. Update `Config.String()` to include `Theme{DefaultName:...}`. Not a secret; print as-is. Keep the existing redaction of `GoogleClientSecret`.
6. Update `README.md` configuration table with the new `THEME_DEFAULT_NAME` row. Place it adjacent to the other admin-managed knobs for discoverability.

### Migration

7. Create `internal/db/migrations/006_create-themes.sql` with:
   - `-- +up` section creating the `themes` table per the schema in the architecture doc:
     - Columns: `id TEXT PRIMARY KEY`, `name TEXT NOT NULL UNIQUE`, `is_default INTEGER NOT NULL DEFAULT 0`, six `color_*` TEXT columns (`color_bg`, `color_surface`, `color_border`, `color_text`, `color_text_muted`, `color_accent`), `font_family TEXT NOT NULL`, `font_family_mono TEXT NOT NULL DEFAULT ''`, `radius TEXT NOT NULL`, `created_at TEXT NOT NULL DEFAULT (datetime('now'))`, `updated_at TEXT NOT NULL DEFAULT (datetime('now'))`.
     - Partial unique index `themes_one_default`: `CREATE UNIQUE INDEX themes_one_default ON themes(is_default) WHERE is_default = 1;`
   - `-- +down` section that `DROP INDEX IF EXISTS themes_one_default;` then `DROP TABLE IF EXISTS themes;`.
8. Mirror the `005_create-devices.sql` format exactly (whitespace, comments, marker placement). The migration runner parses on the markers; do not invent new section markers.
9. Also place the same SQL into `internal/db/schema/006_create-themes.sql` (the `schema/` directory is what `sqlc.yaml` reads to type-check the queries; it parallels the `migrations/` directory). Existing migrations have entries in both places.

### sqlc queries

10. Create `internal/db/queries/themes.sql` with the following queries (annotations exactly as written -- the architecture doc has the SQL verbatim):
    - `CreateTheme :exec` -- INSERT all 12 user-supplied columns. (`created_at` and `updated_at` use the column defaults.)
    - `GetThemeByID :one` -- SELECT all 14 columns WHERE `id = ?`.
    - `GetThemeByName :one` -- SELECT all 14 columns WHERE `name = ?`.
    - `GetDefaultTheme :one` -- SELECT all 14 columns WHERE `is_default = 1` LIMIT 1.
    - `ListThemes :many` -- SELECT all 14 columns ORDER BY `name`.
    - `UpdateTheme :exec` -- UPDATE name, six color columns, font_family, font_family_mono, radius, and `updated_at = datetime('now')` WHERE `id = ?`. (Does NOT touch `is_default` -- that is changed only via SetDefault / ClearDefault.)
    - `DeleteTheme :execresult` -- DELETE FROM themes WHERE `id = ? AND is_default = 0`. (Returns `sql.Result` so the service can distinguish "row deleted" from "row exists but is default".)
    - `ClearDefaultTheme :exec` -- UPDATE themes SET `is_default = 0` WHERE `is_default = 1`.
    - `SetDefaultTheme :execresult` -- UPDATE themes SET `is_default = 1` WHERE `id = ?`. (Returns `sql.Result` so the service can detect "no such id".)
    - `CountDefaultThemes :one` -- SELECT COUNT(*) FROM themes WHERE `is_default = 1`. (Used by EnsureDefault in TASK-017.)

11. Run `sqlc generate` to produce `internal/db/themes.sql.go` and a new `Theme` struct entry in `internal/db/models.go`. Verify the generated code compiles:
    - `db.Theme` should have all 14 columns in the struct, with `IsDefault int64`, all timestamp fields as `string`, and all other columns as `string`.
    - The `Queries` methods should match each `:exec` / `:one` / `:many` / `:execresult` annotation.

12. Do NOT add any application code in this task. The `internal/themes/` package and the views are owned by TASK-017 and TASK-018 respectively.

## Acceptance Criteria

From SPEC-004:

- [ ] AC-32: When `THEME_DEFAULT_NAME` is not set, then the parsed config field equals `"default"`.
- [ ] (Schema-level prerequisites for AC-1, AC-2, AC-16, AC-17, AC-20) The `themes` table exists with the documented columns, the `themes_one_default` partial unique index exists, and a fresh `db.OpenTestDB(t)` produces a working table that accepts the documented INSERT / UPDATE / DELETE shapes.
- [ ] When `Validate()` is called with `Theme.DefaultName = ""`, then it returns a non-nil error mentioning `THEME_DEFAULT_NAME`.
- [ ] When two rows are inserted into `themes` both with `is_default = 1`, then SQLite returns a UNIQUE constraint violation on the second INSERT (the partial unique index works).

## Skills to Use

- `add-config` -- for the new `ThemeConfig` sub-struct, parsing, validation, README update.
- `add-migration` -- for `006_create-themes.sql` and its `schema/` mirror.
- `add-store` -- for `themes.sql` and the `sqlc generate` step.
- `green-bar` -- run before marking complete.

## Test Requirements

1. **Config test** (in `internal/config/`): a table-driven test that asserts:
   - Default `Theme.DefaultName == "default"` when env unset.
   - `THEME_DEFAULT_NAME=onyx` produces `Theme.DefaultName == "onyx"`.
   - `Theme.DefaultName == ""` causes `Validate()` to return a non-nil error mentioning `THEME_DEFAULT_NAME`.
   - Use `t.Setenv` for env-var driven tests; do NOT mutate the process environment without cleanup.

2. **Migration test** (in `internal/db/`): mirror `auth_schema_test.go` if it exists; otherwise add a new `themes_schema_test.go` that:
   - Calls `db.OpenTestDB(t)` (which runs all migrations including the new one).
   - Asserts the `themes` table exists by querying `sqlite_master WHERE type='table' AND name='themes'`.
   - Asserts the partial unique index exists by querying `sqlite_master WHERE type='index' AND name='themes_one_default'`.
   - Inserts two rows with `is_default = 1` (different IDs and names) and asserts the second INSERT fails with a UNIQUE constraint violation. (The error message in modernc.org/sqlite includes the index name; assert that or just assert any UNIQUE-shaped error.)
   - Inserts one row with `is_default = 1` and a second with `is_default = 0`; both succeed.

3. Use table-driven tests where the variations are mostly inputs. Follow `.claude/rules/testing.md` -- tests must earn their existence. Do not assert sqlc-generated method signatures; the compiler does that for free.

4. Do NOT write tests for the queries themselves in this task -- the service tests in TASK-017 will exercise them through the service layer. The migration test above is the single guard for the schema shape.

## Definition of Done

- [ ] `ThemeConfig` sub-struct added with one field (`DefaultName`).
- [ ] `Config.Theme` field added and populated in `Load()`.
- [ ] Validation rejects empty `Theme.DefaultName`.
- [ ] `Config.String()` includes the theme block.
- [ ] README configuration table updated with the new env var.
- [ ] Migration `006_create-themes.sql` created with `+up` and `+down` sections, both in `internal/db/migrations/` and `internal/db/schema/`.
- [ ] `internal/db/queries/themes.sql` created with all ten queries listed above.
- [ ] `sqlc generate` produced `internal/db/themes.sql.go` and updated `internal/db/models.go` with a `Theme` struct.
- [ ] Tests for config + migration pass.
- [ ] green-bar passes (`gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./...`).
- [ ] No new third-party dependencies.
