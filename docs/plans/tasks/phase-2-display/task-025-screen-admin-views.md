---
id: TASK-025
title: "Screen admin views (list + edit), route wiring, Deps, main.go integration, theme-in-use error UI"
spec: SPEC-006
arch: ARCH-006
status: review
priority: p0
prerequisites: [TASK-023, TASK-024]
skills: [add-view, green-bar]
created: 2026-05-13
author: architect
---

# TASK-025: Screen admin views (list + edit), route wiring, Deps, main.go integration, theme-in-use error UI

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Mount the Screen-level admin UI: a list page at `/admin/screens` with a "New Screen" form and a per-screen edit page at `/admin/screens/{id}/edit` that also lists the screen's pages with reorder / edit / delete affordances and a "Add page" form. Wire the five Screen-level routes and the five Page-level routes into the admin sub-mux. Extend `views.Deps` with `Screens *screens.Service`. Update `main.go` to construct the service. Update `views/admin.templ` to link to `/admin/screens`. Add the new error branch in `handleThemeDelete` for `themes.ErrThemeInUse`.

The per-page edit view (`/admin/screens/{id}/pages/{pageID}/edit`) and the widget-instance management UI ship in TASK-026, NOT this task. This task wires everything up to that point: the Screen list, the Screen edit, the page reorder / edit / delete buttons, but the "Edit" link on a page row in TASK-025 leads to the page-edit URL that TASK-026 implements. (Linking to a URL TASK-026 will own is fine; the route would 404 until TASK-026 lands.)

## Context

- The pattern to mirror is `views/themes.go` plus `views/themes.templ` plus the corresponding test files. The Screen list / edit pages have the same shape but with the `?error=...` flash pattern (per ADR-006 / SPEC-006 R30) rather than inline form re-rendering.
- The `routes.go` registration pattern is established by `themeMux` -- mirror it for `screenMux`.
- The admin landing page (`views/admin.templ`) gets one new line for the `/admin/screens` link.
- `views/themes.go::handleThemeDelete` already exists; this task adds one new `errors.Is` branch for `themes.ErrThemeInUse`.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `.claude/skills/add-view/SKILL.md`
- `views/themes.go` -- the handler-factory pattern.
- `views/themes.templ` -- the templ pattern (page wrapper, hero, status/error cards, table, form).
- `views/devices.go` -- the `?error=...` / `?msg=...` flash pattern; mirror this (not the theme inline-error pattern).
- `views/devices.templ` -- the corresponding templ pattern.
- `views/admin.templ` -- where to add the `/admin/screens` link.
- `views/routes.go` -- where to register the new mux and where to add the `Screens` field on `Deps`.
- `internal/screens/service.go` -- the methods you'll call (output of TASK-022 / TASK-023).
- `internal/themes/service.go` -- to confirm the `ErrThemeInUse` variable from TASK-022 is in place.
- `main.go` -- where to construct `screens.Service` and add it to `views.Deps`.
- `docs/plans/specs/phase-2-display/spec-screen-model.md` -- requirements 28-34, 39, 40, 45; AC-1 through AC-15, AC-21 through AC-23.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- sections "API Contract", "Component Design > Package Layout / views/screens.go / views/screens.templ".

## Requirements

### Templ components

