---
id: ARCH-001
title: "Storage Engine"
spec: SPEC-001
status: approved
created: 2026-04-18
author: architect
---

# Storage Engine Architecture

## Overview

This architecture establishes the persistent storage foundation for the screens service. It introduces SQLite via `modernc.org/sqlite`, a migration runner that auto-applies versioned SQL schemas at startup, database configuration via environment variables, a health check integration, and a test helper for in-memory databases. All subsequent store implementations (users, tokens, screens, themes, widgets) build on this foundation.

## References

- Spec: `docs/plans/specs/phase-1-foundation/spec-storage-engine.md`
- Related ADRs: ADR-001 (SQLite via modernc.org/sqlite)
- Prerequisite architecture: None (first in Phase 1)

## Data Model

The storage engine itself introduces one internal table for tracking migrations:

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name    TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

No domain tables are created by this spec -- those belong to their respective feature specs. A single seed migration (`001_create-schema-migrations.sql`) establishes the tracking table.

## API Contract

### Endpoints

No new endpoints are introduced. The existing `GET /health` endpoint is extended:

| Method | Path | Change | Response Addition |
|--------|------|--------|-------------------|
| GET | /health | Add database health check | `"database": "ok"` or `"database": "error: <msg>"` |

### Health Check Response Examples

Database healthy:
```json
{
  "running": "ok",
  "database": "ok"
}
```

Database unhealthy:
```json
{
  "running": "ok",
  "database": "error: failed to ping database"
}
```

## Component Design

### Package Layout

```
internal/
  config/
    config.go          -- ADD: DBConfig sub-struct, DB fields in Load(), validation
  db/
    db.go              -- NEW: Open(), Close(), connection setup (WAL, foreign keys, pool)
    migrate.go         -- NEW: migration runner (embed.FS, schema_migrations tracking)
    migrations/        -- NEW: directory for .sql migration files (embedded)
      001_create-schema-migrations.sql
    queries/           -- NEW: directory for sqlc query files (future stores add files here)
    testhelper.go      -- NEW: OpenTestDB() for in-memory SQLite with migrations applied
api/
  health.go            -- EXISTING: no changes needed (uses RegisterHealthCheck)
main.go                -- MODIFY: open DB, run migrations, register health check, close on shutdown
sqlc.yaml              -- NEW: sqlc configuration at repo root
```

### Key Types and Functions

#### internal/db/db.go

```go
package db

import (
    "context"
    "database/sql"
    "fmt"
    "log/slog"
    "time"

    _ "modernc.org/sqlite"
)

// DBConfig holds database connection settings.
// Passed from config.Config.DB -- do not read env vars here.
type DBConfig struct {
    Path            string
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
}

// Open creates a new database connection pool with SQLite pragmas applied.
// The caller is responsible for calling Close on the returned *sql.DB.
func Open(cfg DBConfig) (*sql.DB, error)

// Close closes the database connection pool.
// Wraps db.Close() with logging. Exported for symmetry and future cleanup hooks.
func Close(db *sql.DB) error
```

The `Open` function:
1. Opens the database with `sql.Open("sqlite", dsn)` where DSN includes `?_pragma=journal_mode(wal)&_pragma=foreign_keys(1)`
2. Configures connection pool settings (MaxOpenConns, MaxIdleConns, ConnMaxLifetime)
3. Pings the database to verify connectivity
4. Returns the `*sql.DB` handle

#### internal/db/migrate.go

```go
package db

import (
    "context"
    "database/sql"
    "embed"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies all pending migrations to the database.
// Migrations are applied in version order inside individual transactions.
// Returns an error if any migration fails, leaving the database in the
// last successfully-applied state.
func Migrate(ctx context.Context, db *sql.DB) error

// MigrateFS applies migrations from the given filesystem.
// This is the testable core -- Migrate() calls it with the embedded FS.
func MigrateFS(ctx context.Context, db *sql.DB, fsys embed.FS) error
```

The migration runner:
1. Ensures the `schema_migrations` table exists (bootstraps itself)
2. Reads `.sql` files from the embedded FS, sorted by version number prefix
3. Checks which versions are already applied via `schema_migrations`
4. For each pending migration, executes the `-- +up` section inside a transaction
5. Records the version in `schema_migrations` on success
6. Logs each applied migration with version and filename
7. Returns an error on first failure (does not continue past a failed migration)

Migration file parsing:
- Files are named `NNN_description.sql` (e.g., `001_create-schema-migrations.sql`)
- The version number is parsed from the filename prefix (before the first `_`)
- The `-- +up` marker separates the up migration section
- The `-- +down` marker (if present) separates the down migration section
- Only the `-- +up` section is executed by `MigrateFS`

#### internal/db/testhelper.go

```go
package db

import (
    "context"
    "database/sql"
    "testing"
)

// OpenTestDB creates an in-memory SQLite database with all migrations applied.
// It registers a cleanup function to close the database when the test completes.
// Fails the test immediately if the database cannot be opened or migrations fail.
func OpenTestDB(t *testing.T) *sql.DB
```

The test helper:
1. Opens an in-memory SQLite database (`:memory:`)
2. Applies the same pragmas as production (WAL mode is not meaningful for `:memory:` but foreign keys are)
3. Runs all migrations via `MigrateFS`
4. Registers `t.Cleanup` to close the database
5. Returns the ready-to-use `*sql.DB`

