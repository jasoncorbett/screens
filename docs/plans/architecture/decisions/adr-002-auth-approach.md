---
id: ADR-002
title: "Google OAuth 2.0 with server-side sessions for admin auth"
status: accepted
date: 2026-04-20
---

# ADR-002: Google OAuth 2.0 with server-side sessions for admin auth

## Context

The screens service needs admin authentication. The system must identify who is making admin requests and ensure only authorized household members can manage dashboards.

The project targets a household environment where:
- Users already have Google accounts
- Managing yet another password is friction
- The admin designates who has access (invite model)
- The threat model is local network + internet-exposed dashboard (not enterprise)

Options considered:

1. **Username/password with bcrypt** -- Traditional auth. Requires password management UI, recovery flows, and adding `golang.org/x/crypto/bcrypt` as a dependency. Users must remember yet another password.

2. **JWT-based stateless auth** -- Tokens contain claims and are validated without database lookup. However: JWTs cannot be revoked without a deny-list (stateful anyway), are complex to implement securely (algorithm confusion attacks, key rotation), and the token size bloats cookies.

3. **Google OAuth 2.0 + server-side sessions** -- Users sign in with Google. The service validates their identity via Google's ID token, checks authorization (is this email allowed?), and creates a server-side session. Sessions are revocable, simple, and the session cookie is a random opaque token.

4. **Magic link / email OTP** -- Passwordless but requires email sending infrastructure (SMTP or third-party service). Over-engineered for a household where the admin can simply invite members in person.

## Decision

Use **Google OAuth 2.0** for identity verification combined with **server-side sessions** for ongoing authentication.

### Identity: Google OAuth 2.0

- Users authenticate with Google using the Authorization Code flow.
- The `golang.org/x/oauth2` package handles the OAuth flow (authorization URL, token exchange, token refresh).
- Google's ID token is validated to extract the user's email and display name.
- Authorization is controlled by an allow-list: the configured admin email plus invited emails.
- The initial admin is designated via the `ADMIN_EMAIL` environment variable.

### Approved dependency: golang.org/x/oauth2

The `golang.org/x/oauth2` package is approved as a project dependency. It is maintained by the Go team as part of the official extended standard library (`golang.org/x/`). It provides:
- Correct OAuth 2.0 Authorization Code flow implementation
- Token exchange with proper error handling
- PKCE support if needed in the future
- Google-specific endpoint configuration via `golang.org/x/oauth2/google`

Implementing OAuth from scratch with `net/http` would be error-prone and duplicate well-tested code from the Go team.

### State: Server-side sessions

- After successful OAuth, the service creates a session stored in SQLite.
- The session token (32 random bytes, hex-encoded) is set as an HttpOnly cookie.
- The token is SHA-256 hashed before database storage.
- Sessions have a configurable TTL (default 7 days).
- Sessions are explicitly revocable (logout, admin deactivation).
- A CSRF token is generated per session and validated on state-changing requests.

### Authorization model

- Two roles: `admin` and `member`.
- The admin (set by `ADMIN_EMAIL`) can invite other emails and assign roles.
- Members can manage screens/widgets but cannot invite or revoke users.
- New users are auto-provisioned on first login if their email is authorized.

## Consequences

**Accepted trade-offs:**
- Requires Google Cloud project setup (OAuth client ID + secret). This is a one-time setup cost and is documented in the README.
- Requires internet connectivity for the initial OAuth redirect. However, once a session is established, admin access works offline until the session expires.
- No fallback login method if Google is unreachable. Acceptable for a household dashboard; sessions last 7 days, so brief Google outages are irrelevant.
- Adds `golang.org/x/oauth2` as a dependency. This is the Go team's official OAuth library and is well-maintained.

**Benefits:**
- No passwords to manage, hash, or secure. No password reset flow needed.
- `golang.org/x/oauth2` provides a battle-tested, correct OAuth implementation.
- Sessions are simple, revocable, and stored in the existing SQLite database.
- Google handles the hard parts (credential storage, MFA, account recovery).
- The invite model is natural for a household: the admin tells family members they can now log in.
- Server-side sessions give full control over revocation (unlike JWTs).
- CSRF protection is straightforward with per-session tokens.
