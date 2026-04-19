# HTTP & Routing

- Use `net/http.ServeMux` with Go 1.22+ method+path patterns (e.g. `"GET /health"`, `"POST /v1/things/{id}"`). No third-party routers.
- Top-level route registration lives in `api/routes.go` via `AddRoutes(mux *http.ServeMux)`. Versioned routes are registered by `api/v1/routes.go` (and future `api/v2/…`) and composed from the top-level `AddRoutes`.
- Handlers take `(w http.ResponseWriter, r *http.Request)` and are registered as `router.HandleFunc("METHOD /path", handleFoo)`. Name handlers `handleXxx` in lowercase — they are package-private.
- Return errors to clients via `http.Error(w, msg, code)`. Do not leak internal error strings; log the detailed error with `slog` and return a sanitized message.
- Read request-scoped values from `r.Context()`. Propagate that context to downstream calls (DB, HTTP clients).
- One structured log line per request outcome is enough; avoid logging on every step of a handler.
- New endpoints: add the handler in the appropriate package, register it in that package's `routes.go`, and add a test using `net/http/httptest`.

- HTML views live in `views/`. Route registration follows the same pattern as `api/v1/` — use `registerRoute` in an `init()` function. View handlers render templ components; htmx fragment endpoints return partial HTML.
- The `.templ` file and its handler `.go` file sit next to each other in `views/`.
- Run `templ generate` after editing `.templ` files to regenerate Go code.

