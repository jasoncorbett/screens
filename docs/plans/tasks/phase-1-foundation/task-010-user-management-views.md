---
id: TASK-010
title: "User management views (invite, deactivate, list)"
spec: SPEC-002
arch: ARCH-002
status: review
priority: p0
prerequisites: [TASK-009]
skills: [add-view, green-bar]
created: 2026-04-20
author: architect
---

# TASK-010: User management views (invite, deactivate, list)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Implement the user management page where admins can view existing users, invite new users by email, deactivate users, and revoke pending invitations. This is the final task in the admin auth feature -- it provides the admin-facing UI for managing who can access the system.

## Context

TASK-009 wires the auth flow and protects admin routes with middleware. The auth service (TASK-006) already has `InviteUser`, `RevokeInvitation`, `DeactivateUser`, `ListUsers`, and `ListInvitations` methods. This task creates the view templates and handlers that call those service methods and render the results.

### Files to Read Before Starting

- `.claude/rules/http.md` -- view handler conventions
- `.claude/rules/testing.md` -- test conventions
- `.claude/skills/add-view/SKILL.md` -- view creation pattern
- `views/login.go` and `views/login.templ` -- pattern from TASK-009
- `views/admin.go` and `views/admin.templ` -- admin landing page pattern from TASK-009
- `views/layout.templ` -- base HTML layout
- `internal/auth/auth.go` -- Service methods for user/invitation management
- `internal/auth/context.go` -- UserFromContext for getting the current user
- `internal/middleware/require_role.go` -- RequireRole middleware (from TASK-008)
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- endpoints table

## Requirements

### User Management Page

1. Create `views/users.templ` with:
   - A user management page component using `@layout("Users - screens")`
   - A section listing all active users with: email, display name, role, created date
   - For each non-self user row, a "Deactivate" button (POST form with CSRF token)
   - The admin cannot deactivate themselves (button hidden/disabled for current user)
   - A section listing pending invitations with: email, role, invited date
   - For each invitation, a "Revoke" button (POST form with CSRF token)
   - An "Invite User" form with: email input, role select (admin/member), submit button, CSRF token
   - Display flash messages for success/error feedback (passed via query params or session)
   - Semantic HTML with proper labels, tables, and form elements

2. Create `views/users.go` with handlers:

   **List users page:**
   - Register route `GET /admin/users` (requires admin role)
   - `handleUserList` that:
     - Gets the current user from context
     - Calls `authService.ListUsers(ctx)` and `authService.ListInvitations(ctx)`
     - Renders the users template with users, invitations, current user, and any flash message

   **Invite user:**
   - Register route `POST /admin/users/invite` (requires admin role)
   - `handleInvite` that:
     - Reads `email` and `role` from the form
     - Validates email is not empty and role is valid (admin or member)
     - Gets the current user from context (for invited_by)
     - Calls `authService.InviteUser(ctx, email, role, currentUser.ID)`
     - On success, redirects to `/admin/users?msg=invited`
     - On error (duplicate email, invalid input), redirects to `/admin/users?error=<message>`

   **Deactivate user:**
   - Register route `POST /admin/users/{id}/deactivate` (requires admin role)
   - `handleDeactivate` that:
     - Reads the user ID from the URL path
     - Prevents self-deactivation (compare with current user ID from context)
     - Calls `authService.DeactivateUser(ctx, userID)`
     - On success, redirects to `/admin/users?msg=deactivated`
     - On error, redirects to `/admin/users?error=<message>`

   **Revoke invitation:**
   - Register route `POST /admin/invitations/{id}/revoke` (requires admin role)
   - `handleRevokeInvitation` that:
     - Reads the invitation ID from the URL path
     - Calls `authService.RevokeInvitation(ctx, invitationID)`
     - On success, redirects to `/admin/users?msg=revoked`
     - On error, redirects to `/admin/users?error=<message>`

3. Apply `RequireRole(auth.RoleAdmin)` middleware to all user management routes so that only admins can access them.

### Route Registration

4. The user management routes require admin role. Wire them through the middleware chain:
   - RequireAuth (from TASK-008) is already applied to `/admin/*`
   - RequireCSRF (from TASK-008) is already applied to POST routes
   - Add RequireRole(RoleAdmin) specifically to the user management handlers

5. Registration options (choose based on how TASK-009 structured things):
   - If using per-handler middleware: wrap each handler with RequireRole
   - If using a sub-mux: create an admin-only sub-mux with RequireRole applied

### Validation

6. Input validation for invite:
   - Email must not be empty
   - Email should look like an email (contains `@` -- simple check is sufficient)
   - Role must be either "admin" or "member"
   - Cannot invite an email that already has an active account
   - Cannot invite an email that already has a pending invitation

## Acceptance Criteria

- [ ] AC-3: When an admin invites `user@example.com` with role `member`, an invitation record is created and the page shows a success message.
- [ ] AC-5: When an admin revokes a pending invitation, the invitation is deleted and the page shows a confirmation.
- [ ] AC-6: When an admin deactivates a user, that user's sessions are invalidated and they appear as deactivated.
- [ ] Only admins can access `/admin/users` -- members are rejected with 403.
- [ ] Admin cannot deactivate themselves (prevented in handler logic).
- [ ] Invite form validates email and role before creating the invitation.

## Skills to Use

- `add-view` -- for users.templ and users.go
- `green-bar` -- run before marking complete

## Test Requirements

1. Test `handleUserList` returns 200 with HTML listing users and invitations.
2. Test `handleUserList` returns 403 when accessed by a member (not admin).
3. Test `handleInvite` creates an invitation and redirects with success message.
4. Test `handleInvite` with empty email returns redirect with error.
5. Test `handleInvite` with invalid role returns redirect with error.
6. Test `handleDeactivate` deactivates the user and redirects.
7. Test `handleDeactivate` with the current user's own ID returns redirect with error (self-deactivation prevented).
8. Test `handleRevokeInvitation` deletes the invitation and redirects.
9. Use `db.OpenTestDB(t)` for tests that need a database.
10. Use `httptest.NewRecorder` with auth context injected for handler tests.
11. Follow `.claude/rules/testing.md`.

## Definition of Done

- [ ] User management template created with user list, invitation list, and invite form
- [ ] All four handlers implemented (list, invite, deactivate, revoke)
- [ ] Admin role check enforced on all user management routes
- [ ] Self-deactivation prevented
- [ ] Input validation on invite form
- [ ] CSRF tokens included in all forms
- [ ] `templ generate` run successfully
- [ ] All tests pass
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies
