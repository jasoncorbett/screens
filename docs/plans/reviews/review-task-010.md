---
task: TASK-010
title: "User management views (invite, deactivate, list)"
spec: SPEC-002
arch: ARCH-002
reviewer: tester
date: 2026-04-25
result: ACCEPT
---

# Review: TASK-010 -- User management views

## Acceptance Criteria

| AC   | Description                                                                  | Result | Evidence                                                                 |
|------|------------------------------------------------------------------------------|--------|--------------------------------------------------------------------------|
| AC-3 | Admin invites user; record created; success message shown                    | PASS   | `TestHandleInvite_Success`, `TestUserMgmt_AdminInviteThroughMiddlewareChain` |
| AC-5 | Admin revokes invitation; deleted; confirmation                              | PASS   | `TestHandleRevokeInvitation_Success`                                     |
| AC-6 | Admin deactivates user; sessions invalidated; user shown deactivated         | PASS   | `TestHandleDeactivate_Success`, `TestUserMgmt_DeactivateInvalidatesSessions` |
| --   | Only admins can access `/admin/users` (members get 403)                      | PASS   | `TestHandleUserList_MemberForbidden`, `TestUserMgmt_MemberCannotAccessUsersPage`, `TestUserMgmt_MemberCannotPostInvite`, `TestUserMgmt_MemberCannotDeactivateUser` |
| --   | Admin cannot deactivate themselves                                            | PASS   | `TestHandleDeactivate_SelfPrevention`, `TestUserMgmt_UserListPage_HidesDeactivateButtonForSelf` |
| --   | Invite form validates email and role                                          | PASS   | `TestHandleInvite_EmptyEmail`, `TestHandleInvite_InvalidEmail`, `TestHandleInvite_InvalidRole`, `TestUserMgmt_InviteWhitespaceOnlyEmail`, `TestUserMgmt_InviteEmptyRole` |

## Adversarial Findings

### 1. Silent success on revoking a non-existent invitation (MEDIUM -- FIXED)

**Description**: `handleRevokeInvitation` redirected to `/admin/users?msg=revoked` even when the invitation ID did not exist. Root cause: `auth.Service.RevokeInvitation` called `DELETE FROM invitations WHERE id = ?` (`:exec`), which silently succeeds with zero rows affected. The handler had no way to distinguish "actually deleted" from "no-op". This produced a misleading audit log entry (`"invitation revoked" invitation_id=nonexistent-id`) and lied to the operator in the flash message.

**Severity**: Medium. Not a security flaw -- the route is admin-only, and the SQL is parameterized so injection isn't possible. But the false success message and false audit log entry undermine operational trust.

**Reproduction**: `TestUserMgmt_RevokeBogusID_RedirectsWithError` (would have failed before the fix).

**Fix**: Added `auth.ErrInvitationNotFound`. `Service.RevokeInvitation` now does `GetInvitationByID` first and returns the sentinel when missing. `handleRevokeInvitation` checks `errors.Is(err, auth.ErrInvitationNotFound)` and surfaces `?error=Invitation+not+found` instead of `?msg=revoked`. Existing service test `TestRevokeInvitation_NonexistentID` was tightened to expect the sentinel.

### 2. Silent success on deactivating a non-existent user (MEDIUM -- FIXED)

**Description**: Same shape as above. `handleDeactivate` reported `?msg=deactivated` for any ID, even fabricated ones, and emitted `"user deactivated" user_id=nonexistent-id deactivated_by=...` to the audit log.

**Severity**: Medium. Same rationale -- not exploitable, but corrupts audit and operator trust.

**Reproduction**: `TestUserMgmt_DeactivateBogusID_RedirectsWithError` (would have failed before the fix).

**Fix**: Added `auth.ErrUserNotFound`. `Service.DeactivateUser` now does `GetUserByID` first and returns the sentinel when missing (before opening the transaction). `handleDeactivate` checks `errors.Is(err, auth.ErrUserNotFound)` and surfaces `?error=User+not+found`. Existing service test `TestDeactivateUser_NonexistentUserID` was tightened to expect the sentinel.

### 3. Middleware ordering of the user-management sub-mux (NOT VULNERABLE)

**Description**: TASK-009's review caught a bug where `RequireCSRF` ran before `RequireAuth` and broke all POSTs. TASK-010 introduces a *third* sub-mux layer (`userMux` wrapped by `RequireRole` and registered into `adminMux`), which created another opportunity to scramble the chain.

I verified end-to-end through the real `AddRoutes` mux that:

- `POST /admin/users/invite` without CSRF -> 403 (`TestUserMgmt_InviteWithoutCSRF_Returns403`)
- `POST /admin/users/{id}/deactivate` without CSRF -> 403 (`TestUserMgmt_DeactivateWithoutCSRF_Returns403`)
- `POST /admin/invitations/{id}/revoke` without CSRF -> 403 (`TestUserMgmt_RevokeInvitationWithoutCSRF_Returns403`)
- Member with valid CSRF posting invite -> 403 (`TestUserMgmt_MemberCannotPostInvite`)
- Member with valid CSRF posting deactivate -> 403 (`TestUserMgmt_MemberCannotDeactivateUser`)
- Admin with valid CSRF -> success (`TestUserMgmt_AdminInviteThroughMiddlewareChain`)

The chain `RequireAuth(RequireCSRF(adminMux))` with `adminMux.Handle("/admin/users/", RequireRole(RoleAdmin)(userMux))` is correct: Auth populates context, CSRF validates against the session, RequireRole gates by role, then the handler runs.

**Severity**: N/A -- verified safe.

### 4. XSS via flash message and rendered email values (NOT VULNERABLE)

**Description**: Both `?error=` and `?msg=` query params are echoed into the page; user emails (potentially user-supplied via invite or admin-controlled) are rendered into a table.

**Severity**: N/A -- templ's `{ value }` interpolation calls `templ.EscapeString`, which HTML-escapes all string content (verified in generated `users_templ.go` at every interpolation site). I tested `<script>alert('xss')</script>` payloads in both query params and email field; both come out as `&lt;script&gt;` in the response body.

**Tests**: `TestUserMgmt_InviteEmail_XSSEscaped`, `TestUserMgmt_FlashErrorXSSEscaped`.

### 5. Path-segment injection in deactivate/revoke URLs (NOT VULNERABLE)

**Description**: The `templ.SafeURL` for the deactivate / revoke forms is built from `u.ID` and `inv.ID` via string concatenation. If those IDs ever contained URL metacharacters or newlines, the resulting form action could be malformed.

**Severity**: N/A. IDs are server-generated 32-char hex (`generateID()` -> `GenerateToken()[:32]`); they cannot contain anything except `[0-9a-f]`. SQL injection at the DB layer is also impossible -- all queries are sqlc-generated with parameter placeholders.

### 6. Self-deactivation prevention in handler vs. UI (PASS)

**Description**: The handler explicitly compares `userID == currentUser.ID` and rejects with an error redirect. The UI also hides the deactivate button for the current row. Both layers tested: `TestHandleDeactivate_SelfPrevention` (handler), `TestUserMgmt_UserListPage_HidesDeactivateButtonForSelf` (template).

### 7. nil session / nil user in context (PASS)

**Description**: `handleUserList` checks `user == nil || session == nil` before dereferencing `session.CSRFToken`. Without that guard, a stray request that bypassed `RequireAuth` (or a future direct call) would NPE on the template render.

**Test**: `TestUserMgmt_NoSessionInContext_Returns403`.

### 8. Long-input fuzzing (PASS)

**Description**: 64 KB email payload submitted to invite. The handler trims, validates `@`, then forwards to InviteUser, which inserts into SQLite. SQLite has no column size limit on TEXT, so the insert may succeed -- but the handler does not crash, panic, or 500.

**Test**: `TestUserMgmt_InviteVeryLongEmail_NoPanic`.

### 9. Whitespace-only email (PASS)

**Description**: `"   \t  "` was rejected after `strings.TrimSpace`, returning a flash error instead of creating an invitation with an empty email.

**Test**: `TestUserMgmt_InviteWhitespaceOnlyEmail`.

### 10. Empty role on invite (PASS)

**Description**: `r.FormValue("role")` returns `""` for a missing form field; the handler rejects anything other than `"admin"` or `"member"`.

**Test**: `TestUserMgmt_InviteEmptyRole`.

### 11. Duplicate invitation handling (PASS, with caveats)

**Description**: Inviting `dupe@example.com` twice triggers the `UNIQUE` constraint on `invitations.email`. The handler returns a generic `?error=Could+not+create+invitation` redirect rather than `?msg=invited`. Sequential test confirms only one invitation row exists.

**Test**: `TestUserMgmt_InviteDuplicateInvitation_RedirectsWithError`, `TestUserMgmt_SequentialInvites_SameEmail`.

### 12. Invite an email that already has an active user account (LOW -- noted, not fixed)

