---
id: TASK-014
title: "Device management views (list, create, revoke)"
spec: SPEC-003
arch: ARCH-003
status: ready
priority: p0
prerequisites: [TASK-013]
skills: [add-view, green-bar]
created: 2026-04-25
author: architect
---

# TASK-014: Device management views (list, create, revoke)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Build the admin-facing device management page. Admins log into `/admin/devices`, see existing devices with last-seen timestamps, create new devices (the freshly created token is shown exactly once), and revoke devices in a single click. Wires the routes through the existing `RequireRole(RoleAdmin)` chain so non-admins are rejected.

This task delivers the "traditional" copy-the-token UI for programmatic clients. The browser-enrollment flow (cookie swap, device landing page, etc.) is the SEPARATE TASK-015 that builds on top of this one. Keep this task focused on list / create / revoke.

## Context

- The view-handler pattern is established in `views/users.go` and `views/users.templ` -- copy that pattern. Each handler reads the user / session from context, calls the corresponding `auth.Service` method, and renders a templ component or 302-redirects with a flash query param.
- Routes are registered in `views/routes.go::registerAuthRoutes`. The user-management routes are wrapped in `RequireRole(RoleAdmin)`. Device-management routes use the same wrapping.
- The CSRF token comes from `session.CSRFToken`. Forms include a hidden `_csrf` field.
- After TASK-013, the admin sub-mux is wrapped: `RequireAuth(...) -> RequireCSRF() -> adminMux`. Device-management routes go inside that same chain.
- The "show the raw token exactly once" requirement is the most security-sensitive part of this task. The handler MUST hold the freshly-generated token in a function-local variable, render it inline, and never put it in a URL, a flash cookie, a slog line, or a redirect target.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `.claude/skills/add-view/SKILL.md`
- `views/users.go` -- handler patterns; mirror these
- `views/users.templ` -- templ component patterns; mirror these
- `views/layout.templ` -- the page wrapper component
- `views/routes.go` -- where to register the new routes
- `internal/auth/auth.go` -- `CreateDevice`, `ListDevices`, `RevokeDevice` (added in TASK-012)
- `internal/auth/identity.go` -- the `Identity` type (only relevant for understanding context; this page reads the admin user via the existing `UserFromContext`)
- `docs/plans/architecture/phase-1-foundation/arch-device-auth.md` -- "Component Design" section, especially the `views/devices.go` and `devices.templ` snippets

## Requirements

### Templ component

1. Create `views/devices.templ` with a single page component:
   ```go
   templ devicesPage(
       devices []auth.Device,
       currentUser *auth.User,
       csrfToken string,
       msg, errMsg string,
       newDeviceName, newDeviceToken string, // both empty unless we just created one
   )
   ```
   The component:
   - Wraps content in `@layout("Devices - screens")`.
   - Renders a "Back to Admin" link (mirror `users.templ`).
   - If `msg` is set, renders a `<div class="card" role="status">` flash; if `errMsg` is set, a `role="alert"` flash.
   - **If `newDeviceToken` is non-empty**, renders a prominent card containing:
     - The device name in a heading.
     - The text `"Save this token now. It will not be displayed again."`
     - The token wrapped in `<pre><code>...</code></pre>` (no escaping concerns: 64-char hex).
   - Renders a "Register Device" form with method POST to `/admin/devices`, a single `name` text input (required), and the `_csrf` hidden field.
   - Renders a table of NON-revoked devices with columns: Name, ID, Created, Last Seen, Actions. The Actions column has a POST form to `/admin/devices/{id}/revoke` with the `_csrf` token.
   - Optionally, renders a separate table of revoked devices below (Name, ID, Created, Revoked At, no Actions). Empty section may be hidden when there are no revoked devices.
   - Format timestamps with `Format("2006-01-02 15:04 MST")` for created/revoked, and either the same format or `"never"` for `LastSeenAt` when nil.
   - Leave room for TASK-015 to add an "Enroll this browser as ..." section on the same page. A simple way to make this easy: structure the templ as `<section>` blocks so a future patch only adds a new section, not a rewrite. Do NOT pre-emptively add the enrollment forms in this task.

2. Run `templ generate` to compile the template.

### Handlers

