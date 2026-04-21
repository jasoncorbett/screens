---
id: TASK-004
title: "Test helper and sqlc configuration"
spec: SPEC-001
arch: ARCH-001
status: done
priority: p0
prerequisites: [TASK-002]
skills: [add-store, green-bar]
created: 2026-04-18
author: architect
---

# TASK-004: Test helper and sqlc configuration

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Create a reusable test helper that provides an in-memory SQLite database with all migrations applied, and set up the `sqlc.yaml` configuration file. These two pieces prepare the foundation for all subsequent store implementations: the test helper enables fast isolated tests, and the sqlc config enables type-safe Go code generation from SQL queries.

## Context

TASK-002 created `internal/db/` with `Open`, `Close`, `Migrate`, and `MigrateFS`. This task builds on that foundation to provide:

1. `OpenTestDB(t *testing.T) *sql.DB` -- a convenience function that other packages' tests will import to get a ready-to-use database.
2. `sqlc.yaml` -- the build-tool configuration that tells `sqlc generate` where to find queries, schemas, and where to output generated Go code.

The test helper lives in `internal/db/` (not `internal/db/*_test.go`) because it must be importable by other packages' tests (e.g., `internal/store/user_test.go` will call `db.OpenTestDB(t)`).

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/testing.md` -- test conventions
- `.claude/rules/config.md` -- config conventions
- `internal/db/db.go` -- `Open` function (created by TASK-002)
- `internal/db/migrate.go` -- `Migrate` and `MigrateFS` functions (created by TASK-002)
- `docs/plans/architecture/phase-1-foundation/arch-storage-engine.md` -- test helper and sqlc sections
- `.claude/skills/add-store/SKILL.md` -- sqlc workflow and configuration

## Requirements

### 1. Create `internal/db/testhelper.go`

Create a test helper file (NOT a `_test.go` file -- it must be importable by other packages):

```go
package db

import (
    "context"
    "database/sql"
    "testing"

    _ "modernc.org/sqlite"
)

// OpenTestDB creates an in-memory SQLite database with all migrations applied.
// It registers a cleanup function to close the database when the test completes.
// Fails the test immediately if the database cannot be opened or migrations fail.
func OpenTestDB(t *testing.T) *sql.DB
```

The function must:
1. Call `t.Helper()` so that test failure messages point to the caller, not this helper.
2. Open an in-memory SQLite database using `sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")`.
   - Note: WAL mode is not meaningful for `:memory:` databases but foreign keys are important for constraint testing.
3. Call `Migrate(context.Background(), db)` to apply all embedded migrations.
4. If either open or migrate fails, call `t.Fatalf` with a descriptive message.
5. Register `t.Cleanup(func() { db.Close() })` to automatically close the database when the test finishes.
6. Return the `*sql.DB`.

### 2. Create `sqlc.yaml` at the repository root

Create the sqlc configuration file:

```yaml
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

This tells sqlc:
- Engine is SQLite
- SQL query files live in `internal/db/queries/`
- Schema (migration) files live in `internal/db/migrations/`
- Generated Go code goes to `internal/db/` with package name `db`

### 3. Create `internal/db/queries/` directory

Create the queries directory with a `.gitkeep` file so it is tracked by git. This directory is where future store tasks will add their SQL query files.

### 4. Create `internal/db/testhelper_test.go`

Tests for the test helper itself:

1. **TestOpenTestDB_ReturnsUsableDB**: Call `OpenTestDB(t)`, then execute `SELECT 1` to verify the database is functional.
2. **TestOpenTestDB_HasMigrations**: Call `OpenTestDB(t)`, then query `schema_migrations` to verify migrations were applied.
3. **TestOpenTestDB_ForeignKeysEnabled**: Call `OpenTestDB(t)`, then execute `PRAGMA foreign_keys` and verify the result is `1`.
4. **TestOpenTestDB_MultipleInstances**: Call `OpenTestDB(t)` twice in the same test to verify independent databases are created (insert a row in one, verify it does not appear in the other).

## Acceptance Criteria

- [ ] AC-12: When a test helper creates an in-memory database, then all migrations are applied and the returned `*sql.DB` is ready for use by store tests.

## Skills to Use

- `add-store` -- reference for sqlc configuration conventions
- `green-bar` -- run before marking complete

## Test Requirements

See Requirements section 4 for detailed test specifications. Key points:

1. Test that `OpenTestDB` returns a functional database that responds to queries.
2. Test that migrations are applied (query `schema_migrations` table).
3. Test that foreign keys are enabled (important for constraint testing in store tests).
4. Test that multiple calls return independent databases (isolation).
5. Do NOT run `sqlc generate` in this task -- there are no query files yet. Just verify the configuration file is valid YAML and the directories exist.

## Definition of Done

- [ ] `internal/db/testhelper.go` created with `OpenTestDB` function
- [ ] `OpenTestDB` opens in-memory SQLite, applies migrations, registers cleanup
- [ ] `sqlc.yaml` created at repository root with correct configuration
- [ ] `internal/db/queries/` directory created with `.gitkeep`
- [ ] Tests verify `OpenTestDB` returns a usable, migrated database with foreign keys enabled
- [ ] Tests verify multiple `OpenTestDB` calls return independent databases
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies added (modernc.org/sqlite already added by TASK-002)
- [ ] Code follows `.claude/rules/go-style.md` and `.claude/rules/testing.md`
