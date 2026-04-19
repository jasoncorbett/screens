---
name: add-store
description: Create a data access layer component following the project's repository pattern. Use when the user asks to add database operations for an entity. Creates the store interface, SQL implementation, and tests. Read .claude/rules/go-style.md and .claude/rules/testing.md before applying.
---

# Add a data store

This skill creates a data access component using `database/sql` with raw SQL. No ORM.

## Files to create or edit

1. **Entity model** -- `internal/model/<entity>.go` (if not already defined):
   - Define the entity struct with exported fields.
   - Keep model structs free of database-specific tags or methods.
   - If the entity is only used by one store, define it in the store file instead.

2. **Store file** -- `internal/store/<entity>.go`:
   - Define the store interface for testability:
     ```go
     type <Entity>Store interface {
         Get(ctx context.Context, id string) (<Entity>, error)
         List(ctx context.Context) ([]<Entity>, error)
         Create(ctx context.Context, e *<Entity>) error
         Update(ctx context.Context, e *<Entity>) error
         Delete(ctx context.Context, id string) error
     }
     ```
   - Implement with a struct that holds `*sql.DB`:
     ```go
     type sql<Entity>Store struct {
         db *sql.DB
     }

     func New<Entity>Store(db *sql.DB) <Entity>Store {
         return &sql<Entity>Store{db: db}
     }
     ```
   - Use `context.Context` for all database operations: `db.QueryContext(ctx, ...)`, `db.ExecContext(ctx, ...)`.
   - Wrap errors with context: `fmt.Errorf("store.<entity>.Get: %w", err)`.
   - Use `sql.ErrNoRows` checks for not-found cases; return a typed error the handler can check.
   - Use parameterized queries to prevent SQL injection.

3. **Migration** -- use the `add-migration` skill to create the table schema.

4. **Health check** -- register a database connectivity check:
   ```go
   api.RegisterHealthCheck(func(ctx context.Context) error {
       return db.PingContext(ctx)
   })
   ```
   Only do this once for the database connection, not per store.

5. **Test** -- `internal/store/<entity>_test.go`:
   - Use a temporary file database (`:memory:` or `t.TempDir()` + file).
   - Run migrations before tests.
   - Test CRUD operations: create, read, list, update, delete.
   - Test not-found returns the expected error.
   - Test constraint violations if applicable.
   - Use `t.Cleanup` to close the database.
   - Follow `.claude/rules/testing.md`.

## Before finishing

Run the `green-bar` skill. All four checks must pass.

## Do not

- Do not use an ORM or query builder library.
- Do not embed SQL in string concatenation -- always use parameterized queries.
- Do not expose `*sql.DB` outside the store package -- consumers use the interface.
- Do not skip the interface definition -- it enables testing handlers without a real database.
