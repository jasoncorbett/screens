---
id: TASK-009
title: "Login/logout views and OAuth route handlers"
spec: SPEC-002
arch: ARCH-002
status: ready
priority: p0
prerequisites: [TASK-007, TASK-008]
skills: [add-view, add-endpoint, green-bar]
created: 2026-04-20
author: architect
---

# TASK-009: Login/logout views and OAuth route handlers

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Implement the login page, Google OAuth flow handlers (start and callback), and the logout handler. Wire everything together in `main.go` so that the full authentication flow works end-to-end: user visits a protected route, is redirected to login, clicks "Sign in with Google", authenticates with Google, returns to the callback, gets a session cookie, and is redirected to the admin area.

## Context

TASK-007 provides the Google OAuth client (authorization URLs, code exchange, ID token validation). TASK-008 provides the middleware (session validation, CSRF). TASK-006 provides the auth service (session creation, user provisioning). This task ties them all together with view handlers and route wiring.

### Files to Read Before Starting

- `.claude/rules/http.md` -- handler conventions and view section
- `.claude/rules/testing.md` -- test conventions
- `.claude/skills/add-view/SKILL.md` -- view creation pattern
- `views/demo.go` and `views/demo.templ` -- existing view handler pattern
- `views/routes.go` -- route registration infrastructure
- `views/layout.templ` -- base HTML layout
- `main.go` -- current wiring to understand where auth fits
- `internal/auth/auth.go` -- Service API (from TASK-006)
- `internal/auth/google.go` -- GoogleClient API (from TASK-007)
- `internal/middleware/session.go` -- RequireAuth middleware (from TASK-008)
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- OAuth flow sequence and main.go wiring

## Requirements

### Login Page

1. Create `views/login.templ` with:
   - A login page component that uses `@layout("Login - screens")`
   - A "Sign in with Google" button/link that navigates to `/auth/google/start`
   - If an error message is present (passed via query param or handler), display it
   - Semantic HTML: proper heading, accessible button
   - Simple, clean styling (can use existing app.css classes)

2. Create `views/login.go` with:
   - Register route `GET /admin/login` via `init()` + `registerRoute`
   - `handleLogin` renders the login page template
   - If the user is already authenticated (has valid session), redirect to `/admin/`
   - If there's an `error` query parameter, pass it to the template for display

### OAuth Start Handler

3. Create `views/auth_handlers.go` (or add to an existing auth views file) with:
   - Register route `GET /auth/google/start` via `init()` + `registerRoute`
   - `handleGoogleStart` handler that:
     - Generates a random state token (16 bytes, hex-encoded)
     - Stores the state in a short-lived cookie (`oauth_state`, 5 minute expiry, HttpOnly, SameSite=Lax)
     - Redirects (302) to `authService.GoogleClient.AuthorizationURL(state)`

### OAuth Callback Handler

4. Add to `views/auth_handlers.go`:
   - Register route `GET /auth/google/callback` via `init()` + `registerRoute`
   - `handleGoogleCallback` handler that:
     - Reads the `state` query parameter and the `oauth_state` cookie
     - If state is missing or does not match the cookie, redirect to login with error
     - Clears the `oauth_state` cookie
     - Reads the `code` query parameter
     - Calls `authService.GoogleClient.ExchangeCode(ctx, code)` to get the ID token
     - Calls `authService.GoogleClient.ValidateIDToken(ctx, idToken, clientID)` to get email and name
     - Calls `authService.ProvisionUser(ctx, email, displayName)` to get or create the user
     - If provisioning fails (unauthorized email, deactivated), redirect to login with error
     - Calls `authService.CreateSession(ctx, user.ID)` to create a session
     - Sets the session cookie (HttpOnly, SameSite=Lax, Secure if not dev, Path=/, MaxAge=session duration)
     - Redirects (302) to `/admin/`

### Logout Handler

5. Add to `views/auth_handlers.go`:
   - Register route `POST /admin/logout` via `init()` + `registerRoute`
   - `handleLogout` handler that:
     - Reads the session cookie
     - Calls `authService.Logout(ctx, rawToken)` to delete the session
     - Clears the session cookie (set MaxAge=-1)
     - Redirects (302) to `/admin/login`
   - Note: logout is a POST with CSRF protection (middleware handles CSRF check)

### Admin Landing Page (placeholder)

