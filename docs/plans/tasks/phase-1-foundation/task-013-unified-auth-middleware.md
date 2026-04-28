---
id: TASK-013
title: "Unified RequireAuth middleware, CSRF device exemption, RequireDevice"
spec: SPEC-003
arch: ARCH-003
status: done
priority: p0
prerequisites: [TASK-012]
skills: [add-middleware, green-bar]
created: 2026-04-25
author: architect
---

# TASK-013: Unified RequireAuth middleware, CSRF device exemption, RequireDevice

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Make protected endpoints accept either an admin session OR a device bearer token. This task rewrites `internal/middleware/session.go` so `RequireAuth` probes both credential kinds and injects a typed `auth.Identity` into the request context, modifies `RequireCSRF` to exempt device-authenticated requests, and adds a small `RequireDevice` middleware for screen-display routes that should ONLY accept devices.

## Context

- `internal/middleware/session.go` currently calls `authService.ValidateSession`, populates `ContextWithUser` and `ContextWithSession`, and 302-redirects on failure. After this task it ALSO probes the bearer header and the device cookie, and falls back to a 401 for non-HTML requests.
- `internal/middleware/csrf.go` currently validates `_csrf` for state-changing methods on every authenticated request. After this task it skips validation when `IdentityFromContext` reports a device.
- `internal/middleware/require_role.go` reads `UserFromContext`. Because `RequireAuth` will continue to populate `ContextWithUser` for the admin path (and only the admin path), `RequireRole` correctly returns 403 for device callers without changes. Verify -- do not modify -- this file.
- The signature change to `RequireAuth` (it now takes both cookie names) propagates to one call site: `views/routes.go::registerAuthRoutes`. That call site MUST be updated in this task; otherwise the build breaks.
- The handler files for the admin views in `views/users.go` and `views/auth_handlers.go` already use `UserFromContext` and `SessionFromContext`. They MUST keep working unchanged.

### Files to Read Before Starting

- `.claude/rules/http.md`
- `.claude/rules/testing.md`
- `.claude/skills/add-middleware/SKILL.md`
- `internal/middleware/session.go` -- current implementation; replace it
- `internal/middleware/csrf.go` -- current implementation; modify it
- `internal/middleware/require_role.go` -- read only; verify no change needed
- `internal/middleware/middleware_test.go` -- existing tests to keep green or extend
- `internal/auth/auth.go` -- `ValidateSession`, `ValidateDeviceToken`, `MarkDeviceSeen` signatures
- `internal/auth/identity.go` -- the `Identity` type from TASK-012
- `internal/auth/context.go` -- helpers added in TASK-012
- `views/routes.go` -- the call site that wires admin routes through `RequireAuth`
- `docs/plans/architecture/phase-1-foundation/arch-device-auth.md` -- "Component Design" / pseudo-implementation of `RequireAuth`

## Requirements

1. Rewrite `internal/middleware/session.go`:
   - New signature: `func RequireAuth(authService *auth.Service, sessionCookie, deviceCookie, loginURL string) func(http.Handler) http.Handler`.
   - Probe order:
     1. Read `sessionCookie` cookie. If present and `ValidateSession` succeeds, build an `auth.Identity{Kind: IdentityAdmin, User: user}`, populate context with `ContextWithUser`, `ContextWithSession`, and `ContextWithIdentity`, then call `next`.
     2. Otherwise check the `Authorization` header for `Bearer <token>` (case-sensitive `Bearer ` prefix). Strip the prefix, `strings.TrimSpace`, and if non-empty call `ValidateDeviceToken`. On success: invoke `MarkDeviceSeen` (best-effort -- log at debug on failure but proceed), build an `auth.Identity{Kind: IdentityDevice, Device: dev}`, populate context with `ContextWithDevice` and `ContextWithIdentity`, call `next`.
     3. Otherwise check the `deviceCookie` cookie. Same handling as step 2 if present.
   - On no successful credential:
     - Clear the session cookie (set MaxAge -1) so a stale invalid one stops being sent.
     - If the request is an HTML navigation (method GET or HEAD AND `Accept` header contains `text/html`), 302 to `loginURL`.
     - Otherwise, set `WWW-Authenticate: Bearer` and respond `401 Unauthorized` with body `unauthenticated`.
   - Log one `slog.Info` line on failure with attrs `kind=none`, `path=r.URL.Path`. Never log raw token or cookie values.
   - Log one `slog.Debug` line on each successful auth (admin or device) with the identity ID, NOT the token. (Optional: skip the debug line if it produces too much noise; the 401/302 path needs the info line.)
   - Update the existing `// Middleware chain (outermost first)` comment block to reflect that admin OR device is now accepted.

