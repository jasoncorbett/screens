---
task: TASK-012
title: "Device service methods, Device + Identity types, context helpers"
spec: SPEC-003
arch: ARCH-003
reviewer: tester
date: 2026-04-25
result: ACCEPT
---

# Review: TASK-012 -- Device service methods, Device + Identity types, context helpers

## Acceptance Criteria

| AC                              | Description                                                                                | Result | Evidence                                                                                          |
|---------------------------------|--------------------------------------------------------------------------------------------|--------|---------------------------------------------------------------------------------------------------|
| AC-3                            | `CreateDevice` rejects empty / whitespace-only names without inserting a row               | PASS   | `TestCreateDevice/rejects_empty_and_whitespace_names_without_inserting`                           |
| AC-4                            | Two consecutive `CreateDevice` calls return distinct raw tokens                            | PASS   | `TestCreateDevice/two_calls_return_distinct_raw_tokens`                                           |
| AC-5                            | `token_hash == HashToken(rawToken)`; raw token never persisted                             | PASS   | `TestCreateDevice/persists_hashed_token_and_returns_raw`                                          |
| AC-12                           | After `RevokeDevice`, `ValidateDeviceToken` for that token returns `ErrDeviceRevoked`      | PASS   | `TestValidateDeviceToken/revoked_device_returns_ErrDeviceRevoked_but_other_devices_still_validate`|
| AC-13                           | `RevokeDevice(idA)` does not affect `ValidateDeviceToken` for device B                     | PASS   | same subtest                                                                                      |
| AC-15                           | `MarkDeviceSeen` updates `last_seen_at` when previous is NULL or older than throttle       | PASS   | `TestMarkDeviceSeen/zero_throttle_updates_every_call`                                             |
| AC-16                           | Two calls within the throttle leave `last_seen_at` unchanged on the second call            | PASS   | `TestMarkDeviceSeen/large_throttle_leaves_second_call_as_no-op`                                   |
| Service prereq for AC-27/31/37  | Rotated token validates; old token does not; revoked device id returns `ErrDeviceNotFound` | PASS   | `TestRotateDeviceToken/*` (4 subtests)                                                            |

## Adversarial Findings

### 1. Concurrent service tests "no such table: devices" on `:memory:` SQLite (MEDIUM -- FIXED)

**Description**: The first run of every adversarial concurrency test (`TestConcurrent_CreateDevice_50Goroutines`, `TestConcurrent_RotateDeviceToken`, `TestConcurrent_MarkDeviceSeen`, `TestConcurrent_ValidateAndRevoke`) failed with `SQL logic error: no such table: devices (1)`. Cause: `db.OpenTestDB(t)` returns a `*sql.DB` whose pool can hold multiple connections, but `modernc.org/sqlite` gives each `:memory:` connection its own private database. The first connection ran the migrations; subsequent connections (taken by parallel goroutines) were blank. This footgun pattern already exists in other adversarial test files in the project (`internal/middleware/adversarial_test.go`, `internal/db/devices_adversarial_test.go`), each of which calls `sqlDB.SetMaxOpenConns(1)` to work around it. The TASK-012 device test helper did not set this cap, so any concurrent test against the device service would have failed.

**Severity**: Medium. Test-only; production code does not use `:memory:`. But without the fix, no concurrency tests of the device service can be added safely -- the next person who tries gets a confusing "no such table" error.

**Reproduction (before fix)**: `go test -run TestConcurrent_CreateDevice_50Goroutines ./internal/auth/` -- 50 goroutines all fail.

