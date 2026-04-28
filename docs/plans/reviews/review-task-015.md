---
id: REVIEW-015
task: TASK-015
spec: SPEC-003
arch: ARCH-003
status: ACCEPT
reviewer: tester
reviewed: 2026-04-25
---

# Review: TASK-015 (Browser Enrollment Endpoints + Device Landing Page)

## Summary

The implementation correctly performs the security-sensitive cookie swap in the
order specified by the architecture (rotate token, delete admin session, clear
admin cookie, set device cookie, redirect). All 12 listed acceptance criteria
pass at the helper, handler, and full-middleware-chain levels.

I tried hard to break this and couldn't find a critical or high issue. The
adversarial probes that follow all came back clean once verified through the
real route registration. One low-severity defense-in-depth gap in the
**config validator** for `DEVICE_LANDING_URL` is fixed in this review (it
previously accepted `//evil.com`-style protocol-relative URLs).

**Recommendation: ACCEPT** with the config-validator hardening applied.

## AC coverage

| AC    | Description                                                      | Status | Evidence                                                                        |
| ----- | ---------------------------------------------------------------- | ------ | ------------------------------------------------------------------------------- |
| AC-27 | enroll-browser 302 + device cookie + admin cookie cleared        | PASS   | `TestHandleDeviceEnrollExisting_HappyPath`                                       |
| AC-28 | admin session row deleted from DB                                | PASS   | `TestHandleDeviceEnrollExisting_HappyPath` (final ValidateSession check)         |
| AC-29 | follow-up GET /device/ with device cookie returns 200 + name     | PASS   | `TestHandleDeviceLanding_DeviceIdentity`                                         |
| AC-30 | only the enrolling browser's session row is deleted              | PASS   | `TestHandleDeviceEnrollExisting_TwoSessionsOnePreserved`                         |
| AC-31 | revoked target -> 302 ?error= and NO cookie/session mutations    | PASS   | `TestHandleDeviceEnrollExisting_RevokedTargetAborts` + chain test                |
| AC-32 | unauthenticated POST -> 302/401, no cookie mutations             | PASS   | `TestEnrollChain_UnauthenticatedPost_NoCookieMutation_NoSessionDeletion`         |
| AC-33 | member POST -> 403, no cookie mutations                          | PASS   | `TestEnrollChain_MemberPost_403_NoCookieMutation`                                |
| AC-34 | GET to enroll path -> 405; admin session NOT terminated          | PASS   | `TestEnrollChain_GetReturns405_NoCookieMutation`                                 |
| AC-35 | POST without _csrf -> 403, no cookie mutations                   | PASS   | `TestEnrollChain_PostWithoutCSRF_403_NoCookieMutation`                           |
| AC-36 | enroll with prior device cookie -> new token replaces old        | PASS   | `TestHandleDeviceEnrollExisting_DeviceCookieReplacement`                         |
| AC-37 | enroll-new-browser creates device + cookie swap + 302 to landing | PASS   | `TestHandleDeviceEnrollNew_HappyPath`                                            |
| AC-38 | landing URL with no auth -> 302 (HTML) or 401                    | PASS   | `TestHandleDeviceLanding_NoAuth_HTML`, `TestHandleDeviceLanding_NoAuth_NonHTML`  |

All 12 ACs pass.

## Adversarial findings

### Findings that surfaced an issue

**F1 (low) — `DEVICE_LANDING_URL` validator accepts protocol-relative paths.**
Before this review, `Config.Validate()` only required `DEVICE_LANDING_URL` to
start with `/`. The values `//evil.com/path` and `/\evil.com` both pass that
check, and `http.Redirect(w, r, deps.DeviceLandingURL, 302)` would emit them
verbatim. Browsers interpret `//host/...` as protocol-relative and would send
a freshly enrolled kiosk to an external host.

This is admin-controlled config, so the realistic threat model is "operator
fat-fingers a dangerous value" rather than "attacker injects a URL." Even so,
since the value flows directly into a `Location:` header, the validator is the
right layer to refuse it. **Severity: low** because exploitation requires
admin write access to env/config.

- Reproduction: set `DEVICE_LANDING_URL=//evil.com` and try to start the
  service; before the fix Validate accepts it, after the fix it rejects with
  `DEVICE_LANDING_URL must be a same-origin path (must not begin with // or /\\)`.
- Fix: extend `Validate()` to reject `//*` and `/\*` after the existing `/`
  check. (Done in `internal/config/config.go`.)
- Tests added: `cfg.Validate` table cases `DeviceLandingURL protocol-relative
  rejected` and `DeviceLandingURL backslash protocol-relative rejected` in
  `internal/config/config_test.go`.

### Findings that did NOT reveal a bug (the implementation held)

**Cookie-swap atomicity (helper-level).** The helper calls `RotateDeviceToken`
first, returns on error before touching cookies, and only writes the
`Set-Cookie` headers + redirect on success. `TestHandleDeviceEnrollExisting_RevokedTargetAborts`
verifies this by revoking the target and asserting NO Set-Cookie headers, NO
deleted admin session, and a 302 to the error redirect.

