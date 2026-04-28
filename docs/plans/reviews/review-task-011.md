---
task: TASK-011
title: "Device config, migration, and sqlc queries"
spec: SPEC-003
arch: ARCH-003
reviewer: tester
date: 2026-04-25
result: ACCEPT
---

# Review: TASK-011 -- Device config, migration, and sqlc queries

## Acceptance Criteria

| AC    | Description                                                                                  | Result | Evidence                                                                                                |
|-------|----------------------------------------------------------------------------------------------|--------|---------------------------------------------------------------------------------------------------------|
| AC-39 | `DEVICE_COOKIE_NAME` defaults to `screens_device` when unset                                 | PASS   | `TestLoadDeviceDefaults`                                                                                |
| AC-40 | `DEVICE_LAST_SEEN_INTERVAL=5m` -> field equals `5*time.Minute` (config half only)            | PASS   | `TestLoadDeviceCustomEnvVars`                                                                           |
| AC-41 | `DEVICE_LANDING_URL` defaults to `/device/`; non-`/`-prefixed and empty values fail validation | PASS | `TestLoadDeviceDefaults`, `TestLoadDeviceLandingURLBadPrefixFailsValidation`, `TestValidateDeviceFields` |
| Schema | `devices` table + `idx_devices_token_hash` + `idx_devices_revoked_at` exist after migration   | PASS   | `TestDevicesTable_ExistsAfterMigration`                                                                 |
| Schema | `token_hash UNIQUE` (collision -> hard error)                                                 | PASS   | `TestDevicesTable_TokenHashUniqueness`, `TestCreateDevice_ParallelSameHashConflicts`                    |
| Schema | `created_by ON DELETE RESTRICT` (deleting owning admin fails)                                 | PASS   | `TestDevicesTable_ForeignKeyRestrictsUserDelete`                                                        |

## Adversarial Findings

### 1. Whitespace-only / invalid `DEVICE_COOKIE_NAME` silently disables device-cookie auth (MEDIUM -- FIXED)

