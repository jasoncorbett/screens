---
id: TASK-018
title: "Theme admin views, route wiring, and main.go integration"
spec: SPEC-004
arch: ARCH-004
status: ready
priority: p0
prerequisites: [TASK-017]
skills: [add-view, green-bar]
created: 2026-04-30
author: architect
---

# TASK-018: Theme admin views, route wiring, and main.go integration

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Mount the Theme System on the admin UI. Build the templ pages (list and edit), the six handler factories (list, create, edit-form, update, delete, set-default), wire the routes inside the existing `RequireRole(RoleAdmin)` chain, extend `views.Deps` with a `Themes *themes.Service` field, and update `main.go` to construct the theme service and run `EnsureDefault` at startup. Also add the `/admin/themes` link to the admin landing page.

This task ships the user-facing surface of the feature. After it merges, an admin can log in, click "Manage Themes", create / edit / delete themes, and switch the system default -- all gated by the existing auth + CSRF middleware.

## Context

- The view-handler pattern is established in `views/devices.go` and `views/devices.templ`. Mirror that pattern for the theme handlers and the templ page. The theme management screen is structurally similar: a list with per-row actions, plus a creation form.
- Differs from devices: theme validation errors are surfaced INLINE in the form (per ADR-004), not via the `?error=...` flash. When `Service.Create` or `Service.Update` returns a `*themes.ValidationError`, the handler re-renders the form template with the rejected `Input` values and a per-field error map.
- Routes register inside the same admin sub-mux structure that `/admin/users` and `/admin/devices` use. The pattern is: a dedicated `themeMux := http.NewServeMux()`, register handlers on it, then wrap the sub-mux with `middleware.RequireRole(auth.RoleAdmin)` and `Handle` it on `/admin/themes` and `/admin/themes/`.
- The admin sub-mux is already wrapped in `RequireAuth` -> `RequireCSRF` by `views.registerAuthRoutes`. Theme routes inherit both protections.
- The CSRF token comes from `session.CSRFToken`. Forms include a hidden `_csrf` field. The handler pattern is: read user + session from context (defending with 403 if either is nil), then proceed.
- The flash pattern is: `?msg=created`, `?msg=updated`, `?msg=deleted`, `?msg=set_default`, `?error=...`. The list handler reads these and renders a status / alert card. Mirror `handleDeviceList`.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `.claude/skills/add-view/SKILL.md`
- `views/devices.go` -- handler patterns; mirror these (especially `handleDeviceList` and `handleDeviceCreate`).
- `views/devices.templ` -- templ component patterns; mirror these for layout, sections, table styling.
- `views/users.go` -- secondary reference for the admin handler shape.
- `views/users.templ` -- secondary reference for forms with multiple inputs.
- `views/admin.templ` -- the admin landing page; one new line links to `/admin/themes`.
- `views/layout.templ` -- the page wrapper component.
- `views/routes.go` -- where to register the new routes; mirror the existing `deviceMux` block.
- `internal/themes/service.go` -- read the methods returned by TASK-017, especially `Create`, `Update`, `Delete`, `SetDefault`, `List`, `GetByID`, `EnsureDefault`.
- `internal/themes/validate.go` -- read `ValidationError` and `IsValidationError`.
- `main.go` -- where to construct the service and seed the default; mirror the `auth.NewService` block.
- `docs/plans/architecture/phase-2-display/arch-theme-system.md` -- "Component Design > views/themes.go" and "views/themes.templ" sections.
- `docs/plans/specs/phase-2-display/spec-theme-system.md` -- "Functional Requirements > CRUD API (Admin UI)" section.

## Requirements

### Templ components

