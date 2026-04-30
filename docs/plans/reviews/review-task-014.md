---
task: TASK-014
spec: SPEC-003
reviewer: tester
date: 2026-04-25
recommendation: ACCEPT
---

# Review: TASK-014 (Device management views)

## Acceptance Criteria

| AC   | Description                                                                                          | Result | Evidence                                                              |
|------|------------------------------------------------------------------------------------------------------|--------|-----------------------------------------------------------------------|
| AC-1 | POST `/admin/devices` with name creates a row and the response HTML contains the raw token in `<pre>` | PASS   | `TestHandleDeviceCreate_HappyPath`                                    |
| AC-2 | A subsequent GET `/admin/devices` does NOT contain the raw token                                     | PASS   | `TestHandleDeviceList_DoesNotLeakPreviousToken`                       |
| AC-3 | POST with empty / whitespace name returns 302 with `error=` and creates no row                       | PASS   | `TestHandleDeviceCreate_RejectsInvalidName` (table)                   |
| AC-12 (UI half) | POST `/{id}/revoke` flips `revoked_at`; subsequent GET shows it under revoked section      | PASS   | `TestHandleDeviceRevoke_HappyPath` + `TestDeviceMgmt_RevokedDeviceShowsInRevokedSection` |
| AC-24 | A logged-in member GET `/admin/devices` receives 403                                                | PASS   | `TestDeviceMgmt_MemberCannotAccessDevicesPage` (full middleware chain) |
| AC-25 | Admin GET shows all non-revoked devices with names + last-seen                                       | PASS   | `TestHandleDeviceList_RendersDeviceNames` + `TestDeviceMgmt_AdminListThroughChainSucceeds` |
| AC-26 | Admin POST renders raw token AND "Save this token now. It will not be displayed again." copy        | PASS   | `TestHandleDeviceCreate_HappyPath` (asserts `Save this token now`)    |

All seven scoped ACs pass.

## Adversarial Findings

### F1 -- Double-revoke silently flashes "Device revoked" again (medium)

**Symptom**: When an admin clicked Revoke twice on the same device (or refreshed the form-submission page), the second request returned `302 -> /admin/devices?msg=revoked` even though it was a database no-op. The user was led to believe a fresh revocation occurred. The task document explicitly called this case out: _"Should error: Device not found because the WHERE clause excludes already-revoked"_.

**Root cause**: `auth.Service.RevokeDevice` only short-circuited when `GetDeviceByID` returned `sql.ErrNoRows`. The query `UPDATE devices SET revoked_at = ... WHERE id = ? AND revoked_at IS NULL` is correctly idempotent at the SQL layer, but the Go method did not surface the no-op to the caller.

**Fix**: Updated `internal/auth/auth.go::RevokeDevice` to also return `ErrDeviceNotFound` when `GetDeviceByID` returns a row whose `RevokedAt` is `Valid` (already non-NULL). The view layer already maps `ErrDeviceNotFound` to `error=Device+not+found`, so no view changes were needed.

Updated the existing `TestRevokeDevice_Idempotent` test (renamed to `TestRevokeDevice_AlreadyRevokedReturnsNotFound`) to assert the new contract: `revoked_at` does not move on the second call AND the second call returns `ErrDeviceNotFound`. The previous test rationale (_"the spec does not require a 'double-revoke' error"_) was wrong — the TASK-014 spec explicitly does require it.

**Severity**: medium (no security impact; misleading UX that could mask a real bug if the admin assumes the revoke went through twice and then audits the wrong row).

**Reproduction**: `go test ./views/... -run TestDeviceMgmt_RevokedDeviceCannotBeRevokedAgain` (passes after the fix).

### F2 -- SQLite `:memory:` test helper could not host concurrent writers (low / infrastructure)

**Symptom**: `TestDeviceMgmt_ConcurrentCreatesUniqueTokens` failed with `no such table: devices` from twenty parallel `CreateDevice` calls. The test was unable to verify the handler-local-only-state property because the test infrastructure itself faulted before any Go-level race could occur.

**Root cause**: `internal/db/testhelper.go::OpenTestDB` opens `:memory:` without bounding the connection pool. `database/sql` is free to open multiple connections, and each new `:memory:` connection is a fresh, empty SQLite database -- migrations applied on the migrate connection are invisible to subsequent ones. Single-threaded tests happened to never observe this because they only ever touched the first connection.