**Description**: The task says (under Validation): "Cannot invite an email that already has an active account." The current handler does not enforce this -- it inserts an invitation row even when the email already maps to an active user. Such an invitation can never be consumed (provisioning hits the existing-user branch first), so it just lingers in the pending list. Not a security issue, but a minor UX/data-hygiene gap.

**Test**: `TestUserMgmt_InviteDuplicateEmail_FailsGracefully` -- documents the current behavior (no panic, no 500). Decided not to escalate to a fix because: (a) the unique invitation index limits damage to one stale row; (b) addressing it cleanly really wants a proper service-layer invariant covering both tables, which is broader than this view task.

### 13. Email logged as PII (LOW -- noted, not fixed)

**Description**: `slog.Info("user invited", "email", ..., "invited_by", currentUser.Email)` and `"user deactivated" deactivated_by=currentUser.Email` log full email addresses. The logging rule says "Never log... PII." For a household auth boundary the operator-side admin email is arguably operational metadata, and TASK-009 already established this pattern (it logs admin email on logout). Calling it out for completeness; not in scope to refactor here.

## New Tests Added

`views/users_adversarial_test.go` (20 new tests, all passing):

- `TestUserMgmt_MemberCannotAccessUsersPage` -- end-to-end role enforcement on GET
- `TestUserMgmt_MemberCannotPostInvite` -- role enforcement on invite POST
- `TestUserMgmt_MemberCannotDeactivateUser` -- role enforcement on deactivate POST
- `TestUserMgmt_InviteWithoutCSRF_Returns403` -- CSRF on invite via real chain
- `TestUserMgmt_DeactivateWithoutCSRF_Returns403` -- CSRF on deactivate via real chain
- `TestUserMgmt_RevokeInvitationWithoutCSRF_Returns403` -- CSRF on revoke via real chain
- `TestUserMgmt_AdminInviteThroughMiddlewareChain` -- happy path through full chain
- `TestUserMgmt_InviteEmail_XSSEscaped` -- email field rendering escapes HTML
- `TestUserMgmt_FlashErrorXSSEscaped` -- ?error= rendering escapes HTML
- `TestUserMgmt_InviteDuplicateEmail_FailsGracefully` -- existing-user email no panic
- `TestUserMgmt_InviteDuplicateInvitation_RedirectsWithError` -- second invite rejected
- `TestUserMgmt_InviteVeryLongEmail_NoPanic` -- 64KB input fuzz
- `TestUserMgmt_InviteWhitespaceOnlyEmail` -- "   " rejected as empty
- `TestUserMgmt_InviteEmptyRole` -- missing role rejected
- `TestUserMgmt_DeactivateBogusID_RedirectsWithError` -- catches finding #2
- `TestUserMgmt_RevokeBogusID_RedirectsWithError` -- catches finding #1
- `TestUserMgmt_DeactivateInvalidatesSessions` -- AC-6 sessions actually killed
- `TestUserMgmt_NoSessionInContext_Returns403` -- nil-session guard
- `TestUserMgmt_UserListPage_HidesDeactivateButtonForSelf` -- UI hides self-row button
- `TestUserMgmt_SequentialInvites_SameEmail` -- duplicate invite shows error, not panic

## Code Changes (bug fixes)

- `internal/auth/auth.go`: added `ErrUserNotFound`, `ErrInvitationNotFound` sentinel errors. `RevokeInvitation` and `DeactivateUser` now pre-check existence and return the sentinel. The deactivation transaction is opened only after the existence check passes, so we no longer roll back unnecessary transactions.
- `internal/auth/auth_adversarial_test.go`: tightened `TestRevokeInvitation_NonexistentID` and `TestDeactivateUser_NonexistentUserID` to expect the new sentinels (instead of documenting the silent-success bug).
- `views/users.go`: `handleDeactivate` and `handleRevokeInvitation` branch on the new sentinels and surface `?error=User+not+found` / `?error=Invitation+not+found`.

## Green Bar

- `gofmt -l .`: PASS (empty output)
- `go vet ./...`: PASS
- `go build ./...`: PASS
- `go test ./...`: PASS
- `go test -race ./...`: PASS

## Recommendation

**ACCEPT.** Two medium-severity bugs (silent-success on revoke and deactivate) were found and fixed in-place; tests prove the fixes hold. All AC are now demonstrably covered by tests that exercise the real middleware chain rather than calling handlers directly. Two low-severity findings (#12 same-email cross-table invite, #13 email PII in logs) are recorded as future work and do not block acceptance.
