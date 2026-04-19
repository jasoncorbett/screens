---
id: SPEC-001
title: "Storage Engine"
phase: 1
status: ready
priority: p0
created: 2026-04-18
author: pm
---

# Storage Engine

## Problem Statement

The screens service currently has no persistent storage. Every feature on the roadmap -- admin authentication, device tokens, themes, screens, widgets -- requires durable data that survives restarts. Without a storage foundation, no other feature can be built. The storage engine must be the first thing implemented so that all subsequent store implementations (user store, token store, screen store, etc.) have a reliable, migration-managed database to build on.

SQLite is the right choice for this project: it is a single-file embedded database that requires zero external infrastructure, matching the project's design philosophy of minimal operational overhead. A household dashboard service should not require a separate database server.

## User Stories

- As an **admin**, I want the service to remember my account, screens, themes, and device tokens across restarts so that I do not have to reconfigure everything each time the service starts.
- As a **device**, I want the service to persistently recognize my authentication token so that I can reconnect after a service restart without re-provisioning.
- As an **admin**, I want to see the database health status on the health endpoint so that I can monitor whether storage is functioning correctly.
- As an **admin**, I want the database schema to upgrade automatically when I deploy a new version of the service so that I do not need to run manual migration commands.

## Functional Requirements

### Database Connection

1. The system MUST use SQLite via `database/sql` with the `modernc.org/sqlite` pure-Go driver (no CGO required).
2. The system MUST configure the database path via the `DB_PATH` environment variable, defaulting to `screens.db` in the working directory.
3. The system MUST open the database connection pool at startup and close it cleanly during graceful shutdown.
4. The system MUST enable WAL (Write-Ahead Logging) mode on the SQLite database for improved concurrent read performance.
5. The system MUST enable foreign key enforcement on the SQLite connection.
6. The system MUST configure connection pool settings (max open connections, max idle connections, connection max lifetime) via environment variables with sensible defaults.
7. The system SHOULD default to a max of 1 open connection for SQLite (since SQLite serializes writes) and allow override via `DB_MAX_OPEN_CONNS`.
8. The system MUST expose the `*sql.DB` instance (or a thin wrapper) so that store implementations in later phases can use it.

### Migration System

9. The system MUST provide a migration runner that applies versioned SQL migrations in order.
10. The system MUST store applied migration versions in a `schema_migrations` table to track which migrations have been applied.
11. The system MUST run pending migrations automatically at startup, before the HTTP server begins accepting requests.
12. The system MUST apply migrations inside a transaction so that a failed migration does not leave the database in a partially-applied state.
13. The system MUST support "up" migrations. Down (rollback) migrations MAY be supported but are not required.
14. The system MUST embed migration SQL files in the Go binary using `embed.FS` so that migrations ship with the executable and do not require a separate file deployment.
15. The system MUST log each migration as it is applied, including the version number and file name.
16. The system MUST reject startup if a migration fails, logging the error and exiting with a non-zero status.

### Health Check

17. The system MUST register a health check with `api.RegisterHealthCheck` that verifies the database is reachable by executing a `SELECT 1` query (or equivalent ping).
18. The health check MUST return an unhealthy status if the database ping fails or times out.
19. The health check MUST include a meaningful name (e.g., "database") in the health check result.

### Configuration

20. The system MUST add database configuration fields to the existing `config.Config` struct following the established `internal/config` patterns.
21. The system MUST support the following environment variables:
    - `DB_PATH` (string, default: `screens.db`) -- path to the SQLite database file.
    - `DB_MAX_OPEN_CONNS` (int, default: `1`) -- maximum open connections.
    - `DB_MAX_IDLE_CONNS` (int, default: `1`) -- maximum idle connections.
    - `DB_CONN_MAX_LIFETIME` (duration, default: `0` meaning no limit) -- maximum connection lifetime.
22. The system MUST validate that `DB_PATH` is not empty during config validation.

### Foundation for Stores

23. The system MUST provide a clear package structure (e.g., `internal/db/`) that store implementations import to obtain a database handle.
24. The system SHOULD provide a test helper that creates an in-memory SQLite database with all migrations applied, so that store tests in later phases do not require filesystem access.

## Non-Functional Requirements

- **Performance**: SQLite with WAL mode should handle the concurrency profile of a household dashboard (low write volume, moderate reads). The single-writer constraint is acceptable.
- **Reliability**: Migrations are transactional. A failed migration halts startup rather than running against a corrupt schema.
- **Operability**: The database is a single file. Backup is a file copy. No external database server to manage.
- **Testability**: In-memory databases allow fast, isolated tests without filesystem side effects.

## Acceptance Criteria

- [ ] AC-1: When the service starts with default configuration and no existing database, then a `screens.db` file is created in the working directory and the `schema_migrations` table exists.
- [ ] AC-2: When the service starts with `DB_PATH` set to a custom path (e.g., `/tmp/test-screens.db`), then the database is created at that path instead of the default.
- [ ] AC-3: When the service starts and there are pending migrations, then each migration is applied in version order, logged, and recorded in the `schema_migrations` table.
- [ ] AC-4: When the service starts and all migrations have already been applied, then no migrations are re-applied and the service starts normally.
- [ ] AC-5: When a migration contains invalid SQL, then the service logs the error and exits with a non-zero status without applying partial changes.
- [ ] AC-6: When `GET /health` is called and the database is reachable, then the response includes `"database": "ok"` and the overall status is 200 (assuming no other checks fail).
- [ ] AC-7: When `GET /health` is called and the database is not reachable, then the response includes `"database"` with an error status and the overall HTTP status is 503.
- [ ] AC-8: When `DB_PATH` is set to an empty string, then config validation fails with a descriptive error.
- [ ] AC-9: When the service shuts down gracefully, then the database connection is closed without errors.
- [ ] AC-10: When `PRAGMA journal_mode` is queried on the opened database, then the result is `wal`.
- [ ] AC-11: When `PRAGMA foreign_keys` is queried on the opened database, then the result is `1` (enabled).
- [ ] AC-12: When a test helper creates an in-memory database, then all migrations are applied and the returned `*sql.DB` is ready for use by store tests.

## Out of Scope

- Specific table schemas for users, tokens, screens, themes, or widgets (those belong to their respective specs).
- Database backup or replication tooling.
- Multi-database or database-per-tenant support.
- Connection encryption (SQLite is local-file; not applicable).
- Down/rollback migrations (MAY be added later but not required for this spec).
- Any HTTP endpoints beyond the existing health check integration.

## Dependencies

- Depends on: None (this is the first spec in Phase 1).
- Blocked by: None (all dependencies approved).

## Open Questions

All resolved.

- **Q1 (resolved)**: `modernc.org/sqlite` is approved as a dependency exception. Pure Go, no CGO.
- **Q2 (resolved)**: Migration files live under `internal/db/migrations/`.
- **Q3 (resolved)**: `sqlc` is approved as a build tool for generating type-safe Go code from SQL queries. Generated code uses `database/sql` (no runtime dependency). Store implementations use sqlc-generated code rather than hand-written SQL. The `sqlc` binary is a development/build tool, not a runtime dependency.
