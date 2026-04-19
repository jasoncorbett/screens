---
id: TASK-003
title: "Health check integration and main.go wiring"
spec: SPEC-001
arch: ARCH-001
status: ready
priority: p0
prerequisites: [TASK-002]
skills: [green-bar]
created: 2026-04-18
author: architect
---

# TASK-003: Health check integration and main.go wiring

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Wire the database into the application lifecycle: open at startup, run migrations, register a database health check, and close during graceful shutdown. After this task, the screens service has a fully functional storage engine that creates the database file on first run, auto-migrates, reports health, and shuts down cleanly.

## Context

TASK-001 added `DBConfig` to the config package. TASK-002 created `internal/db/` with `Open`, `Close`, `Migrate`, and the seed migration. This task integrates those pieces into `main.go` and registers a health check via the existing `api.RegisterHealthCheck` mechanism.

The existing health endpoint (`GET /health`) returns a JSON object with component statuses. The `api.RegisterHealthCheck` function accepts a `HealthCheckFunc` (which returns a `HealthCheck` struct with Name and Status). The database health check will add `"database": "ok"` or `"database": "error: <message>"` to this response.

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/http.md` -- HTTP conventions
- `.claude/rules/logging.md` -- logging conventions
- `main.go` -- current application wiring (to be modified)
- `api/health.go` -- health check registration pattern (`RegisterHealthCheck`, `HealthCheckFunc`, `HealthCheck`, `Status`)
- `internal/db/db.go` -- `Open` and `Close` functions (created by TASK-002)
- `internal/db/migrate.go` -- `Migrate` function (created by TASK-002)
- `internal/config/config.go` -- `DBConfig` fields (created by TASK-001)
- `docs/plans/architecture/phase-1-foundation/arch-storage-engine.md` -- "main.go Wiring" section

## Requirements

### 1. Modify `main.go` to open the database

After config loading and logging setup, add:

1. Call `db.Open()` with the config values from `cfg.DB`. Pass a context for the ping operation. If it fails, `log.Fatalf("database open: %v", err)`.
2. The `db.Open` function from TASK-002 accepts a config struct. Bridge the `cfg.DB` values to whatever parameter type `db.Open` expects.

### 2. Modify `main.go` to run migrations

After opening the database:

1. Call `db.Migrate(context.Background(), sqlDB)`. If it fails, close the database and `log.Fatalf("database migration: %v", err)`.
2. Migrations must complete before the HTTP server starts accepting requests.

### 3. Register the database health check

After migrations complete:

1. Call `api.RegisterHealthCheck` with a function that:
   - Creates a context with a 5-second timeout.
   - Calls `sqlDB.PingContext(ctx)`.
   - Returns `api.HealthCheck{Name: "database", Status: ...}` where Status is `{Ok: true}` on success or `{Ok: false, Message: "error: " + err.Error()}` on failure.

### 4. Modify `main.go` to close the database on shutdown

After the HTTP server has shut down (after `srv.Shutdown` returns):

1. Call `db.Close(sqlDB)`. If it returns an error, log it with `slog.Error` but do not `log.Fatalf` -- the process is already shutting down.

### 5. Add integration test

Create `main_test.go` (or add to existing test file) with:

1. **TestHealthCheckWithDatabase**: Start a test server with the full mux (or at minimum, the health handler with a registered DB health check). Make a GET request to `/health`. Verify the response includes `"database": "ok"`.
2. **TestHealthCheckDatabaseUnhealthy**: Register a health check that returns unhealthy. Verify the response includes the database status with an error and returns HTTP 503.

Alternatively, these can be tested directly through `api/health_test.go` by registering mock health checks and testing the handler.

## Acceptance Criteria

- [ ] AC-1: When the service starts with default configuration and no existing database, then a `screens.db` file is created in the working directory and the `schema_migrations` table exists.
- [ ] AC-2: When the service starts with `DB_PATH` set to a custom path (e.g., `/tmp/test-screens.db`), then the database is created at that path instead of the default.
- [ ] AC-3: When the service starts and there are pending migrations, then each migration is applied in version order, logged, and recorded in the `schema_migrations` table.
- [ ] AC-6: When `GET /health` is called and the database is reachable, then the response includes `"database": "ok"` and the overall status is 200 (assuming no other checks fail).
- [ ] AC-7: When `GET /health` is called and the database is not reachable, then the response includes `"database"` with an error status and the overall HTTP status is 503.
- [ ] AC-9: When the service shuts down gracefully, then the database connection is closed without errors.

## Skills to Use

- `green-bar` -- run before marking complete

## Test Requirements

1. Test the health check handler returns `"database": "ok"` when a healthy DB check is registered. Use `httptest.NewRecorder` to test the handler directly.
2. Test the health check handler returns HTTP 503 and an error status for database when an unhealthy check is registered.
3. Note on testing main.go: Full startup/shutdown integration testing of main.go is complex. Focus the tests on the health check integration. The startup wiring (open, migrate, close) is implicitly tested by the fact that the service runs and the health check passes. AC-1, AC-2, and AC-9 can be verified by manual testing or a higher-level integration test.
4. Follow `.claude/rules/testing.md` conventions.

Important: The `api.healthChecks` slice is package-level state. Tests that call `api.RegisterHealthCheck` will accumulate checks across tests. Either:
- Test the health handler behavior by constructing the expected state carefully, or
- If the current design does not support resetting health checks between tests, note this as a limitation and test accordingly (e.g., test in a single test function that registers and verifies).

## Definition of Done

- [ ] `main.go` opens database after config load, before HTTP server start
- [ ] `main.go` runs migrations after opening database
- [ ] `main.go` registers database health check via `api.RegisterHealthCheck`
- [ ] `main.go` closes database during shutdown (after server stops)
- [ ] Startup fails fast (log.Fatalf) if database open or migration fails
- [ ] Health check returns `"database": "ok"` when DB is reachable
- [ ] Health check returns unhealthy status when DB is not reachable
- [ ] Tests verify health check behavior
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] Code follows `.claude/rules/go-style.md` and `.claude/rules/logging.md`