1. Create `views/themes.templ` with three templ definitions:

   a. `themesPage(themes []themes.Theme, currentUser *auth.User, csrfToken string, msg, errMsg string, formInput themes.Input, formErrors map[string]string)`:
      - Wraps in `@layout("Themes - screens")`.
      - Renders a "Back to Admin" hero block (mirror `devices.templ`).
      - If `msg` is set, render a `<div class="card" role="status">` with the human-readable text from `themeMsgText(msg)`.
      - If `errMsg` is set, render a `<div class="card" role="alert">`.
      - List section: a table with columns Name, Default, Updated, Actions. The "Default" column shows `<strong>default</strong>` for the current default and a `Set default` POST form (with CSRF) for non-defaults. The "Actions" column has an `Edit` link to `/admin/themes/{id}/edit` and, for non-default themes, a `Delete` POST form (with CSRF).
      - "New Theme" section: renders `@themeForm("/admin/themes", formInput, formErrors, csrfToken)`. On a normal GET, `formInput` is the zero value and `formErrors` is empty. On a failed POST that gets re-rendered inline, the create handler passes the rejected input and the field error map.

   b. `themeEditPage(t themes.Theme, currentUser *auth.User, csrfToken string, formInput themes.Input, formErrors map[string]string)`:
      - Wraps in `@layout("Edit Theme - screens")`.
      - Hero with the theme name and a "Back to Themes" link.
      - Renders `@themeForm("/admin/themes/" + t.ID, formInput, formErrors, csrfToken)`.

   c. `themeForm(action string, in themes.Input, errs map[string]string, csrfToken string)`:
      - A `<form method="POST" action={ templ.SafeURL(action) }>` with the `_csrf` hidden field.
      - One labeled `<input type="text" name="..." value={ ... } required>` for each of: `name`, `color_bg`, `color_surface`, `color_border`, `color_text`, `color_text_muted`, `color_accent`, `font_family`, `radius`. (No input for `font_family_mono` -- per spec out-of-scope item, the v1 admin UI does not surface it.)
      - Below each input, if `errs[fieldName]` is non-empty, render a `<p class="error">` with the message.
      - A submit button labelled `Save`.
      - Use the existing `@field(name, label, value, errMsg)` templ if you create one, OR inline the label + input + error pattern (the architecture doc shows the inline pattern). Either is acceptable; prefer the helper if it reduces duplication.

2. Run `templ generate` to compile the templates.

### Handlers

