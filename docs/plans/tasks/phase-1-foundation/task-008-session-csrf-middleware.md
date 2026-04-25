---
id: TASK-008
title: "Session and CSRF middleware"
spec: SPEC-002
arch: ARCH-002
status: review
priority: p0
prerequisites: [TASK-006]
skills: [add-middleware, green-bar]
created: 2026-04-20
author: architect
---

# TASK-008: Session and CSRF middleware

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Create HTTP middleware for session authentication, CSRF protection, and role-based access control. These middleware components wrap protected admin routes to enforce that requests come from authenticated users with valid sessions and proper CSRF tokens.

## Context

TASK-006 implements the auth service with `ValidateSession`, `UserFromContext`, and `SessionFromContext`. This task creates the middleware layer that calls those functions on every request to protected routes. The middleware follows the standard `func(http.Handler) http.Handler` pattern used throughout Go stdlib-based applications.

### Files to Read Before Starting

- `.claude/rules/http.md` -- HTTP handler and middleware conventions
- `.claude/rules/testing.md` -- test conventions
- `.claude/skills/add-middleware/SKILL.md` -- middleware creation pattern
- `internal/auth/auth.go` -- ValidateSession method signature (from TASK-006)
- `internal/auth/context.go` -- ContextWithUser, ContextWithSession, UserFromContext, SessionFromContext (from TASK-006)
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- middleware section

## Requirements

1. Create `internal/middleware/session.go` with:
   - `RequireAuth(authService *auth.Service, cookieName, loginURL string) func(http.Handler) http.Handler`
   - The middleware must:
     - Read the session cookie from the request by `cookieName`
     - If no cookie is present, redirect to `loginURL` (302)
     - Call `authService.ValidateSession(ctx, rawToken)` with the cookie value
     - If validation fails (expired, invalid, DB error), clear the cookie and redirect to `loginURL`
     - If validation succeeds, inject the `*User` and `*Session` into the request context using auth package context helpers
     - Call `next.ServeHTTP(w, r.WithContext(newCtx))` for valid sessions

2. Create `internal/middleware/csrf.go` with:
   - `RequireCSRF() func(http.Handler) http.Handler`
   - The middleware must:
     - For safe methods (GET, HEAD, OPTIONS), pass through without validation
     - For state-changing methods (POST, PUT, PATCH, DELETE):
       - Extract the CSRF token from the request: first check `_csrf` form field, then `X-CSRF-Token` header (for htmx requests)
       - Extract the session from the request context via `auth.SessionFromContext(r.Context())`
       - If no session is in context, return 403 (this middleware must run after RequireAuth)
       - Compare the submitted token against `session.CSRFToken` using `crypto/subtle.ConstantTimeCompare`
       - If tokens don't match or are missing, return 403 Forbidden with a plain error message
       - If tokens match, call `next.ServeHTTP(w, r)`

3. Create `internal/middleware/require_role.go` with:
   - `RequireRole(roles ...auth.Role) func(http.Handler) http.Handler`
   - The middleware must:
     - Extract the user from context via `auth.UserFromContext(r.Context())`
     - If no user in context, return 403 (this middleware must run after RequireAuth)
     - Check if the user's role is in the allowed `roles` list
     - If not, return 403 Forbidden
     - If yes, call `next.ServeHTTP(w, r)`

4. Middleware ordering documentation:
   - Add a comment block in each file explaining the expected middleware chain order:
     ```
     // Middleware chain (outermost first):
     // 1. RequireAuth -- validates session, injects user+session into context
     // 2. RequireCSRF -- validates CSRF token on state-changing requests
     // 3. RequireRole -- checks user has required role (optional, for admin-only routes)
     ```

## Acceptance Criteria

- [ ] AC-7: When an unauthenticated user navigates to a route protected by RequireAuth, then they are redirected to the login URL.
- [ ] AC-11: When a valid session cookie is present, RequireAuth injects the user into context and the next handler receives it.
- [ ] AC-12: When an expired session cookie is present, RequireAuth redirects to login and clears the cookie.
- [ ] AC-16: When a POST request is made without a valid CSRF token, RequireCSRF returns 403.
- [ ] AC-17: When a POST request includes a valid CSRF token matching the session, RequireCSRF passes through.
- [ ] AC-21: When the database is unreachable during session validation, RequireAuth treats the request as unauthenticated (redirect to login).

## Skills to Use

- `add-middleware` -- follow this skill's pattern for file structure, context keys, and tests
- `green-bar` -- run before marking complete

## Test Requirements

1. Test RequireAuth redirects to login URL when no session cookie is present.
2. Test RequireAuth redirects when the session token is invalid/expired (mock the auth service or use a real DB with expired session).
3. Test RequireAuth injects user and session into context when session is valid.
4. Test RequireAuth clears the cookie on invalid session.
5. Test RequireCSRF passes through GET requests without validation.
6. Test RequireCSRF returns 403 on POST with missing CSRF token.
7. Test RequireCSRF returns 403 on POST with wrong CSRF token.
8. Test RequireCSRF passes through POST with correct CSRF token in form field.
9. Test RequireCSRF accepts the token from X-CSRF-Token header (htmx support).
10. Test RequireRole returns 403 when user role is not in allowed list.
11. Test RequireRole passes through when user role is allowed.
12. Use `httptest.NewRecorder` and simple handler funcs for testing.
13. Use `db.OpenTestDB(t)` and real auth.Service for integration tests where appropriate.
14. Follow `.claude/rules/testing.md`.

## Definition of Done

- [ ] RequireAuth middleware implemented and tested
- [ ] RequireCSRF middleware implemented and tested (constant-time comparison)
- [ ] RequireRole middleware implemented and tested
- [ ] Middleware files have proper ordering documentation
- [ ] All tests pass
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies
