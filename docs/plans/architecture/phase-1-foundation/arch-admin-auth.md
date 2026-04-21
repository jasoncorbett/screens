---
id: ARCH-002
title: "Admin Auth"
spec: SPEC-002
status: draft
created: 2026-04-20
author: architect
---

# Admin Auth Architecture

## Overview

This architecture implements admin authentication using Google OAuth 2.0 for identity and server-side sessions for state. Users sign in with their Google account; the service verifies their email is authorized (either the configured admin email or an invited email), creates a server-side session, and protects admin routes via middleware. CSRF protection is built into the session model. The `golang.org/x/oauth2` package handles the OAuth flow.

## References

- Spec: `docs/plans/specs/phase-1-foundation/spec-admin-auth.md`
- Related ADRs: ADR-002 (Google OAuth 2.0 with server-side sessions)
- Prerequisite architecture: ARCH-001 (Storage Engine)

## Data Model

### Database Schema

```sql
-- 002_create-users.sql
-- +up
CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    email        TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    active       INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_users_email ON users(email);

-- +down
DROP INDEX IF EXISTS idx_users_email;
DROP TABLE IF EXISTS users;
```

```sql
-- 003_create-sessions.sql
-- +up
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

-- +down
DROP INDEX IF EXISTS idx_sessions_expires_at;
DROP INDEX IF EXISTS idx_sessions_user_id;
DROP TABLE IF EXISTS sessions;
```

```sql
-- 004_create-invitations.sql
-- +up
CREATE TABLE IF NOT EXISTS invitations (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    invited_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_invitations_email ON invitations(email);

-- +down
DROP INDEX IF EXISTS idx_invitations_email;
DROP TABLE IF EXISTS invitations;
```

### Go Types

```go
// internal/auth/user.go
package auth

import "time"

type Role string

const (
    RoleAdmin  Role = "admin"
    RoleMember Role = "member"
)

type User struct {
    ID          string
    Email       string
    DisplayName string
    Role        Role
    Active      bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

```go
// internal/auth/session.go
package auth

import "time"

type Session struct {
    TokenHash string
    UserID    string
    CSRFToken string
    CreatedAt time.Time
    ExpiresAt time.Time
}
```

```go
// internal/auth/invitation.go
package auth

import "time"

type Invitation struct {
    ID        string
    Email     string
    Role      Role
    InvitedBy string
    CreatedAt time.Time
}
```

## API Contract

### Endpoints

| Method | Path | Request Body | Response | Auth |
|--------|------|-------------|----------|------|
| GET | /admin/login | - | HTML login page | none |
| GET | /auth/google/start | - | 302 redirect to Google | none |
| GET | /auth/google/callback | ?code=&state= | 302 redirect to /admin/ | none |
| POST | /admin/logout | form: _csrf | 302 redirect to /admin/login | session |
| GET | /admin/users | - | HTML user management page | admin |
| POST | /admin/users/invite | form: email, role, _csrf | 302 redirect to /admin/users | admin |
| POST | /admin/users/{id}/deactivate | form: _csrf | 302 redirect to /admin/users | admin |
| POST | /admin/invitations/{id}/revoke | form: _csrf | 302 redirect to /admin/users | admin |

### OAuth Flow Sequence

```
Browser                    Screens Service              Google
  |                              |                        |
  |-- GET /admin/dashboard ----->|                        |
  |<-- 302 /admin/login --------|                        |
  |                              |                        |
  |-- GET /admin/login --------->|                        |
  |<-- HTML (Sign in button) ---|                        |
  |                              |                        |
  |-- GET /auth/google/start --->|                        |
  |   (generates state, stores  |                        |
  |    in short-lived cookie)    |                        |
  |<-- 302 Google auth URL ------|                        |
  |                              |                        |
  |-- GET accounts.google.com ---------------------------------->|
  |<-- user authenticates, consents ----------------------------| 
  |                              |                        |
  |-- GET /auth/google/callback?code=X&state=Y -->|       |
  |                              |-- POST token endpoint ->|
  |                              |<-- {access_token,      |
  |                              |     id_token, ...} ----|
  |                              |                        |
  |                              | (validate ID token,    |
  |                              |  check email auth,     |
  |                              |  provision user,       |
  |                              |  create session)       |
  |                              |                        |
  |<-- 302 /admin/ + Set-Cookie--|                        |
```

## Component Design

### Package Layout

```
internal/
  auth/
    auth.go           -- NEW: Service struct, orchestration (login flow, session management)
    user.go           -- NEW: User type, role constants
    session.go        -- NEW: Session type, token generation, hashing
    invitation.go     -- NEW: Invitation type
    google.go         -- NEW: Google OAuth client (wraps golang.org/x/oauth2, ID token validation)
    context.go        -- NEW: context accessors (UserFromContext, SessionFromContext)
  config/
    config.go         -- MODIFY: add AuthConfig sub-struct
  middleware/
    session.go        -- NEW: session validation middleware
    csrf.go           -- NEW: CSRF validation middleware
    require_role.go   -- NEW: role-checking middleware
  db/
    migrations/
      002_create-users.sql       -- NEW
      003_create-sessions.sql    -- NEW
      004_create-invitations.sql -- NEW
    queries/
      users.sql       -- NEW: sqlc queries for users table
      sessions.sql    -- NEW: sqlc queries for sessions table
      invitations.sql -- NEW: sqlc queries for invitations table
views/
  login.go            -- NEW: login page handler
  login.templ         -- NEW: login page template
  users.go            -- NEW: user management page handler
  users.templ         -- NEW: user management page template
  auth_handlers.go    -- NEW: OAuth flow handlers (start, callback)
main.go              -- MODIFY: wire auth service, middleware, oauth config
```

### Key Interfaces and Types

#### internal/auth/auth.go

```go
package auth

import (
    "context"
    "database/sql"
    "time"
)

// Config holds auth-related configuration.
type Config struct {
    AdminEmail      string
    SessionDuration time.Duration
    CookieName      string
    SecureCookie    bool // false in dev mode
}

// GoogleConfig holds OAuth client configuration.
type GoogleConfig struct {
    ClientID     string
    ClientSecret string
    RedirectURL  string
}

// Service orchestrates authentication operations.
type Service struct {
    db           *sql.DB
    queries      *db.Queries  // sqlc-generated
    config       Config
    google       *GoogleClient
}

// NewService creates an auth service with the given dependencies.
func NewService(sqlDB *sql.DB, cfg Config, googleCfg GoogleConfig) *Service

// AuthorizationURL returns the Google OAuth authorization URL with a state parameter.
// The state parameter should be generated by the caller and stored in a cookie for validation.
func (s *Service) AuthorizationURL(state string) string

// HandleCallback processes the OAuth callback, exchanges the code for tokens,
// validates the ID token, checks authorization, provisions the user if needed,
// and creates a session. Returns the raw session token (for the cookie).
func (s *Service) HandleCallback(ctx context.Context, code string) (sessionToken string, err error)

// ValidateSession checks if a session token is valid and not expired.
// Returns the associated user and session. Returns an error if invalid.
func (s *Service) ValidateSession(ctx context.Context, rawToken string) (*User, *Session, error)

// Logout deletes a session by its raw token.
func (s *Service) Logout(ctx context.Context, rawToken string) error

// InviteUser creates an invitation for the given email with the specified role.
func (s *Service) InviteUser(ctx context.Context, email string, role Role, invitedBy string) error

// RevokeInvitation deletes a pending invitation.
func (s *Service) RevokeInvitation(ctx context.Context, invitationID string) error

// DeactivateUser marks a user as inactive and deletes all their sessions.
func (s *Service) DeactivateUser(ctx context.Context, userID string) error

// ListUsers returns all user accounts.
func (s *Service) ListUsers(ctx context.Context) ([]User, error)

// ListInvitations returns all pending invitations.
func (s *Service) ListInvitations(ctx context.Context) ([]Invitation, error)

// CleanExpiredSessions removes sessions past their expires_at.
func (s *Service) CleanExpiredSessions(ctx context.Context) (int64, error)
```

#### internal/auth/google.go

```go
package auth

import (
    "context"

    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"
)

// GoogleClient handles OAuth 2.0 interactions with Google.
// Wraps golang.org/x/oauth2 for the authorization code flow
// and provides ID token validation.
type GoogleClient struct {
    oauthConfig *oauth2.Config
}

// NewGoogleClient creates a Google OAuth client.
func NewGoogleClient(clientID, clientSecret, redirectURL string) *GoogleClient {
    return &GoogleClient{
        oauthConfig: &oauth2.Config{
            ClientID:     clientID,
            ClientSecret: clientSecret,
            RedirectURL:  redirectURL,
            Scopes:       []string{"openid", "email", "profile"},
            Endpoint:     google.Endpoint,
        },
    }
}

// AuthorizationURL builds the Google authorization URL with the given state.
func (g *GoogleClient) AuthorizationURL(state string) string

// ExchangeCode exchanges an authorization code for an OAuth2 token.
// The token's Extra("id_token") field contains the ID token string.
func (g *GoogleClient) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error)