1. Create `views/screens.templ` with the following definitions:

   a. `screensListPage(summaries []screens.ScreenSummary, currentUser *auth.User, csrfToken string, msg, errMsg string, themesList []themes.Theme)`:
      - Wraps in `@layout("Screens - screens")`.
      - Hero with "Screen Management" title and a "Back to Admin" link.
      - Status card if `msg != ""` (rendered via `screenMsgText`).
      - Alert card if `errMsg != ""`.
      - List section: a table with columns Name, Theme, Pages, Rotation (s), Actions. For each `ScreenSummary`, render its `Name`, `ThemeName`, `PageCount`, `RotationIntervalSeconds`, and an `Edit` link to `/admin/screens/{id}/edit` plus a Delete POST form (with CSRF).
      - "New Screen" section: a form with `name` (text), `theme_id` (`<select>` populated from `themesList`, showing each theme's name with the theme's ID as the option value -- mark the default theme `selected`), `rotation_interval_seconds` (number input, min=5, max=3600, value=30), `_csrf` hidden, Submit button.

   b. `screenEditPage(screen screens.Screen, pages []screens.Page, currentUser *auth.User, csrfToken string, msg, errMsg string, themesList []themes.Theme)`:
      - Wraps in `@layout("Edit Screen - screens")`.
      - Hero with `Edit Screen: {screen.Name}` and a "Back to Screens" link.
      - Status / error cards.
      - Section 1: "Screen Settings" -- a form POSTing to `/admin/screens/{screen.ID}` with the same fields as the create form (name pre-filled from `screen.Name`, theme_id pre-selected from `screen.ThemeID`, rotation pre-filled from `screen.RotationIntervalSeconds`).
      - Section 2: "Pages" -- a table with columns Position, Name (or "(no name)"), Actions. For each page in `pages` (already in position order):
        - Position number.
        - Name (or italic "(no name)" if empty).
        - "Move up" POST button to `/admin/screens/{screen.ID}/pages/{page.ID}/move-up` (disabled / hidden if `page.Position == 1`, or rendered but clicking still triggers the service's no-op).
        - "Move down" POST button similarly.
        - "Edit" link to `/admin/screens/{screen.ID}/pages/{page.ID}/edit` (the destination URL belongs to TASK-026).
        - "Delete" POST form.
      - Section 3: "New Page" -- a form POSTing to `/admin/screens/{screen.ID}/pages` with `name` (optional), `_csrf`, Submit.

2. Run `templ generate` so `views/screens_templ.go` is produced.

### Handlers

3. Create `views/screens.go` with the following handler factories. All read user + session from context and 403 if either is nil. All inputs are validated by the service; this handler tier just translates form data into service calls and HTTP responses.

   a. `func handleScreenList(svc *screens.Service, themesSvc *themes.Service) http.HandlerFunc`:
      - Call `svc.ListScreens(ctx)`.
      - Call `themesSvc.List(ctx)` for the theme `<select>` on the New Screen form.
      - Read `msg` and `error` query params; pass into the templ via `screenMsgText` for the success message.
      - Render `screensListPage`.

   b. `func handleScreenCreate(svc *screens.Service) http.HandlerFunc`:
      - Parse the form: `name`, `theme_id`, `rotation_interval_seconds` (use `strconv.Atoi`; on parse error, redirect with `?error=Invalid+rotation+interval`).
      - Call `svc.CreateScreen(ctx, screens.ScreenInput{...})`.
      - On `*screens.ValidationError`: 302 to `/admin/screens?error=<message>` where the message is a friendly summary (e.g., for `name` errors: "Name is required" or "Invalid name format"). The message text can be derived from the `Fields` map's first entry -- pick a stable order (alphabetical by field name) for determinism.
      - On `screens.ErrDuplicateName`: 302 to `?error=A+screen+with+that+name+already+exists`.
      - On `screens.ErrThemeNotFound`: 302 to `?error=Theme+not+found`.
      - On other errors: log at slog.Error and 302 to `?error=Could+not+create+screen`.
      - On success: log info ("screen created", "screen_id", id, "name", name, "created_by", user.Email); 302 to `/admin/screens?msg=created`.

   c. `func handleScreenEditForm(svc *screens.Service, themesSvc *themes.Service) http.HandlerFunc`:
      - Read `id := r.PathValue("id")`; if empty, 302 to `?error=Missing+screen+ID`.
      - Call `svc.GetScreenByID(ctx, id)`. On `ErrScreenNotFound`, 302 to `/admin/screens?error=Screen+not+found`.
      - Call `svc.queries.ListPagesByScreen(ctx, id)` -- wait, the handler should NOT touch `queries` directly; expose a service method instead. ACTION: extend the service with `ListPages(ctx, screenID string) ([]Page, error)` if not already present from TASK-023; if TASK-023 included it, use it. Otherwise add it here as a minimal addition.
      - Call `themesSvc.List(ctx)` for the theme `<select>`.
      - Read `msg` / `error` query params.
      - Render `screenEditPage`.

   d. `func handleScreenUpdate(svc *screens.Service) http.HandlerFunc`:
      - Read id from path.
      - Build `ScreenInput` from form values.
      - Call `svc.UpdateScreen(ctx, id, in)`.
      - Translate errors to `?error=...`: validation, duplicate name, theme not found, screen not found.
      - On success: log + 302 to `/admin/screens?msg=updated`.

   e. `func handleScreenDelete(svc *screens.Service) http.HandlerFunc`:
      - Read id from path.
      - Call `svc.DeleteScreen(ctx, id)`.
      - On `ErrScreenNotFound`: 302 to `?error=Screen+not+found`.
      - On success: log + 302 to `?msg=deleted`.

   f. `func handlePageCreate(svc *screens.Service) http.HandlerFunc`:
      - Read `screenID` from path, `name` from form.
      - Call `svc.CreatePage(ctx, screenID, name)`.
      - On `ErrScreenNotFound`: 302 to `/admin/screens?error=Screen+not+found`.
      - On `*screens.ValidationError`: 302 to `/admin/screens/{id}/edit?error=<message>`.
      - On success: log + 302 to `/admin/screens/{id}/edit?msg=page_created`.

   g. `func handlePageUpdate(svc *screens.Service) http.HandlerFunc`:
      - Read screenID, pageID from path; name from form.
      - Call `svc.UpdatePage(ctx, screenID, pageID, name)`.
      - Translate errors. On success: 302 to `/admin/screens/{id}/edit?msg=page_updated`.

   h. `func handlePageDelete(svc *screens.Service) http.HandlerFunc`:
      - Read screenID, pageID from path.
      - Call `svc.DeletePage(ctx, screenID, pageID)`.
      - Translate errors. On success: 302 to `/admin/screens/{id}/edit?msg=page_deleted`.

   i. `func handlePageMoveUp(svc *screens.Service) http.HandlerFunc`:
      - Read screenID, pageID from path.
      - Call `svc.MovePageUp(ctx, screenID, pageID)`.
      - Translate errors. On success: 302 to `/admin/screens/{id}/edit?msg=page_moved`. The no-op case (already at top) is `nil` from the service; the redirect happens normally and the flash reads "Page reordered." -- that's fine even when the position did not change.

   j. `func handlePageMoveDown(svc *screens.Service) http.HandlerFunc`: same shape as MoveUp.

4. Add a small helper `func screenMsgText(code string) string` mapping flash codes to messages: `created`, `updated`, `deleted`, `page_created`, `page_updated`, `page_deleted`, `page_moved`. (The widget-related codes (`widget_added`, `widget_deleted`, `widget_moved`) land in TASK-026's helper extension.)

### Deps and route wiring

5. Add `Screens *screens.Service` to `views.Deps` in `views/routes.go`.

6. In `views/routes.go::registerAuthRoutes`, register the screen-level routes inside the admin sub-mux. Mirror the existing `themeMux` block:
   ```go
   // Screen management routes require admin role.
   screenMux := http.NewServeMux()
   screenMux.HandleFunc("GET  /admin/screens",                                       handleScreenList(deps.Screens, deps.Themes))
   screenMux.HandleFunc("POST /admin/screens",                                       handleScreenCreate(deps.Screens))
   screenMux.HandleFunc("GET  /admin/screens/{id}/edit",                             handleScreenEditForm(deps.Screens, deps.Themes))
   screenMux.HandleFunc("POST /admin/screens/{id}",                                  handleScreenUpdate(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/delete",                           handleScreenDelete(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages",                            handlePageCreate(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}",                   handlePageUpdate(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/delete",            handlePageDelete(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/move-up",           handlePageMoveUp(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/move-down",         handlePageMoveDown(deps.Screens))
   adminMux.Handle("/admin/screens",  middleware.RequireRole(auth.RoleAdmin)(screenMux))
   adminMux.Handle("/admin/screens/", middleware.RequireRole(auth.RoleAdmin)(screenMux))
   ```

   The page-edit GET route and the widget routes are NOT in this list -- they belong to TASK-026 (which adds them to the same `screenMux`).

### Admin landing page

7. In `views/admin.templ`, add `<p><a href="/admin/screens">Manage Screens</a></p>` near the existing `Manage Users`, `Manage Devices`, `Manage Themes` links, inside the `if isAdmin { ... }` block.

### main.go integration

8. In `main.go`, after the existing `themesSvc := themes.NewService(...)` block, construct the screens service:
   ```go
   screensSvc := screens.NewService(sqlDB, themesSvc, widget.Default())
   ```
   No seed step is needed (no default screen).

9. Pass `screensSvc` into the `views.AddRoutes` Deps:
   ```go
   views.AddRoutes(mux, &views.Deps{
       Auth:             authSvc,
       Google:           googleClient,
       ClientID:         cfg.Auth.GoogleClientID,
       CookieName:       cfg.Auth.CookieName,
       DeviceCookieName: cfg.Auth.DeviceCookieName,
       DeviceLandingURL: cfg.Auth.DeviceLandingURL,
       SecureCookie:     !cfg.Log.DevMode,
       Themes:           themesSvc,
       Widgets:          widget.Default(),
       Screens:          screensSvc,
   })
   ```

10. Add the import `"github.com/jasoncorbett/screens/internal/screens"` to `main.go` if not already present.

### Theme delete: surface ErrThemeInUse

11. In `views/themes.go::handleThemeDelete`, add a new branch BEFORE the generic catch-all `slog.Error("delete theme", ...)`:
    ```go
    if errors.Is(err, themes.ErrThemeInUse) {
        http.Redirect(w, r, "/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen", http.StatusFound)
        return
    }
    ```
    Place it next to the existing `ErrCannotDeleteDefault` branch.

### List pages helper on the service

12. If the screens service does not already expose a method `ListPages(ctx, screenID) ([]Page, error)` (TASK-023 may or may not have included this -- the spec lists it in requirement 35), add it now. Implementation: call `s.queries.ListPagesByScreen(ctx, screenID)` and convert each row via `pageFromRow`. Return an empty (non-nil) slice when no rows exist.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-1: POST `/admin/screens` with a valid form → 302 to `/admin/screens?msg=created` and the row is persisted.
- [ ] AC-2 / AC-3 / AC-4 / AC-5 / AC-6: each invalid input → 302 to `/admin/screens?error=...` and no row is created.
- [ ] AC-7: POST `/admin/screens/{id}/delete` → 302 to `?msg=deleted`; child pages and widget instances are gone (DB-layer cascade).
- [ ] AC-8: GET `/admin/screens` shows every screen with name, theme name, page count, rotation interval.
- [ ] AC-9 / AC-10: When a theme is referenced by a screen, POST `/admin/themes/{themeID}/delete` → 302 to `/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen`; theme is not deleted.
- [ ] AC-11: POST `/admin/screens/{id}/pages` with `name=clock` → 302 to `/admin/screens/{id}/edit?msg=page_created`; row is persisted with `position=1` (or max+1).
- [ ] AC-12: POST `/admin/screens/{id}/pages/{pageID}/delete` → 302 to `?msg=page_deleted`; row + widget children are gone.
- [ ] AC-13: With three pages, POST move-down on position 1 → after the redirect, GET `/admin/screens/{id}/edit` shows the pages in order [old-P2, old-P1, P3].
- [ ] AC-14: POST move-up on the top page → 302 to `?msg=page_moved`; no rows mutated.
- [ ] AC-21: A member (non-admin) GETs `/admin/screens` → 403 from `RequireRole`.
- [ ] AC-22: POST any state-changing route without `_csrf` → 403 from `RequireCSRF` middleware; no row mutated.
- [ ] AC-23: GET `/admin/screens/{id}/edit` for a screen with two pages → response body lists both pages in position order with names and position numbers.

## Skills to Use

- `add-view` -- mirror `views/themes.go` / `views/themes.templ`.
- `green-bar` -- run before marking review (includes `templ generate`).

## Test Requirements

Tests live in `views/screens_test.go`. Mirror the test helpers in `views/themes_test.go` (especially `adminContext`, `newTestDeps`, `createTestUser`). Construct `*screens.Service` with the test DB and a registry that has the `text` widget registered.

1. **Screen list page renders**: create a screen via the service, GET `handleScreenList`. Assert 200, body contains the screen's name, theme name, page count "0", rotation interval.

2. **Screen create happy path**: POST a valid form. Assert 302 to `/admin/screens?msg=created`. Assert `ListScreens` returns the new row.

3. **Screen create rejects empty name**: POST `name=`. Assert 302 to `/admin/screens?error=...` with a name-related message. Assert no new row.

4. **Screen create rejects invalid theme_id**: POST with `theme_id=does-not-exist`. Assert 302 to `?error=Theme+not+found`. Assert no new row.

5. **Screen create rejects out-of-range rotation**: POST with `rotation_interval_seconds=2`. Assert 302 to `?error=...`. Assert no new row.

6. **Screen edit form pre-populates**: GET `/admin/screens/{id}/edit`. Assert 200, body contains the screen's name in a `value="..."` attribute and the theme-`<select>` option for the current theme is `selected`.

7. **Screen update happy path**: POST a valid update. Assert 302 to `/admin/screens?msg=updated`. Assert `GetScreenByID` returns the new values.

8. **Screen update unknown ID**: POST to `/admin/screens/nonexistent`. Assert 302 to `/admin/screens?error=Screen+not+found`.

9. **Screen delete happy path**: create then delete. Assert 302 to `/admin/screens?msg=deleted`. Assert `GetScreenByID` returns `ErrScreenNotFound`.

10. **Page create happy path**: POST `/admin/screens/{id}/pages` with `name=clock`. Assert 302 to `/admin/screens/{id}/edit?msg=page_created`. Assert `ListPages` returns the new row.

11. **Page reorder happy path**: create three pages. POST move-down on the first. Assert 302 to `?msg=page_moved`. Assert `ListPages` returns pages in the new order.

12. **Page reorder at top is a no-op redirect**: POST move-up on position 1. Assert 302 to `?msg=page_moved`. Assert positions unchanged.

13. **Page delete cascades** (integration with TASK-024's widget methods): create a page, add a `text` widget via the service, POST page delete. Assert the widget row is gone.

14. **Member is 403**: spin up the full admin sub-mux via `httptest.NewServer`; log in as a member (`createTestMember` helper -- mirror the theme test's pattern); GET `/admin/screens`. Assert 403.

15. **CSRF rejected**: POST `/admin/screens/{id}/delete` with NO `_csrf` field through the full admin sub-mux. Assert 403. Assert the row is still present.

16. **Theme delete in use shows clear flash**: create a theme + a screen using it; POST `/admin/themes/{themeID}/delete`. Assert 302 to `/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen`. (This exercises the new branch in `handleThemeDelete`.)

17. **Admin landing page link**: GET `/admin/` as admin. Assert body contains `href="/admin/screens"`.

Tests follow `.claude/rules/testing.md`. Use `t.Helper()` and table-driven where the cases share a setup.

## Definition of Done

- [ ] `views/screens.templ` and `views/screens_templ.go` (via `templ generate`) created.
- [ ] `views/screens.go` with ten handler factories + `screenMsgText` helper.
- [ ] `views/routes.go` registers all ten screen-level + page-level routes inside the admin sub-mux under `RequireRole(RoleAdmin)`.
- [ ] `views.Deps` has the new `Screens *screens.Service` field.
- [ ] `views/admin.templ` links to `/admin/screens`.
- [ ] `views/themes.go::handleThemeDelete` translates `themes.ErrThemeInUse` to a user-visible flash.
- [ ] `main.go` constructs `screens.Service` and threads it through.
- [ ] All acceptance criteria tests pass.
- [ ] `templ generate` was run; the committed `_templ.go` files are up to date.
- [ ] green-bar passes.
- [ ] No new third-party dependencies.
- [ ] No raw user input is logged at warn / error level -- handler slog lines include only IDs, names, and the actor's email.
