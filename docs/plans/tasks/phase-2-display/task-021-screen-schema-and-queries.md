---
id: TASK-021
title: "Screen / Page / WidgetInstance migrations and sqlc queries"
spec: SPEC-006
arch: ARCH-006
status: ready
priority: p0
prerequisites: []
skills: [add-migration, add-store, green-bar]
created: 2026-05-13
author: architect
---

# TASK-021: Screen / Page / WidgetInstance migrations and sqlc queries

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add three new database tables (`screens`, `pages`, `widget_instances`) with their foreign-key constraints (RESTRICT for `screens.theme_id`, CASCADE for `pages.screen_id` and `widget_instances.page_id`), their unique-position indexes, and the sqlc query files that the downstream service layer (TASK-022 / TASK-023 / TASK-024) will use. Run `sqlc generate` so the typed query code lands in `internal/db/`.

This task is pure data-layer plumbing. No service code, no HTTP handlers. It is a strict prerequisite for every other task in this spec.

## Context

- The migration runner reads files from `internal/db/migrations/*.sql`. Files are applied in numeric order by filename prefix. Existing migrations are 001-006; this task adds 007, 008, 009.
- sqlc queries live under `internal/db/queries/<entity>.sql`. Each `:exec`, `:one`, `:many`, `:execresult` annotation tells sqlc what to generate. The output is in `internal/db/<entity>.sql.go` plus shared structs in `internal/db/models.go`.
- The existing `internal/db/queries/themes.sql` and `internal/db/queries/devices.sql` are the patterns to mirror.
- FK behaviour requires `PRAGMA foreign_keys = ON` per connection. The existing `db.Open` sets this; this task assumes it.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/skills/add-migration/SKILL.md`
- `.claude/skills/add-store/SKILL.md`
- `internal/db/migrations/005_create-devices.sql` -- example of a table with FK constraints and indexes.
- `internal/db/migrations/006_create-themes.sql` -- example of a table with a partial unique index.
- `internal/db/queries/themes.sql` -- example of the sqlc annotation style.
- `internal/db/queries/devices.sql` -- example of a sqlc file with a `:execresult` query.
- `internal/db/migrate.go` -- read so you understand how migrations are picked up (no changes required here).
- `internal/db/db.go` and `internal/db/open.go` -- check that `PRAGMA foreign_keys = ON` is already set.
- `sqlc.yaml` -- the project's sqlc configuration; do not modify.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- sections "Data Model > Database Schema" and "Storage > sqlc Queries".

## Requirements

### Migrations

1. Create `internal/db/migrations/007_create-screens.sql` with both `-- +up` and `-- +down` sections:
   - Up: create the `screens` table with columns `id`, `name`, `theme_id`, `rotation_interval_seconds`, `created_at`, `updated_at`. The `theme_id` column is `TEXT NOT NULL REFERENCES themes(id) ON DELETE RESTRICT`. The `name` column has a UNIQUE constraint. The `rotation_interval_seconds` column defaults to 30. Timestamps default to `datetime('now')`. Create `idx_screens_theme_id` on `theme_id` for the in-use lookup.
   - Down: drop the index, then drop the table.

2. Create `internal/db/migrations/008_create-pages.sql`:
   - Up: create the `pages` table with columns `id`, `screen_id`, `name`, `position`, `created_at`, `updated_at`. The `screen_id` is `TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE`. The `name` defaults to `''`. Create a UNIQUE index `idx_pages_screen_position` on `(screen_id, position)`. Create a secondary index `idx_pages_screen_id` on `screen_id` for the list-by-screen lookup.
   - Down: drop both indexes, then drop the table.

3. Create `internal/db/migrations/009_create-widget-instances.sql`:
   - Up: create the `widget_instances` table with columns `id`, `page_id`, `type`, `config`, `position`, `created_at`, `updated_at`. The `page_id` is `TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE`. `type` and `config` are `TEXT NOT NULL`. Create UNIQUE index `idx_widget_instances_page_position` on `(page_id, position)`. Create secondary index `idx_widget_instances_page_id` on `page_id`.
   - Down: drop both indexes, then drop the table.

4. The exact SQL is in the architecture document's "Data Model > Database Schema" section -- copy it verbatim.

### sqlc Queries

5. Create `internal/db/queries/screens.sql` with the following annotated queries (full SQL is in the architecture document's "Storage > sqlc Queries > screens.sql"):
   - `CreateScreen :exec`
   - `GetScreenByID :one`
   - `GetScreenByName :one`
   - `ListScreens :many`
   - `ListScreenSummaries :many` -- joins `themes` for the theme name and uses a subquery for the page count.
   - `UpdateScreen :exec`
   - `DeleteScreen :execresult`
   - `CountScreensUsingTheme :one`

6. Create `internal/db/queries/pages.sql` with:
   - `CreatePage :exec`
   - `GetPageByID :one` -- WHERE clause includes both `id = ?` and `screen_id = ?` for defence-in-depth.
   - `ListPagesByScreen :many`
   - `MaxPagePosition :one` -- uses `COALESCE(MAX(position), 0)` so an empty page set returns 0.
   - `UpdatePage :exec`
   - `DeletePage :execresult`
   - `GetPageNeighbor :one` -- returns the row at a specific `(screen_id, position)`. Used by the reorder swap.
   - `SetPagePosition :exec` -- updates a single page's position by `(id, screen_id)`.

7. Create `internal/db/queries/widget_instances.sql` with:
   - `CreateWidgetInstance :exec`
   - `GetWidgetInstanceByID :one` -- WHERE includes `page_id = ?` for defence-in-depth.
   - `ListWidgetInstancesByPage :many`
   - `ListWidgetInstancesByPageIDs :many` -- accepts `IN (sqlc.slice('page_ids'))` for batch fetch in `GetScreenFull`.
   - `MaxWidgetPosition :one` -- `COALESCE(MAX(position), 0)`.
   - `DeleteWidgetInstance :execresult`
   - `GetWidgetNeighbor :one`
   - `SetWidgetPosition :exec`

### Generation

8. Run `sqlc generate` (from the repo root). Verify three new files are produced:
   - `internal/db/screens.sql.go`
   - `internal/db/pages.sql.go`
   - `internal/db/widget_instances.sql.go`

9. `internal/db/models.go` MUST gain three new structs (sqlc generates them automatically):
   - `Screen` -- ID, Name, ThemeID, RotationIntervalSeconds (int64), CreatedAt, UpdatedAt
   - `Page` -- ID, ScreenID, Name, Position (int64), CreatedAt, UpdatedAt
   - `WidgetInstance` -- ID, PageID, Type, Config, Position (int64), CreatedAt, UpdatedAt

10. Do NOT hand-edit `screens.sql.go`, `pages.sql.go`, `widget_instances.sql.go`, or `models.go` -- they are sqlc-generated and would be overwritten on regeneration. If sqlc produces an unexpected result, fix the input `.sql` file and regenerate.

### Verification

11. After `sqlc generate`, run `go build ./...` to confirm the generated code compiles.

12. Add a focused migration test in `internal/db/screens_schema_test.go` (mirror the existing `internal/db/themes_schema_test.go`):
    - Use `db.OpenTestDB(t)` to get a migrated DB.
    - Assert the three tables exist by querying `sqlite_master`.
    - Assert the foreign-key constraints are configured correctly: insert a screen referencing a non-existent theme should fail; insert a page referencing a non-existent screen should fail; insert a widget_instance referencing a non-existent page should fail.
    - Assert CASCADE on screen delete: create a theme, a screen, a page, a widget_instance, then delete the screen, then assert the page row and widget_instance row are gone.
    - Assert RESTRICT on theme delete: create a theme, a screen referencing it, then attempt `DELETE FROM themes WHERE id = ?` and verify the error message contains `FOREIGN KEY constraint failed`. The theme row MUST still exist after the attempt.
    - Assert the unique-position constraint: insert two pages with the same `(screen_id, position)` and verify the second INSERT fails.

13. The test file lives in package `db` (internal tests) so it can use the unexported `OpenTestDB` helper. The test file MUST be `internal/db/screens_schema_test.go` to keep the migration-level tests grouped with the existing patterns.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-7 (data layer half): The CASCADE constraint on `pages.screen_id` and `widget_instances.page_id` is configured such that deleting a screen via SQL deletes its descendants.
- [ ] AC-9 (data layer half): The RESTRICT constraint on `screens.theme_id` causes a raw SQL theme-delete to fail with the SQLite FK-violation error when at least one screen references the theme.
- [ ] AC-15 (data layer half): The `UNIQUE (screen_id, position)` index on `pages` and `UNIQUE (page_id, position)` index on `widget_instances` prevent duplicate position values.

## Skills to Use

- `add-migration` -- for creating the three SQL migration files.
- `add-store` -- for creating the three SQL query files and running sqlc.
- `green-bar` -- run before marking review (gofmt, vet, build, test).

## Test Requirements

The test file `internal/db/screens_schema_test.go` MUST exercise the FK-and-index behaviour against a real (in-memory) SQLite database. Specifically:

1. **All three tables exist after migration**: query `sqlite_master` for `screens`, `pages`, `widget_instances` and assert each is found.

2. **Theme FK is RESTRICT, not SILENT, not CASCADE, not SET NULL**:
   - Seed a theme, insert a screen pointing at it.
   - Attempt `DELETE FROM themes WHERE id = ?`.
   - Assert the error string contains `FOREIGN KEY constraint failed`.
   - Assert `SELECT COUNT(*) FROM themes WHERE id = ?` is still 1.
   - Assert `SELECT COUNT(*) FROM screens WHERE theme_id = ?` is still 1.

3. **Screen → pages → widget_instances CASCADE chain**:
   - Seed a theme, screen, page, widget_instance.
   - `DELETE FROM screens WHERE id = ?`.
   - Assert the page row is gone AND the widget_instance row is gone.
   - (The DB does the cascade; no application code involved.)

4. **Unique position within parent**:
   - Insert two pages with the same `(screen_id, position)`. Assert the second insert fails with a UNIQUE-constraint error.
   - Same for widget_instances on `(page_id, position)`.

5. **MaxPagePosition / MaxWidgetPosition return 0 on empty**:
   - With no pages: `MaxPagePosition` returns 0 (via the `COALESCE(MAX(position), 0)`).
   - Same for `MaxWidgetPosition`.

6. **CountScreensUsingTheme**:
   - Seed a theme and zero screens: count is 0.
   - Insert one screen using that theme: count is 1.

Test style: `package db`, `t.Helper()` where appropriate, table-driven where the cases share a setup. Follow `.claude/rules/testing.md`.

## Definition of Done

- [ ] All three migrations created and apply cleanly under `db.OpenTestDB`.
- [ ] All three sqlc query files created and `sqlc generate` produces the expected `.sql.go` files.
- [ ] `internal/db/models.go` contains the three new structs (sqlc-generated; do not hand-edit).
- [ ] `internal/db/screens_schema_test.go` covers FK behaviour, CASCADE chain, and uniqueness constraints.
- [ ] green-bar passes: `gofmt -l .` is empty, `go vet ./...` clean, `go build ./...` succeeds, `go test ./...` passes.
- [ ] No new third-party dependencies.
- [ ] Migration filenames are zero-padded and sequential (007, 008, 009).
