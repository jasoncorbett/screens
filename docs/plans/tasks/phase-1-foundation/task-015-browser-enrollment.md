---
id: TASK-015
title: "Browser enrollment endpoints and device landing page"
spec: SPEC-003
arch: ARCH-003
status: done
priority: p0
prerequisites: [TASK-014]
skills: [add-endpoint, add-view, green-bar]
created: 2026-04-25
author: architect
---

# TASK-015: Browser enrollment endpoints and device landing page

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add the in-person browser-enrollment flow: an admin standing at a wall-mounted kiosk clicks one button on `/admin/devices` and the kiosk's browser is converted from "currently signed in as the admin" into "is now the device". Concretely this task delivers two new POST endpoints on the admin side that swap the admin session cookie for a device cookie and redirect to a device landing URL, plus the device landing URL itself (a placeholder page that says "this browser is enrolled as <device-name>"). Phase 2 Screen Display will replace the body of the landing page with real screen content; this task only ships the placeholder so the redirect target exists.

The browser-enrollment flow is the primary mechanism by which a device cookie ends up on a real wall-mounted kiosk. TASK-014 shipped the "create device + copy the token" UI for programmatic clients; this task ships the in-person UI for the dominant deployment shape.

## Context

- TASK-014 already created `views/devices.templ`, `views/devices.go`, and the route registration block under `RequireRole(RoleAdmin)`. This task ADDS a section to the templ for the enrollment forms, ADDS two handler factories to `views/devices.go`, and EXTENDS the route registration block with two new routes.
- TASK-013 added `DeviceCookieName` and `DeviceLandingURL` to both `auth.Config` and `views.Deps`. The handlers in this task consume both.
- TASK-012 added `auth.Service.RotateDeviceToken`. This task is the only consumer; the method exists so the cookie-swap can issue a fresh token without going through the create flow.
- The existing `auth.Service.Logout(ctx, rawToken)` method (from SPEC-002) deletes a single session row keyed by `HashToken(rawToken)`. This task uses it to delete the admin session row backing the enrolling request, so the database does not accumulate orphans.
- The middleware chain wrapping `/admin/...` is `RequireAuth -> RequireCSRF -> RequireRole(RoleAdmin) -> handler`. Both new endpoints sit at the bottom of that chain, so by the time the handler runs the caller is already authenticated, has a valid CSRF token, and is an admin. The handler does not have to re-check those conditions.
- The device landing URL handler sits OUTSIDE `/admin/...` because the admin cookie is gone by the time the browser arrives. It is wrapped in `RequireAuth` only -- no `RequireRole`, no CSRF -- so a device identity is sufficient.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `.claude/rules/logging.md`
- `.claude/skills/add-endpoint/SKILL.md`
- `.claude/skills/add-view/SKILL.md`
- `views/devices.go` -- TASK-014 handlers; mirror the style and add to this file
- `views/devices.templ` -- TASK-014 template; add a new section to this file
- `views/auth_handlers.go::handleLogout` -- the cookie-clearing pattern to mirror for the admin session
- `views/routes.go` -- the route registration block to extend
- `internal/auth/auth.go::Logout` -- existing method this task calls
- `internal/auth/auth.go::RotateDeviceToken` -- TASK-012 method this task calls
- `internal/auth/identity.go` -- the `Identity` type read by the landing handler
- `docs/plans/specs/phase-1-foundation/spec-device-auth.md` -- "Browser Enrollment" section, AC-27 through AC-38
- `docs/plans/architecture/phase-1-foundation/arch-device-auth.md` -- "Browser Enrollment Flow" section (sequence diagram, cookie mutations, handler skeletons)

## Requirements

### performBrowserEnrollment helper

