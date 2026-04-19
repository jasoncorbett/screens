---
name: add-view
description: Scaffold a new templ view page with its handler following the project's views/ conventions. Use when the user asks to add a page, view, or HTML route. Creates the templ file, handler file with route registration, and an httptest-based test. Read .claude/rules/http.md and .claude/rules/testing.md before applying.
---

# Add a view page

Follow `.claude/rules/http.md` (the views section) exactly. This skill encodes that flow so nothing is skipped.

## Reference pattern

Read these files before starting to understand the established pattern:
- `views/routes.go` -- route registration infrastructure
- `views/demo.go` -- handler pattern (init + registerRoute + handleXxx)
- `views/demo.templ` -- templ component pattern (@layout wrapper)
- `views/layout.templ` -- base HTML layout

## Files to create or edit

1. **Templ file** -- `views/<name>.templ`:
   - Define a templ component that wraps content in `@layout("<Page Title>")`.
   - If the page needs htmx fragment endpoints, define separate fragment components in the same file.
   - Keep components focused -- one page component, plus fragment components as needed.

2. **Handler file** -- `views/<name>.go`:
   - Register routes in an `init()` function using `registerRoute(func(router *http.ServeMux) { ... })`.
   - Full-page handler: `func handleXxx(w http.ResponseWriter, r *http.Request)` that calls `<component>().Render(r.Context(), w)`.
   - Fragment handlers (for htmx): `func handleXxxFragment(w http.ResponseWriter, r *http.Request)` that render partial HTML.
   - Read request-scoped values from `r.Context()`.
   - Errors to client via `http.Error(w, "sanitized message", code)`. Log details with `slog`.

3. **Test** -- `views/<name>_test.go`:
   - Use `net/http/httptest.NewRecorder` to test the handler.
   - Verify the response status is 200.
   - Verify the content type contains `text/html`.
   - Test at least one error/edge case if the handler has branching logic.
   - Follow `.claude/rules/testing.md` -- tests must earn their existence.

## After creating files

1. Run `templ generate` to compile the `.templ` file to Go code.
2. Run the `green-bar` skill. Do not report the view as done until all four checks pass.

## Do not

- Do not import third-party template or middleware libraries.
- Do not register routes outside the `init()` function in the handler file.
- Do not put business logic in handlers -- keep handlers thin, delegate to internal packages.
- Do not create layout variants -- use the existing `layout.templ` wrapper.