**Fix**: `db.SetMaxOpenConns(1)` in `OpenTestDB`. Test workloads are tiny; serializing through one connection is the right tradeoff for `:memory:`. All existing tests still pass; the new concurrency test now holds.

**Severity**: low (test infrastructure, not production code). Filed because every future concurrency test in this repo would have hit it.

### F3 -- Spot-checked items that held up

The implementation is otherwise tight. The following adversarial probes were tried and produced clean behaviour:

- **Token leakage in slog**: captured `slog.Default` with a JSON-buffer handler around a `handleDeviceCreate` call and verified the freshly-minted token is not present in any log line. (`TestDeviceMgmt_TokenNotInSlogOutput`)
- **Token leakage in response headers**: scanned every header key / value emitted by the create handler; the token only appears in the body. (`TestDeviceMgmt_TokenNotInResponseHeaders`)
- **Token leakage cross-request**: the create handler stores `rawToken` in a function-local var that is passed through `devicesPage(...).Render(ctx, w)`. There is no flash cookie, no URL parameter, no logged attribute. The follow-up list page never receives the value. (Re-verified by `TestHandleDeviceList_DoesNotLeakPreviousToken` and the existing `views/devices_test.go`.)
- **XSS in device name**: `<script>alert('xss')</script>` as a name renders as `&lt;script&gt;...` in the active-devices table. templ auto-escape is intact. (`TestDeviceMgmt_DeviceNameXSSEscaped`)
- **XSS in flash error**: an attacker-supplied `?error=<script>...</script>` is escaped on render. (`TestDeviceMgmt_FlashErrorXSSEscaped`)
- **Flash msg allowlist**: an attacker-supplied `?msg=anything-not-known` is dropped, not reflected. The handler only maps the known `revoked` key to a friendly string. (`TestDeviceMgmt_FlashMsgIsAllowlisted`)
- **Long device name**: 1MB name body is accepted by the handler without crash. (`TestDeviceMgmt_LongDeviceNameNoCrash`)
- **Unicode device names**: emoji, RTL Arabic, accented Latin, and CJK round-trip cleanly through form decode -> DB -> HTML. (`TestDeviceMgmt_UnicodeDeviceName`)
- **Revoke form action carries the correct device id**: with two devices in the table, both rows render with their own device id baked into the form action. No cross-row leakage. (`TestDeviceMgmt_RevokeFormActionUsesCorrectID`)
- **Empty path id on revoke**: `r.SetPathValue("id", "")` is intercepted by the handler's empty check and redirected with `error=Missing+device+ID`, never reaching the auth layer. (`TestDeviceMgmt_RevokeMissingPathID`)
- **Empty list state**: zero devices renders cleanly with the `Active Devices` heading and the Revoked Devices section hidden. (`TestDeviceMgmt_ListEmptyRendersCleanly`)
- **120-device fleet**: large-list render does not crash; all 120 revoke forms appear. (`TestDeviceMgmt_ListMany`)
- **Concurrent creates**: 20 parallel POST `/admin/devices` requests by the same admin produce 20 distinct tokens and 20 distinct rows, no overlap. (`TestDeviceMgmt_ConcurrentCreatesUniqueTokens`)
- **Full middleware chain coverage**: GET as member -> 403; GET unauthenticated HTML -> 302 to `/admin/login`; POST unauthenticated non-HTML -> 401 with `WWW-Authenticate: Bearer`; POST without `_csrf` -> 403; member POST create / revoke -> 403; admin POST through full chain -> 200 / 302. All catch the kind of regression that bit TASK-009.

## New Tests

`views/devices_adversarial_test.go` (24 test functions plus 4 sub-cases on the unicode table):