2. Modify `internal/middleware/csrf.go`:
   - After the safe-method short-circuit, before the existing session-CSRF check, fetch the identity via `auth.IdentityFromContext(r.Context())`. If `id != nil && id.IsDevice()`, call `next.ServeHTTP(w, r)` and return. Devices are exempt from CSRF.
   - Existing admin session path (lookup `SessionFromContext`, compare with `subtle.ConstantTimeCompare`) is unchanged.

3. Verify (do NOT modify) `internal/middleware/require_role.go`:
   - It reads `UserFromContext`. Devices do not populate that key. So `RequireRole` returns 403 for device callers automatically. Add a one-line code comment in this file pointing readers at this fact: `// Devices do not have a User and are rejected here even though RequireAuth admitted them.`

4. Create `internal/middleware/require_device.go`:
   - `func RequireDevice() func(http.Handler) http.Handler`.
   - Reads `IdentityFromContext`. If nil or not `IsDevice()`, respond 403 with body `Forbidden`.
   - Otherwise call `next`.
   - Add the same `// Middleware chain (outermost first)` comment block used in the other middleware files.

5. Update `views/routes.go`:
   - The call to `middleware.RequireAuth(deps.Auth, deps.CookieName, "/admin/login")` becomes `middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")`.
   - Add `DeviceCookieName string` and `DeviceLandingURL string` fields to the `Deps` struct. (`DeviceLandingURL` is consumed by TASK-015's enrollment handlers; declaring it here keeps the `Deps` struct stable across both tasks.)
   - Do NOT change the order of the wrapping for the admin sub-mux (RequireAuth -> RequireCSRF -> sub-mux); only the cookie-name argument changes.

6. Update `main.go`:
   - When constructing `&views.Deps{...}`, set `DeviceCookieName: cfg.Auth.DeviceCookieName` and `DeviceLandingURL: cfg.Auth.DeviceLandingURL`.
   - When constructing `auth.Config{...}` for `auth.NewService`, set `DeviceCookieName: cfg.Auth.DeviceCookieName`, `DeviceLastSeenInterval: cfg.Auth.DeviceLastSeenInterval`, and `DeviceLandingURL: cfg.Auth.DeviceLandingURL`.

## Acceptance Criteria

From SPEC-003:

- [ ] AC-6: A GET with a valid `Authorization: Bearer <token>` succeeds and a downstream handler reads the device identity from context.
- [ ] AC-7: A GET with the device cookie (and no Authorization) succeeds with the same context.
- [ ] AC-8: When BOTH a header and a (different-device) cookie are present, the header wins. Verify by setting up two devices, sending both, and asserting `IdentityFromContext` returns the header device.
- [ ] AC-9: An unknown bearer token yields 401.
- [ ] AC-10: `Authorization: Basic <anything>` is ignored by the device probe (no panic, no auth granted as a device).
- [ ] AC-11: `Authorization: Bearer ` (empty) yields 401.
- [ ] AC-14: A revoked device produces a slog info line with `kind=device` and a sanitised reason; the raw token does not appear in the log line.
- [ ] AC-17: An admin session through `RequireAuth` populates `IdentityFromContext` with `IsAdmin() == true` and the User field set.
- [ ] AC-18: A device through `RequireAuth` populates `IdentityFromContext` with `IsDevice() == true` and the Device field set.
- [ ] AC-19: No-credential GET with `Accept: text/html` -> 302 to login URL.
- [ ] AC-20: No-credential request without `text/html` Accept -> 401 with `WWW-Authenticate: Bearer` header.
- [ ] AC-21: A device hitting a route wrapped in `RequireRole(RoleAdmin)` -> 403.
- [ ] AC-22: A device POST (no `_csrf`) through `RequireCSRF` after `RequireAuth` succeeds.
- [ ] AC-23: An admin POST without `_csrf` is still rejected with 403 (regression-protect existing admin behaviour).

## Skills to Use

- `add-middleware` -- mirror existing middleware file structure and tests.
- `green-bar` -- run before marking complete.

## Test Requirements

Use `httptest.NewRecorder` plus the real `auth.Service` backed by `db.OpenTestDB(t)`. Many of these tests can use the existing patterns in `internal/middleware/middleware_test.go`.

1. **Admin path**: a session cookie that validates -> next handler runs, identity in context is admin, `UserFromContext` also populated.
2. **Bearer path**: register a device, send `Authorization: Bearer <raw>`, assert next handler runs and `IdentityFromContext.IsDevice()` is true.
3. **Cookie path**: same as #2 but with `Cookie: screens_device=<raw>` and no Authorization header.
4. **Header beats cookie**: register devices A and B; send `Authorization: Bearer <A.token>` and cookie `screens_device=<B.token>`; assert the device in context has A's id.
5. **No credential, HTML nav**: GET with `Accept: text/html` -> 302 to `/admin/login`.
6. **No credential, JSON / device-style**: GET with no Accept (or `Accept: application/json`) -> 401 with `WWW-Authenticate: Bearer` header.
7. **Bad bearer scheme**: `Authorization: Basic abc` -> 401 (no panic, no authentication).
8. **Empty bearer value**: `Authorization: Bearer ` -> 401.
9. **Revoked device**: register a device, revoke it via the service, then send the bearer -> 401, and assert via `slogtest` (or by attaching a captured handler) that an info line was emitted with the right reason. If capturing slog adds too much noise, settle for asserting the 401 + that no row was authenticated.
10. **Stale session cookie cleared**: send an invalid session cookie with a non-HTML Accept; assert the response sets `Set-Cookie: screens_session=; Max-Age=-1`.
11. **CSRF exemption for devices**: build a chain of `RequireAuth -> RequireCSRF -> next`. Send a POST authenticated by bearer (no `_csrf`); assert `next` runs.
12. **CSRF still required for admin**: same chain, POST authenticated by session, no `_csrf` -> 403.
13. **RequireRole rejects devices**: chain `RequireAuth -> RequireRole(RoleAdmin) -> next`. POST authenticated by bearer -> 403; admin user authenticated by session -> next.
14. **RequireDevice**: directly test the new middleware: identity-less request -> 403; admin identity -> 403; device identity -> next runs.
15. Build a small `t.Helper()` factory that returns a configured `auth.Service` plus pre-created device records to keep test bodies short.
16. Tests follow `.claude/rules/testing.md`. Do not assert structural details like exact log message text -- assert observable behaviour.

## Definition of Done

- [ ] `RequireAuth` rewritten with new signature and three-probe order.
- [ ] `RequireCSRF` exempts device identities.
- [ ] `RequireDevice` added in its own file.
- [ ] `RequireRole` carries the new clarifying comment but no behaviour change.
- [ ] `views/routes.go` and `main.go` updated to pass `DeviceCookieName` and `DeviceLandingURL`.
- [ ] `views.Deps` gains the `DeviceCookieName` and `DeviceLandingURL` fields.
- [ ] All listed middleware tests pass; existing admin-session middleware tests still pass.
- [ ] green-bar passes.
- [ ] No new third-party dependencies.
