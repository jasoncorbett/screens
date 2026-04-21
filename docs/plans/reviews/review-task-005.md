---
task: TASK-005
title: "Auth configuration and database migrations"
reviewer: tester
result: ACCEPT
date: 2026-04-21
---

# Review: TASK-005 -- Auth configuration and database migrations

## AC Coverage

| AC | Description | Result | Evidence |
|----|-------------|--------|----------|
| AC-18 | Empty ADMIN_EMAIL fails validation | PASS | `TestValidateAuthFields/empty_AdminEmail_rejected` |
| AC-19 | Empty GOOGLE_CLIENT_ID or SECRET fails validation | PASS | `TestValidateAuthFields/empty_GoogleClientID_rejected`, `empty_GoogleClientSecret_rejected` |
| AC-22 | GoogleClientSecret not in logs/health (String() redaction) | PASS | `TestConfigStringRedactsSecret`, `TestConfigStringDoesNotLeakSecret` |
| Migrations create auth tables | users, sessions, invitations tables exist | PASS | `TestOpenTestDB_AuthTablesExist` |
| sqlc code compiles | Generated code builds without errors | PASS | `go build ./...` succeeds |

## Adversarial Findings

### Finding 1: AuthConfig has no String() method -- default formatting leaks secret

**Severity**: medium

**Description**: The `Config` struct has a `String()` method that redacts `GoogleClientSecret`. However, the `AuthConfig` sub-struct has no `String()` method. If any code path logs or prints `cfg.Auth` directly (e.g., `fmt.Sprintf("%v", cfg.Auth)` or `slog.Info("auth config", "auth", cfg.Auth)`), the secret will appear in plaintext.

**Reproduction**: `TestAuthConfigDefaultFormatLeaksSecret` in `config_adversarial_test.go` demonstrates this. The test passes (documenting the risk) and logs a note.

**Suggested fix**: Add a `String()` method to `AuthConfig` that redacts `GoogleClientSecret`, providing defense-in-depth. This is not critical for TASK-005 since the task only requires `Config.String()` redaction, but it should be addressed in a follow-up.

### Finding 2: golang.org/x/oauth2 removed by go mod tidy

**Severity**: low

**Description**: The task requires adding `golang.org/x/oauth2` to go.mod. It is present as `// indirect`. However, since no Go source file imports it, running `go mod tidy` removes it. This is fragile -- any developer running `go mod tidy` (a common operation) would undo part of this task's work.

**Reproduction**: `go mod tidy -diff` shows the removal.

**Suggested fix**: Acceptable as-is since TASK-007 will add a direct import. Document in the task that this dependency will become direct when TASK-007 is implemented. Alternatively, add a small Go file that imports the package with a build tag to anchor it.

### Finding 3: DeactivateUser on nonexistent ID is a silent no-op

**Severity**: low

**Description**: `DeactivateUser` with a nonexistent user ID returns `nil` (no error). This is inherent to the `:exec` sqlc annotation -- the UPDATE affects 0 rows and SQLite does not treat that as an error. The auth service layer (TASK-006) should check the affected row count if it needs to report "user not found."

**Reproduction**: `TestDeactivateUser_NonexistentID` confirms the behavior.

**Suggested fix**: No change needed for TASK-005. The service layer in TASK-006 should use `:execresult` instead of `:exec` if it needs to detect this case, or handle it at the application level.

## Tests Written

### internal/db/auth_schema_test.go (14 tests)

- `TestUsersTable_EmailUniqueness` -- UNIQUE constraint on users.email rejects duplicates
- `TestUsersTable_RoleCheckConstraint` -- CHECK constraint rejects invalid roles (superadmin, empty, uppercase)
- `TestSessionsTable_ForeignKeyToUsers` -- FK rejects sessions for nonexistent users
- `TestSessionsTable_CascadeDeleteOnUserRemoval` -- ON DELETE CASCADE removes sessions when user is deleted
- `TestInvitationsTable_EmailUniqueness` -- UNIQUE constraint on invitations.email rejects duplicates
- `TestInvitationsTable_RoleCheckConstraint` -- CHECK constraint rejects invalid invitation roles
- `TestInvitationsTable_ForeignKeyToUsers` -- FK rejects invitations with nonexistent invited_by
- `TestUsersTable_UnicodeEmail` -- Unicode in display_name survives round-trip
- `TestCountActiveAdmins_EmptyDatabase` -- returns 0 on empty table (first-run edge case)
- `TestCountActiveAdmins_ExcludesDeactivated` -- deactivated admins not counted
- `TestDeactivateUser_NonexistentID` -- silent no-op, documents behavior
- `TestDeleteExpiredSessions` -- removes only expired sessions, keeps valid ones
- `TestDeleteSessionsByUserID` -- bulk deletion of all sessions for a user

### internal/config/config_adversarial_test.go (5 tests)

- `TestValidateAccumulatesMultipleErrors` -- all 5 validation errors reported in one message
- `TestValidateSessionDurationBoundary` -- exact boundary at 1 minute, 59s, 0, negative
- `TestConfigStringDoesNotLeakSecret` -- various secret values (normal, matching other fields, "REDACTED" itself, special chars, very long)
- `TestAuthConfigDefaultFormatLeaksSecret` -- documents that AuthConfig %v leaks secret
- `TestConfigStringEmptySecret` -- empty secret shows empty, not REDACTED

## Green-bar Results

| Check | Result |
|-------|--------|
| `gofmt -l .` | PASS (no output) |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |

## Recommendation

**ACCEPT**

The implementation is solid. All acceptance criteria pass. The migration schemas match the architecture document. The sqlc-generated code compiles and the queries work correctly against the schema. Config validation catches all required empty fields and accumulates errors. Secret redaction works in Config.String().

The three findings are all medium/low severity:
1. AuthConfig default formatting leak is a defense-in-depth concern (medium) -- the required Config.String() redaction is in place.
2. go mod tidy fragility is expected and will self-resolve with TASK-007 (low).
3. Silent no-op on nonexistent deactivation is a store-layer design question for TASK-006 (low).

No critical or high severity issues found.