3. Create `views/themes.go` with the six handler factories:

   - `func handleThemeList(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context (defend with 403 if either is nil).
     - Calls `themesSvc.List(ctx)`.
     - Reads `msg` and `error` query params.
     - Renders `themesPage` with empty `formInput` and empty `formErrors`.

   - `func handleThemeCreate(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context.
     - Builds an `themes.Input` from `r.FormValue("name")`, `r.FormValue("color_bg")`, etc. The `FontFamilyMono` field stays empty (the form does not submit it).
     - Calls `themesSvc.Create(ctx, in)`.
     - On success: log `slog.Info("theme created", "theme_id", t.ID, "name", t.Name, "created_by", currentUser.Email)`, then 302 to `/admin/themes?msg=created`.
     - On `*themes.ValidationError`: re-fetch the existing theme list, then RENDER `themesPage` in-place with the rejected `formInput` and the `Fields` map as `formErrors`. No redirect (the form's contents would be lost on a 302). Status 200.
     - On `themes.ErrDuplicateName`: same as ValidationError but populate `formErrors["name"]` with `"a theme with this name already exists"` and re-render.
     - On other errors: log `slog.Error("create theme", "err", err)` and 302 to `/admin/themes?error=Could+not+create+theme`.

   - `func handleThemeEditForm(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context.
     - Reads `id := r.PathValue("id")`. If empty: 302 to `/admin/themes?error=Missing+theme+ID`.
     - Calls `themesSvc.GetByID(ctx, id)`. On `themes.ErrThemeNotFound`: 302 to `/admin/themes?error=Theme+not+found`. On other errors: log + 302 to `?error=...`.
     - Builds an `Input` from the loaded theme's current values (this populates the form with the current theme).
     - Renders `themeEditPage` with empty `formErrors`.

   - `func handleThemeUpdate(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context.
     - Reads `id := r.PathValue("id")`. If empty: 302 to `/admin/themes?error=Missing+theme+ID`.
     - Builds an `Input` from form fields (mirror the create handler).
     - Calls `themesSvc.Update(ctx, id, in)`.
     - On success: log `slog.Info("theme updated", "theme_id", id, "updated_by", currentUser.Email)`, then 302 to `/admin/themes?msg=updated`.
     - On `themes.ErrThemeNotFound`: 302 to `/admin/themes?error=Theme+not+found`.
     - On `*themes.ValidationError`: re-render `themeEditPage` with the rejected `formInput` and the `Fields` map. The handler must still load the existing theme via `GetByID` for the page header (so the user knows which theme they're editing). Status 200.
     - On `themes.ErrDuplicateName`: same as the create handler, with `formErrors["name"]`.
     - On other errors: log + 302 to `?error=Could+not+update+theme`.

   - `func handleThemeDelete(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context.
     - Reads `id := r.PathValue("id")`. If empty: 302 to `/admin/themes?error=Missing+theme+ID`.
     - Calls `themesSvc.Delete(ctx, id)`.
     - On success: log `slog.Info("theme deleted", "theme_id", id, "deleted_by", currentUser.Email)`, then 302 to `/admin/themes?msg=deleted`.
     - On `themes.ErrThemeNotFound`: 302 to `/admin/themes?error=Theme+not+found`.
     - On `themes.ErrCannotDeleteDefault`: 302 to `/admin/themes?error=Cannot+delete+the+default+theme`.
     - On other errors: log + 302 to `?error=Could+not+delete+theme`.

   - `func handleThemeSetDefault(themesSvc *themes.Service) http.HandlerFunc`:
     - Reads user + session from context.
     - Reads `id := r.PathValue("id")`. If empty: 302 to `/admin/themes?error=Missing+theme+ID`.
     - Calls `themesSvc.SetDefault(ctx, id)`.
     - On success: log `slog.Info("theme default changed", "theme_id", id, "changed_by", currentUser.Email)`, then 302 to `/admin/themes?msg=set_default`.
     - On `themes.ErrThemeNotFound`: 302 to `/admin/themes?error=Theme+not+found`.
     - On other errors: log + 302 to `?error=Could+not+set+default+theme`.

4. Add a small helper `func themeMsgText(code string) string` in `views/themes.go` that maps flash codes to human-readable messages:
   - `created` -> `"Theme created."`
   - `updated` -> `"Theme updated."`
   - `deleted` -> `"Theme deleted."`
   - `set_default` -> `"Default theme changed."`
   - default: `""` (the templ does not render the status card when text is empty -- mirror `handleDeviceList`'s `flashMsg` logic).

### Deps and route wiring

5. Add a new field `Themes *themes.Service` to the `views.Deps` struct in `views/routes.go`.

6. In `views/routes.go::registerAuthRoutes`, register the theme routes inside the admin sub-mux. Mirror the existing `deviceMux` block:
   ```go
   themeMux := http.NewServeMux()
   themeMux.HandleFunc("GET  /admin/themes",                    handleThemeList(deps.Themes))
   themeMux.HandleFunc("POST /admin/themes",                    handleThemeCreate(deps.Themes))
   themeMux.HandleFunc("GET  /admin/themes/{id}/edit",          handleThemeEditForm(deps.Themes))
   themeMux.HandleFunc("POST /admin/themes/{id}",               handleThemeUpdate(deps.Themes))
   themeMux.HandleFunc("POST /admin/themes/{id}/delete",        handleThemeDelete(deps.Themes))
   themeMux.HandleFunc("POST /admin/themes/{id}/set-default",   handleThemeSetDefault(deps.Themes))
   adminMux.Handle("/admin/themes",  middleware.RequireRole(auth.RoleAdmin)(themeMux))
   adminMux.Handle("/admin/themes/", middleware.RequireRole(auth.RoleAdmin)(themeMux))
   ```

   These routes inherit the outer `RequireAuth` -> `RequireCSRF` chain that already wraps `adminMux`.

### Admin landing page link

7. In `views/admin.templ`, add a link to `/admin/themes` near the existing `/admin/users` and `/admin/devices` links. Place it inside the `if isAdmin { ... }` block so members do not see a link they cannot use.

### main.go integration

8. In `main.go`, after the existing `auth.NewService` block, construct the theme service and seed the default:

   ```go
   themesSvc := themes.NewService(sqlDB, themes.Config{
       DefaultName: cfg.Theme.DefaultName,
   })
   if err := themesSvc.EnsureDefault(context.Background()); err != nil {
       db.Close(sqlDB)
       log.Fatalf("seed default theme: %v", err)
   }
   ```

   Mirror the existing `db.Migrate` fail-on-error pattern: a startup that cannot establish the bare-minimum schema or seed data is a hard error.

9. Pass the new service through to `views.AddRoutes`:

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
   })
   ```

10. Add the import `"github.com/jasoncorbett/screens/internal/themes"` to `main.go` if it is not already present.

## Acceptance Criteria

From SPEC-004:

- [ ] AC-3: POST `/admin/themes` with `name=   ` (whitespace-only) re-renders the form inline with a per-field error on `name` and creates no row.
- [ ] AC-4: POST `/admin/themes` with `name=theme<script>` re-renders the form inline with a per-field error on `name` and creates no row.
- [ ] AC-5: POST `/admin/themes` with a 65-character name re-renders the form inline with a per-field error on `name` and creates no row.
- [ ] AC-6 (UI half): POST `/admin/themes` with a name that already exists re-renders the form with `formErrors["name"]` populated.
- [ ] AC-9 / AC-10 / AC-11 / AC-12 / AC-13 / AC-15 (UI halves): each invalid color / font / radius value causes the form to re-render inline with a per-field error.
- [ ] AC-21: A logged-in member (non-admin) GETting `/admin/themes` receives 403 from `RequireRole`.
- [ ] AC-22: An admin GET shows every theme with the default theme clearly marked.
- [ ] AC-23: An admin POSTs a valid create form, then a 302 to `/admin/themes?msg=...` follows; the next GET shows the new theme.
- [ ] AC-24: An admin GETs `/admin/themes/{id}/edit` for an existing theme; the form's input values match the theme's current values.
- [ ] AC-25: An admin POSTs a valid update; `GetByID` returns the new values and a 302 to `/admin/themes?msg=updated` follows.
- [ ] AC-26: A POST to `/admin/themes/unknown-id` returns 302 to `/admin/themes?error=...`.
- [ ] AC-27: A POST to `/admin/themes/{id}/delete` without a valid `_csrf` field is rejected by the existing CSRF middleware with 403; the row is not deleted. (Verified by integration test using the real middleware chain.)

## Skills to Use

- `add-view` -- mirror `views/devices.go` / `views/devices.templ` and the route-wiring pattern in `views/routes.go`.
- `green-bar` -- run before marking complete (this includes running `templ generate`).

## Test Requirements

Tests use `httptest.NewRecorder` plus a real `themes.Service` backed by `db.OpenTestDB(t)`. Inject the user + session into context manually, mirroring `views/devices_test.go` / `views/users_test.go` test helpers.

1. **List page renders**: build a service, EnsureDefault, create one extra theme via the service, GET `handleThemeList` with an admin user in context. Assert 200, content type contains `text/html`, body contains both theme names, body contains the literal text `default` next to the default theme's row.

2. **Create happy path**: POST a fully-valid form. Assert 302 to `/admin/themes?msg=created`. Assert `Service.List(ctx)` now returns one extra theme with the submitted name and color values.

3. **Create rejects empty name**: POST `name=`. Assert response 200 (re-rendered inline, no redirect). Assert body contains the error message text near the name field. Assert `Service.List(ctx)` is unchanged.

4. **Create rejects invalid color**: POST `color_bg=red`. Assert response 200, body contains an error message for `color_bg`. Assert no row created.

5. **Create rejects duplicate name**: create theme `A`, POST another `A`. Assert response 200, body contains an error message for `name`. Assert only one `A` exists.

6. **Edit form pre-populates**: GET `/admin/themes/{existingID}/edit`. Assert response 200 and body contains the theme's current name in a `value="..."` attribute.

7. **Update happy path**: POST a valid update to an existing theme's URL. Assert 302 to `/admin/themes?msg=updated`. Assert `Service.GetByID` returns the new values.

8. **Update of unknown ID**: POST to `/admin/themes/nonexistent`. Assert 302 to `/admin/themes?error=...`.

9. **Update rejects validation errors**: POST an invalid update. Assert response 200, body re-renders the form with the error.

10. **Delete happy path**: create a non-default theme, POST to `/admin/themes/{id}/delete`. Assert 302 to `/admin/themes?msg=deleted`. Assert `Service.GetByID` now returns `ErrThemeNotFound`.

11. **Delete of default rejected**: POST to `/admin/themes/{defaultID}/delete`. Assert 302 to a path containing `error=` (specifically `Cannot+delete+the+default+theme`). Assert the row is still present.

12. **Set-default happy path**: EnsureDefault gives default `A`. Create non-default `B`. POST to `/admin/themes/{B.ID}/set-default`. Assert 302 to `/admin/themes?msg=set_default`. Assert `Service.GetByID(B.ID).IsDefault == true` and `Service.GetByID(A.ID).IsDefault == false`.

13. **Set-default unknown ID**: POST to `/admin/themes/nonexistent/set-default`. Assert 302 to `?error=...`.

14. **CSRF integration test** (AC-27): spin up the full admin sub-mux via `httptest.NewServer` so the real `RequireCSRF` middleware runs. POST to `/admin/themes/{id}/delete` with NO `_csrf` field. Assert 403. Assert the row is still present. (This test exercises the existing CSRF middleware -- if it gets too involved, the test for AC-27 may live alongside the existing CSRF tests as a representative case.)

15. **List does NOT leak the previous form's posted values**: after a successful create, GET `/admin/themes`. Assert the response body does NOT contain a stale `value="..."` from the previous POST (i.e., the create form is re-rendered with empty inputs after a successful redirect-then-GET).

16. Tests follow `.claude/rules/testing.md`. Use table-driven tests for the validation-error branches (one row per invalid field). The role-check test is exercised by `RequireRole` tests in TASK-013; do not duplicate.

## Definition of Done

- [ ] `views/themes.templ` created and `templ generate`'d (so `views/themes_templ.go` is up to date).
- [ ] `views/themes.go` with the six handlers + `themeMsgText` helper created.
- [ ] `views/routes.go` registers all six theme routes inside the admin sub-mux with the `RequireRole(RoleAdmin)` wrapping.
- [ ] `views.Deps` has a new `Themes *themes.Service` field.
- [ ] `views/admin.templ` links to `/admin/themes`.
- [ ] `main.go` constructs the theme service, runs `EnsureDefault` at startup, and threads it into `views.Deps`.
- [ ] All acceptance criteria tests pass.
- [ ] No new third-party dependencies.
- [ ] green-bar passes (gofmt, vet, build, test). Run `templ generate` before `green-bar`.
- [ ] No raw secrets logged. Theme values are not secrets, but the slog lines for create / update / delete / set-default include only `theme_id`, `name`, and the actor's `email` -- nothing in the theme's color or font payload.
