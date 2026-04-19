---
name: add-store
description: Create a data access layer component using sqlc for type-safe SQL. Use when the user asks to add database operations for an entity. Creates SQL queries, runs sqlc generate, and adds tests. Read .claude/rules/go-style.md and .claude/rules/testing.md before applying.
---

# Add a data store

This skill creates a data access component using `sqlc` to generate type-safe Go code from SQL queries. No ORM, no hand-written SQL boilerplate.

## Before starting

1. Read the existing `sqlc.yaml` configuration to understand the project's sqlc setup.
2. Read existing query files in `internal/db/queries/` for the established pattern.
3. If adding a new entity, use the `add-migration` skill first to create the table.

## Files to create or edit

1. **Migration** -- use the `add-migration` skill to create the table schema if it doesn't exist yet.

2. **SQL queries** -- `internal/db/queries/<entity>.sql`:
   - Write SQL queries with sqlc annotations:
     ```sql
     -- name: Get<Entity> :one
     SELECT * FROM <entities> WHERE id = ? LIMIT 1;

     -- name: List<Entities> :many
     SELECT * FROM <entities> ORDER BY created_at DESC;

     -- name: Create<Entity> :exec
     INSERT INTO <entities> (id, name, created_at)
     VALUES (?, ?, ?);

     -- name: Update<Entity> :exec
     UPDATE <entities>
     SET name = ?, updated_at = ?
     WHERE id = ?;

     -- name: Delete<Entity> :exec
     DELETE FROM <entities> WHERE id = ?;
     ```
   - Use sqlc annotations: `:one`, `:many`, `:exec`, `:execresult` as appropriate.
   - Use `?` for SQLite parameter placeholders.
   - One query file per entity (or per logical domain if closely related).

3. **Run sqlc generate**:
   ```
   sqlc generate
   ```
   This produces type-safe Go code in the configured output directory (typically `internal/db/`).
   The generated code includes:
   - A `Queries` struct with methods for each SQL query
   - Model structs matching the database schema
   - All methods accept `context.Context`

4. **Store wrapper** (optional) -- `internal/store/<entity>.go`:
   - If the generated sqlc interface is sufficient, use it directly.
   - If you need a higher-level interface (e.g., to combine multiple queries, add business logic, or provide a mockable interface for handlers):
     ```go
     type <Entity>Store interface {
         Get(ctx context.Context, id string) (<Entity>, error)
         List(ctx context.Context) ([]<Entity>, error)
         Create(ctx context.Context, e *<Entity>) error
         Update(ctx context.Context, e *<Entity>) error
         Delete(ctx context.Context, id string) error
     }
     ```
   - Implement by wrapping the sqlc-generated `*db.Queries`.
   - Wrap errors with context: `fmt.Errorf("store.<entity>.Get: %w", err)`.
   - Use `sql.ErrNoRows` checks for not-found cases; return a typed error the handler can check.

5. **Health check** -- register a database connectivity check:
   ```go
   api.RegisterHealthCheck(func(ctx context.Context) error {
       return db.PingContext(ctx)
   })
   ```
   Only do this once for the database connection, not per store.

6. **Test** -- `internal/store/<entity>_test.go` (or alongside the generated code):
   - Use the test helper to create an in-memory SQLite database with all migrations applied.
   - Test CRUD operations: create, read, list, update, delete.
   - Test not-found returns the expected error.
   - Test constraint violations if applicable.
   - Use `t.Cleanup` to close the database.
   - Follow `.claude/rules/testing.md`.

## sqlc configuration

The project uses `sqlc.yaml` at the repo root. If it doesn't exist yet, create it:
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

## Before finishing

1. Run `sqlc generate` to regenerate code after any query changes.
2. Run the `green-bar` skill. All four checks must pass.

## Do not

- Do not hand-write SQL query execution code -- let sqlc generate it.
- Do not modify sqlc-generated files -- they are overwritten on regenerate.
- Do not use an ORM or query builder library.
- Do not expose `*sql.DB` outside the store/db package -- consumers use the generated Queries struct or a store interface.
- Do not skip tests -- test through the generated code against a real (in-memory) database.
