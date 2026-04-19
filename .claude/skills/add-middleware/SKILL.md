---
name: add-middleware
description: Create a new HTTP middleware following stdlib patterns. Use when the user asks to add middleware for auth, logging, CORS, rate limiting, or other cross-cutting concerns. Creates the middleware, context accessors, wiring, and tests. Read .claude/rules/http.md and .claude/rules/testing.md before applying.
---

# Add HTTP middleware

This skill creates middleware using the standard `func(http.Handler) http.Handler` pattern. No third-party middleware libraries.

## Files to create or edit

1. **Middleware file** -- `internal/middleware/<name>.go`:
   - Package: `middleware`
   - Signature: `func <Name>(next http.Handler) http.Handler`
   - Return an `http.HandlerFunc` that wraps `next.ServeHTTP(w, r)`.
   - For middleware that injects values into context:
     - Define an unexported key type: `type <name>Key struct{}`
     - Set values with `context.WithValue(r.Context(), <name>Key{}, value)`
     - Pass the new context via `r = r.WithContext(ctx)`
   - Provide exported accessor functions: `func <Value>FromContext(ctx context.Context) (<type>, bool)`
   - Use `context.Context` value accessors with the comma-ok pattern.

2. **Wiring** -- `main.go`:
   - Wrap the mux or specific route groups: `handler := middleware.<Name>(mux)`
   - Or for route-specific middleware, wrap individual handlers.
   - Import `github.com/jasoncorbett/screens/internal/middleware`.
   - Middleware ordering matters: outermost wraps execute first.

3. **Test** -- `internal/middleware/<name>_test.go`:
   - Test with `httptest.NewRecorder` and a simple `http.HandlerFunc` as the `next` handler.
   - Verify the middleware modifies the request/response as expected.
   - Verify the next handler is called (or blocked, for auth middleware).
   - Test the context accessor returns the correct value.
   - Test edge cases: missing headers, invalid tokens, etc.
   - Follow `.claude/rules/testing.md`.

## Before finishing

Run the `green-bar` skill. All four checks must pass.

## Do not

- Do not import third-party middleware libraries.
- Do not use exported context key types (they leak implementation details).
- Do not put business logic in middleware -- keep it focused on the cross-cutting concern.
- Do not add middleware that logs every request field -- one structured log line per request outcome is enough.
