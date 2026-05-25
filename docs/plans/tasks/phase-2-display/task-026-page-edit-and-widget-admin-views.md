---
id: TASK-026
title: "Page edit view + widget instance admin UI (add / delete / reorder)"
spec: SPEC-006
arch: ARCH-006
status: review
priority: p0
prerequisites: [TASK-025]
skills: [add-view, green-bar]
created: 2026-05-13
author: architect
---

# TASK-026: Page edit view + widget instance admin UI (add / delete / reorder)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add the per-page admin surface: a templ at `/admin/screens/{id}/pages/{pageID}/edit` that renders the page's widget instance list (with reorder / delete affordances) and an "Add widget" form populated from `widget.Default().List()`. Wire the four widget routes (create / delete / move-up / move-down) plus the page-edit GET route into the admin sub-mux. Extend `screenMsgText` with the widget-related flash codes.

This task completes Screen Model's admin UI. After it merges, an admin can create a Screen, add Pages, add widget instances by type, reorder, and delete -- all the data manipulation the data model supports.

Per-instance config editing is OUT of scope (Widget Selection UI's job).

## Context

- The route `GET /admin/screens/{id}/pages/{pageID}/edit` was deliberately not wired in TASK-025; this task adds it.
- The "Add widget" form uses a `<select name="type">` whose options come from `widget.Default().List()` (or, in tests, the registry constructed for the test).
- The displayed config JSON on the page-edit view is read-only `<pre><code>...</code></pre>` -- the admin sees the current config but cannot edit it. Editing is Widget Selection UI's job.
- Move-up / Move-down for widget instances use the same UX as for pages: a small POST form per button.
- The handler for the page-edit GET needs both `*screens.Service` (for the screen / page / widget data) AND `*widget.Registry` (for the registration list). Pass the registry via `Deps.Widgets`.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `views/screens.go` (output of TASK-025) -- existing handlers; this task extends the file.
- `views/screens.templ` (output of TASK-025) -- this task adds a `pageEditPage` templ.
- `views/screens_test.go` (output of TASK-025) -- the test helpers to reuse.
- `internal/screens/service.go` -- methods to call: `GetScreenByID`, `GetPageByID`, `ListWidgetInstancesByPage` (add it to the service if not present), `AddWidget`, `DeleteWidget`, `MoveWidgetUp`, `MoveWidgetDown`.
- `internal/widget/registry.go` -- `List()` returns `[]Registration` deterministically.
- `internal/widget/text/text.go` -- the placeholder widget used in tests.
- `docs/plans/specs/phase-2-display/spec-screen-model.md` -- requirements 26, 28 (last 5 routes), 31, 34; AC-16 through AC-20, AC-24, AC-25.
- `docs/plans/architecture/phase-2-display/arch-screen-model.md` -- section "Component Design > views/screens.templ > pageEditPage".

## Requirements

### Service: ListWidgetInstancesByPage

1. If the screens service does not already expose a method `ListWidgetInstancesByPage(ctx, screenID, pageID string) ([]WidgetInstance, error)`, add it now. Implementation:
   - Confirm the page exists via `GetPageByID(ctx, screenID, pageID)`. Return `ErrPageNotFound` on miss.
   - Call `s.queries.ListWidgetInstancesByPage(ctx, pageID)`. Convert each row via `widgetFromRow`.
   - Return an empty (non-nil) slice when no widgets exist.

   This is a thin extension; could land in TASK-024 if discovered there. If it is missing at this task's start, add it. (Aligned with SPEC R35 / R36's "GetScreenFull" pattern: this is just one slice of that aggregate.)

### Templ: pageEditPage

2. Add to `views/screens.templ`:

   ```go
   templ pageEditPage(
       screen screens.Screen,
       page screens.Page,
       widgets []screens.WidgetInstance,
       registrations []widget.Registration,
       currentUser *auth.User,
       csrfToken string,
       msg, errMsg string,
   )
   ```

   Layout:
   - `@layout("Edit Page - screens")`.
   - Hero: title "Edit Page" plus the page's name (or "(no name)") plus a "Back to {screen.Name}" link to `/admin/screens/{screen.ID}/edit`.
   - Status / error cards.
   - Section 1: "Page Settings" -- a form POSTing to `/admin/screens/{screen.ID}/pages/{page.ID}` with one `name` input (pre-filled from `page.Name`), `_csrf`, Save button.
   - Section 2: "Widgets" -- a table with columns Position, Type, Config, Actions. For each widget in `widgets` (already in position order):
     - Position number.
     - Type: render the widget type's `DisplayName` (look up via `registrationsByType` helper -- pass the registrations slice into the templ and have the helper return the matching display name, or fall back to the raw `Type` string if no registration matches).
     - Config: `<pre><code>...</code></pre>` block containing the JSON bytes as a string. (Use `string(widget.Config)`; templ HTML-escapes it.) This is intentionally read-only.
     - Actions: Move up POST button (no-op for position 1), Move down POST button, Delete POST button.
   - Section 3: "Add Widget" -- a form POSTing to `/admin/screens/{screen.ID}/pages/{page.ID}/widgets` with:
     - A `<select name="type">` populated from `registrations`. Each option's value is `reg.Type`, label is `reg.DisplayName + " - " + reg.Description` (or just `DisplayName` if you prefer concision -- the spec only requires the display name to be present, but the description helps the admin pick).
     - `_csrf` hidden.
     - "Add" submit button.

