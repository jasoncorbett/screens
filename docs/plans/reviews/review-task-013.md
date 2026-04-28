---
task: TASK-013
title: "Unified RequireAuth middleware, CSRF device exemption, RequireDevice"
spec: SPEC-003
arch: ARCH-003
reviewer: tester
date: 2026-04-25
result: ACCEPT
---

# Review: TASK-013 -- Unified RequireAuth middleware, CSRF device exemption, RequireDevice

## Acceptance Criteria

| AC    | Description                                                                                              | Result | Evidence                                                       |
|-------|----------------------------------------------------------------------------------------------------------|--------|----------------------------------------------------------------|
| AC-6  | Bearer header authenticates as device; handler reads device identity                                     | PASS   | `TestRequireAuth_BearerHeader_DevicePath`                      |
| AC-7  | Device cookie (no Authorization) authenticates identically                                               | PASS   | `TestRequireAuth_DeviceCookie_DevicePath`                      |
| AC-8  | Header beats cookie when both are valid                                                                  | PASS   | `TestRequireAuth_HeaderBeatsCookie`                            |
| AC-9  | Unknown bearer token -> 401                                                                              | PASS   | `TestRequireAuth_BadBearerScheme_Returns401` and unknown-token branch in `TestRequireAuth_UnknownBearer_LogsKindDevice` |
| AC-10 | `Authorization: Basic ...` is ignored, no panic                                                          | PASS   | `TestRequireAuth_BadBearerScheme_Returns401/basic`             |
| AC-11 | `Authorization: Bearer ` (empty) -> 401                                                                  | PASS   | `TestRequireAuth_EmptyBearerValue_Returns401`                  |
| AC-14 | Revoked device logs `kind=device` with sanitised reason; raw token not present                           | PASS (after fix) | `TestRequireAuth_RevokedDevice_LogsKindDevice`         |
| AC-17 | Admin session populates `IdentityFromContext` with `IsAdmin()` and User                                  | PASS   | `TestRequireAuth_AdminPath_PopulatesIdentity`                  |
| AC-18 | Device populates `IdentityFromContext` with `IsDevice()` and Device                                      | PASS   | `TestRequireAuth_BearerHeader_DevicePath`                      |
| AC-19 | No credential + `Accept: text/html` -> 302 to login URL                                                  | PASS   | `TestRequireAuth_NoCredentialHTMLNav_RedirectsToLogin`         |
| AC-20 | No credential + non-HTML Accept -> 401 + `WWW-Authenticate: Bearer`                                      | PASS   | `TestRequireAuth_NoCredentialNonHTML_Returns401`, `TestRequireAuth_WWWAuthenticateExactly` |
| AC-21 | Device hitting `RequireRole(RoleAdmin)` -> 403                                                           | PASS   | `TestRequireRole_RejectsDevices/device_blocked`                |
| AC-22 | Device POST with no `_csrf` succeeds through `RequireCSRF`                                               | PASS   | `TestRequireCSRF_DeviceExempt`                                 |
| AC-23 | Admin POST with no `_csrf` -> 403                                                                        | PASS   | `TestRequireCSRF_AdminWithoutTokenStillRejected`               |

## Adversarial Findings

### 1. Revoked-device failure logged as `kind=none` instead of `kind=device` (MEDIUM -- FIXED)

**Description**: AC-14 mandates that a revoked device produce an info-level slog line whose `kind` field is `device` and whose `reason` field indicates revocation, so an operator triaging "why did my screen stop working?" can grep for the right kind/reason pair. The original implementation discarded the device-probe error and emitted a single failure line `slog.Info("auth failed", "kind", "none", "path", r.URL.Path)` regardless of which probe failed, so a revoked device looked identical in the logs to a no-credential request. There was also no `reason` attribute at all, even though SPEC-003 requirement 22 calls for one.

**Severity**: MEDIUM. Not a security flaw -- the revoked device is still rejected with a 401, and the raw token is still never logged -- but the contract that operators rely on for incident triage was broken.

**Reproduction**: Before the fix, capturing slog output during `TestRequireAuth_RevokedDevice_LogsKindDevice` yielded `"kind":"none"` with no `reason` field. After the fix it yields `"kind":"device","reason":"revoked"`.

