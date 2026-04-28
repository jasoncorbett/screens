---
id: TASK-012
title: "Device service methods, Device + Identity types, context helpers"
spec: SPEC-003
arch: ARCH-003
status: review
priority: p0
prerequisites: [TASK-011]
skills: [green-bar]
created: 2026-04-25
author: architect
---

# TASK-012: Device service methods, Device + Identity types, context helpers

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add device-domain code to `internal/auth`: a `Device` type, an `Identity` type that unifies admin and device callers, the context helpers handlers will use to read those values, and five new methods on `auth.Service` that handle the full device lifecycle (create, validate, mark-seen, revoke, list). This task produces no HTTP code; it gives TASK-013 (middleware) and TASK-014 (views) the service surface area they need.

## Context

- The `auth.Service` already exists in `internal/auth/auth.go` with admin-session and user-management methods. It holds `*sql.DB`, the sqlc-generated `*db.Queries`, and a `Config` struct. The new methods extend the same struct -- they share the database connection and queries.
- The token primitive `auth.GenerateToken` (32 random bytes -> 64-char hex) and `auth.HashToken` (SHA-256 -> 64-char hex) live in `internal/auth/session.go` and are reused.
- The id generator `generateID()` (32-char hex) at the bottom of `internal/auth/auth.go` is reused for the device id.
- Existing context helpers in `internal/auth/context.go` define `userKey` and `sessionKey` plus accessor functions. This task adds two more keys for `Device` and `Identity`. Existing helpers MUST NOT be removed -- TASK-013 keeps populating `ContextWithUser` for the admin path so that handlers from SPEC-002 keep working.
- sqlc generated `db.Device` (from TASK-011) uses `sql.NullString` for the nullable `LastSeenAt` and `RevokedAt` columns. The `deviceFromRow` translator converts those into `*time.Time`.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `internal/auth/auth.go` -- Service definition and existing methods
- `internal/auth/session.go` -- GenerateToken, HashToken
- `internal/auth/user.go` -- userFromRow pattern (mirror it)
- `internal/auth/context.go` -- existing context helpers, mirror style
- `internal/db/devices.sql.go` (after TASK-011) -- sqlc-generated method signatures
- `internal/db/models.go` -- the `db.Device` struct with sql.NullString for nullable fields
- `docs/plans/architecture/phase-1-foundation/arch-device-auth.md` -- "Component Design" section, especially `internal/auth/auth.go` additions and `internal/auth/device.go`

## Requirements

1. Create `internal/auth/device.go`:
   - Define `Device` struct with fields `ID, Name, TokenHash, CreatedBy string`, `CreatedAt time.Time`, `LastSeenAt *time.Time`, `RevokedAt *time.Time`.
   - Define method `func (d Device) IsRevoked() bool` returning `d.RevokedAt != nil`.
   - Define unexported `deviceFromRow(row db.Device) (Device, error)` that parses the timestamps:
     - `CreatedAt` parses as before with `time.Parse("2006-01-02 15:04:05", row.CreatedAt)`.
     - `LastSeenAt` and `RevokedAt`: if the corresponding `sql.NullString` is invalid, leave the pointer nil; otherwise parse and store the address.

2. Create `internal/auth/identity.go`:
   - Define `IdentityKind` as `int` with constants `IdentityNone`, `IdentityAdmin`, `IdentityDevice` (in that iota order).
   - Define `Identity` struct: `Kind IdentityKind`, `User *User`, `Device *Device`.
   - Define methods:
     - `func (i Identity) ID() string` -- returns `"user:<id>"` or `"device:<id>"` or `""`.
     - `func (i Identity) IsAdmin() bool` -- `i.Kind == IdentityAdmin`.
     - `func (i Identity) IsDevice() bool` -- `i.Kind == IdentityDevice`.

3. Modify `internal/auth/context.go`:
   - Add `type identityKey struct{}` and `type deviceKey struct{}`.
   - Add `func ContextWithIdentity(ctx context.Context, id *Identity) context.Context`.
   - Add `func IdentityFromContext(ctx context.Context) *Identity` returning nil if absent.
   - Add `func ContextWithDevice(ctx context.Context, d *Device) context.Context`.
   - Add `func DeviceFromContext(ctx context.Context) *Device` returning nil if absent.
   - Do not change the existing `userKey`, `sessionKey`, or their accessors.

4. Extend `internal/auth/auth.go` (`Config` struct + `Service` methods):
   - Add `DeviceCookieName string`, `DeviceLastSeenInterval time.Duration`, and `DeviceLandingURL string` fields to `Config`.
   - Add two sentinel errors at package level:
     - `var ErrDeviceNotFound = errors.New("device not found")`
     - `var ErrDeviceRevoked = errors.New("device revoked")`
   - Add six new methods on `*Service`:
     - `func (s *Service) CreateDevice(ctx context.Context, name, createdBy string) (Device, string, error)`
       - Trim whitespace from name; reject empty with `fmt.Errorf("device name required")`.
       - Generate raw token via `GenerateToken()`.
       - Generate device id via the existing `generateID()` helper.
       - Hash the token with `HashToken`.
       - Call `queries.CreateDevice(ctx, db.CreateDeviceParams{...})`.
       - Re-fetch the row via `GetDeviceByID` and convert with `deviceFromRow` so the returned `Device` carries the database-assigned `created_at`.
       - Return `(Device, rawToken, nil)` on success.
     - `func (s *Service) ValidateDeviceToken(ctx context.Context, rawToken string) (*Device, error)`
       - Hash the token, look up via `GetDeviceByTokenHash`.
       - On `sql.ErrNoRows`, return `nil, ErrDeviceNotFound`.
       - Convert with `deviceFromRow`.
       - If `dev.IsRevoked()`, return `nil, ErrDeviceRevoked`.
       - Otherwise return `&dev, nil`.
     - `func (s *Service) MarkDeviceSeen(ctx context.Context, deviceID string) error`
       - Build the SQL interval string from `s.config.DeviceLastSeenInterval`. Example: 60-second interval becomes `"-60 seconds"`. Use whole seconds (truncate sub-second precision); zero or negative interval becomes `"-0 seconds"` so the throttle is effectively disabled.
       - Call `queries.TouchDeviceSeen(ctx, db.TouchDeviceSeenParams{ID: deviceID, Column2: interval})` (the second sqlc-generated parameter name may vary; use whichever name sqlc emits).
       - Best-effort -- never returns the throttled "0 rows affected" as an error. Wrap and return only true SQL errors.
     - `func (s *Service) RevokeDevice(ctx context.Context, deviceID string) error`
       - Call `GetDeviceByID` first; on `sql.ErrNoRows`, return `ErrDeviceNotFound`.
       - Call `queries.RevokeDevice(ctx, deviceID)`.
       - Wrap errors with `fmt.Errorf("revoke device: %w", err)`.
     - `func (s *Service) ListDevices(ctx context.Context) ([]Device, error)`
       - Call `queries.ListDevices(ctx)`, map each row through `deviceFromRow`, return the slice.
     - `func (s *Service) RotateDeviceToken(ctx context.Context, deviceID string) (string, error)`
       - Generate a new raw token via `GenerateToken()`.
       - Call `queries.RotateDeviceToken(ctx, db.RotateDeviceTokenParams{TokenHash: HashToken(rawToken), ID: deviceID})`.
       - On error, wrap with `fmt.Errorf("rotate device token: %w", err)`.
       - Inspect `result.RowsAffected()`. If 0, return `("", ErrDeviceNotFound)` -- the device id is unknown OR has been revoked. Both cases are equivalent for the caller (refuse the operation).
       - Otherwise return `(rawToken, nil)`. The caller is expected to immediately put the raw token in a cookie and never persist it.

5. Update `auth.NewService` so the `Config` struct fields you added in step 4 are stored. (No code change needed if you simply added them to the struct -- the existing `NewService` already copies the whole `Config`.)

## Acceptance Criteria

From SPEC-003 (filtered to what this task verifies via direct service-level tests):

- [ ] AC-3: `CreateDevice` rejects empty / whitespace-only names without inserting a row.
- [ ] AC-4: Two consecutive `CreateDevice` calls return distinct raw tokens (probabilistic check is fine; assert non-equal).
- [ ] AC-5: After `CreateDevice`, the row's `token_hash` equals `HashToken(rawToken)` and the raw token does not appear in any column of the row.
- [ ] AC-12: After `RevokeDevice`, `ValidateDeviceToken` with that device's raw token returns `ErrDeviceRevoked`.
- [ ] AC-13: `RevokeDevice(idA)` does not affect `ValidateDeviceToken` for device B.
- [ ] AC-15: `MarkDeviceSeen` updates `last_seen_at` when the previous value is NULL or older than the throttle interval.
- [ ] AC-16: Calling `MarkDeviceSeen` twice within the throttle window leaves `last_seen_at` unchanged on the second call.
- Service-level prerequisite for AC-27/AC-31/AC-37 (the enrollment flow ACs in the spec): `RotateDeviceToken` returns a fresh raw token whose hash matches the new `token_hash`, the previous token no longer validates, and a revoked device's id returns `ErrDeviceNotFound`.

## Skills to Use

- `green-bar` -- run before marking complete.

(No scaffold skills apply -- this task is plain Go code that follows existing patterns.)

## Test Requirements

Use `db.OpenTestDB(t)` for a real in-memory database (the schema is the actual migration set, not a hand-rolled mock). Build the `auth.Service` with `auth.NewService(db, auth.Config{DeviceLastSeenInterval: <test value>, ...})` and exercise the methods directly.

1. **Identity tests** (`internal/auth/identity_test.go`): table-driven test of `Identity.ID()`, `IsAdmin()`, `IsDevice()` for each kind plus an `IdentityNone` case and a Kind=Admin-but-User=nil edge case.
2. **Context tests** (extend `internal/auth/context_test.go` if present, otherwise create): `IdentityFromContext` and `DeviceFromContext` return nil on a context with no value; round-trip a non-nil Identity / Device through `ContextWith*` and read it back.
3. **Service tests** (extend `internal/auth/auth_test.go` or create a new `auth_device_test.go`):
   - `CreateDevice` returns a non-empty raw token and persists a hashed row whose `token_hash == HashToken(rawToken)`.
   - `CreateDevice("   ", ...)` returns an error and inserts no row (assert `ListDevices` is empty / has one fewer row than expected).
   - Two `CreateDevice` calls return distinct raw tokens.
   - `ValidateDeviceToken(rawToken)` returns the matching device.
   - `ValidateDeviceToken("garbage")` returns `ErrDeviceNotFound`.
   - After `RevokeDevice`, `ValidateDeviceToken` returns `ErrDeviceRevoked` for that device's token; an unrelated device still validates fine.
   - `RevokeDevice("non-existent-id")` returns `ErrDeviceNotFound`.
   - `MarkDeviceSeen` with throttle = 0 updates the timestamp on every call.
   - `MarkDeviceSeen` with a large throttle (e.g. `1h`) updates once and leaves the timestamp unchanged on the immediate second call. To verify: read `LastSeenAt` from a `GetDeviceByID` lookup before and after the second call; assert equality.
   - `ListDevices` returns devices in `created_at` order including revoked ones.
   - `RotateDeviceToken` returns a non-empty raw token and updates the row so that `ValidateDeviceToken(newToken)` succeeds AND `ValidateDeviceToken(oldToken)` returns `ErrDeviceNotFound`.
   - Two consecutive `RotateDeviceToken` calls return distinct raw tokens.
   - `RotateDeviceToken("non-existent-id")` returns `ErrDeviceNotFound`.
   - After `RevokeDevice(id)`, `RotateDeviceToken(id)` returns `ErrDeviceNotFound` (the `WHERE revoked_at IS NULL` clause makes a revoked device behave like a missing one for rotation purposes).
4. Write tests as a single table where it reads naturally; otherwise individual functions.
5. Use `t.Helper()` in any test helper that builds a service.
6. Follow `.claude/rules/testing.md`. Do not write tests that simply assert a method exists or returns non-nil.

## Definition of Done

- [ ] `internal/auth/device.go` created with `Device`, `IsRevoked`, `deviceFromRow`.
- [ ] `internal/auth/identity.go` created with `IdentityKind` constants, `Identity`, helpers.
- [ ] `internal/auth/context.go` extended with `Identity` and `Device` keys + helpers; existing helpers untouched.
- [ ] `auth.Config` extended with `DeviceCookieName`, `DeviceLastSeenInterval`, and `DeviceLandingURL`.
- [ ] `ErrDeviceNotFound` and `ErrDeviceRevoked` added.
- [ ] Six new `*Service` methods implemented (CreateDevice, ValidateDeviceToken, MarkDeviceSeen, RevokeDevice, ListDevices, RotateDeviceToken).
- [ ] All listed tests pass, including the four `RotateDeviceToken` cases.
- [ ] green-bar passes.
- [ ] No new third-party dependencies.