**Description**: `Validate()` only rejected the empty string. Any value that fails RFC 6265 token rules (whitespace, separator chars like `;`, `=`, `,`, `"`, `/`, `\`, `<`, `>`, control chars, non-ASCII) passed validation but caused `http.SetCookie` to silently emit no `Set-Cookie` header at all. With no header on the response, the browser would never store a device cookie, and subsequent device-cookie auth would always fail without any operator-visible signal -- the system would just look like the device isn't authenticating.

**Severity**: Medium. Not externally exploitable (env var is admin-controlled), but a typo in the cookie name would silently break the entire cookie-auth path. The bearer-header path would still work, so the failure mode is "wall-display kiosks broken in mysterious ways."

**Reproduction**: `TestValidateDeviceCookieNameRejectsInvalidChars` (asserts validation now catches each invalid form), `TestValidateDeviceCookieNameProducesSetCookieHeader` (round-trip regression: every accepted name MUST produce a non-empty Set-Cookie header).

**Fix**: Added `isValidCookieName` helper in `internal/config/config.go` enforcing RFC 6265/7230 token rules. `Validate()` now returns `"DEVICE_COOKIE_NAME contains characters not permitted in a cookie name"` for invalid values. The set of accepted characters matches what `net/http.SetCookie` will actually serialize, so the validation contract is "if it passes, the runtime will use it."

### 2. `RotateDeviceToken` row-affected semantics under revocation (NOT VULNERABLE -- regression-tested)

**Description**: The architecture relies on `RowsAffected == 0` from `RotateDeviceToken` to mean "device missing or revoked"; the `WHERE id = ? AND revoked_at IS NULL` clause is the database-side guarantee that a revoked device's hash cannot be silently rotated by a service-layer bug. I tried to break this by:
- Rotating a freshly revoked device (`TestRotateDeviceToken_ZeroRowsForRevoked`): correctly returned 0 rows AND the original `token_hash` was preserved.
- Rotating an unknown device id (`TestRotateDeviceToken_ZeroRowsForUnknownID`): correctly returned 0 rows with no error.
- Rotating an active device (`TestRotateDeviceToken_OneRowForActive`): returned exactly 1 row and the new hash was visible via `GetDeviceByID`.

**Severity**: N/A -- the implementation correctly enforces the contract.

### 3. `RevokeDevice` idempotency under repeated calls (NOT VULNERABLE -- regression-tested)

**Description**: The query `UPDATE devices SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL` should be idempotent: a second call must NOT advance the audit timestamp. I tested by revoking, sleeping >1s (sqlite's clock granularity), and revoking again. The `revoked_at` was unchanged.

**Severity**: N/A -- correct.

**Reproduction**: `TestRevokeDevice_IsIdempotent`.

### 4. `GetDeviceByTokenHash` returns revoked rows (NOT VULNERABLE -- regression-tested)

**Description**: The architecture deliberately keeps the revoked-at check in Go (not in SQL) so the middleware can return distinct `ErrDeviceRevoked` and `ErrDeviceNotFound`. If `GetDeviceByTokenHash` started filtering revoked rows, the middleware would conflate the two and the audit log would lose the "revoked device tried to authenticate" signal. Regression-tested by revoking a device then looking it up by hash; the row still comes back with `revoked_at` set.

**Severity**: N/A -- correct.

**Reproduction**: `TestGetDeviceByTokenHash_ReturnsRevokedRow`.

### 5. `TouchDeviceSeen` throttle behavior (NOT VULNERABLE -- regression-tested)

**Description**: This query is on the hot path for every device request. The NFR is that a device polling every 5 seconds must not generate one UPDATE per request. I verified:
- First touch (last_seen_at IS NULL): 1 row affected (`TestTouchDeviceSeen_ThrottlesWithinInterval`).
- Second touch with `-60 seconds` offset, immediately: 0 rows.
- After waiting >throttle: 1 row (`TestTouchDeviceSeen_UpdatesAfterIntervalElapsed`).
- Touching an unknown id is a 0-row no-op, no error (`TestTouchDeviceSeen_NoOpForUnknownID`).

**Side-finding (LOW)**: The `:execresult` param `Datetime interface{}` is awkward. Passing `"0 seconds"` (which is what a service layer might naively use to represent "always update" when the operator sets `DEVICE_LAST_SEEN_INTERVAL=0`) does NOT mean "always update": the comparison is strict less-than, so `last_seen_at < datetime('now', '0 seconds')` is false when the row's timestamp equals the current second. Documented in `TestTouchDeviceSeen_ZeroIntervalAlwaysUpdates` so TASK-012's service layer is on notice: "0 = update on every auth" requires bypassing the query, not passing `'0 seconds'`. (No code change here -- the spec says "zero means every auth" and TASK-012 implements the throttle, so the service layer is the right place to handle this.)

### 6. `DEVICE_LANDING_URL` accepts protocol-relative paths like `//evil.com/` (LOW -- noted, no fix)

**Description**: The validator's contract per the spec is "must start with /". A protocol-relative URL `//evil.com/` literally starts with `/` so passes. Browsers would interpret a redirect to that target as `https://evil.com/`. Since the env var is admin-controlled and its only consumer is the post-enrollment redirect (also admin-driven), this is not externally exploitable. Importantly, `net/http.ServeMux` would panic at startup when route registration tries `mux.Handle("GET //evil.com/", ...)` (verified manually: `parsing "GET //evil.com/": at offset 4: non-CONNECT pattern with unclean path can never match`), so the misconfiguration fails fast in TASK-015.

**Severity**: Low. The spec's literal "starts with /" is met; a stricter validator (no `//` prefix, no `..`, etc.) would be defense-in-depth. Deferred to a future hardening pass.

### 7. NULL `token_hash` rejected by schema (NOT VULNERABLE -- regression-tested)

**Description**: A service-layer bug that forgot to hash a token and inserted NULL would be catastrophic -- any future request whose hash sorted to the same column index would match. The `NOT NULL` constraint is the safety net. Verified by raw-SQL insert (sqlc cannot drive a NULL through `string`).

**Severity**: N/A -- correct.

**Reproduction**: `TestDevicesTable_NullTokenHashRejected`, `TestDevicesTable_NullNameRejected`.

### 8. Concurrent `CreateDevice` is race-clean and the UNIQUE constraint holds under contention (NOT VULNERABLE -- regression-tested)

**Description**: 50 goroutines inserting distinct token hashes in parallel: all succeed, count is 50 (`TestCreateDevice_ParallelDistinctTokens`). 20 goroutines racing to insert the SAME token hash: exactly one wins, all others get the UNIQUE-violation error (`TestCreateDevice_ParallelSameHashConflicts`). `go test -race` clean.

The parallel tests pin `MaxOpenConns(1)` because `:memory:` SQLite databases are per-connection; without that, a second pool connection would see "no such table". This is a quirk of the test helper, not of the production code (which already runs with `MaxOpenConns: 1` by default).

**Severity**: N/A -- correct.

### 9. Unrelated existing issue: `SESSION_COOKIE_NAME` has the same validation gap (LOW -- not in scope)

**Description**: While investigating the cookie-name fix, I noticed `SESSION_COOKIE_NAME` (added in an earlier task) does not validate cookie-name characters either. A misconfigured `SESSION_COOKIE_NAME=" "` would silently break the admin session cookie. This is identical to the `DEVICE_COOKIE_NAME` bug I fixed but lives in a different feature's task scope.

**Severity**: Low. Recommend a small follow-up: extend the new `isValidCookieName` check to `c.Auth.CookieName` as well. Out of scope for this task.

## New Tests Added

In `internal/config/config_adversarial_test.go`:
- `TestValidateDeviceCookieNameRejectsInvalidChars` -- 20 sub-tests covering whitespace, separators, control chars, non-ASCII, and valid cases.
- `TestValidateDeviceCookieNameProducesSetCookieHeader` -- regression invariant: every accepted name produces a non-empty `Set-Cookie` header.

In `internal/db/devices_adversarial_test.go` (new file):
- `TestDevicesTable_NullTokenHashRejected` -- NOT NULL on token_hash.
- `TestDevicesTable_NullNameRejected` -- NOT NULL on name.
- `TestDevicesTable_CreatedAtDefaultPopulated` -- the schema DEFAULT populates `created_at` so the service layer doesn't have to.
- `TestGetDeviceByTokenHash_ReturnsRevokedRow` -- query MUST return revoked rows so middleware can distinguish revoked vs unknown.
- `TestGetDeviceByTokenHash_UnknownReturnsErrNoRows` -- unknown hash yields `sql.ErrNoRows` (the canonical "not found" signal).
- `TestRevokeDevice_IsIdempotent` -- second revoke does not rewrite `revoked_at`.
- `TestRotateDeviceToken_ZeroRowsForRevoked` -- revoked device's hash cannot be silently rotated.
- `TestRotateDeviceToken_OneRowForActive` -- active device rotates and reports 1 row.
- `TestRotateDeviceToken_ZeroRowsForUnknownID` -- unknown id returns 0 rows, no error.
- `TestTouchDeviceSeen_ThrottlesWithinInterval` -- write amplification guard.
- `TestTouchDeviceSeen_UpdatesAfterIntervalElapsed` -- throttle expires correctly.
- `TestTouchDeviceSeen_ZeroIntervalAlwaysUpdates` -- documents the `'0 seconds'` semantic gotcha for TASK-012.
- `TestTouchDeviceSeen_NoOpForUnknownID` -- safe on unknown id.
- `TestListDevices_OrdersByCreatedAt` -- deterministic ordering with explicit timestamps.
- `TestListDevices_EmptyDatabase` -- nil-safe empty result.
- `TestListDevices_IncludesRevoked` -- ListDevices returns revoked rows (UI decides what to show).
- `TestCreateDevice_TokenHashMaxLength` -- 1MB hash round-trip (no implicit cap).
- `TestCreateDevice_UnicodeName` -- unicode name round-trip.
- `TestCreateDevice_DuplicateIDRejected` -- PRIMARY KEY violation.
- `TestCreateDevice_UnknownCreatedByRejected` -- FK violation surfaces at insert time.
- `TestCreateDevice_ParallelDistinctTokens` -- 50 goroutines, no shared state, race-clean.
- `TestCreateDevice_ParallelSameHashConflicts` -- exactly one wins under contention.

## Code Changes

- `internal/config/config.go`: added `isValidCookieName` helper; `Validate()` now rejects `DEVICE_COOKIE_NAME` values with cookie-name-illegal characters.

## Green-Bar Results

- `gofmt -l .` -- empty (clean)
- `go vet ./...` -- clean
- `go build ./...` -- clean
- `go test ./...` -- PASS (all packages)
- `go test -race ./...` -- PASS (all packages)

## Recommendation

**ACCEPT.** The implementation correctly delivers the schema, queries, config wiring, and validation called for in TASK-011. The one real bug found (silent cookie-auth disablement on invalid `DEVICE_COOKIE_NAME`) was fixed in-place with a focused validator and a regression test that asserts the round-trip invariant. All other adversarial probes (revoked-device rotation, idempotent revocation, throttled touch, FK enforcement, NULL constraints, concurrent inserts, ordering, unicode, max-length, malformed durations) found no defects and produced regression tests that future tasks can rely on. The two low-severity findings (protocol-relative landing URL; identical gap in `SESSION_COOKIE_NAME`) are noted but outside this task's scope.