**Fix**: In `internal/middleware/session.go`, capture the `ValidateDeviceToken` error from each device probe, classify it via a new `deviceFailureReason(err)` helper that maps the typed errors to short strings (`revoked`, `unknown_token`, `lookup_error`), and emit a `kind=device` log line when any device probe fired. The fall-through chain (admin -> bearer -> cookie) is unchanged; only the final log line was modified. The `denyUnauthenticated` response is unchanged so AC-9 / AC-11 still pass.

**Test**: `TestRequireAuth_RevokedDevice_LogsKindDevice` (passes only after the fix). Sister tests `TestRequireAuth_UnknownBearer_LogsKindDevice` and `TestRequireAuth_NoCredential_LogsKindNone` pin the kind/reason values for the other failure modes so they are protected against future regressions.

### 2. Authentication-confusion attacks (probed; HOLDS UP)

**Probes**:
- Both a valid admin session cookie AND a valid bearer token: admin must win (probe order, item 19 of the spec). Confirmed by `TestRequireAuth_AdminAndDevice_AdminWins`. Importantly the test also checks that `IdentityFromContext.Device == nil` so the admin-vs-device branch is unambiguous downstream.
- Admin session for a deactivated user PLUS a valid bearer: the deactivated `ValidateSession` returns an error, the middleware falls through to the bearer probe, and the device authenticates. Confirmed by `TestRequireAuth_DeactivatedAdmin_FallsThroughToDevice`. (Important corollary: a deactivated admin cannot impersonate a device, but they also do not poison the chain.)
- Invalid bearer header alongside valid device cookie: the bearer is dropped and the cookie is accepted. Confirmed by `TestRequireAuth_BadBearer_FallsThroughToCookie`.
- Real session token sent in the device cookie slot: the device probe hashes it, finds no row in `devices`, and 401s. Confirmed by `TestRequireAuth_DeviceCookieWithSessionLikeValue`. (This rules out a "wrong slot" cross-table accidental authentication.)

### 3. Authorization-header parsing (probed; HOLDS UP)

**Probes**:
- `Bearer\ttoken` (tab separator): `strings.HasPrefix(h, "Bearer ")` rejects it -> 401. Confirmed by `TestRequireAuth_BearerWithTabSeparator_NotAccepted`.
- `Bearer  <token>` (double space): `TrimSpace` recovers the token -> 200. Confirmed by `TestRequireAuth_BearerDoubleSpace_TrimsAndAccepts`.
- Lowercase `bearer`, mixed case `BeArEr`, `Token <x>`, `Basic ...`, `Bearerabc`: all rejected, no panic. Already covered by `TestRequireAuth_BadBearerScheme_Returns401`.
- 1 MiB bearer payload: hashes to nothing and returns 401, no panic and no DoS. Confirmed by `TestRequireAuth_VeryLongBearer_NoPanic`.
- Control characters / invalid UTF-8 / null bytes in the bearer value: all rejected cleanly with no panic. Confirmed by `TestRequireAuth_BearerWithControlChars_NoPanic`.

### 4. Cookie attacks (probed; HOLDS UP)

**Probes**:
- Multiple cookies with the same name: Go's `r.Cookie(name)` returns the first matching cookie. Already covered by `TestRequireAuth_MultipleSessionCookies` -- the first cookie's value is the one validated.
- Empty cookie value: `c.Value != ""` short-circuits both the session probe (existing behaviour) and the device cookie probe. Already covered by `TestRequireAuth_EmptyCookieValue`.
- Whitespace-only cookie value: passes the `!= ""` check, hashes to nothing, returns 401. (This is harmless; a non-empty random string is not going to collide with a real hashed token.)

### 5. Failure-mode probing (probed; HOLDS UP)

**Probes**:
- A `ValidateSession` error that is NOT "not found" (e.g., DB unavailable): the code path falls through to device probes, then to `denyUnauthenticated`. The middleware never returns 500; it always 401s or 302s on auth failure (fail-closed). Verified by code-reading -- the `vErr == nil` guard is the only success path.
- `MarkDeviceSeen` failure: `finishDevice` logs at `debug` and continues to `next`. Device authentication is not blocked by a touch-table failure. Verified by code-reading; the existing service-level `TestMarkDeviceSeen_UnknownDevice` proves the underlying call returns nil for missing rows.
- `ValidateDeviceToken` returning the generic `lookup_error` reason path is exercised via the unit-test plumbing of `deviceFailureReason` in the new fix (the third branch of the switch).