1. Add an unexported helper in `views/devices.go`:
   ```go
   // performBrowserEnrollment swaps the caller's admin session cookie for a
   // fresh device cookie bound to the given deviceID, deletes the admin
   // session row from the database, and writes a 302 to the device landing
   // URL. Returns a non-nil error WITHOUT mutating any cookies if the device
   // is missing or revoked; the caller MUST handle that case by redirecting
   // back to /admin/devices?error=... .
   func performBrowserEnrollment(
       w http.ResponseWriter,
       r *http.Request,
       deps *Deps,
       deviceID string,
   ) error
   ```
   Body, in order:
   1. Call `rawToken, err := deps.Auth.RotateDeviceToken(ctx, deviceID)`. On `errors.Is(err, auth.ErrDeviceNotFound)`, return `errors.New("Device not found or revoked")`. On any other error, log at `error` and return `errors.New("Could not enroll browser")`. (DO NOT clear cookies in either error path.)
   2. Look up the admin session cookie via `r.Cookie(deps.CookieName)`. If present and non-empty, call `deps.Auth.Logout(ctx, c.Value)`. Log at `error` if Logout returns an error but PROCEED with the cookie swap regardless -- the user-visible state is more important than the orphan cleanup.
   3. Write `Set-Cookie` to clear the admin session cookie (Name=deps.CookieName, Value="", Path="/", MaxAge=-1, HttpOnly=true, Expires=time.Unix(0, 0)). Mirror the pattern in `handleLogout`.
   4. Write `Set-Cookie` to set the device cookie (Name=deps.DeviceCookieName, Value=rawToken, Path="/", HttpOnly=true, Secure=deps.SecureCookie, SameSite=http.SameSiteLaxMode).
   5. Read the current admin user via `auth.UserFromContext(r.Context())`. Log `slog.Info("device enrolled via browser", "device_id", deviceID, "enrolled_by", currentUser.Email)`. If `currentUser` is nil, log with `enrolled_by=""`. NEVER log the raw token.
   6. Write `http.Redirect(w, r, deps.DeviceLandingURL, http.StatusFound)` and return nil.

### handleDeviceEnrollExisting

2. Add `func handleDeviceEnrollExisting(deps *Deps) http.HandlerFunc` in `views/devices.go`:
   - Read `deviceID := r.PathValue("id")`. If empty, 302 to `/admin/devices?error=Missing+device+ID`.
   - Call `performBrowserEnrollment(w, r, deps, deviceID)`.
   - On error, 302 to `/admin/devices?error=` + URL-escaped error message. Use `net/url.QueryEscape` for the message (the messages are short, sanitised, English).
   - On success, do nothing further -- the helper already wrote the redirect.

### handleDeviceEnrollNew

3. Add `func handleDeviceEnrollNew(deps *Deps) http.HandlerFunc` in `views/devices.go`:
   - Read user from context: `currentUser := auth.UserFromContext(r.Context())`. If nil, respond 403 (defense-in-depth; the middleware should have caught this).
   - Read `name := strings.TrimSpace(r.FormValue("name"))`. If empty, 302 to `/admin/devices?error=Name+is+required`.
   - Call `dev, _, err := deps.Auth.CreateDevice(r.Context(), name, currentUser.ID)`. On error, log at `error` and 302 to `/admin/devices?error=Could+not+create+device`. Discard the returned raw token; we re-issue one in the next step.
   - Call `performBrowserEnrollment(w, r, deps, dev.ID)`. On error (which would be very unusual since we just created the device), log at `error` and 302 to `/admin/devices?error=` + URL-escaped error.

### Templ updates

4. Add a section to `views/devices.templ` (between the create-device form and the device list -- pick a sensible position so the page reads top-to-bottom: create-device form, enrollment section, active device list, revoked device list):
   - A heading `"Enroll This Browser as a Device"`.
   - One paragraph explaining what the action does and warning that it signs the admin out of THIS browser.
   - One form `POST /admin/devices/enroll-new-browser` with hidden `_csrf`, a required `name` text input, and a submit button labelled e.g. `"Create new device and enroll this browser"`.
   - For each non-revoked device in the `devices` slice, one form `POST /admin/devices/{id}/enroll-browser` with hidden `_csrf` and a submit button labelled e.g. `"Enroll this browser as <device.Name>"`. Use `templ.SafeURL("/admin/devices/" + d.ID + "/enroll-browser")` for the action attribute.
   - Run `templ generate` after editing.

### Route registration

5. In `views/routes.go::registerAuthRoutes`, extend the device sub-mux (or wherever TASK-014 registered the device routes inside `RequireRole(RoleAdmin)`) to add the two new routes:
   ```go
   deviceMux.HandleFunc("POST /admin/devices/{id}/enroll-browser",  handleDeviceEnrollExisting(deps))
   deviceMux.HandleFunc("POST /admin/devices/enroll-new-browser",   handleDeviceEnrollNew(deps))
   ```
   The wildcard route `/admin/devices/{id}/enroll-browser` and the literal `/admin/devices/enroll-new-browser` MUST coexist. Go's ServeMux treats `enroll-new-browser` as a literal path segment that takes precedence over `{id}` -- verify this with a quick test once registered. (If for some reason the precedence is wrong, a workaround is to use a different prefix for the bulk-create case, e.g., `POST /admin/enroll-new-browser`. But the standard ServeMux precedence rules should make the original layout work.)

