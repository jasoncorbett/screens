---
id: SPEC-002
title: "Admin Auth"
phase: 1
status: draft
priority: p0
created: 2026-04-20
author: pm
---

# Admin Auth

## Problem Statement

The screens service has no authentication. Every endpoint is publicly accessible, meaning anyone on the network can reconfigure dashboards, create device tokens, or tamper with settings. Before any admin-facing functionality can be built (theme CRUD, screen management, widget configuration), there must be a way to verify that the person making changes is an authorized user.

This is a household dashboard. The auth model uses Google OAuth 2.0 for identity: users sign in with their Google account, and the service verifies they are on the authorized list. The initial admin is designated by email address via configuration. That admin can then invite additional household members by their Google email address.

Admin auth is the security boundary that every subsequent feature depends on. Without it, the service cannot distinguish between an authorized household member managing dashboards and an unauthorized visitor.

## User Stories

- As an **admin**, I want to designate my Google account as the initial administrator via a config variable so that no setup wizard is needed.
- As a **user**, I want to sign in with my Google account so that I do not need to manage another password.
- As an **admin**, I want to invite other household members by their Google email so that they can also manage screens.
- As a **user**, I want my session to persist across page loads so that I do not need to re-authenticate on every request.
- As a **user**, I want to log out so that my session is invalidated and the browser can no longer act on my behalf.
- As a **user**, I want session cookies to be HttpOnly and Secure (in production) so that client-side scripts and network sniffers cannot steal my session.
- As a **user**, I want form submissions to be protected from CSRF attacks so that a malicious website cannot perform actions on my behalf.
- As a **visitor**, I want to be redirected to the login page when I try to access an admin route without being logged in.
- As an **admin**, I want to revoke access for an invited user so that they can no longer manage screens.

## Functional Requirements

### User Accounts

1. The system MUST store user accounts in a `users` table with fields: id, email, display_name, role (admin/member), created_at, updated_at.
2. The system MUST enforce unique email addresses at the database level.
3. The system MUST support two roles: `admin` (full access, can invite/revoke) and `member` (can manage screens but cannot invite/revoke).
4. The system MUST auto-provision the initial admin account on first login when the user's email matches the configured `ADMIN_EMAIL`.
5. The system MUST NOT allow login for email addresses that are neither the configured admin email nor present in the invitations list.

### Invitations

6. The system MUST store invitations in an `invitations` table with fields: id, email, role, invited_by (user_id), created_at.
7. The system MUST allow admins to invite users by email address with a specified role.
8. The system MUST auto-provision an invited user's account on their first Google login.
9. The system MUST delete the invitation record after the user account is provisioned.
10. The system MUST allow admins to revoke invitations that have not yet been claimed.
11. The system MUST allow admins to deactivate existing user accounts (soft-delete or active flag).

### Google OAuth 2.0 Flow

12. The system MUST implement the Google OAuth 2.0 Authorization Code flow.
13. The system MUST redirect unauthenticated users to `/admin/login` which presents a "Sign in with Google" button.
14. The system MUST redirect the user to Google's authorization endpoint with appropriate scopes (email, profile, openid).
15. The system MUST include a `state` parameter in the authorization request to prevent CSRF during the OAuth flow.
16. The system MUST handle the OAuth callback at `/auth/google/callback`, exchanging the authorization code for tokens.
17. The system MUST validate the ID token received from Google to extract the user's email and display name.
18. The system MUST verify the ID token signature using Google's public keys (fetched from the JWKS endpoint) or by calling Google's tokeninfo endpoint.
19. The system MUST reject login if the user's email is not authorized (neither the admin email nor an invited email).
20. The system MUST create a local session after successful OAuth verification.

### Sessions

21. The system MUST generate session tokens using `crypto/rand` with at least 32 bytes of entropy.
22. The system MUST store sessions server-side in a `sessions` table with fields: token_hash, user_id, csrf_token, created_at, expires_at.
23. The system MUST hash session tokens before storing them (SHA-256) so that a database leak does not directly expose valid session tokens.
24. The system MUST set session cookies with attributes: HttpOnly, SameSite=Lax, Path=/, and Secure when not in dev mode.
25. The system MUST support configurable session duration, defaulting to 7 days.
26. The system MUST reject expired sessions and treat them as unauthenticated.
27. The system SHOULD clean up expired sessions periodically (lazily on login or via a cleanup query).

### Logout

28. The system MUST provide a logout endpoint at `POST /admin/logout`.
29. The system MUST delete the session from the database on logout.
30. The system MUST clear the session cookie on logout.
31. The system MUST redirect to the login page after logout.

### CSRF Protection

32. The system MUST generate a CSRF token per session and store it alongside the session.
33. The system MUST include the CSRF token in all HTML forms rendered for authenticated pages (as a hidden input field).
34. The system MUST validate the CSRF token on all state-changing requests (POST, PUT, DELETE) to protected routes.
35. The system MUST reject requests with missing or invalid CSRF tokens with a 403 Forbidden response.
36. The system MUST generate CSRF tokens using `crypto/rand` with at least 32 bytes of entropy.

### Configuration

37. The system MUST support the following environment variables:
    - `ADMIN_EMAIL` (string, required) -- Google email address of the initial admin.
    - `GOOGLE_CLIENT_ID` (string, required) -- OAuth 2.0 client ID from Google Cloud Console.
    - `GOOGLE_CLIENT_SECRET` (string, required) -- OAuth 2.0 client secret.
    - `GOOGLE_REDIRECT_URL` (string, required) -- OAuth callback URL (e.g., `http://localhost:8080/auth/google/callback`).
    - `SESSION_DURATION` (duration, default: `168h`) -- how long a session remains valid.
    - `SESSION_COOKIE_NAME` (string, default: `screens_session`) -- name of the session cookie.
38. The system MUST validate that `ADMIN_EMAIL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `GOOGLE_REDIRECT_URL` are not empty.
39. The system MUST NOT log `GOOGLE_CLIENT_SECRET` or session tokens. The Config struct MUST redact secrets in any String() representation.

## Non-Functional Requirements

- **Performance**: OAuth login involves a round-trip to Google (one-time per session). Session validation (token lookup) must be fast since it is checked on every admin request -- a simple hash + DB lookup.
- **Security**: Session tokens are hashed before storage. Cookies are HttpOnly/SameSite/Secure. CSRF tokens protect state-changing operations. OAuth state parameter prevents authorization CSRF. ID tokens are validated. Failed logins return generic errors. Only authorized emails can authenticate.
- **Accessibility**: Login page must be usable with keyboard navigation and screen readers. The "Sign in with Google" button uses semantic HTML.
- **Reliability**: Session validation must not panic. If the database is unreachable, session checks should fail closed (treat as unauthenticated, not as authorized).

## Acceptance Criteria

### Initial Admin

- [ ] AC-1: When `ADMIN_EMAIL` is configured and a user logs in with that Google account for the first time, then an admin account is auto-provisioned with role `admin`.
- [ ] AC-2: When a user logs in with a Google account that is neither the admin email nor an invited email, then login is rejected and an error message is shown.

### Invitations

- [ ] AC-3: When an admin invites `user@example.com` with role `member`, then an invitation record is created and that user can subsequently log in.
- [ ] AC-4: When an invited user logs in for the first time, then their account is provisioned with the invited role and the invitation is consumed.
- [ ] AC-5: When an admin revokes an invitation, then the invited email can no longer log in (assuming they haven't already provisioned an account).
- [ ] AC-6: When an admin deactivates an existing user account, then that user's sessions are invalidated and they cannot log in again.

### Google OAuth Flow

- [ ] AC-7: When an unauthenticated user navigates to any `/admin/*` route, then they are redirected to `/admin/login`.
- [ ] AC-8: When a user clicks "Sign in with Google" on the login page, then they are redirected to Google's authorization endpoint with correct client_id, redirect_uri, scope, and state parameters.
- [ ] AC-9: When Google redirects back to `/auth/google/callback` with a valid code, then the system exchanges it for tokens, validates the ID token, and creates a local session.
- [ ] AC-10: When the OAuth callback receives a mismatched `state` parameter, then the login is rejected with a 403.

### Sessions

- [ ] AC-11: When a session cookie is present with a valid, non-expired token, then the request is treated as authenticated and the user identity is available in the request context.
- [ ] AC-12: When a session cookie is present with an expired token, then the request is treated as unauthenticated and the user is redirected to `/admin/login`.
- [ ] AC-13: When the session token stored in the database is compared to the cookie value, then the comparison uses the SHA-256 hash of the cookie value.

### Logout

- [ ] AC-14: When a POST request is made to `/admin/logout` with a valid session, then the session is deleted, the cookie is cleared, and the response redirects to `/admin/login`.

### CSRF

- [ ] AC-15: When an authenticated form page is rendered, then it contains a hidden `_csrf` input field with a token value.
- [ ] AC-16: When a POST request is made to a protected route without a valid CSRF token, then a 403 Forbidden response is returned.
- [ ] AC-17: When a POST request is made with a valid CSRF token matching the session, then the request is processed normally.

### Configuration

- [ ] AC-18: When `ADMIN_EMAIL` is empty, then config validation fails with a descriptive error.
- [ ] AC-19: When `GOOGLE_CLIENT_ID` or `GOOGLE_CLIENT_SECRET` is empty, then config validation fails.
- [ ] AC-20: When `SESSION_DURATION` is set to `48h`, then sessions expire after 48 hours.

### Security Boundary

- [ ] AC-21: When the database is unreachable during session validation, then the request is treated as unauthenticated (fail closed).
- [ ] AC-22: When `GOOGLE_CLIENT_SECRET` is set, then it does not appear in any log output or health check response.

## Out of Scope

- Device authentication (separate spec: Device Auth).
- Role-based access beyond admin/member (two roles is sufficient for a household).
- Multiple OAuth providers (Google only; can be extended later).
- Custom admin dashboard content (this spec only ensures authenticated access; dashboard UI is later).
- Email notifications for invitations (household members are told in person).
- Google token refresh / offline access (we only need the ID token at login time).
- Password-based login as a fallback (Google-only simplifies the household experience).

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- the database, migration system, and test helper must be in place.
- External dependency: Google OAuth 2.0 APIs (authorization endpoint, token endpoint, JWKS/tokeninfo endpoint).
- No new Go module dependencies required beyond stdlib -- `net/http` for OAuth HTTP calls, `crypto/sha256` for hashing, `encoding/json` for token parsing, `crypto/rsa` + `encoding/base64` for JWT validation.

## Open Questions

All resolved.

- Q1: **Resolved** -- No new Go module dependencies are needed. Google OAuth can be implemented with `net/http` (HTTP client), `encoding/json` (parsing responses), `crypto/sha256` (token hashing), and `crypto/rsa` + `math/big` (JWT signature validation). The stdlib is sufficient.
- Q2: **Resolved** -- ID token validation will use Google's JWKS endpoint to fetch public keys and verify the RS256 signature locally. This avoids a network round-trip on every validation and is more robust than the tokeninfo endpoint.