### 6. Response inspection (probed; HOLDS UP)

**Probes**:
- 401 body: exactly `unauthenticated` plus the trailing newline `http.Error` adds. Confirmed by `TestRequireAuth_401Body_Sanitised`, which also verifies neither the bearer token nor the session cookie value is echoed.
- 302 Location: the configured `loginURL` (a static string passed at construction time, not request-derived), so no open-redirect surface. Confirmed by all existing 302 tests (`TestRequireAuth_NoCredentialHTMLNav_RedirectsToLogin` etc.).
- `WWW-Authenticate` header: exactly `Bearer` (no realm, no challenge string). Pinned by `TestRequireAuth_WWWAuthenticateExactly`.
- Cleared session cookie attributes: `MaxAge < 0`, empty value, `HttpOnly`, `Path=/`. Pinned by `TestRequireAuth_ClearedCookieAttributes`. (Important because a cleared cookie that lost `HttpOnly` would regress the SPEC-002 cookie-hygiene contract.)

### 7. Concurrency (probed under -race; HOLDS UP)

**Probes**:
- 50 concurrent device requests with the same bearer token: all succeed, no race detector hits. Confirmed by `TestRequireAuth_ConcurrentDeviceRequests` under `go test -race`. `MarkDeviceSeen` writes are throttled in the database; the application-side path holds no shared mutable state.
- 40 concurrent mixed admin + device requests: alternating between session-cookie auth and bearer auth. All succeed under `-race`. Confirmed by `TestRequireAuth_ConcurrentMixedAuth`.

### 8. CSRF middleware (probed; HOLDS UP)

**Probes**:
- Device POST without `_csrf`: succeeds (AC-22). Confirmed by `TestRequireCSRF_DeviceExempt`.
- Admin POST without `_csrf`: 403 (AC-23). Confirmed by `TestRequireCSRF_AdminWithoutTokenStillRejected`.
- Defensive: if a request has BOTH a device identity and a session in context (impossible in the documented chain but could happen if a future caller wires them defensively), the device check still wins and CSRF is skipped. Confirmed by `TestRequireCSRF_DeviceWithSessionContextDoesNotApply`.
- Existing adversarial tests (TRACE method, unknown method, oversized token, null bytes in token, constant-time compare, no-session-context) continue to pass.

### 9. RequireDevice (probed; HOLDS UP)

**Probes**:
- No identity in context -> 403 with body `Forbidden`. Confirmed by `TestRequireDevice/no_identity_is_forbidden` and the body assertion in `TestRequireDevice_BodyDoesNotLeak` (also rules out leakage of query parameters into the body).
- Admin identity -> 403. Confirmed by `TestRequireDevice/admin_identity_is_forbidden`.
- Device identity -> next runs. Confirmed by `TestRequireDevice/device_identity_passes_through`.

### 10. HTML detection (probed; HOLDS UP)

**Probes**:
- Various `Accept` headers: `text/html` (302), q-weighted text/html alongside JSON (302), `text/*` (401, no literal `text/html`), `*/*` (401), empty Accept (401). Confirmed by `TestRequireAuth_HTMLNav_QValueAccept` (table-driven). The implementation uses `strings.Contains(accept, "text/html")`, so any header that mentions `text/html` -- even at a low q-value -- is treated as an HTML nav. This is a deliberate, simple implementation; future tightening (proper Accept parsing) would be a follow-up.
- POST with `Accept: text/html` -> 401 (only GET/HEAD redirect). Confirmed by `TestRequireAuth_POSTHTMLAccept_StillReturns401`.
- HEAD with `Accept: text/html` -> 302. Confirmed by `TestRequireAuth_HEADHTMLAccept_Redirects`.

### 11. Information leakage in slog output (probed; HOLDS UP)

**Probes**: For every adversarial logging test (`TestRequireAuth_RevokedDevice_LogsKindDevice`, `TestRequireAuth_UnknownBearer_LogsKindDevice`, `TestRequireAuth_NoCredential_LogsKindNone`), the test captures `slog.Default()` output via a `bytes.Buffer`-backed JSON handler and asserts `!strings.Contains(out, raw)` for each token used. No raw token, cookie value, or Authorization header content reaches the log lines.