**Cookie-swap atomicity (chain-level).**
`TestEnrollChain_AdminPostingForRevokedDeviceLeavesAdminAuthenticated`
exercises the same scenario through `httptest.NewServer` with the full route
registration. After the failed enrollment, a follow-up GET
`/admin/devices` with the original admin cookie still returns 200, confirming
the admin's session row was not deleted at any layer of the chain.

**`Logout` failure tolerance.** Per the task spec, the helper proceeds with
the cookie swap even if `Logout` returns an error. `Logout` calls
`DeleteSession` which is `:exec` (no rows-affected check), so a stale or
unknown cookie value is a silent no-op rather than an error. The code reads as
intended; I confirmed by reading `internal/auth/auth.go:148-151` and
`internal/db/queries/sessions.sql:10-11`.

**Token leakage probes.** Three independent checks:
1. `TestEnrollChain_TokenNotInResponseHeaders` reads the device cookie value
   off the response, then iterates every other response header and asserts
   the raw token does NOT appear. (Set-Cookie is the legitimate channel.)
2. `TestEnrollChain_TokenNotInSlogOutput` swaps a buffer-backed JSON slog
   handler and asserts the captured raw token never appears in any log line
   produced by the helper.
3. The Location header on both success (`/device/`) and error (`/admin/devices?error=...`)
   was inspected: neither carries the raw token.

The `slog.Info("device enrolled via browser", "device_id", ..., "enrolled_by", ...)`
call deliberately omits the token; the templ for `device.templ` also does not
render it.

**Route precedence.** `TestEnrollChain_LiteralBeatsWildcard` POSTs to
`/admin/devices/enroll-new-browser` through the real chain and asserts a new
device row was created with the form-supplied name -- proving the literal
handler ran rather than the wildcard handler treating `enroll-new-browser` as
a device id. I also independently confirmed via a standalone main that
ServeMux's literal-vs-wildcard precedence works for the registered routes.

**Session isolation across multiple browsers.**
`TestHandleDeviceEnrollExisting_TwoSessionsOnePreserved` creates two admin
sessions for the same user, enrolls the kiosk via session A's cookie, and
asserts session B's row still validates afterward. Catches a regression where
someone replaces `Logout(adminCookie)` with `DeleteSessionsByUserID(userID)`.

**Method enforcement.** `TestEnrollChain_GetReturns405_NoCookieMutation`
exercises GET / PUT / DELETE against both enroll routes through the chain.
GET returns 405 from the mux layer; PUT/DELETE return 403 from the CSRF layer
(state-changing methods get CSRF-checked before the mux dispatch). In all
cases, no device cookie is set and the admin session row remains.

**CSRF enforcement.**
`TestEnrollChain_PostWithoutCSRF_403_NoCookieMutation` POSTs to both enroll
routes from a valid admin session with no `_csrf` field. Both return 403.
After the rejected requests, the admin session is intact and no devices
were created by the would-be enroll-new-browser request.

**Member-role rejection.** `TestEnrollChain_MemberPost_403_NoCookieMutation`
authenticates as a member and POSTs both enroll routes with valid CSRF. Both
return 403. The member's session row is intact and no rogue device was
provisioned.

**Unauthenticated POST.**
`TestEnrollChain_UnauthenticatedPost_NoCookieMutation_NoSessionDeletion`
POSTs from a client with no cookies at all and verifies an unrelated admin's
session row survives the attempt. The response is 401 with the
`WWW-Authenticate: Bearer` challenge from RequireAuth.

**Empty path id.** `TestHandleDeviceEnrollExisting_EmptyPathID` exercises the
empty-string path-value short-circuit; the handler redirects to
`/admin/devices?error=Missing+device+ID` without invoking the helper, so
neither cookies nor sessions are touched.

**Path-traversal-shaped device ids.** Out-of-band test: posting to
`/admin/devices/..%2F..%2Fetc/enroll-browser` decodes the path value to
`../../etc`, which the auth layer treats as an unknown device id; the handler
falls through to the same `Device not found or revoked` flash. No file-system
or template injection. Verified by reading the handler logic alongside
`net/http`'s URL decoding behaviour.

**Concurrent enrollments for the same device.**
`TestEnrollChain_ConcurrentEnrollmentsForSameDevice` fires 10 concurrent
enrollment requests against the same device id (each with its own admin
session). At end-of-test, exactly one of the issued raw tokens validates
against the device row -- the SQLite engine serialises the rotations and the
last writer wins. Run with `-race`; clean.

**Landing-page admin-precedence and XSS.**
`TestEnrollChain_LandingPageAdminTakesPrecedence` confirms that when both
cookies are present, `RequireAuth` admits the admin identity (per its
documented probe order) and the landing page renders the "viewing as admin:
..." message instead of the device name.
`TestEnrollChain_LandingPageXSSEscapesDeviceName` registers a device named
`<script>alert("xss")</script>`, renders the landing page, and asserts the
literal `<script>` tag is not present (templ escaping holds).