3. Add a small helper to `views/screens.go` (NOT in the templ -- templ helpers should be pure):
   ```go
   func widgetDisplayName(registrations []widget.Registration, typeName string) string {
       for _, reg := range registrations {
           if reg.Type == typeName {
               return reg.DisplayName
           }
       }
       return typeName // fallback if the type is no longer registered
   }
   ```
   Used by the templ via interpolation.

4. Run `templ generate` after editing the file.

### Handlers

5. Add to `views/screens.go`:

   a. `func handlePageEditForm(svc *screens.Service, registry *widget.Registry) http.HandlerFunc`:
      - Read user + session from context (403 if either nil).
      - Read `screenID := r.PathValue("id")`, `pageID := r.PathValue("pageID")`.
      - Call `svc.GetScreenByID(ctx, screenID)`. On `ErrScreenNotFound`: 302 to `/admin/screens?error=Screen+not+found`.
      - Call `svc.GetPageByID(ctx, screenID, pageID)`. On `ErrPageNotFound`: 302 to `/admin/screens/{screenID}/edit?error=Page+not+found`.
      - Call `svc.ListWidgetInstancesByPage(ctx, screenID, pageID)`. On error: log + 302 to `/admin/screens/{id}/edit?error=Could+not+load+widgets`.
      - Build `registrations := registry.List()`.
      - Read `msg` / `error` query params; render the success message via `screenMsgText`.
      - Render `pageEditPage`.

   b. `func handleWidgetCreate(svc *screens.Service) http.HandlerFunc`:
      - Read screenID, pageID from path. Read `type` from form.
      - If `type == ""`: 302 back to `/admin/screens/{id}/pages/{pageID}/edit?error=Widget+type+is+required`.
      - Call `svc.AddWidget(ctx, screenID, pageID, type)`.
      - On `ErrUnknownWidgetType`: 302 with `?error=Unknown+widget+type`.
      - On `ErrPageNotFound`: 302 to `/admin/screens/{id}/edit?error=Page+not+found`.
      - On `ErrScreenNotFound`: 302 to `/admin/screens?error=Screen+not+found`.
      - On other errors: log + 302 with `?error=Could+not+add+widget`.
      - On success: log + 302 to `/admin/screens/{id}/pages/{pageID}/edit?msg=widget_added`.

   c. `func handleWidgetDelete(svc *screens.Service) http.HandlerFunc`:
      - Read screenID, pageID, widgetID from path.
      - Call `svc.DeleteWidget(ctx, screenID, pageID, widgetID)`.
      - On `ErrWidgetNotFound`: 302 to `/admin/screens/{id}/pages/{pageID}/edit?error=Widget+not+found`.
      - Other errors translate similarly.
      - On success: log + 302 to `?msg=widget_deleted`.

   d. `func handleWidgetMoveUp(svc *screens.Service) http.HandlerFunc`:
      - Read path params.
      - Call `svc.MoveWidgetUp(ctx, screenID, pageID, widgetID)`.
      - Translate errors.
      - On success (including no-op at top): 302 to `?msg=widget_moved`.

   e. `func handleWidgetMoveDown(svc *screens.Service) http.HandlerFunc`: same shape.

### screenMsgText extension

6. Extend `screenMsgText` (from TASK-025) with the new codes:
   - `"widget_added"` → `"Widget added."`
   - `"widget_deleted"` → `"Widget removed."`
   - `"widget_moved"` → `"Widget reordered."`

### Route wiring

7. In `views/routes.go::registerAuthRoutes`, add the five new routes to the existing `screenMux` (from TASK-025):
   ```go
   screenMux.HandleFunc("GET  /admin/screens/{id}/pages/{pageID}/edit",                          handlePageEditForm(deps.Screens, deps.Widgets))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets",                       handleWidgetCreate(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete",     handleWidgetDelete(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-up",    handleWidgetMoveUp(deps.Screens))
   screenMux.HandleFunc("POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-down",  handleWidgetMoveDown(deps.Screens))
   ```

   The `RequireRole(RoleAdmin)` wrapping established in TASK-025 covers these routes too.

