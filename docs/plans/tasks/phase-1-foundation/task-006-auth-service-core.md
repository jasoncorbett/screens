---
id: TASK-006
title: "Auth service core (session management, user provisioning)"
spec: SPEC-002
arch: ARCH-002
status: done
priority: p0
prerequisites: [TASK-005]
skills: [green-bar]
created: 2026-04-20
author: architect
---

# TASK-006: Auth service core (session management, user provisioning)

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Implement the core auth service package (`internal/auth/`) with session management, user provisioning, token utilities, and context accessors. This is the business logic layer that the middleware and view handlers call. It does NOT include the Google OAuth client -- that is TASK-007.

## Context

TASK-005 creates the database tables (users, sessions, invitations) and generates sqlc query code. This task builds the service layer on top of those generated queries. The auth service encapsulates all session and user operations -- creating sessions, validating tokens, provisioning users from OAuth data, managing invitations, and deactivating users.

### Files to Read Before Starting

- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/testing.md` -- test conventions
- `internal/db/` -- sqlc-generated code (created by TASK-005)
- `internal/config/config.go` -- AuthConfig struct (created by TASK-005)
- `docs/plans/architecture/phase-1-foundation/arch-admin-auth.md` -- Service struct, methods, and types

## Requirements

1. Create `internal/auth/user.go` with:
   - `Role` type (string) with constants `RoleAdmin` and `RoleMember`
   - `User` struct with fields: ID, Email, DisplayName, Role, Active, CreatedAt, UpdatedAt
   - Conversion function from sqlc-generated user row to `User` type

2. Create `internal/auth/session.go` with:
   - `Session` struct with fields: TokenHash, UserID, CSRFToken, CreatedAt, ExpiresAt
   - `GenerateToken() (string, error)` -- 32 random bytes, hex-encoded
   - `HashToken(token string) string` -- SHA-256, hex-encoded

3. Create `internal/auth/invitation.go` with:
   - `Invitation` struct with fields: ID, Email, Role, InvitedBy, CreatedAt

4. Create `internal/auth/context.go` with:
   - `ContextWithUser(ctx, *User) context.Context`
   - `UserFromContext(ctx) *User`
   - `ContextWithSession(ctx, *Session) context.Context`
   - `SessionFromContext(ctx) *Session`
   - Use unexported key types to prevent collisions

5. Create `internal/auth/auth.go` with:
   - `Config` struct (AdminEmail, SessionDuration, CookieName, SecureCookie)
   - `Service` struct holding db queries, config, and a reference to the GoogleClient interface
   - `NewService(...)` constructor
   - `CreateSession(ctx, userID string) (rawToken string, err error)` -- generates token, CSRF token, stores hashed token in DB with expiry
   - `ValidateSession(ctx, rawToken string) (*User, *Session, error)` -- hashes token, looks up session, checks expiry, loads user, returns both
   - `Logout(ctx, rawToken string) error` -- hashes token, deletes session
   - `ProvisionUser(ctx, email, displayName string) (*User, error)` -- checks if email is authorized (admin email or invitation), provisions the user with appropriate role, consumes invitation if applicable
   - `InviteUser(ctx, email string, role Role, invitedBy string) error` -- creates invitation record
   - `RevokeInvitation(ctx, invitationID string) error` -- deletes invitation
   - `DeactivateUser(ctx, userID string) error` -- sets active=0, deletes all sessions for user
   - `ListUsers(ctx) ([]User, error)` -- returns all users
   - `ListInvitations(ctx) ([]Invitation, error)` -- returns all pending invitations
   - `CleanExpiredSessions(ctx) (int64, error)` -- deletes expired sessions, returns count

6. The `ProvisionUser` method must:
   - First check if a user with this email already exists and is active -- if so, return that user
   - If user exists but is inactive, return an "account deactivated" error
   - If no user exists, check if email matches `Config.AdminEmail` -- if so, create with `RoleAdmin`
   - If email matches an invitation, create with the invitation's role and delete the invitation
   - If email is not authorized, return an "unauthorized email" error

## Acceptance Criteria

- [ ] AC-11: When a session cookie is present with a valid, non-expired token, then ValidateSession returns the user and session.
- [ ] AC-12: When a session cookie is present with an expired token, then ValidateSession returns an error.
- [ ] AC-13: When ValidateSession checks the token, it hashes it with SHA-256 before the database lookup.
- [ ] AC-1: When a user with the admin email logs in for the first time, ProvisionUser creates an admin account.
- [ ] AC-3: When an invited email logs in, ProvisionUser creates an account with the invited role and consumes the invitation.
- [ ] AC-2: When an unauthorized email attempts to provision, ProvisionUser returns an error.
- [ ] AC-6: When DeactivateUser is called, the user's sessions are deleted and they cannot validate a session.

## Skills to Use

- `green-bar` -- run before marking complete

## Test Requirements

1. Test `GenerateToken()` returns a 64-character hex string and successive calls produce different values.
2. Test `HashToken()` produces a consistent SHA-256 hash for the same input.
3. Test `CreateSession` stores a hashed token and returns a raw token that, when hashed, matches the stored value.
4. Test `ValidateSession` with a valid token returns the correct user.
5. Test `ValidateSession` with an expired session returns an error.
6. Test `ValidateSession` with a non-existent token returns an error.
7. Test `ProvisionUser` with the admin email creates an admin user.
8. Test `ProvisionUser` with an invited email creates a user with the invitation role and deletes the invitation.
9. Test `ProvisionUser` with an unauthorized email returns an error.
10. Test `ProvisionUser` with a deactivated user's email returns an error.
11. Test `DeactivateUser` removes all sessions for that user.
12. Test `Logout` deletes the session.
13. Use `db.OpenTestDB(t)` for all tests that need a database.
14. Follow `.claude/rules/testing.md` -- table-driven where appropriate.

## Definition of Done

- [ ] All types created (User, Session, Invitation, Role, context keys)
- [ ] Token generation and hashing utilities implemented
- [ ] Service struct with all methods implemented
- [ ] Context accessors implemented
- [ ] All tests pass with meaningful coverage of business logic
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies added (uses sqlc-generated code and stdlib crypto)
