---
name: add-endpoint
description: Scaffold a new HTTP endpoint following the project's net/http + ServeMux conventions. Use when the user asks to add a route, endpoint, or handler. Creates the handler file, registers it in the appropriate routes.go, and adds an httptest-based test. Read .claude/rules/http.md and .claude/rules/testing.md before applying.
---

# Add an HTTP endpoint

Follow `.claude/rules/http.md` exactly. This skill encodes that flow so nothing is skipped.

## Decide the package

- Unversioned, infra-level routes (health, readiness, metrics): `api/` — registered in `api/routes.go`.
- Versioned API routes: `api/v1/` (or the version the user specifies) — registered in that package's `routes.go`.
- If adding a new version, create `api/vN/routes.go` with an `AddRoutes(router *http.ServeMux)` and compose it from the top-level `api/routes.go`.

## Files to create or edit

1. **Handler file** — `api/<pkg>/<resource>.go`:
   - Handler signature: `func handleXxx(w http.ResponseWriter, r *http.Request)`.
   - Lowercase (package-private) name: `handleThing`, not `HandleThing`.
   - Read request-scoped values from `r.Context()`; propagate context into any downstream call.
   - Errors to client: `http.Error(w, "sanitized message", http.StatusXxx)`. Log details separately with `slog`, including an `"err"` key.
   - Emit at most one structured log line per request outcome.

2. **Route registration** — in that package's `routes.go`, inside `AddRoutes`:
   - `router.HandleFunc("METHOD /path", handleXxx)` using Go 1.22+ method+path patterns (e.g. `"POST /v1/things/{id}"`).

3. **Test** — `api/<pkg>/<resource>_test.go`:
   - Table-driven; use `net/http/httptest.NewRecorder` for unit tests, `httptest.NewServer` when exercising the real mux.
   - Cover: happy path, at least one error/validation path, and any non-obvious branch.
   - Follow `.claude/rules/testing.md` — tests must earn their existence. Do not add coverage-padding assertions.

## Before finishing

Run the `green-bar` skill (gofmt, vet, build, test). Do not report the endpoint as done until all four pass.

## Do not

- Do not import a third-party router or middleware library.
- Do not leak internal error strings to clients.
- Do not register routes outside the package's `routes.go`.