3. Create `views/devices.go` with three handler factories:

   - `func handleDeviceList(authSvc *auth.Service) http.HandlerFunc`
     - Reads user and session from context (the middleware chain guarantees they're present, but defend with 403 if either is nil -- mirror `handleUserList`).
     - Calls `authSvc.ListDevices(ctx)`.
     - Reads `msg` and `error` query params for flashes.
     - Renders the page with empty `newDeviceName, newDeviceToken`.

   - `func handleDeviceCreate(authSvc *auth.Service) http.HandlerFunc`
     - Reads user / session from context (same defense as above).
     - Reads `name := strings.TrimSpace(r.FormValue("name"))`.
     - If empty: 302 to `/admin/devices?error=Name+is+required`.
     - Calls `dev, rawToken, err := authSvc.CreateDevice(ctx, name, currentUser.ID)`.
     - On error: log at `error` and 302 to `/admin/devices?error=Could+not+create+device`. Do NOT include the raw token (there isn't one on error anyway, but be explicit).
     - On success: re-fetch the full device list, then RENDER the page in-place (no redirect) with `newDeviceName=dev.Name` and `newDeviceToken=rawToken`. Doing it inline (rather than redirecting) is what makes the token visible exactly once -- a redirect would lose the token.
     - Log `slog.Info("device created", "device_id", dev.ID, "name", dev.Name, "created_by", currentUser.Email)`. Do NOT log the raw token.

   - `func handleDeviceRevoke(authSvc *auth.Service) http.HandlerFunc`
     - Reads user from context (same defense).
     - Reads `deviceID := r.PathValue("id")`. If empty: 302 to `/admin/devices?error=Missing+device+ID`.
     - Calls `authSvc.RevokeDevice(ctx, deviceID)`.
     - On `ErrDeviceNotFound`: 302 to `/admin/devices?error=Device+not+found`.
     - On other error: log at `error` and 302 to `/admin/devices?error=Could+not+revoke+device`.
     - On success: 302 to `/admin/devices?msg=revoked` and log `slog.Info("device revoked", "device_id", deviceID, "revoked_by", currentUser.Email)`.

### Route registration

4. In `views/routes.go::registerAuthRoutes`, register device routes on the admin-only sub-mux (the same `userMux` style used for `/admin/users`). Reuse the existing `RequireRole(RoleAdmin)` pattern. Concretely, EITHER extend the existing `userMux` (rename it to e.g. `adminOnlyMux`) and add the device handlers there, OR create a parallel `deviceMux` wrapped in `RequireRole(RoleAdmin)` and `Handle`d on `/admin/devices` and `/admin/devices/`. Prefer the option that minimises code duplication; the existing pattern handles `/admin/users` and `/admin/users/` separately so extending it is straightforward.

   The three routes are:
   - `GET /admin/devices`              -> `handleDeviceList(deps.Auth)`
   - `POST /admin/devices`             -> `handleDeviceCreate(deps.Auth)`
   - `POST /admin/devices/{id}/revoke` -> `handleDeviceRevoke(deps.Auth)`

5. The `Deps` struct already gained `DeviceCookieName` in TASK-013; no further `Deps` changes are required for this task.

### Admin landing page link

6. In `views/admin.templ`, add a link to `/admin/devices` near the existing `/admin/users` link so admins can find the page. (Look at the file; if it already lists user management as a card or anchor, mirror that style.)

## Acceptance Criteria

From SPEC-003:

- [ ] AC-1: POSTing a name to `/admin/devices` creates a device row and the response HTML contains the raw token in a `<pre>` block.
- [ ] AC-2: A subsequent GET `/admin/devices` does NOT contain the raw token anywhere in the response body.
- [ ] AC-3: POST `/admin/devices` with `name=` (empty) or `name=   ` (whitespace) returns a 302 to `/admin/devices?error=...` and does NOT create a row.
- [ ] AC-12 (UI half): POST `/admin/devices/{id}/revoke` flips `revoked_at` and the next GET `/admin/devices` shows the device under the revoked section (or omits it from the active list).
- [ ] AC-24: A logged-in member (non-admin) GETting `/admin/devices` receives 403.
- [ ] AC-25: An admin GET shows all non-revoked devices with their names and last-seen timestamps.
- [ ] AC-26: After admin POSTs the create form, the response page contains the raw token AND the explicit "Save this token now. It will not be displayed again." copy.

## Skills to Use

- `add-view` -- mirror `users.go` / `users.templ`.
- `green-bar` -- run before marking complete (this includes running `templ generate`).

## Test Requirements

Use `httptest.NewRecorder` plus an `auth.Service` backed by `db.OpenTestDB(t)`. Inject the user into context manually (mirror the test helpers in `views/users_test.go`).

1. **List page renders**: build a service, create two devices via the service, GET the handler with an admin user in context. Assert 200, content type contains `text/html`, and the body contains both device names.
2. **Create happy path**: POST `name=kitchen-tablet`. Assert response 200 (no redirect -- the page renders inline so the token can be shown). Assert the body contains the literal text `Save this token now`. Assert the body contains a 64-character hex token (regex match). Assert that calling `authSvc.ListDevices(ctx)` returns one device named `kitchen-tablet`.
3. **Create rejects empty name**: POST `name=`. Assert 302 to a path containing `error=`. Assert `ListDevices` is empty.
4. **Create rejects whitespace name**: POST `name=   `. Same assertions as above.
5. **List does NOT leak token**: after #2, GET the list. Assert the body does NOT contain the previously-shown raw token anywhere. (Capture the token in the test's local variable from the create-response body via regex; then assert `!strings.Contains(listBody, rawToken)`.)
6. **Revoke happy path**: create a device, then POST to its revoke route. Assert 302 to `/admin/devices?msg=revoked`. Assert subsequent `ValidateDeviceToken(rawToken)` returns `ErrDeviceRevoked`.
7. **Revoke unknown ID**: POST to `/admin/devices/unknown-id/revoke`. Assert 302 to a path containing `error=`.
8. **Self-deactivation analogue**: not applicable here (an admin cannot lock themselves out via device revocation), so no test required.
9. Tests follow `.claude/rules/testing.md`. Use table-driven tests where the variations are mostly inputs.

You do not need to test the role-check at the view layer -- that is exercised by `RequireRole` tests in TASK-013. If you want belt-and-suspenders coverage, an integration test that spins up the full admin sub-mux via `httptest.NewServer` and hits the routes with a member user is acceptable but optional.

## Definition of Done

- [ ] `views/devices.templ` created and `templ generate`'d.
- [ ] `views/devices.go` with the three handlers created.
- [ ] Routes registered in `views/routes.go` inside the admin-only sub-mux.
- [ ] Admin landing page links to the new device page.
- [ ] All acceptance criteria tests pass.
- [ ] Token-leakage test confirms a freshly created token is shown in the create response and absent from subsequent list responses.
- [ ] No raw token appears in any slog output (verify by reading the code; no test hook needed).
- [ ] green-bar passes (gofmt, vet, build, test). Run `templ generate` before `green-bar`.
- [ ] No new third-party dependencies.
