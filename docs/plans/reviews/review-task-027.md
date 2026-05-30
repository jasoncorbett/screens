---
task: TASK-027
spec: SPEC-007
status: pass
tested-by: tester
date: 2026-05-29
---

# Review: TASK-027 -- devices.screen_id schema + auth.AssignDeviceToScreen

## Acceptance Criteria Coverage

| AC    | Description                                                                                  | Status | Notes                                                                                              |
|-------|----------------------------------------------------------------------------------------------|--------|----------------------------------------------------------------------------------------------------|
| AC-1  | devices table has nullable screen_id; existing rows have NULL                                | PASS   | Pinned by `TestDevices_ScreenIDColumnExistsAfterMigration`; index pinned by sibling test           |
| AC-6  | DELETE of a Screen referenced by 2 devices sets both screen_ids to NULL                      | PASS   | Pinned by `TestDevices_ScreenDeleteSetsNullForMultipleDevices` + new tx-rollback test              |
| AC-T1 | `AssignDeviceToScreen(ctx, "unknown", "anyValidScreen")` returns ErrDeviceNotFound           | PASS   | Pinned by `TestAssignDeviceToScreen/unknown device id` and new adversarial subtest                 |
| AC-T2 | `AssignDeviceToScreen(ctx, valid, "")` sets screen_id NULL; ListDevices reflects ScreenID=nil | PASS   | Pinned by `TestAssignDeviceToScreen/empty screen id clears`                                        |
| AC-T3 | `AssignDeviceToScreen(ctx, valid, validScreenID)` -> ScreenID set; round-trips via ListDevices and ValidateDeviceToken | PASS   | Pinned by `TestAssignDeviceToScreen/assigns screen id` + new `TestValidateDeviceToken_ReturnsScreenIDAfterAssignment` (closes a gap for the upcoming render handler) |
| AC-T4 | Migration 010 runs cleanly against a DB with existing device rows; screen_id IS NULL afterward | PASS   | Implied by all tests that use `OpenTestDB(t)`; the migration is additive and the column defaults to NULL |

## Adversarial Findings

### 1. `AssignDeviceToScreen` with nonexistent screen ID surfaces a wrapped SQLite FK error to the caller -- **Severity: low (documented design)**

- **Repro**: `svc.AssignDeviceToScreen(ctx, validDeviceID, "no-such-screen")` returns `assign device screen: constraint failed: FOREIGN KEY constraint failed (787)`, not `ErrDeviceNotFound` (since the device exists) and not `ErrScreenNotFound` (because the auth package cannot import screens).
- **Analysis**: The task explicitly documents this: the caller (TASK-029 admin handler) MUST pre-validate the screen exists; the FK is documented as a "secondary defence" only. ADR-009 also discusses this. The error message is a constant 76 bytes -- it does NOT include the rejected screen ID, so there is no log-leak surface. A new test pins both "non-nil error" and "no rejected value in err.Error()" so a future change that started leaking the user-supplied value would fail.
- **No fix needed** -- this is by design. New tests guard against the secondary-defence regression and the leak risk.

### 2. The "non-existent screen" error path leaves the device row unchanged -- **Verified, not a bug**

- **Verified by**: `TestAssignDeviceToScreen_AdversarialInputs/nonexistent_screen_ID_returns_a_non-nil_error_(FK_secondary_defence)`.
- The FK fires *before* the UPDATE commits; the device row's `screen_id` stays NULL.

### 3. `ValidateDeviceToken` -> `ScreenID` round-trip was not pinned by any test -- **Severity: medium (gap; fixed)**

- **Pre-existing gap**: `GetDeviceByTokenHash` is the hot path for the upcoming render handler (TASK-030). The dev added `screen_id` to the SELECT list and to the `db.Device` struct, but no test exercised the path `bearer token -> Device.ScreenID`. A future refactor that dropped `screen_id` from that one SELECT would silently make every device appear unassigned, and only an end-to-end render test would catch it.
- **Fix**: added `TestValidateDeviceToken_ReturnsScreenIDAfterAssignment` which exercises pre-assign nil, post-assign set, post-clear nil -- all through the bearer-token path.
- The test now passes against the current implementation; the regression surface is closed.

### 4. Defensive mapper behaviour for `sql.NullString{Valid:false, String:"non-empty"}` -- **Severity: low (defensive); now pinned**

- **Verified**: The mapper at `internal/auth/device.go:58` checks `row.ScreenID.Valid` before reading `String`, so a defensive-but-impossible row state (NULL flag false, String "ghost") correctly maps to `ScreenID == nil`. A future refactor that switched to `dev.ScreenID = &row.ScreenID.String` would break this; the new table-driven test pins all three cases.
- **Fix**: added `TestDeviceFromRow_ScreenIDMapping` (table-driven: 3 cases).

### 5. Idempotent re-assign and clear-while-already-NULL are not pinned -- **Severity: low; now pinned**

- The task docstring says "a no-op re-assign is not an error" but no test verifies the *clear while already NULL* path or *set while already that value*. The current implementation uses an unconditional `UPDATE devices SET screen_id = ? WHERE id = ?` so RowsAffected stays 1 even on no-op -- which is what makes the no-op return nil instead of `ErrDeviceNotFound`.
- **Fix**: added `TestAssignDeviceToScreen_AdversarialInputs/idempotent` covering both directions.

### 6. SQL metacharacters and empty-id inputs -- **Verified, not a bug; now pinned**

- Long device/screen IDs (1MB), SQL metacharacters in IDs, null bytes -- all handled correctly via parameterized queries. The pre-existing `devices_adversarial_test.go` tests cover similar territory for other queries; the new `TestAssignDeviceToScreen_AdversarialInputs/sql_metacharacters_in_deviceID_do_not_corrupt_the_UPDATE` extends the coverage to the new query and verifies that a metachar device id matched no rows (ErrDeviceNotFound) and that the unrelated device's assignment was untouched.
- An empty device ID returns `ErrDeviceNotFound` (since no real device has id="") -- but if a future query change introduced `WHERE id LIKE ?` or similar, an empty pattern could match every row. The new test pins that the empty-id path does NOT mutate any other device.

### 7. Concurrency: 32 goroutines all hitting `AssignDeviceToScreen` on the same device -- **Verified; now pinned**

- SQLite serialises the UPDATEs cleanly (the test pool is pinned to MaxOpenConns=1, mirroring the production OpenTestDB pattern). No race detected, no errors returned, final state is one of the two valid screens.
- **Fix**: added `TestAssignDeviceToScreen_Concurrent`. Runs clean under `-race`.

### 8. FK `SET NULL` transactionality -- **Verified; now pinned**

- An open question I probed: when the parent DELETE is rolled back, does the cascaded SET NULL also roll back? SQLite's contract is yes (FK actions are part of the same transaction). Pinned by `TestDevices_ScreenDeleteSetNullRollsBackWithParentTx` which BEGINs a tx, deletes a screen, verifies the device's screen_id went NULL inside the tx, then rolls back and verifies both the screen and the device's assignment are restored.

### 9. Down migration against a populated table -- **Verified; now pinned**

- `ALTER TABLE devices DROP COLUMN screen_id` against a table with both assigned and unassigned device rows: works cleanly, rows survive, column and index both gone. Pinned by `TestDevices_ScreenIDDownMigration_PreservesRows`.

### 10. No secret / token / hash leakage in new code -- **Verified**

- No `slog`, `log`, `fmt.Print*` calls in either `internal/auth/auth.go::AssignDeviceToScreen` or `internal/auth/device.go::deviceFromRow`. The wrapped FK error message is constant-length and excludes the rejected value. No regression risk added by this task.

## New Tests Added

In `internal/auth/auth_device_test.go`:

- `TestAssignDeviceToScreen_AdversarialInputs` (5 subtests):
  - `empty_deviceID_returns_ErrDeviceNotFound`
  - `nonexistent_screen_ID_returns_a_non-nil_error_(FK_secondary_defence)`
  - `nonexistent_screen_ID_error_does_not_leak_the_screen_id_verbatim`
  - `idempotent`
  - `sql_metacharacters_in_deviceID_do_not_corrupt_the_UPDATE`
- `TestValidateDeviceToken_ReturnsScreenIDAfterAssignment` -- closes the bearer-token-path gap.
- `TestAssignDeviceToScreen_Concurrent` -- 32 goroutine race test.

In `internal/auth/auth_device_adversarial_test.go`:

- `TestDeviceFromRow_ScreenIDMapping` -- table-driven, 3 cases covering the defensive mapper behaviour.

In `internal/db/devices_screen_id_schema_test.go`:

- `TestDevices_ScreenDeleteSetNullRollsBackWithParentTx` -- pins that FK SET NULL is transactional.
- `TestDevices_ScreenIDDownMigration_PreservesRows` -- pins down migration against populated table.

## Test Results

```
go test ./...
ok      github.com/jasoncorbett/screens/api
ok      github.com/jasoncorbett/screens/internal/auth
ok      github.com/jasoncorbett/screens/internal/config
ok      github.com/jasoncorbett/screens/internal/db
ok      github.com/jasoncorbett/screens/internal/middleware
ok      github.com/jasoncorbett/screens/internal/screens
ok      github.com/jasoncorbett/screens/internal/themes
ok      github.com/jasoncorbett/screens/internal/widget
ok      github.com/jasoncorbett/screens/internal/widget/text
ok      github.com/jasoncorbett/screens/views

go test -race ./internal/db/... ./internal/auth/...
ok      github.com/jasoncorbett/screens/internal/db   8.383s
ok      github.com/jasoncorbett/screens/internal/auth 4.125s
```

## Green Bar

- gofmt -l .  : PASS (empty output)
- go vet ./... : PASS
- go build ./... : PASS
- go test ./... : PASS
- go test -race ./... : PASS

## Recommendation

ACCEPT

All acceptance criteria pass. The implementation is small, surgical, and follows the patterns of the surrounding device-service methods. The FK schema correctly fires SET NULL on screen delete, transactionally. The mapper handles the `sql.NullString` -> `*string` translation defensively. The new service method is idempotent, parameter-safe against SQL injection, and concurrent-safe. No raw tokens or hashes are logged.

Three test gaps were filled by this review:

1. `ValidateDeviceToken` -> `ScreenID` round-trip (closes a real risk for the upcoming TASK-030 render handler).
2. Defensive mapper behaviour (table-driven, 3 cases).
3. FK SET NULL transactional rollback (pins SQLite's expected behaviour).

No follow-up tasks needed.

## Follow-Up Tasks Needed

None.
