---
id: TASK-005
title: "Auth configuration and database migrations"
spec: SPEC-002
arch: ARCH-002
status: ready
priority: p0
prerequisites: []
skills: [add-config, add-migration, green-bar]
created: 2026-04-20
author: architect
---

# TASK-005: Auth configuration and database migrations

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add authentication configuration settings and create the database migrations for users, sessions, and invitations tables. This is the foundation task for admin auth -- all subsequent auth tasks depend on these database tables and config values. Also add the sqlc query files and run code generation so that type-safe Go query code is available for later tasks.

## Context

The project's configuration pattern is established in `internal/config/config.go` with sub-structs and env-var helpers. The database migration system is implemented in `internal/db/migrate.go` using embedded SQL files. The existing `001_initial.sql` is a seed migration. The `sqlc.yaml` at the repo root is already configured for SQLite with queries in `internal/db/queries/` and output to `internal/db/`.

### Files to Read Before Starting

- `.claude/rules/config.md` -- configuration conventions
- `.claude/rules/go-style.md` -- Go style conventions
- `internal/config/config.go` -- existing config pattern to extend
- `internal/db/migrate.go` -- migration runner (understand file naming)
- `internal/db/migrations/001_initial.sql` -- existing migration format
- `sqlc.yaml` -- sqlc configuration
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- data model and config sections

## Requirements

1. Add an `AuthConfig` sub-struct to `internal/config/config.go` with the following fields:
   - `AdminEmail` (string) -- Google email of the initial admin
   - `GoogleClientID` (string) -- OAuth 2.0 client ID
   - `GoogleClientSecret` (string) -- OAuth 2.0 client secret
   - `GoogleRedirectURL` (string) -- OAuth callback URL
   - `SessionDuration` (time.Duration) -- session lifetime
   - `CookieName` (string) -- session cookie name

2. Parse the following environment variables in `Load()`:
   - `ADMIN_EMAIL` (string, no default -- required)
   - `GOOGLE_CLIENT_ID` (string, no default -- required)
   - `GOOGLE_CLIENT_SECRET` (string, no default -- required)
   - `GOOGLE_REDIRECT_URL` (string, no default -- required)
   - `SESSION_DURATION` (duration, default: `168h`)
   - `SESSION_COOKIE_NAME` (string, default: `screens_session`)

3. Add validation in `Config.Validate()`:
   - `ADMIN_EMAIL` must not be empty
   - `GOOGLE_CLIENT_ID` must not be empty
   - `GOOGLE_CLIENT_SECRET` must not be empty
   - `GOOGLE_REDIRECT_URL` must not be empty
   - `SESSION_DURATION` must be at least 1 minute

4. Add a `String()` method to `Config` (or update it if it exists) that redacts `GoogleClientSecret` (never print secrets).

5. Create migration `internal/db/migrations/002_create-users.sql` with the users table schema from the architecture doc.

6. Create migration `internal/db/migrations/003_create-sessions.sql` with the sessions table schema from the architecture doc.

7. Create migration `internal/db/migrations/004_create-invitations.sql` with the invitations table schema from the architecture doc.

8. Create sqlc query file `internal/db/queries/users.sql` with queries: GetUserByEmail, GetUserByID, CreateUser, ListUsers, DeactivateUser, CountActiveAdmins.

9. Create sqlc query file `internal/db/queries/sessions.sql` with queries: CreateSession, GetSessionByTokenHash, DeleteSession, DeleteSessionsByUserID, DeleteExpiredSessions.

10. Create sqlc query file `internal/db/queries/invitations.sql` with queries: CreateInvitation, GetInvitationByEmail, GetInvitationByID, ListInvitations, DeleteInvitation, DeleteInvitationByEmail.

11. Run `sqlc generate` to produce type-safe Go code.

12. Add `golang.org/x/oauth2` to `go.mod` (run `go get golang.org/x/oauth2@latest`). This is needed now so that subsequent tasks can import it without dependency issues.

13. Update the README.md configuration table with the new environment variables.

## Acceptance Criteria

- [ ] AC-18: When `ADMIN_EMAIL` is empty, then config validation fails with a descriptive error.
- [ ] AC-19: When `GOOGLE_CLIENT_ID` or `GOOGLE_CLIENT_SECRET` is empty, then config validation fails.
- [ ] AC-22: When `GOOGLE_CLIENT_SECRET` is set, then it does not appear in any log output or health check response (verified via Config.String() redaction).
- [ ] Migrations create users, sessions, and invitations tables with correct schemas when the service starts.
- [ ] sqlc-generated code compiles without errors.

## Skills to Use

- `add-config` -- for the AuthConfig sub-struct, parsing, validation, README update
- `add-migration` -- for the three migration files
- `add-store` -- for the sqlc query files and code generation
- `green-bar` -- run before marking complete

## Test Requirements

1. Test that `Validate()` returns an error when `AdminEmail` is empty.
2. Test that `Validate()` returns an error when `GoogleClientID` is empty.
3. Test that `Validate()` returns an error when `GoogleClientSecret` is empty.
4. Test that `Validate()` returns an error when `GoogleRedirectURL` is empty.
5. Test that `Validate()` passes when all required auth fields are set.
6. Test that `Config.String()` does not contain the client secret value.
7. Test that migrations apply successfully using `db.OpenTestDB(t)` (the test helper runs all migrations including the new ones).
8. Use table-driven tests. Follow `.claude/rules/testing.md`.

## Definition of Done

- [ ] `AuthConfig` sub-struct added with all six fields
- [ ] `Config.Auth` field added and populated in `Load()`
- [ ] Validation rejects empty required auth fields
- [ ] Config String() redacts GoogleClientSecret
- [ ] Three migration files created with correct schemas
- [ ] Three sqlc query files created
- [ ] `sqlc generate` runs successfully and produces compilable code
- [ ] `golang.org/x/oauth2` added to go.mod
- [ ] README.md configuration table updated
- [ ] Tests pass for config validation and migration application
- [ ] green-bar passes (gofmt, vet, build, test)