**Fix**: Added `sqlDB.SetMaxOpenConns(1)` in `newDeviceTestService` (the package's device test helper in `internal/auth/auth_device_test.go`) with a comment explaining why. All four concurrency tests now pass under `go test -race`.

### 2. `MarkDeviceSeen` with negative `DeviceLastSeenInterval` clamps correctly (NOT VULNERABLE -- regression-tested)

**Description**: A misconfiguration that produced a negative duration could either pass through as a positive seconds value (`-(-3600) = +3600`, making the throttle window extend INTO THE FUTURE and silently disable updates), or raise a SQLite error (`datetime('now', '+3600 seconds') > now`, so the row would never qualify). I checked: the code clamps `seconds < 0` to 0 before formatting the SQL string. Touched a device with `DeviceLastSeenInterval = -time.Hour` and confirmed `last_seen_at` gets updated as expected.

**Severity**: N/A -- correct.

**Reproduction**: `TestMarkDeviceSeen_NegativeIntervalClampsToZero`.

### 3. `MarkDeviceSeen` on unknown device id is a silent no-op (NOT VULNERABLE -- regression-tested)

**Description**: The "best-effort" contract requires that an unknown device id (e.g., one that was just revoked + cleaned up by a sweeper that doesn't yet exist) does NOT raise an error. I verified the call returns nil and writes nothing.

**Severity**: N/A -- correct (contract per spec NFR).

**Reproduction**: `TestMarkDeviceSeen_UnknownDevice`.

### 4. `RevokeDevice` is idempotent under repeated calls (NOT VULNERABLE -- regression-tested)

**Description**: Spec preserves audit data via the `WHERE revoked_at IS NULL` clause on the UPDATE. A second `RevokeDevice` call must NOT advance the audit timestamp. I confirmed by revoking, sleeping >1s (sqlite clock granularity), and revoking again -- `revoked_at` was unchanged. The spec doesn't require returning a "double revoke" error and the implementation correctly returns nil.

**Severity**: N/A -- correct.

**Reproduction**: `TestRevokeDevice_Idempotent`.

### 5. Concurrent `RotateDeviceToken` exhibits a documented "lost update" race (NOT VULNERABLE -- regression-tested)

**Description**: The architecture's `RotateDeviceToken` uses `UPDATE devices SET token_hash = ? WHERE id = ? AND revoked_at IS NULL`. With 20 concurrent rotations on the same device, all return non-empty tokens with no error, but the database can only hold one `token_hash`. After the dust settles, only ONE of the 20 returned tokens still validates -- the rest are stale. This is the documented behaviour and the spec does not promise serialisation; the architecture's only invariant is "the latest writer wins, and `RowsAffected==0` means revoked-or-missing." A future caller that uses rotation under contention (the browser-enrollment flow does not -- it always rotates inside an admin-driven HTTP request) needs to know this. Test asserts: no error from any goroutine, every returned token is 64 chars, and exactly 1 of the returned tokens validates.

**Severity**: N/A -- documented behaviour. Worth flagging in the review but no fix is appropriate at the service layer.

**Reproduction**: `TestConcurrent_RotateDeviceToken`.

### 6. Concurrent `MarkDeviceSeen` is race-clean (NOT VULNERABLE -- regression-tested)

**Description**: 50 simultaneous touches on the same device complete with no errors and produce a single non-NULL `last_seen_at`. The throttle is enforced inside the SQL `WHERE` clause so concurrent writers see consistent results. `go test -race` clean.

**Severity**: N/A -- correct.

**Reproduction**: `TestConcurrent_MarkDeviceSeen`.

### 7. Concurrent validate-vs-revoke race produces only allowed outcomes (NOT VULNERABLE -- regression-tested)

**Description**: 20 concurrent `ValidateDeviceToken` calls racing against one `RevokeDevice` -- each validate either returns the device, returns `ErrDeviceRevoked`, or (theoretically, if the device row were deleted) `ErrDeviceNotFound`. The implementation only ever returns those three results; no panics, no wrapped/internal errors. Final state: device is revoked.

**Severity**: N/A -- correct.

**Reproduction**: `TestConcurrent_ValidateAndRevoke`.

### 8. SQL injection in token validation (NOT VULNERABLE -- regression-tested)

**Description**: `ValidateDeviceToken` is the most attacker-controlled surface here -- the raw token comes straight from a header or cookie. I tried `' OR 1=1 --`, `'; DROP TABLE devices; --`, `"; DELETE FROM devices; --`, and `\x00admin`. All map cleanly to `ErrDeviceNotFound` (the malicious string is hashed by SHA-256 before reaching SQL, then the lookup is via parameterised query). Verified the `devices` table is intact after the attempts.

**Severity**: N/A -- the hash-then-lookup design is structurally safe.

**Reproduction**: `TestValidateDeviceToken_SQLMetacharactersDoNotInject`.

### 9. Error messages do not leak the raw token or its hash (NOT VULNERABLE -- regression-tested)

**Description**: `ValidateDeviceToken` wraps unexpected errors with `fmt.Errorf("lookup device: %w", err)`. I verified that the wrapped error string never contains the raw token nor the SHA-256 hash. `RotateDeviceToken` and `CreateDevice` likewise return only sanitised messages on failure paths. The raw token returned from `CreateDevice`/`RotateDeviceToken` is only the success-path return value -- never written back into an error.

**Severity**: N/A -- correct.

**Reproduction**: `TestErrorMessages_DoNotLeakRawTokenOrHash`.

### 10. `deviceFromRow` rejects malformed timestamps (NOT VULNERABLE -- regression-tested)

**Description**: A corrupted database row with a malformed `created_at`, `last_seen_at`, or `revoked_at` should NOT silently produce a `Device` with a zero `time.Time` (which would render as "0001-01-01" in the UI and could confuse audit trails). I verified the parser returns an error in all three cases.

**Severity**: N/A -- correct.

**Reproduction**: `TestDeviceFromRow_MalformedCreatedAt`, `TestDeviceFromRow_MalformedLastSeenAt`, `TestDeviceFromRow_MalformedRevokedAt`.

### 11. `CreatedAt` is in UTC, not server-local time (NOT VULNERABLE -- regression-tested)

**Description**: `time.Parse("2006-01-02 15:04:05", ...)` defaults to UTC when the format has no zone, which is what we want. Verified the returned `CreatedAt.Location() == time.UTC`. A future maintainer who switches to `time.ParseInLocation` with `time.Local` would silently drift timestamps in the management UI.

**Severity**: N/A -- correct.

**Reproduction**: `TestCreateDevice_TimestampsAreUTC`.

### 12. `CreateDevice` rejects bad `createdBy` via FK (NOT VULNERABLE -- regression-tested)

**Description**: A non-existent or empty `createdBy` would produce a row "owned" by no real user. The `ON DELETE RESTRICT` FK on `devices.created_by` -> `users.id` enforces this at insert time. Confirmed: both unknown-id and empty-string return an error and no row is inserted.

**Severity**: N/A -- correct (DB constraint).

**Reproduction**: `TestCreateDevice_RejectsNonExistentCreator`, `TestCreateDevice_RejectsEmptyCreator`.

### 13. `ListDevices` returns empty slice (not nil) on an empty database (NOT VULNERABLE -- regression-tested)

**Description**: `ListDevices` preallocates `make([]Device, 0, len(rows))`, which yields `[]Device{}` even when the underlying sqlc result is nil. Callers can range without a nil check. Also tested the failure path: a closed DB produces a wrapped error instead of swallowing the failure.

**Severity**: N/A -- correct.

**Reproduction**: `TestListDevices_EmptyDatabaseReturnsEmptySliceNotNil`, `TestListDevices_OnClosedDBReturnsError`.

### 14. `MarkDeviceSeen` surfaces real DB errors (NOT VULNERABLE -- regression-tested)

**Description**: "Best-effort" semantics for the throttled write must NOT mask a real underlying SQL failure (e.g., DB closed, disk full). I closed the DB and called `MarkDeviceSeen`; the call returns an error wrapped with `"touch device seen"` context.

**Severity**: N/A -- correct.

**Reproduction**: `TestMarkDeviceSeen_OnClosedDB`.

### 15. Long names, unicode, and null-byte names round-trip cleanly (NOT VULNERABLE -- regression-tested)

**Description**: A 1 MiB device name, a name with emoji + RTL override sequences, and a name with embedded NUL bytes all persist verbatim. SQLite TEXT does not truncate or normalise. (The middleware/UI layer should sanitise on render, but that's a TASK-014 concern; persistence is correct.)

**Severity**: N/A -- correct.

**Reproduction**: `TestCreateDevice_LongName`, `TestCreateDevice_UnicodeName`, `TestCreateDevice_NameWithNullBytes`.

### 16. `Identity` with out-of-range `Kind` is safe (NOT VULNERABLE -- regression-tested)

**Description**: `Identity{Kind: 99}` does not panic; both `IsAdmin()` and `IsDevice()` correctly report false; `ID()` returns `""`. Future maintainers who add a new `IdentityKind` constant won't accidentally cause a callsite to mis-identify a caller.

**Severity**: N/A -- correct.

**Reproduction**: `TestIdentity_OutOfRangeKindIsSafe`.

### 17. `DeviceFromContext` / `IdentityFromContext` are isolated from foreign keys (NOT VULNERABLE -- regression-tested)

**Description**: The unexported `deviceKey struct{}` and `identityKey struct{}` types prevent any other package's context value from ever colliding with ours -- even another `struct{}` type defined elsewhere has a distinct identity. Verified by stuffing a `*Device` under a fake key into the context and confirming our accessors return nil.

**Severity**: N/A -- correct.

**Reproduction**: `TestContext_KeyTypeIsolation`.

### 18. `MarkDeviceSeen` with sub-second positive `DeviceLastSeenInterval` (LOW -- noted, no fix)

**Description**: The implementation truncates `DeviceLastSeenInterval` to whole seconds via `.Truncate(time.Second)`. A configured value of `500ms` therefore becomes `0` and is formatted as `-0 seconds`. Per the docstring and existing tests, this means "throttle effectively disabled" -- but the SQL `last_seen_at < datetime('now', '-0 seconds')` is FALSE when both timestamps round to the same second, so a 500ms-resolution caller would see the SECOND call within the same second be a no-op. The spec only contemplates whole-second intervals (`DEVICE_LAST_SEEN_INTERVAL` defaults to `1m`), so this is not actually a problem. Documenting in case someone later sets a sub-second interval and is surprised.

**Severity**: Low. Spec-allowable values (whole minutes/seconds) work correctly; sub-second values would be surprising but are not exercised.

**Reproduction**: N/A; covered indirectly by `TestTouchDeviceSeen_ZeroIntervalAlwaysUpdates` in `internal/db/devices_adversarial_test.go`, which already documents the gotcha.

## New Tests Added

In `internal/auth/auth_device_adversarial_test.go` (new file):

- `TestCreateDevice_RejectsNonExistentCreator` -- FK rejects unknown user id, no row inserted.
- `TestCreateDevice_RejectsEmptyCreator` -- empty createdBy is not a valid user id.
- `TestCreateDevice_LongName` -- 1 MiB name persisted intact.
- `TestCreateDevice_UnicodeName` -- emoji + RTL override sequences persisted intact.
- `TestCreateDevice_NameWithNullBytes` -- NUL bytes preserved.
- `TestCreateDevice_TimestampsAreUTC` -- `CreatedAt.Location() == time.UTC`.
- `TestValidateDeviceToken_EmptyToken` -- `ErrDeviceNotFound`, no panic.
- `TestValidateDeviceToken_LongToken` -- 1 MiB raw token, `ErrDeviceNotFound`.
- `TestValidateDeviceToken_SQLMetacharactersDoNotInject` -- four malicious payloads, table intact afterwards.
- `TestErrorMessages_DoNotLeakRawTokenOrHash` -- error strings never include raw or hashed token.
- `TestRevokeDevice_Idempotent` -- second revoke does not move `revoked_at`.
- `TestMarkDeviceSeen_UnknownDevice` -- best-effort no-op on unknown id.
- `TestMarkDeviceSeen_NegativeIntervalClampsToZero` -- negative duration does not produce a future-dated throttle.
- `TestMarkDeviceSeen_OnClosedDB` -- DB error surfaces with wrapped context.
- `TestRotateDeviceToken_EmptyDeviceID` -- empty id treated as unknown.
- `TestRotateDeviceToken_AfterRotateThenRevoke` -- defence-in-depth: revoked devices cannot be rotated.
- `TestListDevices_EmptyDatabaseReturnsEmptySliceNotNil` -- safe ranging contract.
- `TestListDevices_OnClosedDBReturnsError` -- DB error surfaces.
- `TestDeviceFromRow_MalformedCreatedAt` -- parser rejects garbage `created_at`.
- `TestDeviceFromRow_MalformedLastSeenAt` -- parser rejects garbage `last_seen_at`.
- `TestDeviceFromRow_MalformedRevokedAt` -- parser rejects garbage `revoked_at`.
- `TestConcurrent_CreateDevice_50Goroutines` -- 50 parallel inserts, distinct ids and tokens, table count matches.
- `TestConcurrent_RotateDeviceToken` -- 20 parallel rotations: all succeed; exactly one return value validates afterwards.
- `TestConcurrent_MarkDeviceSeen` -- 50 parallel touches, no errors, `last_seen_at` set.
- `TestConcurrent_ValidateAndRevoke` -- 20 validators race against 1 revoker: only allowed result types returned.
- `TestIdentity_OutOfRangeKindIsSafe` -- defensive-coding regression for future iota additions.
- `TestContext_KeyTypeIsolation` -- foreign struct{} keys cannot satisfy our context lookups.

## Code Changes

- `internal/auth/auth_device_test.go`: added `sqlDB.SetMaxOpenConns(1)` in the `newDeviceTestService` helper (with a comment explaining the `:memory:` connection-pool footgun) so concurrent service tests can run safely against the in-memory test DB.

No production code changes were required -- the implementation correctly delivers every method on the documented surface and survives every fuzz / race / injection probe in the adversarial suite.

## Green-Bar Results

- `gofmt -l .` -- empty (clean)
- `go vet ./...` -- clean
- `go build ./...` -- clean
- `go test ./...` -- PASS (all packages)
- `go test -race ./...` -- PASS (all packages)

## Recommendation

**ACCEPT.** Every acceptance criterion in the task scope passes. Twenty-seven adversarial probes against fuzzed inputs, concurrency, SQL injection, error-message leakage, FK enforcement, malformed timestamps, and identity-type edge cases all hold. The one defect found was test-infrastructure (the test helper did not pin `MaxOpenConns(1)` for `:memory:` SQLite); it was fixed in the test helper local to this package, and the four new concurrency tests now pass under `go test -race`. The lost-update race in `RotateDeviceToken` under concurrent callers is documented behaviour per the architecture and is consistent with the spec's "single admin in front of a kiosk" usage model.