### Device landing handler and template

6. Create `views/device.templ` with a single component:
   ```go
   templ deviceLandingPage(deviceName string)
   ```
   The component:
   - Wraps in `@layout("Device - screens")`.
   - Renders a `<main>` block with a heading like `"Enrolled"` and a paragraph `"This browser is enrolled as <deviceName>. The screen display will appear here once the screen feature ships."`.
   - No forms, no admin links (the admin cookie is gone).

7. Create `views/device.go` with a single handler factory:
   ```go
   func handleDeviceLanding(authSvc *auth.Service) http.HandlerFunc
   ```
   Body:
   - Read `id := auth.IdentityFromContext(r.Context())`. If nil, respond 403 -- this should be unreachable because RequireAuth gates the route, but fail closed.
   - Determine the display name:
     - If `id.IsDevice() && id.Device != nil`, use `id.Device.Name`.
     - Else if `id.IsAdmin() && id.User != nil`, use `"(viewing as admin: " + id.User.Email + ")"` so an admin browsing directly to the URL sees something useful instead of a blank or 403 page.
     - Else respond 403.
   - Render `deviceLandingPage(name).Render(r.Context(), w)`.

8. Run `templ generate` after editing the new `device.templ`.

### Wiring the landing route

9. In `views/routes.go::registerAuthRoutes`, register the landing handler under `RequireAuth` only (not under any of the admin-related wrappers). The cleanest layout is:
   ```go
   landingHandler := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
       http.HandlerFunc(handleDeviceLanding(deps.Auth)),
   )
   mux.Handle("GET "+deps.DeviceLandingURL, landingHandler)
   ```
   This is OK even if `/admin/` already wraps its own `RequireAuth` -- the landing URL does not overlap with `/admin/`.

   Alternative: factor out a single shared `RequireAuth` instance and dispatch internally. The simpler option above is acceptable for Phase 1; refactoring to one wrapper is a clean follow-up.

## Acceptance Criteria

From SPEC-003:

- [ ] AC-27: Authenticated admin POST to `/admin/devices/{id}/enroll-browser` for a non-revoked device returns 302 to `cfg.Auth.DeviceLandingURL`, sets the device cookie to the freshly issued raw token, and clears the admin session cookie (MaxAge -1).
- [ ] AC-28: After AC-27, the admin session row used by the enrolling request is no longer present in the `sessions` table (verify by querying directly).
- [ ] AC-29: After AC-27, a follow-up GET to the landing URL with only the device cookie returns 200, identity in context is a device, and the device's name appears in the response body.
- [ ] AC-30: When the same admin user has a SECOND session (e.g., simulate by calling `authSvc.CreateSession` twice for the same user), only the session whose cookie was sent on the enrollment request is deleted; the other session row remains and still validates.
- [ ] AC-31: When the target device id refers to a revoked device, the response is 302 to `/admin/devices?error=...`, no cookies are mutated, the admin's session row is NOT deleted, and a follow-up GET to `/admin/devices` with the original admin cookie still succeeds.
- [ ] AC-32: An unauthenticated POST to the enroll-browser path returns the standard `RequireAuth` failure (302 to login for HTML Accept, 401 otherwise) and no cookies are mutated. (This AC is mostly enforced by the middleware tests in TASK-013; verify by smoke test only.)
- [ ] AC-33: A member (non-admin) authenticated POST returns 403 and no cookies are mutated.
- [ ] AC-34: A GET request to either enroll path returns 405 or 404 (no route match) and the admin session is NOT terminated.
- [ ] AC-35: An admin POST without a valid `_csrf` returns 403 (existing CSRF middleware behaviour) and no cookies are mutated.
- [ ] AC-36: An admin POSTs from a browser that already has a device cookie set to some OTHER device's token. The response sets the device cookie to the newly enrolled device's token (the `Set-Cookie` overwrites the prior value) and clears the admin session cookie.
- [ ] AC-37: An admin POST to `/admin/devices/enroll-new-browser` with `name=foo` creates a new device row, returns 302 to the landing URL, sets the device cookie, and clears the admin session cookie.
- [ ] AC-38: A request to the landing URL with no auth cookie returns the standard `RequireAuth` failure (302 to login for HTML, 401 otherwise).

## Skills to Use

- `add-endpoint` -- the two enroll endpoints follow the standard handler+route+test pattern.
- `add-view` -- the device landing page is a small templ component with its own handler.
- `green-bar` -- run before marking complete (includes `templ generate`).

## Test Requirements

