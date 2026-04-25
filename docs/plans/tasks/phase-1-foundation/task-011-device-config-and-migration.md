---
id: TASK-011
title: "Device config, migration, and sqlc queries"
spec: SPEC-003
arch: ARCH-003
status: ready
priority: p0
prerequisites: []
skills: [add-config, add-migration, add-store, green-bar]
created: 2026-04-25
author: architect
---

# TASK-011: Device config, migration, and sqlc queries

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Lay the data-layer foundation for device authentication: add the two new config knobs the rest of the spec needs, create the `devices` table migration, and add sqlc queries so that subsequent tasks can use type-safe Go for device CRUD. This task touches no Go HTTP code -- it is the bottom of the dependency chain so every later task can assume the schema and queries exist.

## Context

- Existing config sub-struct: `internal/config/config.go` already has an `AuthConfig` with admin-auth fields. New device-auth fields belong on the same struct because they are conceptually part of authentication.
- Existing migrations: `001_initial.sql` through `004_create-invitations.sql`. The next number is `005`.
- Existing sqlc setup: `sqlc.yaml` at repo root, queries in `internal/db/queries/*.sql`, generated code emitted into `internal/db/`. Models for `users`, `sessions`, `invitations` already exist in `internal/db/models.go`.
- The schema uses TEXT-as-ISO8601 timestamps (matching the existing pattern). Nullable timestamps (`last_seen_at`, `revoked_at`) become `sql.NullString` in the generated code.

### Files to Read Before Starting

- `.claude/rules/config.md`
- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/config/config.go` -- existing AuthConfig; add fields here
- `internal/db/migrate.go` -- understand the `-- +up` / `-- +down` parser
- `internal/db/migrations/002_create-users.sql` -- copy formatting/style for new migration
- `internal/db/queries/sessions.sql` -- mirror sqlc annotation style
- `internal/db/queries/invitations.sql` -- mirror sqlc annotation style
- `sqlc.yaml`
- `docs/plans/architecture/phase-1-foundation/arch-device-auth.md` -- "Storage" and "Data Model" sections

## Requirements

1. Add two fields to `AuthConfig` in `internal/config/config.go`:
   - `DeviceCookieName string`
   - `DeviceLastSeenInterval time.Duration`

2. Parse them in `Load()`:
   - `DEVICE_COOKIE_NAME` -- string, default `screens_device`.
   - `DEVICE_LAST_SEEN_INTERVAL` -- duration, default `1m`.

3. Add validation in `Config.Validate()`:
   - `DeviceCookieName` MUST NOT be empty.
   - `DeviceLastSeenInterval` MUST be `>= 0` (zero means "every auth"; negative is meaningless).

4. Update the `Config.String()` method to include the two new fields. They are not secrets, so they print as-is. Keep the existing redaction of `GoogleClientSecret`.

5. Update `README.md` configuration table with the two new env vars. Order them adjacent to the other `SESSION_*` rows for discoverability.

6. Create `internal/db/migrations/005_create-devices.sql` with the schema in the architecture doc:
   - `devices` table with columns: `id TEXT PRIMARY KEY`, `name TEXT NOT NULL`, `token_hash TEXT NOT NULL UNIQUE`, `created_by TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT`, `created_at TEXT NOT NULL DEFAULT (datetime('now'))`, `last_seen_at TEXT`, `revoked_at TEXT`.
   - Index `idx_devices_token_hash` on `token_hash`.
   - Index `idx_devices_revoked_at` on `revoked_at`.
   - Both `-- +up` and `-- +down` sections.

7. Create `internal/db/queries/devices.sql` with the following queries (sqlc annotations exactly as written):
   - `CreateDevice :exec` -- INSERT id, name, token_hash, created_by.
   - `GetDeviceByTokenHash :one` -- SELECT all columns WHERE token_hash = ?.
   - `GetDeviceByID :one` -- SELECT all columns WHERE id = ?.
   - `ListDevices :many` -- SELECT all columns ORDER BY created_at.
   - `RevokeDevice :exec` -- UPDATE devices SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL.
   - `TouchDeviceSeen :execresult` -- UPDATE devices SET last_seen_at = datetime('now') WHERE id = ? AND (last_seen_at IS NULL OR last_seen_at < datetime('now', ?)). The second `?` is the interval expression like `'-60 seconds'`.

8. Run `sqlc generate` to produce `internal/db/devices.sql.go` and a new `Device` struct entry in `internal/db/models.go`. Verify the generated code compiles.

## Acceptance Criteria

From SPEC-003:

- [ ] AC-27: When `DEVICE_COOKIE_NAME` is not set, then the cookie name defaults to `screens_device`.
- [ ] AC-28: When `DEVICE_LAST_SEEN_INTERVAL` is set to `5m`, then the parsed config field equals `5 * time.Minute`. (Throttling behaviour itself is verified in TASK-012.)
- Schema-level prerequisites for AC-1 through AC-5 (devices table exists, token_hash UNIQUE constraint, etc.) verified by migration tests.

## Skills to Use

- `add-config` -- for the two new AuthConfig fields, parsing, validation, README update.
- `add-migration` -- for `005_create-devices.sql`.
- `add-store` -- for `devices.sql` and the `sqlc generate` step.
- `green-bar` -- run before marking complete.

## Test Requirements

1. A table-driven config test that asserts:
   - Default `DeviceCookieName == "screens_device"` when env unset.
   - `DEVICE_COOKIE_NAME=foo` produces `DeviceCookieName == "foo"`.
   - `DEVICE_LAST_SEEN_INTERVAL=5m` produces `DeviceLastSeenInterval == 5*time.Minute`.
   - `DeviceCookieName == ""` causes `Validate()` to return a non-nil error mentioning `DEVICE_COOKIE_NAME`.
   - Negative `DeviceLastSeenInterval` (use `t.Setenv` with `-1s`) causes `Validate()` to fail.
2. A migration test (in `internal/db/`) that calls `db.OpenTestDB(t)` and asserts the `devices` table exists by querying `sqlite_master`. Also asserts the unique index on `token_hash` exists.
3. Use `t.Setenv` for env-var driven tests; do NOT mutate the process environment without cleanup.
4. Follow `.claude/rules/testing.md` -- tests must earn their existence. Do not assert sqlc-generated method signatures; the compiler does that for free.

## Definition of Done

- [ ] Two new `AuthConfig` fields added, parsed, validated, included in `String()`.
- [ ] README configuration table updated.
- [ ] Migration `005_create-devices.sql` created with `+up` and `+down` sections.
- [ ] `internal/db/queries/devices.sql` created with all six queries.
- [ ] `sqlc generate` produced `internal/db/devices.sql.go` and updated `internal/db/models.go` with a `Device` struct.
- [ ] Tests for config + migration pass.
- [ ] green-bar passes (`gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./...`).
- [ ] No new third-party dependencies.
