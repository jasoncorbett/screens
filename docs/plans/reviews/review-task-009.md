---
task: TASK-009
title: "Login/logout views and OAuth route handlers"
reviewer: tester
date: 2026-04-24
result: ACCEPT
---

# Review: TASK-009 -- Login/logout views and OAuth route handlers

## Acceptance Criteria

| AC | Description | Result | Evidence |
|----|-------------|--------|----------|
| AC-5 | Login page with "Sign in with Google" button | PASS | `TestHandleLogin_ShowsLoginPage` |
| AC-7 | Unauthenticated `/admin/*` redirects to login | PASS | `TestProtectedRoutes_RedirectWithoutAuth`, `TestProtectedRoutes_MultipleRoutes` |
| AC-8 | "Sign in with Google" redirects to Google auth endpoint | PASS | `TestHandleGoogleStart_SetsStateCookieAndRedirects` |
| AC-9 | Valid callback exchanges code, validates token, creates session | PASS | Handler logic correct; full end-to-end requires real Google (tested via handler structure) |
| AC-10 | Mismatched state parameter rejects login | PASS | `TestHandleGoogleCallback_MismatchedState`, `TestCallback_EmptyStateAndCookieBothEmpty`, `TestCallback_NoStateParam` |
| AC-14 | POST /admin/logout deletes session, clears cookie, redirects | PASS | `TestHandleLogout_ClearsCookieAndRedirects`, `TestLogoutThroughMiddlewareChain` |
| AC-6 (partial) | Full login flow start-to-admin | PASS | Verified through middleware integration tests |

## Adversarial Findings

### 1. Middleware ordering: CSRF before Auth (CRITICAL -- FIXED)

**Description**: In `views/routes.go`, the middleware chain was wired as `RequireCSRF(RequireAuth(adminMux))`. This means CSRF middleware executes before RequireAuth, so the session has not yet been injected into the request context when CSRF tries to read it. For all POST/PUT/DELETE requests, `auth.SessionFromContext(r.Context())` returns nil, and CSRF middleware returns 403 Forbidden unconditionally. This made POST /admin/logout completely unusable through the real middleware chain.

**Severity**: Critical. All state-changing operations (logout, and future invite/deactivate) would fail with 403 regardless of CSRF token validity.

**Reproduction**: `TestLogoutThroughMiddlewareChain` (would have failed before fix).

**Fix applied**: Swapped the middleware ordering to `RequireAuth(RequireCSRF(adminMux))` so that Auth runs first (injects session into context), then CSRF validates against the session's token. This matches the comment in csrf.go line 19: "This middleware must run after RequireAuth so the session is in context."

### 2. XSS via error query parameter (NOT VULNERABLE)

**Description**: The `error` query parameter on `/admin/login` is rendered in the page. Tested with `<script>alert('xss')</script>` payload.

**Severity**: N/A -- templ auto-escapes all string interpolation via `templ.EscapeString()`.

**Evidence**: `TestLogin_ErrorParamHTMLInjection` confirms `<script>` is escaped to `&lt;script&gt;`.

### 3. OAuth state bypass with empty values (NOT VULNERABLE)

**Description**: Tested whether an attacker could bypass state validation by setting both the state query param and oauth_state cookie to empty string.

**Severity**: N/A -- the handler explicitly checks `state == ""` before comparing to the cookie value.

**Evidence**: `TestCallback_EmptyStateAndCookieBothEmpty`, `TestCallback_NoStateParam`.

### 4. Invalid/corrupt session cookie handling (NOT VULNERABLE)

**Description**: Tested with garbage session cookie values and empty cookie values.

**Severity**: N/A -- `ValidateSession` returns an error for unknown tokens, and the login handler falls through to show the login page.

**Evidence**: `TestLogin_InvalidSessionCookie`, `TestLogin_EmptySessionCookie`.

### 5. CSRF protection on logout (VERIFIED AFTER FIX)

**Description**: After fixing middleware ordering, verified that logout without CSRF token returns 403, logout with wrong CSRF token returns 403, and logout with correct CSRF token succeeds.

**Evidence**: `TestLogoutWithoutCSRFToken_Returns403`, `TestLogoutWithWrongCSRFToken_Returns403`, `TestLogoutThroughMiddlewareChain`.

### 6. Nil user/session context in admin handler (NOT VULNERABLE)

**Description**: Tested admin handler when context has user but no session, and session but no user.

**Severity**: N/A -- handler checks both `user == nil || session == nil` and redirects to login.

**Evidence**: `TestAdmin_NilSessionInContext`, `TestAdmin_NilUserInContext`.

### 7. Spec discrepancy: AC-10 says 403, implementation returns 302 redirect (LOW)

**Description**: The spec AC-10 says "login is rejected with a 403" but the implementation redirects to `/admin/login?error=Invalid+authentication+state` with 302. The task document says "redirect to login with error" which is what was implemented. The redirect approach is better UX.

**Severity**: Low -- task document and implementation agree; spec wording is slightly different.

**No fix needed**: The task document's requirement takes precedence and the implementation is correct.

### 8. State comparison is not constant-time (LOW)

**Description**: The OAuth state parameter comparison uses `!=` (string comparison) rather than `subtle.ConstantTimeCompare`. Since the state is a random nonce (not a reusable credential), timing attacks are not practically exploitable.

**Severity**: Low -- no realistic attack vector.

## New Tests Written

| Test | What it covers |
|------|---------------|
| `TestLogoutThroughMiddlewareChain` | Full middleware chain: Auth -> CSRF -> logout handler (catches middleware ordering bug) |
| `TestLogoutWithoutCSRFToken_Returns403` | CSRF rejection when token is missing |
| `TestLogoutWithWrongCSRFToken_Returns403` | CSRF rejection when token is wrong |
| `TestCallback_EmptyStateAndCookieBothEmpty` | Empty-string state bypass attempt |
| `TestCallback_NoStateParam` | Missing state parameter |
| `TestLogin_ErrorParamHTMLInjection` | XSS via error query parameter |
| `TestLogin_InvalidSessionCookie` | Corrupt session cookie handling |
| `TestLogin_EmptySessionCookie` | Empty session cookie handling |
| `TestAdmin_NilSessionInContext` | Missing session in context |
| `TestAdmin_NilUserInContext` | Missing user in context |
| `TestPublicRoutes_AccessibleWithoutAuth` | Public routes don't require auth |
| `TestProtectedRoutes_MultipleRoutes` | Multiple admin paths redirect without auth |
| `TestGoogleStart_StateCookieAttributes` | Cookie security attributes |
| `TestGoogleStart_UniqueStatePerRequest` | State randomness across requests |
| `TestLogout_SessionCookieAttributes` | Cleared cookie attributes |
| `TestAuthenticatedAdmin_IntegrationWithMiddleware` | Full auth flow through middleware |
| `TestCallback_ClearsStateCookieEvenOnError` | State cookie cleanup on missing code |
| `TestAddRoutes_NilDeps` | Nil deps doesn't panic |

## Green-bar Results

| Check | Result |
|-------|--------|
| `gofmt -l .` | PASS (empty output) |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |

## Recommendation

**ACCEPT** -- One critical bug found (middleware ordering) and fixed. All other security surfaces tested clean. 18 adversarial tests added.