#### internal/config/config.go additions

```go
type DBConfig struct {
    Path            string
    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
}

type Config struct {
    HTTP HTTPConfig
    Log  LogConfig
    DB   DBConfig  // NEW
}
```

In `Load()`:
```go
DB: DBConfig{
    Path:            env("DB_PATH", "screens.db"),
    MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 1),
    MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 1),
    ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 0),
},
```

In `Validate()`:
```go
if c.DB.Path == "" {
    errs = append(errs, "DB_PATH must not be empty")
}
```

### Dependencies Between Components

```
main.go
  |-- config.Load()           --> config.Config (including DB settings)
  |-- db.Open(cfg.DB)         --> *sql.DB
  |-- db.Migrate(ctx, sqlDB)  --> applies pending migrations
  |-- api.RegisterHealthCheck --> registers DB ping check
  |-- srv.ListenAndServe()    --> HTTP server starts AFTER migrations complete
  |-- db.Close(sqlDB)         --> called during shutdown, after server stops
```

### main.go Wiring

The modified `main.go` adds these steps between config loading and HTTP server start:

```go
// After config load and logging setup:
sqlDB, err := db.Open(db.DBConfig{
    Path:            cfg.DB.Path,
    MaxOpenConns:    cfg.DB.MaxOpenConns,
    MaxIdleConns:    cfg.DB.MaxIdleConns,
    ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
})
if err != nil {
    log.Fatalf("database open: %v", err)
}

if err := db.Migrate(context.Background(), sqlDB); err != nil {
    log.Fatalf("database migration: %v", err)
}

api.RegisterHealthCheck(func() api.HealthCheck {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    err := sqlDB.PingContext(ctx)
    status := api.Status{Ok: true}
    if err != nil {
        status = api.Status{Ok: false, Message: "error: " + err.Error()}
    }
    return api.HealthCheck{Name: "database", Status: status}
})

// ... existing mux and server setup ...

// During shutdown (after srv.Shutdown):
if err := db.Close(sqlDB); err != nil {
    slog.Error("database close failed", "err", err)
}
```

## Storage

### Schema

The only schema introduced by this spec is the migration tracking table:

```sql
-- 001_create-schema-migrations.sql

-- +up
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- +down
DROP TABLE IF EXISTS schema_migrations;
```

### sqlc Configuration

```yaml
# sqlc.yaml (repo root)
version: "2"
sql:
  - engine: "sqlite"
    queries: "internal/db/queries"
    schema: "internal/db/migrations"
    gen:
      go:
        package: "db"
        out: "internal/db"
```

Note: `sqlc generate` is not run in this spec since there are no query files yet. The configuration is set up so that subsequent store tasks (Phase 1 and beyond) can immediately add query files and generate code.

### SQLite Pragmas

Applied at connection time via DSN parameters:
- `journal_mode=wal` -- Write-Ahead Logging for concurrent reads
- `foreign_keys=1` -- Enable foreign key constraint enforcement

## Security Considerations

- **DB_PATH validation**: The config validates that `DB_PATH` is not empty. The path is not sanitized beyond this -- it is an operator-provided value, not user input.
- **No secrets in DB config**: The current DB configuration contains no secrets (SQLite is a local file). If encryption-at-rest is added later, the key would need redaction in `Config.String()`.
- **SQL injection**: The migration runner executes embedded SQL files that ship with the binary. No user input is interpolated into migration SQL. Future stores use sqlc-generated parameterized queries.
- **File permissions**: SQLite creates the database file with default OS permissions. Documentation should note that operators can restrict access via filesystem permissions.

## Task Breakdown

This architecture decomposes into the following tasks:

1. TASK-001: Add database configuration -- (prerequisite: none)
2. TASK-002: Database connection and migration runner -- (prerequisite: TASK-001)
3. TASK-003: Health check integration and main.go wiring -- (prerequisite: TASK-002)
4. TASK-004: Test helper and sqlc configuration -- (prerequisite: TASK-002)

### Task Dependency Graph

```
TASK-001 (config)
    |
    v
TASK-002 (db package: Open, Migrate, seed migration)
    |
    +-------+-------+
    v               v
TASK-003         TASK-004
(health +        (test helper +
 main.go          sqlc.yaml)
 wiring)
```

TASK-003 and TASK-004 are independent of each other and can be developed in parallel after TASK-002 completes.

## Alternatives Considered

See ADR-001 for the storage engine selection rationale. Additional alternatives considered during architecture design:

- **Third-party migration library (goose, golang-migrate)**: Rejected. The migration runner is simple enough to implement with the stdlib (`embed.FS`, `database/sql`). Adding a dependency for this would violate the project's stdlib-only philosophy.
- **Separate `internal/store/` package for DB connection**: Considered placing the connection management in `internal/store/`. Chose `internal/db/` instead because the connection management, migration runner, sqlc-generated code, and query files form a cohesive unit. Higher-level store wrappers (if needed) can live in `internal/store/` and import `internal/db/`.
- **Auto-detecting migration directory vs. embedded FS**: Chose `embed.FS` so migrations ship with the binary. No separate file deployment required.