**Long device name in enroll-new-browser.**
`TestEnrollChain_LongDeviceNameDoesNotCrash` posts a 1MB device name to
enroll-new-browser; the handler returns 302 either to the landing URL (if
SQLite accepts the row) or to a flash error redirect (if the underlying
write rejects it). No 5xx, no panic.

**Audit trail for enroll-new-browser.**
`TestEnrollChain_NewBrowserRouteCreatesDeviceWithEnrollerAsCreator` confirms
the device row's `created_by` matches the enrolling admin's id, so an
operator can answer "who provisioned this kiosk" from the database.

### Notes that did not warrant fixes

- The device landing route is registered as `mux.Handle("GET "+deps.DeviceLandingURL, ...)`.
  When `DEVICE_LANDING_URL=/device/`, this is a subtree pattern and any path
  under `/device/` is served by the placeholder handler. For Phase 1 this is
  fine (and arguably desirable, since Phase 2 may want sub-paths under
  `/device/` for screen content). I'm not flagging it as an issue, just
  noting it for context.
- The handler skeleton's parameter `authSvc *auth.Service` on
  `handleDeviceLanding` is intentionally retained even though the body
  doesn't use it: the task and architecture both call out that Phase 2 will
  attach real screen content to this handler.

## New tests added

All in `views/enrollment_adversarial_test.go` unless noted:

- `TestEnrollChain_AdminPostHappyPath_ThroughFullMiddlewareChain` -- happy
  path via `httptest.NewServer` exercising RequireAuth + RequireCSRF +
  RequireRole + handler.
- `TestEnrollChain_GetReturns405_NoCookieMutation` (table-driven over GET /
  PUT / DELETE for both routes) -- AC-34 plus extra method coverage.
- `TestEnrollChain_PostWithoutCSRF_403_NoCookieMutation` (table-driven over
  both routes) -- AC-35 end-to-end.
- `TestEnrollChain_MemberPost_403_NoCookieMutation` (table-driven over both
  routes) -- AC-33 end-to-end.
- `TestEnrollChain_UnauthenticatedPost_NoCookieMutation_NoSessionDeletion`
  -- AC-32 end-to-end with a separate admin's session that must survive.
- `TestEnrollChain_LiteralBeatsWildcard` -- ServeMux literal-vs-wildcard
  precedence, asserts the literal handler creates a device.
- `TestEnrollChain_TokenNotInResponseHeaders` -- raw token only in Set-Cookie
  for the device cookie, nowhere else.
- `TestEnrollChain_TokenNotInSlogOutput` -- buffer-backed slog handler
  asserts the rotated token never appears in log output.
- `TestEnrollChain_ConcurrentEnrollmentsForSameDevice` -- 10 concurrent
  enrollments, exactly one winning token, race-clean.
- `TestEnrollChain_LongDeviceNameDoesNotCrash` -- 1MB name yields a clean
  302 (no 5xx, no panic).
- `TestEnrollChain_AdminPostingForRevokedDeviceLeavesAdminAuthenticated` --
  AC-31 end-to-end via the chain, plus a follow-up GET that proves the admin
  session is intact.
- `TestEnrollChain_LandingPageAdminTakesPrecedence` -- both cookies present;
  landing page shows the admin-precedence formatting per RequireAuth's probe
  order.
- `TestEnrollChain_LandingPageXSSEscapesDeviceName` -- device name with
  HTML metacharacters round-trips through the landing page escaped.
- `TestEnrollChain_NewBrowserRouteCreatesDeviceWithEnrollerAsCreator` -- the
  audit trail (`created_by`) is correct.

In `internal/config/config_test.go`:

- `DeviceLandingURL protocol-relative rejected` (table case) -- new
  `//evil.com/path` validator branch.
- `DeviceLandingURL backslash protocol-relative rejected` (table case) --
  new `/\evil.com` validator branch.

## Fixes applied

- **`internal/config/config.go::Validate`** -- reject `DEVICE_LANDING_URL`
  values starting with `//` or `/\` to close the protocol-relative
  open-redirect vector. Updates the corresponding entry in `README.md`.

No source changes were required to `views/devices.go`, `views/devices.templ`,
`views/device.go`, `views/device.templ`, or `views/routes.go`. The
implementation is solid; the adversarial probes uncovered no behavioural
defects in the enrollment path itself.

## Green-bar

```
gofmt -l .             # empty
go vet ./...           # clean
go build ./...         # clean
go test ./...          # ok
go test -race ./...    # ok
```

## Recommendation

**ACCEPT.** The cookie-swap operation is the most security-sensitive surface
in SPEC-003 and the implementation gets it right: token rotation gates the
swap, the admin's other browsers are never affected, no token leaks into
headers / logs / bodies, and the full middleware chain enforces auth + CSRF
+ role at every entry point. The single defense-in-depth gap (config
validator) is fixed in this review.
