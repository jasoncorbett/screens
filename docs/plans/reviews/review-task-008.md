---
task: TASK-008
spec: SPEC-002
status: pass
tested-by: tester
date: 2026-04-24
---

# Review: TASK-008

## Acceptance Criteria Coverage

| AC    | Description                                                                 | Status | Notes                                                                           |
|-------|-----------------------------------------------------------------------------|--------|---------------------------------------------------------------------------------|
| AC-7  | Unauthenticated user redirected to login URL                                | PASS   | TestRequireAuth_NoCookie, TestRequireAuth_InvalidToken                           |
| AC-11 | Valid session injects user into context                                      | PASS   | TestRequireAuth_ValidSession                                                    |
| AC-12 | Expired session redirects and clears cookie                                 | PASS   | TestRequireAuth_ExpiredSession, TestRequireAuth_ClearsInvalidCookie             |
| AC-16 | POST without valid CSRF returns 403                                         | PASS   | TestRequireCSRF_POSTMissingToken, TestRequireCSRF_POSTWrongToken                |
| AC-17 | POST with valid CSRF passes through                                         | PASS   | TestRequireCSRF_POSTValidFormToken, TestRequireCSRF_POSTValidHeaderToken         |
| AC-21 | DB unreachable during validation treats as unauthenticated                  | PASS   | Fail-closed by design: ValidateSession wraps all DB errors, middleware redirects |

## Adversarial Findings

### No critical or high-severity issues found

The implementation is solid. The middleware is stateless (no shared mutable state), uses constant-time comparison for CSRF tokens, returns generic error messages, and properly clears invalid cookies.

### Low-severity findings (not requiring fixes):

1. **Cookie clearing omits SameSite attribute** -- Severity: low
   - The cookie-clearing code in `RequireAuth` sets `HttpOnly: true` and `MaxAge: -1` but does not set `SameSite=Lax` to match the attributes of the original session cookie. Browsers will still clear the cookie regardless, but for consistency the clearing cookie should mirror the original's attributes. This is a TASK-009 concern since the session cookie creation happens there.
   - No fix needed in TASK-008 scope.

2. **Session validation logged at Info level** -- Severity: low
   - `slog.Info("session validation failed", ...)` logs every invalid/expired session at Info level. In production with many expired cookies, this could be noisy. Consider `slog.Debug` for routine validation failures and `slog.Warn` only for unexpected errors.
   - Not fixed: style preference, does not affect correctness.

### What I tried that held up:

- **Empty cookie value**: Correctly redirects to login (hashed empty string not found in DB).
- **Very long cookie value (10K chars)**: Handled gracefully, no panic, redirects to login.
- **Null bytes in cookie**: Go sanitizes these; middleware handles the result correctly.
- **Unicode/control chars in cookie**: Handled gracefully.
- **Deactivated user with existing session**: `DeactivateUser` deletes sessions in a transaction, so the session is gone. Correctly redirects.
- **Deactivated user with manually re-inserted session**: `ValidateSession` checks `user.Active` and rejects, correctly redirects.
- **Multiple session cookies**: Go's `r.Cookie()` returns the first match. Works correctly.
- **Concurrent requests with same session**: 10 goroutines all get correct user from context. No race conditions detected.
- **CSRF with empty session token**: Both sides empty -- caught by `token == ""` check. Returns 403.
- **CSRF form field priority over header**: Form field takes priority as specified. Works correctly.
- **CSRF header fallback**: When no form body, header is used. Works correctly.
- **CSRF with very long token (100K chars)**: Rejected by ConstantTimeCompare mismatch. No crash.
- **CSRF with null bytes**: Rejected correctly (different from session token).
- **TRACE/custom HTTP methods**: Not in safe method list, require CSRF. Correctly returns 403.
- **Empty roles list in RequireRole**: Blocks everyone. Correct behavior.
- **Custom/unknown role string**: Not in allowed list, blocked. Correct.
- **Middleware ordering: CSRF without auth**: Returns 403 (no session in context). Correct fail-closed.
- **Middleware ordering: role without auth**: Returns 403 (no user in context). Correct fail-closed.
- **GET bypasses CSRF but not role check**: Confirmed correct -- CSRF passes GET through but role check still applies.
- **Form body accessibility after CSRF check**: Downstream handler can still read form values. `ParseForm()` caches results.
- **Constant-time comparison**: Verified `crypto/subtle.ConstantTimeCompare` is used. Tokens differing at first char, last char, or entirely all produce the same 403.
- **Concurrent role checks**: 20 goroutines with mixed roles produce correct results. No race detected.

