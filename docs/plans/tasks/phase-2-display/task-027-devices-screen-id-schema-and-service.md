---
id: TASK-027
title: "devices.screen_id schema migration + sqlc regen + auth.Device.ScreenID + AssignDeviceToScreen service method"
spec: SPEC-007
arch: ARCH-007
status: ready
priority: p0
prerequisites: []
skills: [add-migration, add-store, green-bar]
created: 2026-05-25
author: architect
---

# TASK-027: devices.screen_id schema migration + sqlc regen + auth.Device.ScreenID + AssignDeviceToScreen service method

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add the device-to-Screen mapping at the data layer: a new nullable `screen_id TEXT REFERENCES screens(id) ON DELETE SET NULL` column on the existing `devices` table (additive migration 010), the regenerated sqlc bindings, a new `AssignDeviceScreen` query, a `ScreenID *string` field on `auth.Device`, and an `auth.Service.AssignDeviceToScreen(ctx, deviceID, screenID)` method. This task ships nothing user-facing; it is the data-layer prerequisite for the admin UI (TASK-029) and the render handler (TASK-030).

## Context

- The `devices` table was created by migration `005_create-devices.sql` (SPEC-003). The columns are `id`, `name`, `token_hash`, `created_by`, `created_at`, `last_seen_at`, `revoked_at`. This task adds one column.
- The `screens` table was created by migration `007_create-screens.sql` (SPEC-006). It already exists by the time migration 010 runs; the FK target is valid.
- The `auth.Device` Go type lives in `internal/auth/device.go`. The `deviceFromRow` mapper translates `db.Device` (sqlc-generated) into `auth.Device`. The existing pattern for nullable timestamps (`LastSeenAt *time.Time`, `RevokedAt *time.Time`) is the model to mirror for the new `ScreenID *string` field.
- The `auth.Service` lives in `internal/auth/auth.go`. The pattern for device-mutating service methods (`CreateDevice`, `RotateDeviceToken`, `RevokeDevice`) is what `AssignDeviceToScreen` should mirror: call the sqlc query, check `res.RowsAffected()`, translate 0 rows to `ErrDeviceNotFound`.
- sqlc is configured at `sqlc.yaml`; running `sqlc generate` from the project root regenerates the bindings.

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- stdlib-only, idiomatic Go.
- `.claude/rules/testing.md` -- write tests that earn their existence.
- `.claude/skills/add-migration/SKILL.md` -- migration conventions.
- `.claude/skills/add-store/SKILL.md` -- sqlc query + service-method conventions.
- `internal/db/migrations/005_create-devices.sql` -- the existing devices table shape.
- `internal/db/migrations/007_create-screens.sql` -- the FK target table.
- `internal/db/queries/devices.sql` -- where the new query goes; existing SELECTs need their column list expanded.
- `internal/db/devices.sql.go` -- the sqlc-generated file you must NOT hand-edit; regenerate with `sqlc generate`.
- `internal/db/models.go` -- the sqlc-generated `Device` struct; will gain `ScreenID sql.NullString` after regen.
- `internal/auth/device.go` -- the domain `Device` struct + `deviceFromRow` mapper; gain `ScreenID *string` field + mapping logic.
- `internal/auth/auth.go` -- the `Service` methods that mirror the new shape (see `CreateDevice`, `RotateDeviceToken`).
- `internal/auth/auth_device_test.go` -- existing patterns for device-related service tests.
- `internal/db/screens_schema_test.go` -- analogous schema test for screens migrations; use as a model for verifying the new column exists and the FK fires.
- `docs/plans/specs/phase-2-display/spec-screen-display.md` -- requirements 1-7; AC-1, AC-5, AC-6.
- `docs/plans/architecture/phase-2-display/arch-screen-display.md` -- "Data Model" and "Storage" sections.
- `docs/plans/architecture/decisions/adr-009-device-to-screen-mapping.md` -- the rationale for SET NULL.

## Requirements

### Migration

1. Create `internal/db/migrations/010_add-device-screen-id.sql` with:
   ```sql
   -- +up
   ALTER TABLE devices ADD COLUMN screen_id TEXT
       REFERENCES screens(id) ON DELETE SET NULL;

   CREATE INDEX idx_devices_screen_id ON devices(screen_id);

   -- +down
   DROP INDEX IF EXISTS idx_devices_screen_id;
   ALTER TABLE devices DROP COLUMN screen_id;
   ```

2. The migration must run cleanly against an existing database with rows in `devices` (test setup verifies this -- existing rows get NULL).

### sqlc Queries

3. Edit `internal/db/queries/devices.sql`:
   - Add `screen_id` to the column list of EVERY existing `SELECT` query: `GetDeviceByID`, `GetDeviceByTokenHash`, `ListDevices`. The new column goes at the end of the select list (after `revoked_at`).
   - Add the new query:
     ```sql
     -- name: AssignDeviceScreen :execresult
     UPDATE devices
        SET screen_id = ?
      WHERE id = ?;
     ```

4. Run `sqlc generate` (from project root). The regenerated `internal/db/devices.sql.go` and `internal/db/models.go` will:
   - Add `ScreenID sql.NullString` as the final field on `db.Device`.
   - Add `AssignDeviceScreenParams` struct (`ScreenID sql.NullString`, `ID string`) and `Queries.AssignDeviceScreen` method.

   Commit the regenerated files alongside the SQL files.

### auth.Device domain type + mapper

5. Edit `internal/auth/device.go`:
   - Add field `ScreenID *string` to the `Device` struct (placed after `RevokedAt`).
   - In `deviceFromRow`, after the existing `revoked_at` handling, add:
     ```go
     if row.ScreenID.Valid {
         s := row.ScreenID.String
         dev.ScreenID = &s
     }
     ```

### auth.Service.AssignDeviceToScreen

6. Edit `internal/auth/auth.go` to add the new service method (placed near `RotateDeviceToken`):

   ```go
   // AssignDeviceToScreen sets the device's screen_id to the given screenID,
   // or clears it if screenID == "". The caller MUST have pre-validated that
   // a non-empty screenID resolves to an existing Screen (the auth package
   // does not depend on the screens package). The FK constraint at the DB
   // layer also catches an invalid screenID as a secondary defence.
   //
   // Returns ErrDeviceNotFound when the device id is unknown. Returns nil
   // on success (including when the assignment did not change -- a no-op
   // re-assign is not an error).
   func (s *Service) AssignDeviceToScreen(ctx context.Context, deviceID, screenID string) error {
       var screenIDArg sql.NullString
       if screenID != "" {
           screenIDArg = sql.NullString{String: screenID, Valid: true}
       }
       res, err := s.queries.AssignDeviceScreen(ctx, db.AssignDeviceScreenParams{
           ScreenID: screenIDArg,
           ID:       deviceID,
       })
       if err != nil {
           return fmt.Errorf("assign device screen: %w", err)
       }
       n, err := res.RowsAffected()
       if err != nil {
           return fmt.Errorf("assign device screen rows: %w", err)
       }
       if n == 0 {
           return ErrDeviceNotFound
       }
       return nil
   }
   ```

7. Verify the existing `ErrDeviceNotFound` is exported from `internal/auth/auth.go` (it already is); no new error variable is required by this task (the spec mentions `ErrScreenNotFoundForAssignment` as an option, but the actual design uses `screens.ErrScreenNotFound` at the handler tier; this task does not add a new auth error).

## Acceptance Criteria

From SPEC-007:

- [ ] AC-1: After the migration runs, the `devices` table has a nullable `screen_id` column, and existing device rows (created before this spec) have `screen_id IS NULL`.
- [ ] AC-6: When an admin DELETEs a Screen that two devices reference, then both devices' `screen_id` columns become NULL atomically (FK SET NULL).

Plus the task-internal AC:

- [ ] AC-T1: `auth.Service.AssignDeviceToScreen(ctx, "nonexistent", "anyValidScreenID")` returns `auth.ErrDeviceNotFound`.
- [ ] AC-T2: `auth.Service.AssignDeviceToScreen(ctx, validDeviceID, "")` sets the device's `screen_id` to NULL; a subsequent `auth.Service.ListDevices` returns the device with `ScreenID == nil`.
- [ ] AC-T3: `auth.Service.AssignDeviceToScreen(ctx, validDeviceID, validScreenID)` sets the device's `screen_id` to the given value; the returned device (via `ListDevices` or a fresh `GetDeviceByTokenHash` of the token returned at create time) has `ScreenID != nil` with `*ScreenID == validScreenID`.
- [ ] AC-T4: Migration 010 runs cleanly against a database that has existing device rows; those rows have `ScreenID == nil` after the migration.

## Skills to Use

- `add-migration` -- for the new schema migration.
- `add-store` -- for the new sqlc query.
- `green-bar` -- run before marking review.

## Test Requirements

Tests live alongside the affected packages:

1. **Schema migration test**: in a new (or existing) `internal/db/devices_screen_id_schema_test.go` (or extend `internal/db/devices_adversarial_test.go`), open a test DB, INSERT a device row, then INSERT a screen row, then UPDATE the device to set its `screen_id`, then DELETE the screen. Assert: a SELECT of the device row returns `screen_id IS NULL` (the FK SET NULL fired).

2. **Service AssignDeviceToScreen happy path**: in `internal/auth/auth_device_test.go`, create a device via `authSvc.CreateDevice`. Insert a screen row directly via SQL (we cannot import `internal/screens` from `internal/auth` -- it would create a cycle; use a raw INSERT). Call `authSvc.AssignDeviceToScreen(ctx, dev.ID, screenID)`. Assert no error. Call `authSvc.ListDevices` and find the device. Assert `dev.ScreenID != nil && *dev.ScreenID == screenID`.

3. **Service AssignDeviceToScreen clear**: same setup as the happy path, then call `AssignDeviceToScreen(ctx, dev.ID, "")`. Assert the device's `ScreenID` is `nil` afterwards.

4. **Service AssignDeviceToScreen unknown device**: call `AssignDeviceToScreen(ctx, "nonexistent", "")`. Assert the error is `auth.ErrDeviceNotFound`.

5. **Cascade-on-Screen-delete**: insert a screen, insert two devices both referencing it, DELETE the screen via raw SQL, SELECT both devices, assert both have `screen_id IS NULL`.

Use `db.OpenTestDB(t)` for an isolated SQLite database per test. Insert screens via raw SQL with `INSERT INTO screens (id, name, theme_id, rotation_interval_seconds) VALUES (...)` -- the test does not need the full themes-service round-trip; it just needs a valid screen ID for the FK. (The themes table seed runs as part of migrations, so a default theme exists with a stable `is_default = 1` row; use `SELECT id FROM themes WHERE is_default = 1` to pick a valid `theme_id`.)

Follow `.claude/rules/testing.md`: use table-driven tests where multiple cases share setup, use `t.Helper()` in helpers.

## Definition of Done

- [ ] `internal/db/migrations/010_add-device-screen-id.sql` exists with up+down.
- [ ] `internal/db/queries/devices.sql` includes `screen_id` in every SELECT plus the new `AssignDeviceScreen` query.
- [ ] `sqlc generate` has been run; the regenerated files are committed.
- [ ] `internal/auth/device.go` has the `ScreenID *string` field and the mapper update.
- [ ] `internal/auth/auth.go` has `AssignDeviceToScreen` matching the signature in Requirement 6.
- [ ] All acceptance criteria tests pass (including the schema cascade test).
- [ ] green-bar passes (`gofmt`, `vet`, `build`, `test`).
- [ ] No new third-party dependencies.
- [ ] No raw token / cookie value is logged anywhere in the new code.
- [ ] Existing `auth_device_test.go` tests still pass (the `Device` struct is extended additively; the zero-value `ScreenID == nil` is the existing behaviour).