## New Tests Written

All tests below are in `internal/middleware/auth_device_adversarial_test.go`:

- `TestRequireAuth_RevokedDevice_LogsKindDevice` -- AC-14 contract (kind=device, reason=revoked, no token leak). Required the source fix to pass.
- `TestRequireAuth_UnknownBearer_LogsKindDevice` -- pins `kind=device, reason=unknown_token` for unknown tokens.
- `TestRequireAuth_NoCredential_LogsKindNone` -- pins `kind=none` for the no-credential path so the kind=device fix doesn't accidentally flip it.
- `TestRequireAuth_AdminAndDevice_AdminWins` -- probe-order test for the both-credentials case (admin wins over device, device pointer is nil on the admin identity).
- `TestRequireAuth_BadBearer_FallsThroughToCookie` -- invalid bearer must not short-circuit; cookie still authenticates.
- `TestRequireAuth_DeactivatedAdmin_FallsThroughToDevice` -- admin probe failure does not poison the chain.
- `TestRequireAuth_BearerWithTabSeparator_NotAccepted` -- prefix is `"Bearer "` exactly (case- and whitespace-sensitive at the `B` and the trailing space).
- `TestRequireAuth_BearerDoubleSpace_TrimsAndAccepts` -- trailing whitespace after the prefix is trimmed.
- `TestRequireAuth_VeryLongBearer_NoPanic` -- 1 MiB bearer does not crash.
- `TestRequireAuth_BearerWithControlChars_NoPanic` -- null bytes / control chars / 0x7f-0xff bytes do not crash.
- `TestRequireAuth_DeviceCookieWithSessionLikeValue` -- cross-slot confusion: a session token in the device cookie slot does NOT authenticate.
- `TestRequireAuth_401Body_Sanitised` -- 401 body is `unauthenticated` and contains no token contents.
- `TestRequireAuth_WWWAuthenticateExactly` -- challenge header is exactly `Bearer`.
- `TestRequireAuth_HTMLNav_QValueAccept` -- table-driven Accept-header behaviours.
- `TestRequireAuth_POSTHTMLAccept_StillReturns401` -- only GET/HEAD redirect, regardless of Accept.
- `TestRequireAuth_HEADHTMLAccept_Redirects` -- HEAD is treated like GET for the redirect decision.
- `TestRequireAuth_ConcurrentDeviceRequests` -- 50 concurrent device requests, race-clean.
- `TestRequireAuth_ConcurrentMixedAuth` -- 40 concurrent mixed admin/device requests, race-clean.
- `TestRequireCSRF_DeviceWithSessionContextDoesNotApply` -- device exemption beats session check even when both are in context.
- `TestRequireDevice_BodyDoesNotLeak` -- 403 body is exactly `Forbidden`, no query-param leakage.
- `TestRequireAuth_ClearedCookieAttributes` -- cleared session cookie keeps `HttpOnly` + `Path=/`.

## Source Changes Made During Review

`internal/middleware/session.go`:
- Added `errors` import.
- Captured the device-probe failure reason (`revoked`, `unknown_token`, `lookup_error`) via a new `deviceFailureReason(err)` helper.
- The final failure log line now emits `kind=device` with the captured reason whenever a device probe ran, falling back to `kind=none` only when no device credential was presented at all.
- Public API and behaviour are otherwise unchanged: probe order is identical, response codes are identical, and the helper is package-private.

## Green-Bar Results

```
gofmt -l .              # empty
go vet ./...            # clean
go build ./...          # ok
go test ./...           # all packages PASS
go test -race ./...     # all packages PASS
```

## Recommendation

**ACCEPT**. One MEDIUM-severity finding (AC-14 logging contract) was fixed in source and is now covered by a passing test. Eleven categories of adversarial probes (auth confusion, header parsing, cookie attacks, failure modes, response inspection, concurrency, CSRF, RequireDevice, HTML detection, log leakage) all held up under stress. No CRITICAL or HIGH findings. The middleware is appropriately fail-closed: every uncovered code path that could plausibly slip into a 500 or a silent grant ends in either 401 or 302 with a sanitised body and no token leakage.