Use `httptest.NewRecorder` plus an `auth.Service` backed by `db.OpenTestDB(t)`. Inject the user and session into context manually (mirror the test helpers in `views/users_test.go` and `views/devices_test.go` from TASK-014).

1. **performBrowserEnrollment happy path** (test the helper directly via the public handler):
   - Build a service, create an admin user, create an admin session via `authSvc.CreateSession`, create a device via `authSvc.CreateDevice`.
   - Build an http.Request with the admin session cookie set, the user / session injected into context.
   - Call `handleDeviceEnrollExisting(deps)` with `r.SetPathValue("id", dev.ID)`.
   - Assert: response is 302 to `deps.DeviceLandingURL`. Two `Set-Cookie` headers: one clears the admin cookie (MaxAge=-1), one sets the device cookie with HttpOnly+Secure(per deps)+SameSite=Lax. Then call `authSvc.ValidateDeviceToken(<value of device cookie>)` and assert it returns the same device id. Then call `authSvc.ValidateSession(<original admin token>)` and assert it returns an error (the row is gone).

2. **Two admin sessions, one is preserved**:
   - Create user; call `CreateSession(userID)` twice to get two distinct raw tokens A and B.
   - POST enrollment with cookie token A.
   - Assert: `ValidateSession(A)` errors; `ValidateSession(B)` still returns the user.

3. **Revoked target device aborts cleanly**:
   - Create user, session, device. Revoke the device via `authSvc.RevokeDevice`.
   - POST enrollment.
   - Assert: response is 302 to `/admin/devices?error=Device+not+found+or+revoked` (or whatever exact wording the helper produces). NO Set-Cookie for the device cookie. NO Set-Cookie clearing the session cookie. `ValidateSession(adminToken)` STILL returns the user (the row was not deleted).

4. **Empty path id**:
   - POST `/admin/devices//enroll-browser` is unlikely to match the route at all; instead test the handler directly with `r.SetPathValue("id", "")`. Assert: 302 to `/admin/devices?error=Missing+device+ID`. No cookie mutations.

5. **enroll-new-browser happy path**:
   - Build a service, create an admin user + session.
   - POST `name=kitchen-tablet`.
   - Assert: 302 to landing URL. `ListDevices` now returns one device named `kitchen-tablet`. The device cookie value, run through `ValidateDeviceToken`, returns that device.

6. **enroll-new-browser empty name**:
   - POST `name=  `. Assert: 302 to `/admin/devices?error=Name+is+required`. `ListDevices` is empty.

7. **Device cookie replacement**:
   - POST enrollment with a request that already has both an admin cookie AND a device cookie for some prior device. Assert the response Set-Cookie for the device cookie name has the NEW token, not the old one. The browser will use the most-recently-set cookie.

8. **Landing handler with device identity**:
   - Build a request with the device cookie. Wrap in the `RequireAuth` middleware (real chain). Assert: 200, body contains the device's name in some recognisable form.

9. **Landing handler with admin identity**:
   - Build a request with an admin session cookie. Wrap in `RequireAuth`. Assert: 200, body contains the admin's email in the "viewing as admin" formatting.

10. **Landing handler with no auth**:
    - Plain request through `RequireAuth`. Assert: 302 to `/admin/login` for HTML Accept, 401 for non-HTML. (This is mostly a smoke test; the middleware behaviour is exhaustively tested in TASK-013.)

11. **Token never logged** (code review, not a test): scan the implementation for `slog.*("...token...", ...)` patterns to confirm no slog call interpolates the raw device token. The compile-time pattern enforces this.

12. Use `t.Setenv` only when env vars matter; for these tests build `Deps` and `auth.Config` directly.

13. Follow `.claude/rules/testing.md`. The tests above are intentionally specific because the cookie swap is the security-critical operation; we want to catch a regression that, e.g., clears the admin cookie before checking that the device exists.

## Definition of Done

- [ ] `views/devices.go` extended with `performBrowserEnrollment`, `handleDeviceEnrollExisting`, `handleDeviceEnrollNew`.
- [ ] `views/devices.templ` extended with the enrollment section; `templ generate` re-run.
- [ ] `views/device.templ` and `views/device.go` created for the landing page; `templ generate` run.
- [ ] `views/routes.go` extended to register the two new POST routes inside the existing admin sub-mux and the GET landing route under `RequireAuth` only.
- [ ] All listed acceptance criteria tests pass.
- [ ] No new third-party dependencies.
- [ ] No code path logs the raw device token.
- [ ] green-bar passes (gofmt, vet, build, test). Run `templ generate` before `green-bar`.