6. Create `views/admin.templ` with a minimal admin landing page:
   - Uses `@layout("Admin - screens")`
   - Displays the authenticated user's email and display name
   - Shows a "Logout" button (POST form to `/admin/logout` with CSRF token)
   - Shows a link to user management (`/admin/users`) if user role is admin
   - This is a placeholder -- full admin dashboard is a future feature

7. Create `views/admin.go` with:
   - Register route `GET /admin/{$}` (exact match for /admin/)
   - `handleAdmin` renders the admin landing page with user info from context

### main.go Wiring

8. Modify `main.go` to:
   - Create the `auth.Service` after DB migration using config values
   - Create the `auth.GoogleClient` with OAuth config
   - Set up the `auth.Service` with the `GoogleClient`
   - Pass `authService` to view handlers that need it (via closure or package-level variable set during init)
   - Apply `middleware.RequireAuth` to `/admin/` routes (except `/admin/login`)
   - Apply `middleware.RequireCSRF` to state-changing admin routes
   - Keep public routes (`/admin/login`, `/auth/google/start`, `/auth/google/callback`) outside the auth middleware

9. The wiring must ensure:
   - `GET /admin/login` is public (no auth required)
   - `GET /auth/google/start` is public
   - `GET /auth/google/callback` is public
   - `POST /admin/logout` requires auth + CSRF
   - `GET /admin/` requires auth
   - All other `/admin/*` routes require auth

### Dependency Injection Pattern

10. Since views use `init()` + `registerRoute` which runs before `main()`, the auth service must be injected at route-registration time or via a package-level setter. Options:
    - **Option A**: Change `views.AddRoutes(mux)` to `views.AddRoutes(mux, authService)` and have AddRoutes pass the service to handlers.
    - **Option B**: Use `views.SetAuthService(authService)` called from main before `views.AddRoutes(mux)`.
    - **Recommended**: Option A -- modify `views.AddRoutes` to accept dependencies. This is the cleanest approach and avoids global state. Auth-dependent handlers are registered in AddRoutes rather than init().

## Acceptance Criteria

- [ ] AC-5: When an unauthenticated user navigates to `/admin/login`, a login page is displayed with a "Sign in with Google" button.
- [ ] AC-7: When an unauthenticated user navigates to any `/admin/*` route (except login), they are redirected to `/admin/login`.
- [ ] AC-8: When a user clicks "Sign in with Google", they are redirected to Google's authorization endpoint with correct parameters.
- [ ] AC-9: When Google redirects back with a valid code, the system exchanges it for tokens, validates the ID token, and creates a session.
- [ ] AC-10: When the OAuth callback receives a mismatched state parameter, login is rejected.
- [ ] AC-14: When a POST is made to `/admin/logout` with a valid session, the session is deleted, cookie cleared, and user redirected to login.
- [ ] AC-6 (partial): The full login flow works end-to-end (start -> Google -> callback -> session -> admin page).

## Skills to Use

- `add-view` -- for login.templ, admin.templ, and their handlers
- `add-endpoint` -- for OAuth route handlers (though they live in views/)
- `green-bar` -- run before marking complete

## Test Requirements

1. Test `handleLogin` returns 200 with HTML containing "Sign in with Google".
2. Test `handleLogin` redirects to `/admin/` if user already has a valid session.
3. Test `handleGoogleStart` sets an `oauth_state` cookie and redirects to a Google URL.
4. Test `handleGoogleCallback` with mismatched state returns redirect to login with error.
5. Test `handleGoogleCallback` with valid state and mock token exchange creates a session cookie and redirects to `/admin/`.
6. Test `handleLogout` clears the session cookie and redirects to `/admin/login`.
7. Test `handleAdmin` returns 200 with user info when authenticated.
8. Test that `/admin/` returns 302 to `/admin/login` when not authenticated (middleware integration).
9. Use `httptest.NewRecorder` and `httptest.NewServer` as appropriate.
10. Mock external Google calls using `httptest.NewServer`.
11. Follow `.claude/rules/testing.md`.

## Definition of Done

- [ ] Login page template and handler implemented
- [ ] Google OAuth start handler implemented (state cookie + redirect)
- [ ] Google OAuth callback handler implemented (state validation, code exchange, session creation)
- [ ] Logout handler implemented (session deletion, cookie clearing)
- [ ] Admin landing page (placeholder) with user info and logout button
- [ ] main.go wired with auth service, middleware, and route protection
- [ ] `templ generate` run successfully
- [ ] All tests pass
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies
