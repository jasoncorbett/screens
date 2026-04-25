# Review: TASK-006 Auth Service Core

**Reviewer**: tester
**Date**: 2026-04-20
**Verdict**: ACCEPT (after fixes applied)

## AC Coverage

| AC | Description | Result | Evidence |
|----|-------------|--------|----------|
| AC-11 | ValidateSession returns user+session for valid token | PASS | `TestValidateSession/valid_token` |
| AC-12 | ValidateSession returns error for expired token | PASS | `TestValidateSession/expired_session` |
| AC-13 | ValidateSession hashes token with SHA-256 before lookup | PASS | `TestCreateSession` verifies hashed token in DB matches `HashToken(rawToken)` |
| AC-1 | Admin email auto-provisions admin account | PASS | `TestProvisionUser/admin_email_creates_admin` |
| AC-3 | Invited email creates account with invitation role, consumes invitation | PASS | `TestProvisionUser/invited_email_creates_user_with_invitation_role` |
| AC-2 | Unauthorized email returns error | PASS | `TestProvisionUser/unauthorized_email_returns_error` |
| AC-6 | DeactivateUser deletes sessions, user cannot validate | PASS | `TestDeactivateUser` |

## Adversarial Findings

### 1. ValidateSession did not check user.Active (FIXED)

- **Severity**: HIGH (security flaw -- deactivated users retained access)
- **Description**: `ValidateSession` loaded the user from the database but never checked `user.Active`. If a session existed for a deactivated user (possible via race condition in `DeactivateUser` where sessions are deleted before user is deactivated, or if a session was created between those two operations), the deactivated user was returned as authenticated with no error.
- **Reproduction**: `TestValidateSession_InactiveUser` -- deactivates user directly via DB without deleting sessions, then calls ValidateSession. Before the fix, this returned the inactive user with no error.
- **Fix applied**: Added `!user.Active` check after loading the user in `ValidateSession`. Returns "account deactivated" error and cleans up the orphaned session.

### 2. Email case sensitivity in ProvisionUser (FIXED)

- **Severity**: MEDIUM (admin unable to log in if email casing differs)
- **Description**: `ProvisionUser` compared `email == s.config.AdminEmail` using case-sensitive string equality. Email addresses are case-insensitive per RFC 5321. If Google returns "Admin@Example.com" but the config has "admin@example.com", the admin provision path would be skipped and the login would fail with "unauthorized email".
- **Reproduction**: `TestProvisionUser_EmailCaseSensitivity/different_case_login` and `different_case_config` -- both failed before the fix.
- **Fix applied**: Changed both comparisons to `strings.EqualFold(email, s.config.AdminEmail)`.

### 3. DeactivateUser is not transactional

- **Severity**: MEDIUM (data integrity on partial failure)
- **Description**: `DeactivateUser` runs two independent queries: `DeleteSessionsByUserID` then `DeactivateUser`. If the second query fails (e.g., transient DB error), the user's sessions are already deleted but the user remains active. The user loses all sessions but can create new ones. Should wrap both operations in a transaction.
- **Suggested fix**: Use `s.sqlDB.BeginTx()` to wrap both operations in a single transaction.

### 4. InviteUser does not validate Role at service level

- **Severity**: LOW (DB CHECK constraint catches it, but error message is opaque)
- **Description**: `InviteUser` passes the role string directly to the DB. Invalid roles like "superadmin" are caught by the SQLite CHECK constraint, but the error returned to callers is a raw DB error, not a clear validation message.
- **Reproduction**: `TestInviteUser_InvalidRole` -- the DB rejects it, so the test passes (error is returned), but the error message is not user-friendly.
- **Suggested fix**: Add role validation at the service level: `if role != RoleAdmin && role != RoleMember { return fmt.Errorf("invalid role: %q", role) }`.

### 5. Logout and RevokeInvitation silently succeed for non-existent IDs

- **Severity**: LOW (idempotent behavior is arguably correct)
- **Description**: `Logout` and `RevokeInvitation` run DELETE queries that match zero rows and return nil. Callers cannot distinguish "deleted successfully" from "nothing to delete". This is acceptable for idempotent APIs but worth noting.
- **Reproduction**: `TestLogout_NonexistentToken`, `TestRevokeInvitation_NonexistentID` -- both return nil.

### 6. DeactivateUser silently succeeds for non-existent user

- **Severity**: LOW (same idempotent pattern)
- **Description**: `DeactivateUser("nonexistent-id")` runs DELETE + UPDATE on zero rows and returns nil.
- **Reproduction**: `TestDeactivateUser_NonexistentUserID`.

## New Tests Written

All in `internal/auth/auth_adversarial_test.go`:

| Test | What it covers |
|------|---------------|
| `TestValidateSession_InactiveUser` | Session validation rejects deactivated users |
| `TestProvisionUser_EmailCaseSensitivity` | Case-insensitive admin email matching (3 sub-cases) |
| `TestDeactivateUser_PartialFailure` | Verifies both session deletion and user deactivation occur |
| `TestProvisionUser_EmptyEmail` | Empty email is rejected as unauthorized |
| `TestProvisionUser_EmptyDisplayName` | Empty display name is accepted (valid) |
| `TestCreateSession_EmptyUserID` | Empty user ID fails on FK constraint |
| `TestValidateSession_EmptyToken` | Empty token returns error (session not found) |
| `TestLogout_NonexistentToken` | Idempotent logout behavior |
| `TestInviteUser_InvalidRole` | Invalid role rejected by DB CHECK |
| `TestInviteUser_DuplicateEmail` | Duplicate invitation email rejected by UNIQUE |
| `TestRevokeInvitation_NonexistentID` | Idempotent revocation |
| `TestDeactivateUser_NonexistentUserID` | Idempotent deactivation |
| `TestProvisionUser_InvitationForAdminEmail` | Admin email gets admin role even if invitation exists for it |
| `TestProvisionUser_ConcurrentSameEmail` | Second provision returns existing user |
| `TestListUsers_EmptyDB` | Empty DB returns empty slice, not nil/error |
| `TestListInvitations_EmptyDB` | Empty DB returns empty slice, not nil/error |
| `TestCleanExpiredSessions_NoExpired` | No expired sessions returns count=0 |
| `TestCleanExpiredSessions_RemovesExpired` | Expired sessions are cleaned |
| `TestHashToken_EmptyInput` | Empty string hashing produces known SHA-256 |
| `TestProvisionUser_UnicodeEmail` | Unicode email and display name work correctly |
| `TestProvisionUser_LongDisplayName` | Very long display name stored correctly |
| `TestProvisionUser_AdminEmailNotConsumedAsInvitation` | Admin path does not attempt invitation consumption |
| `TestCreateSession_MultipleSessions` | Multiple sessions per user are independently valid; logout of one does not affect others |

## Green-Bar Results

| Check | Result |
|-------|--------|
| `gofmt -l .` | PASS (empty output) |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./internal/auth/` | PASS |

## Recommendation

**ACCEPT** -- Two bugs were found and fixed (inactive user bypass in ValidateSession, email case sensitivity in ProvisionUser). The remaining findings are medium/low severity and can be addressed in follow-up work. The implementation is solid with good error handling, proper use of parameterized queries (no SQL injection), and correct session token hashing.