## New Tests Written

23 adversarial tests in `internal/middleware/adversarial_test.go`:

| Test | What it covers |
|------|----------------|
| TestRequireAuth_EmptyCookieValue | Empty cookie value handled gracefully |
| TestRequireAuth_VeryLongCookieValue | 10K-char cookie does not panic |
| TestRequireAuth_NullByteInCookie | Null bytes in cookie handled |
| TestRequireAuth_UnicodeInCookie | Unicode/control chars handled |
| TestRequireAuth_DeactivatedUserDenied | Deactivated user's session is rejected |
| TestRequireAuth_ConcurrentRequests | 10 concurrent requests with same session |
| TestRequireAuth_MultipleSessionCookies | Multiple cookies with same name |
| TestRequireAuth_InactiveUser_RecreatedSession | Manually re-inserted session for inactive user |
| TestRequireCSRF_EmptySessionCSRFToken | Empty CSRF token on session |
| TestRequireCSRF_FormFieldTakesPriorityOverHeader | Form field wins over header |
| TestRequireCSRF_HeaderUsedWhenNoFormField | Header fallback when no form body |
| TestRequireCSRF_VeryLongToken | 100K-char CSRF token |
| TestRequireCSRF_NullByteInToken | Null byte in submitted token |
| TestRequireCSRF_TRACEMethodBlocked | TRACE method requires CSRF |
| TestRequireCSRF_UnknownMethodBlocked | Custom method requires CSRF |
| TestRequireCSRF_ConstantTimeCompareUsed | Table-driven: first/last/all chars wrong + match |
| TestRequireCSRF_FormBodyStillAccessibleDownstream | Downstream handler reads form values after CSRF |
| TestRequireRole_EmptyRolesList | Empty roles list blocks all |
| TestRequireRole_CustomRoleString | Unknown role string blocked |
| TestRequireRole_ConcurrentAccess | 20 concurrent role checks |
| TestMiddlewareChain_CSRFWithoutAuth_Returns403 | CSRF without auth in context |
| TestMiddlewareChain_RoleWithoutAuth_Returns403 | Role without auth in context |
| TestMiddlewareChain_GETBypassesCSRFButNotRole | GET passes CSRF but blocked by role |
| TestMiddlewareChain_ValidPOSTWithAllMiddleware | Full chain with CSRF via header (htmx) |
| TestMiddlewareChain_ExpiredSessionClearedAndRedirected | Expired session in full chain |

## Test Results

```
go test ./...
ok      github.com/jasoncorbett/screens/api                 (cached)
ok      github.com/jasoncorbett/screens/internal/auth        (cached)
ok      github.com/jasoncorbett/screens/internal/config      (cached)
ok      github.com/jasoncorbett/screens/internal/db          (cached)
ok      github.com/jasoncorbett/screens/internal/middleware   0.390s

go test -race ./...
ok      github.com/jasoncorbett/screens/internal/middleware   1.796s
(all packages pass)
```

## Green Bar

- gofmt: PASS
- go vet: PASS
- go build: PASS
- go test: PASS
- go test -race: PASS

## Recommendation

ACCEPT

The implementation is clean, correct, and well-structured. All acceptance criteria pass. No critical, high, or medium-severity issues were found. The middleware is stateless, uses constant-time token comparison, returns generic error messages, and handles all tested edge cases gracefully. The two low-severity findings (cookie attribute consistency, log level) are style issues that do not affect security or correctness.
