---
id: TASK-002
title: "Database connection and migration runner"
spec: SPEC-001
arch: ARCH-001
status: review
priority: p0
prerequisites: [TASK-001]
skills: [add-migration, green-bar]
created: 2026-04-18
author: architect
---

# TASK-002: Database connection and migration runner

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Create the `internal/db/` package with database connection management and a migration runner. This is the core of the storage engine: it opens SQLite with the correct pragmas, provides a migration runner that auto-applies versioned SQL schemas from an embedded filesystem, and includes the seed migration for the `schema_migrations` table. After this task, the database can be opened, migrated, and closed -- but it is not yet wired into `main.go` (that is TASK-003).

## Context

TASK-001 adds the `DBConfig` sub-struct to `internal/config/config.go`. This task consumes those config values. The `internal/db/` package does not exist yet and must be created from scratch.

The project uses `modernc.org/sqlite` as the SQLite driver (approved, see ADR-001). This dependency must be added to `go.mod` via `go get`.

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/testing.md` -- test conventions
- `.claude/rules/logging.md` -- logging conventions
- `.claude/rules/config.md` -- config conventions (do not read env vars in this package)
- `internal/config/config.go` -- the `DBConfig` struct created by TASK-001
- `docs/plans/architecture/phase-1-foundation/arch-storage-engine.md` -- full component design
- `.claude/skills/add-migration/SKILL.md` -- migration file conventions

## Requirements

### 1. Add the `modernc.org/sqlite` dependency

Run `go get modernc.org/sqlite@latest` to add the pure-Go SQLite driver to `go.mod`.

### 2. Create `internal/db/db.go`

Create the database connection management file with:

- A blank import of `modernc.org/sqlite` to register the driver (or use it in `sql.Open`).
- An `Open` function that:
  1. Accepts a config struct with Path, MaxOpenConns, MaxIdleConns, and ConnMaxLifetime fields. Define a local `Config` struct in this package that mirrors the relevant fields from `config.DBConfig`, OR accept the individual parameters, OR accept `config.DBConfig` directly. The architecture recommends a local `DBConfig` struct but importing `config.DBConfig` is also acceptable -- choose whichever avoids circular imports (there should be none since `internal/db` does not import `internal/config`; the caller in `main.go` bridges them).
  2. Constructs the SQLite DSN with pragma parameters: `?_pragma=journal_mode(wal)&_pragma=foreign_keys(1)`. The DSN format for `modernc.org/sqlite` is the file path followed by query parameters.
  3. Calls `sql.Open("sqlite", dsn)`.
  4. Configures connection pool: `db.SetMaxOpenConns`, `db.SetMaxIdleConns`, `db.SetConnMaxLifetime`.
  5. Pings the database with `db.PingContext(ctx)` using a context with a reasonable timeout (e.g., 5 seconds).
  6. Logs the successful connection with `slog.Info("database opened", "path", cfg.Path)`.
  7. Returns `(*sql.DB, error)`.

- A `Close` function that:
  1. Accepts `*sql.DB`.
  2. Calls `db.Close()`.
  3. Logs the close with `slog.Info("database closed")`.
  4. Returns the error from `db.Close()`.

### 3. Create `internal/db/migrate.go`

Create the migration runner with:

- An `embed.FS` variable embedding `migrations/*.sql`:
  ```go
  //go:embed migrations/*.sql
  var migrationFS embed.FS
  ```

- A `Migrate` function that:
  1. Accepts `context.Context` and `*sql.DB`.
  2. Delegates to `MigrateFS(ctx, db, migrationFS)`.
  3. Returns the error.

- A `MigrateFS` function that:
  1. Accepts `context.Context`, `*sql.DB`, and `embed.FS`.
  2. Creates the `schema_migrations` table if it does not exist (this is self-bootstrapping -- do NOT use a migration for this table, create it directly in the runner):
     ```sql
     CREATE TABLE IF NOT EXISTS schema_migrations (
         version    INTEGER PRIMARY KEY,
         name       TEXT NOT NULL,
         applied_at TEXT NOT NULL DEFAULT (datetime('now'))
     );
     ```
  3. Reads all `.sql` files from the `migrations/` directory in the embedded FS.
  4. Sorts files by version number (parsed from the filename prefix before the first `_`).
  5. For each migration file, checks if the version is already in `schema_migrations`.
  6. For each pending migration:
     a. Reads the file content.
     b. Parses the `-- +up` section (content between `-- +up` and either `-- +down` or end-of-file).
     c. Begins a transaction.
     d. Executes the up SQL within the transaction.
     e. Inserts a record into `schema_migrations` with the version and filename.
     f. Commits the transaction.
     g. Logs: `slog.Info("migration applied", "version", version, "name", filename)`.
  7. If any migration fails, rolls back that transaction and returns the error with context: `fmt.Errorf("migration %d (%s): %w", version, filename, err)`.
  8. If no pending migrations exist, logs: `slog.Info("migrations up to date")`.

### 4. Create `internal/db/migrations/` directory with seed migration

Create `internal/db/migrations/001_initial.sql`:

```sql
-- +up
-- Seed migration: no domain tables yet.
-- The schema_migrations table is created by the migration runner itself.
-- This file exists so the migrations directory is not empty and the embed directive works.

-- +down
-- Nothing to reverse.
```

Note: The `schema_migrations` table is created by the migration runner directly (in step 3.2 above), NOT by a migration file. This seed file ensures the `migrations/` directory is not empty (Go's `embed` directive requires at least one matching file). Future specs will add real migrations here (e.g., `002_create-users.sql`).

### 5. Create `internal/db/db_test.go`

Tests for the connection and migration logic:

1. **TestOpen_InMemory**: Test that `Open` with path `":memory:"` returns a valid `*sql.DB` that responds to Ping. Verify WAL pragma (note: `:memory:` may report `memory` instead of `wal` for journal_mode -- test with a temp file if needed). Verify `PRAGMA foreign_keys` returns `1`.
2. **TestMigrateFS_CreatesSchemaTable**: Test that `MigrateFS` with an empty (no `.sql` files) FS creates the `schema_migrations` table. Use an in-memory database.
3. **TestMigrateFS_AppliesMigrations**: Create a test `embed.FS` (use `testing/fstest.MapFS`) with two migration files. Verify both are applied and recorded in `schema_migrations`.
4. **TestMigrateFS_SkipsApplied**: Run migrations twice. Verify the second run does not re-apply and returns no error.
5. **TestMigrateFS_InvalidSQL**: Create a test FS with a migration containing invalid SQL. Verify the error is returned and the migration is NOT recorded in `schema_migrations`.
6. **TestMigrateFS_OrderByVersion**: Create migrations with versions out of filename sort order (e.g., `002_second.sql` before `010_tenth.sql`). Verify they are applied in numeric version order.

Note on `testing/fstest.MapFS`: This implements `fs.FS` but NOT `embed.FS`. The `MigrateFS` function should accept `fs.FS` (not `embed.FS`) as its parameter type so that tests can pass `fstest.MapFS`. The public `Migrate` function calls `MigrateFS` with the embedded FS. Update the signature from the architecture doc accordingly:
```go
func MigrateFS(ctx context.Context, db *sql.DB, fsys fs.FS) error
```

## Acceptance Criteria

- [ ] AC-1: When the service starts with default configuration and no existing database, then a `screens.db` file is created in the working directory and the `schema_migrations` table exists.
- [ ] AC-3: When the service starts and there are pending migrations, then each migration is applied in version order, logged, and recorded in the `schema_migrations` table.
- [ ] AC-4: When the service starts and all migrations have already been applied, then no migrations are re-applied and the service starts normally.
- [ ] AC-5: When a migration contains invalid SQL, then the service logs the error and exits with a non-zero status without applying partial changes.
- [ ] AC-10: When `PRAGMA journal_mode` is queried on the opened database, then the result is `wal`.
- [ ] AC-11: When `PRAGMA foreign_keys` is queried on the opened database, then the result is `1` (enabled).

Note: AC-1 is partially covered here (the database creation and schema_migrations table). Full coverage of AC-1 requires TASK-003 (main.go wiring) so the service actually starts and creates the file.

## Skills to Use

- `add-migration` -- reference for migration file format and conventions
- `green-bar` -- run before marking complete

## Test Requirements

See Requirements section 5 for detailed test specifications. Key points:

1. Use in-memory SQLite for tests (no filesystem access needed for most tests).
2. Use `testing/fstest.MapFS` for migration FS injection in tests.
3. Use `t.Cleanup` to close database connections.
4. Accept `fs.FS` (not `embed.FS`) in `MigrateFS` to enable test injection.
5. Verify pragma values by querying `PRAGMA journal_mode` and `PRAGMA foreign_keys` on opened databases.
6. Test idempotency: running migrations twice must not error or re-apply.
7. Test failure isolation: a bad migration must not leave partial state.

## Definition of Done

- [ ] `internal/db/db.go` created with `Open` and `Close` functions
- [ ] `internal/db/migrate.go` created with `Migrate` and `MigrateFS` functions
- [ ] `internal/db/migrations/001_initial.sql` created as seed migration
- [ ] `modernc.org/sqlite` added to `go.mod`
- [ ] WAL mode and foreign keys enabled via DSN pragmas
- [ ] Migration runner self-bootstraps `schema_migrations` table
- [ ] Migration runner applies pending migrations in version order within transactions
- [ ] Migration runner logs each applied migration
- [ ] All tests pass (open, migrate, skip-applied, invalid-sql, ordering)
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] Code follows `.claude/rules/go-style.md` and `.claude/rules/logging.md`
