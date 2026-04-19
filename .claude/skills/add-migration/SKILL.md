---
name: add-migration
description: Add a database schema migration following the project's sequential versioned pattern. Use when the user asks to create or modify database tables. Creates the SQL migration file with up/down sections. Read the existing migrations and migration runner before applying.
---

# Add a database migration

This skill adds a versioned SQL migration file for schema changes.

## Before starting

1. Check existing migrations in `internal/store/migrations/` to determine the next sequence number.
2. Read the migration runner in `internal/store/migrate.go` to understand how migrations are applied.

## Files to create

1. **Migration file** -- `internal/store/migrations/NNN_<name>.sql`:
   - NNN is zero-padded (001, 002, ...), sequential, and globally unique.
   - Name describes the change in kebab-case (e.g., `001_create-users.sql`, `002_add-device-tokens.sql`).
   - Include both up and down sections:
     ```sql
     -- +up
     CREATE TABLE IF NOT EXISTS users (
         id TEXT PRIMARY KEY,
         username TEXT NOT NULL UNIQUE,
         password_hash TEXT NOT NULL,
         created_at TEXT NOT NULL DEFAULT (datetime('now'))
     );

     -- +down
     DROP TABLE IF EXISTS users;
     ```
   - Use SQLite-compatible SQL syntax.
   - Use `IF NOT EXISTS` / `IF EXISTS` guards for safety.
   - Use `TEXT` for timestamps (SQLite stores as ISO-8601 strings).
   - Use `TEXT` for UUIDs/IDs.

## Guidelines

- One logical change per migration. Don't combine unrelated schema changes.
- The down migration should fully reverse the up migration.
- For data migrations (INSERT/UPDATE), include rollback logic in the down section.
- Column additions to existing tables: use `ALTER TABLE ... ADD COLUMN`.
- Column removals: SQLite doesn't support `DROP COLUMN` before 3.35.0. If needed, use the rename-copy-drop pattern.
- Foreign keys: SQLite requires `PRAGMA foreign_keys = ON` at connection time, not in migrations.
- Indexes: create indexes in the same migration as the table they reference.

## Before finishing

Run the `green-bar` skill. Verify:
- The migration runner picks up the new file.
- `go build ./...` compiles (migration may be embedded).
- `go test ./...` passes (store tests should apply migrations).

## Do not

- Do not skip the down migration -- it's required for rollback.
- Do not modify existing migration files -- create a new migration instead.
- Do not use auto-increment integers for IDs unless there's a specific reason (prefer UUIDs).
- Do not hardcode data that should be configurable.