// ValidateIDToken verifies the ID token and extracts user info.
// Validates: signature (via Google's JWKS), expiry, audience, issuer.
// Returns the user's email and display name.
func (g *GoogleClient) ValidateIDToken(ctx context.Context, rawIDToken string) (email, displayName string, err error)
```

#### internal/auth/session.go

```go
package auth

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
)

// GenerateToken creates a cryptographically random token (32 bytes, hex-encoded = 64 chars).
func GenerateToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return hex.EncodeToString(b), nil
}

// HashToken returns the SHA-256 hex digest of a raw token string.
func HashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return hex.EncodeToString(h[:])
}
```

#### internal/auth/context.go

```go
package auth

import "context"

type userKey struct{}
type sessionKey struct{}

// ContextWithUser returns a context carrying the authenticated user.
func ContextWithUser(ctx context.Context, user *User) context.Context

// UserFromContext extracts the user from the context. Returns nil if absent.
func UserFromContext(ctx context.Context) *User

// ContextWithSession returns a context carrying the session (for CSRF token access).
func ContextWithSession(ctx context.Context, session *Session) context.Context

// SessionFromContext extracts the session from the context. Returns nil if absent.
func SessionFromContext(ctx context.Context) *Session
```

#### internal/middleware/session.go

```go
package middleware

import (
    "net/http"

    "github.com/jasoncorbett/screens/internal/auth"
)

// RequireAuth returns middleware that validates the session cookie,
// injects the user and session into the request context, and
// redirects to loginURL if unauthenticated or unauthorized.
func RequireAuth(authService *auth.Service, cookieName, loginURL string) func(http.Handler) http.Handler
```

#### internal/middleware/csrf.go

```go
package middleware

import (
    "net/http"

    "github.com/jasoncorbett/screens/internal/auth"
)

// RequireCSRF returns middleware that validates the _csrf form field
// (or X-CSRF-Token header) against the session's CSRF token
// for state-changing HTTP methods (POST, PUT, DELETE).
// Passes through GET, HEAD, OPTIONS without validation.
func RequireCSRF() func(http.Handler) http.Handler
```

#### internal/middleware/require_role.go

```go
package middleware

import (
    "net/http"

    "github.com/jasoncorbett/screens/internal/auth"
)

// RequireRole returns middleware that checks the authenticated user
// has one of the allowed roles. Returns 403 if not.
func RequireRole(roles ...auth.Role) func(http.Handler) http.Handler
```

### Dependencies Between Components

```
main.go
  |-- config.Load()                     --> config.Config (including Auth settings)
  |-- db.Open(cfg.DB)                   --> *sql.DB
  |-- db.Migrate(ctx, sqlDB)            --> applies pending migrations
  |-- auth.NewService(sqlDB, authCfg, googleCfg) --> *auth.Service
  |-- middleware stack                  --> RequireAuth, RequireCSRF, RequireRole
  |-- view/API route registration       --> login, callback, user mgmt
  |-- srv.ListenAndServe()
```

### main.go Wiring Changes

```go
// After db.Migrate:

authSvc := auth.NewService(sqlDB, auth.Config{
    AdminEmail:      cfg.Auth.AdminEmail,
    SessionDuration: cfg.Auth.SessionDuration,
    CookieName:      cfg.Auth.CookieName,
    SecureCookie:    !cfg.Log.DevMode,
}, auth.GoogleConfig{
    ClientID:     cfg.Auth.GoogleClientID,
    ClientSecret: cfg.Auth.GoogleClientSecret,
    RedirectURL:  cfg.Auth.GoogleRedirectURL,
})

// Public routes (no auth required)
mux.HandleFunc("GET /admin/login", views.HandleLogin(authSvc))
mux.HandleFunc("GET /auth/google/start", views.HandleGoogleStart(authSvc))
mux.HandleFunc("GET /auth/google/callback", views.HandleGoogleCallback(authSvc))

// Protected admin routes
adminMux := http.NewServeMux()
adminMux.HandleFunc("POST /admin/logout", views.HandleLogout(authSvc))
adminMux.HandleFunc("GET /admin/users", views.HandleUserList(authSvc))
adminMux.HandleFunc("POST /admin/users/invite", views.HandleInvite(authSvc))
adminMux.HandleFunc("POST /admin/users/{id}/deactivate", views.HandleDeactivate(authSvc))
adminMux.HandleFunc("POST /admin/invitations/{id}/revoke", views.HandleRevokeInvitation(authSvc))

// Apply middleware chain: CSRF -> Auth -> admin routes
protected := middleware.RequireCSRF()(
    middleware.RequireAuth(authSvc, cfg.Auth.CookieName, "/admin/login")(adminMux),
)
mux.Handle("/admin/", protected)
```

## Storage

### SQL Queries (sqlc)

See the query files defined in the Package Layout section above. The queries use `?` placeholders (SQLite) and follow sqlc annotation conventions (`:one`, `:many`, `:exec`, `:execresult`).

After creating query files, run `sqlc generate` to produce type-safe Go code in `internal/db/`.

### Migration Numbering

The existing migration is `001_initial.sql` (seed). This feature adds:
- `002_create-users.sql`
- `003_create-sessions.sql`
- `004_create-invitations.sql`

## Security Considerations

### OAuth Security

- **State parameter**: A random value generated per OAuth flow, stored in a short-lived cookie (5 minutes). Validated on callback to prevent CSRF during the authorization flow.
- **ID token validation**: The service validates the JWT signature using Google's public RSA keys (fetched from the JWKS endpoint and cached). It verifies the `aud` claim matches the client ID and the `iss` claim is `accounts.google.com` or `https://accounts.google.com`.
- **Authorization code**: Used only once, exchanged server-side over HTTPS. The client secret is never exposed to the browser.
- **Email authorization**: Only the configured admin email and explicitly invited emails can log in. Arbitrary Google accounts are rejected.

### Session Security

- **Token generation**: 32 bytes from `crypto/rand` (256 bits of entropy), hex-encoded to 64 characters.
- **Token storage**: SHA-256 hash stored in DB. A database leak exposes hashes, not usable tokens.
- **Cookie attributes**: HttpOnly (no JS access), SameSite=Lax (CSRF mitigation for GET), Secure (HTTPS-only in production), Path=/ (available on all routes for middleware).
- **Expiration**: Enforced server-side via `expires_at` column check on every validation.

### CSRF Protection

- **Per-session CSRF token**: Generated at session creation (32 bytes, hex-encoded).
- **Validation**: All POST/PUT/DELETE requests to protected routes must include `_csrf` form field or `X-CSRF-Token` header matching the session's stored token.
- **htmx integration**: htmx can send CSRF token via `hx-headers` attribute or a request header configured globally.

### Secret Handling

- `GOOGLE_CLIENT_SECRET` is never logged. The `Config` struct's `String()` method redacts it.
- Session tokens are never logged (only the hash may appear in debug logs).
- The CSRF token is embedded in HTML forms but not logged server-side.

### Fail-Closed Behavior

- If the database is unreachable during session validation, the middleware treats the request as unauthenticated (redirect to login).
- If Google's token endpoint or JWKS endpoint is unreachable during login, the login fails gracefully with a user-facing error message.

## Task Breakdown

This architecture decomposes into the following tasks:

1. TASK-005: Auth configuration and database migrations -- (prerequisite: none)
2. TASK-006: Auth service core (session management, user provisioning, token utils) -- (prerequisite: TASK-005)
3. TASK-007: Google OAuth client (wrapping golang.org/x/oauth2, ID token validation) -- (prerequisite: TASK-005)
4. TASK-008: Session and CSRF middleware -- (prerequisite: TASK-006)
5. TASK-009: Login/logout views and OAuth route handlers, main.go wiring -- (prerequisite: TASK-007, TASK-008)
6. TASK-010: User management views (invite, deactivate, list) -- (prerequisite: TASK-009)

### Task Dependency Graph

```
TASK-005 (config + migrations + sqlc queries)
    |
    +----------+-----------+
    |                      |
    v                      v
TASK-006                TASK-007
(auth service:          (Google OAuth client:
 sessions, users,        golang.org/x/oauth2,
 token utils)            ID token validation)
    |                      |
    v                      |
TASK-008                   |
(middleware:               |
 session + CSRF +          |
 role check)               |
    |                      |
    +----------+-----------+
               |
               v
           TASK-009
           (login/logout views,
            OAuth route handlers,
            main.go wiring)
               |
               v
           TASK-010
           (user management:
            invite, deactivate,
            list views)
```

TASK-006 and TASK-007 are independent of each other and can be developed in parallel after TASK-005 completes.

## Alternatives Considered

See ADR-002 for the full decision rationale. Additional architectural alternatives:

- **Store sessions in a signed cookie (no DB)**: Simpler but cannot revoke sessions. Admin deactivation would not take immediate effect. Rejected for the lack of revocation capability.
- **Implement OAuth from scratch with net/http**: Possible but error-prone and duplicates well-tested code. Using `golang.org/x/oauth2` is the right trade-off -- it is the Go team's official library.
- **Separate `internal/oauth/` package**: Considered isolating OAuth logic in its own package. Placed it in `internal/auth/google.go` instead since the Google client is tightly coupled to the auth flow and not reusable elsewhere.
- **Use Google's tokeninfo endpoint instead of local JWKS validation**: Simpler but requires a network call per login. JWKS keys are cached, making local validation faster and more resilient.