| Test | Catches |
|------|---------|
| `TestDeviceMgmt_MemberCannotAccessDevicesPage` | Missing `RequireRole(RoleAdmin)` wrap on `deviceMux` |
| `TestDeviceMgmt_MemberCannotPostCreate` | New POST handler accidentally outside the role wrap |
| `TestDeviceMgmt_MemberCannotPostRevoke` | Same, for the parametric revoke route |
| `TestDeviceMgmt_AdminCreateThroughChainSucceeds` | Auth+CSRF+Role chain ordering for POST create |
| `TestDeviceMgmt_CreateWithoutCSRF_Returns403` | CSRF not bypassed on POST create |
| `TestDeviceMgmt_RevokeWithoutCSRF_Returns403` | CSRF not bypassed on revoke |
| `TestDeviceMgmt_UnauthenticatedHTML_RedirectsToLogin` | RequireAuth HTML branch |
| `TestDeviceMgmt_UnauthenticatedAPI_Returns401` | RequireAuth non-HTML branch w/ `WWW-Authenticate` |
| `TestDeviceMgmt_TokenNotInSlogOutput` | Future regression that adds `"token"` attr to slog calls |
| `TestDeviceMgmt_TokenNotInResponseHeaders` | Token leaking via Set-Cookie / Location / custom header |
| `TestDeviceMgmt_DeviceNameXSSEscaped` | Switch to `templ.Raw` on the Name cell |
| `TestDeviceMgmt_FlashErrorXSSEscaped` | Same, for the error flash |
| `TestDeviceMgmt_FlashMsgIsAllowlisted` | Reflected `?msg=` becoming a stored-XSS surface if escaping ever broke |
| `TestDeviceMgmt_RevokedDeviceShowsInRevokedSection` | AC-12 UI half regression |
| `TestDeviceMgmt_RevokeFormActionUsesCorrectID` | Loop variable capture bug in the templ |
| `TestDeviceMgmt_RevokedDeviceCannotBeRevokedAgain` | F1 regression (the bug fixed in this review) |
| `TestDeviceMgmt_RevokeMissingPathID` | Empty id reaching the auth layer |
| `TestDeviceMgmt_LongDeviceNameNoCrash` | Buffer / panic on 1MB form value |
| `TestDeviceMgmt_UnicodeDeviceName` (4 subtests) | Handler / templ choke on non-ASCII |
| `TestDeviceMgmt_ConcurrentCreatesUniqueTokens` | Handler-local state shared across goroutines |
| `TestDeviceMgmt_ListEmptyRendersCleanly` | Templ nil-deref on empty slice |
| `TestDeviceMgmt_ListMany` | Templ pathological behaviour on large fleets |
| `TestDeviceMgmt_AdminListThroughChainSucceeds` | Full chain GET happy path |

Plus the auth-layer test was rewritten to match the new contract:

- `TestRevokeDevice_AlreadyRevokedReturnsNotFound` -- replaces the old `TestRevokeDevice_Idempotent`; now asserts that a second revoke returns `ErrDeviceNotFound` and does not move the existing `revoked_at` timestamp.

## Source Changes (fixes)

- `internal/auth/auth.go::RevokeDevice` -- detect already-revoked rows and return `ErrDeviceNotFound`, with an updated doc comment explaining why both cases collapse for the caller.
- `internal/db/testhelper.go::OpenTestDB` -- pin the connection pool to one connection so concurrent test goroutines all see the same `:memory:` schema.
- `internal/auth/auth_device_adversarial_test.go::TestRevokeDevice_AlreadyRevokedReturnsNotFound` -- rewrite of the prior idempotent test to match the new contract.

No view-layer code changes were necessary; the existing handler already routes `ErrDeviceNotFound` to the correct flash.

## Green-bar

```
$ gofmt -l .
(empty)
$ go vet ./...
(clean)
$ go build ./...
(clean)
$ go test ./...
ok  	github.com/jasoncorbett/screens/api          0.385s
ok  	github.com/jasoncorbett/screens/internal/auth        1.743s
ok  	github.com/jasoncorbett/screens/internal/config      0.854s
ok  	github.com/jasoncorbett/screens/internal/db          3.826s
ok  	github.com/jasoncorbett/screens/internal/middleware  1.208s
ok  	github.com/jasoncorbett/screens/views                1.474s
$ go test -race ./...
ok  	github.com/jasoncorbett/screens/api          1.306s
ok  	github.com/jasoncorbett/screens/internal/auth        3.372s
ok  	github.com/jasoncorbett/screens/internal/config      1.285s
ok  	github.com/jasoncorbett/screens/internal/db          6.000s
ok  	github.com/jasoncorbett/screens/internal/middleware  2.553s
ok  	github.com/jasoncorbett/screens/views                2.859s
```

All gates pass.

## Recommendation

**ACCEPT** -- one medium-severity bug found and fixed (double-revoke false success), one low-severity test-infrastructure paper-cut found and fixed, plus 24 new adversarial tests covering token leakage, full middleware chain enforcement, XSS, unicode, concurrency, and edge cases. The implementation otherwise holds up well under attack: token leakage is genuinely confined to the create response body, role / CSRF wiring is correct on every route, and templ auto-escape protects all reflected user input.