## Acceptance Criteria

From SPEC-006:

- [ ] AC-16: POST `/admin/screens/{id}/pages/{pageID}/widgets` with `type=text` → 302 to `?msg=widget_added`; the inserted row has `type=text`, `config = text.DefaultConfig()`, `position = max+1`.
- [ ] AC-17: POST same endpoint with `type=nonexistent` → 302 to `?error=Unknown+widget+type`; no row created.
- [ ] AC-18: POST `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete` → 302 to `?msg=widget_deleted`; the row is gone.
- [ ] AC-19: Given two widgets at positions 1 and 2, POST move-up on position 2 → 302 to `?msg=widget_moved`; positions are swapped.
- [ ] AC-20: After widget creation through the handler, the persisted `config` bytes pass `widget.Default().Validate("text", config)` without error.
- [ ] AC-24: GET `/admin/screens/{id}/pages/{pageID}/edit` for a page with one `text` widget → response body contains the widget's `DisplayName` (`Text`), its position number, and a `<pre>` block containing its config JSON.
- [ ] AC-25: The "Add widget" `<select>` is populated with every widget type from `widget.Default().List()` (in the test, the registry-with-text-only).

## Skills to Use

- `add-view` -- extends the patterns from TASK-025.
- `green-bar` -- run before marking review.

## Test Requirements

Tests live in `views/screens_test.go` (or a new `views/page_edit_test.go` if file size is an issue). Reuse test helpers from TASK-025.

1. **Page edit GET renders the widget list**: build a screen + page + one `text` widget via the service. GET `handlePageEditForm`. Assert 200, body contains the widget's `DisplayName` (`Text`), the position number `1`, and a `<pre>` block containing `"text":"Hello, screens"`.

2. **Page edit GET shows the widget-type `<select>`**: GET the page-edit URL. Assert body contains `<select name="type">` with at least one option whose `value="text"`. (The registry passed in the test has only `text` registered.)

3. **Page edit GET for unknown screen**: 302 to `/admin/screens?error=Screen+not+found`.

4. **Page edit GET for unknown page**: 302 to `/admin/screens/{screenID}/edit?error=Page+not+found`.

5. **Widget create happy path**: POST `/admin/screens/{id}/pages/{pageID}/widgets` with `type=text`. Assert 302 to `?msg=widget_added`. Assert `ListWidgetInstancesByPage` returns the new row with `Type="text"`, `Position=1`.

6. **Widget create rejects unknown type**: POST `type=nope`. Assert 302 to `?error=Unknown+widget+type`. Assert no new row.

7. **Widget create rejects missing type**: POST with no `type` field. Assert 302 to `?error=Widget+type+is+required`.

8. **Widget delete happy path**: add a widget, POST delete. Assert 302 to `?msg=widget_deleted`. Assert the row is gone.

9. **Widget delete unknown**: POST delete on a nonexistent widget ID. Assert 302 to `?error=Widget+not+found`.

10. **Widget reorder happy path**: add two widgets. POST move-up on position 2. Assert 302 to `?msg=widget_moved`. Assert positions are swapped.

11. **Widget reorder at top is no-op**: add two widgets. POST move-up on position 1. Assert 302 to `?msg=widget_moved`. Assert positions unchanged.

12. **Default config validates** (AC-20): after a widget-create handler call, fetch the row and run `widget.NewRegistry()` (with text registered) `.Validate("text", config)`. Assert nil error.

13. **CSRF integration**: through the full admin sub-mux, POST `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete` with NO `_csrf`. Assert 403. Assert the row is still present.

14. **GetScreenFull end-to-end**: build a screen + 2 pages + 3 widgets purely through the service / handler APIs. Call `svc.GetScreenFull` (from TASK-022). Assert the tree matches expectation. (This test arguably belongs to TASK-022 / TASK-024 but is included here to verify the full handler-driven path.)

## Definition of Done

- [ ] `views/screens.templ` extended with `pageEditPage` and re-generated.
- [ ] `views/screens.go` extended with five new handler factories + `widgetDisplayName` helper.
- [ ] `views/routes.go` registers the five new routes inside `screenMux`.
- [ ] `screenMsgText` extended with the three widget codes.
- [ ] `screens.Service.ListWidgetInstancesByPage` exists (added in this task if missing).
- [ ] All acceptance criteria tests pass.
- [ ] green-bar passes (gofmt, vet, build, test). `templ generate` was run.
- [ ] No new third-party dependencies.
- [ ] The widget admin UI does NOT include any per-instance config editing form. The config column is `<pre>`-rendered read-only. Editing belongs to Widget Selection UI.
- [ ] After this task, an end-to-end "create screen → add page → add widget → reorder → delete widget → delete page → delete screen" flow works in the admin UI.
