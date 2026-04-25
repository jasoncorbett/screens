---
id: ARCH-003
title: "Device Auth"
spec: SPEC-003
status: draft
created: 2026-04-25
author: architect
---

# Device Auth Architecture

## Overview

This architecture adds a second identity kind -- device -- alongside the existing admin user identity, and unifies both behind a single `RequireAuth` middleware. Devices are pre-provisioned by admins; provisioning issues a 256-bit random token, hashes it with SHA-256, and stores only the hash. The raw token is shown to the admin once. Devices authenticate with `Authorization: Bearer <token>` (or, for browser page loads, a cookie). The unified middleware probes for an admin session first, falls back to the device token, and injects an `auth.Identity` value into the request context that handlers can branch on. Revocation is a single column update that takes effect on the next request. Reuses the existing `auth.GenerateToken`, `auth.HashToken`, sqlc query pattern, and migration runner from SPEC-001 / SPEC-002.

## References

- Spec: `docs/plans/specs/phase-1-foundation/spec-device-auth.md`
- Related ADRs: ADR-003 (this feature -- token presentation and unified-middleware design)
- Prerequisite architecture: ARCH-001 (Storage Engine), ARCH-002 (Admin Auth)

## Data Model

### Database Schema

```sql
-- 005_create-devices.sql
-- +up
CREATE TABLE IF NOT EXISTS devices (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL UNIQUE,
    created_by    TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at  TEXT,
    revoked_at    TEXT
);

CREATE INDEX idx_devices_token_hash  ON devices(token_hash);
CREATE INDEX idx_devices_revoked_at  ON devices(revoked_at);

-- +down
DROP INDEX IF EXISTS idx_devices_revoked_at;
DROP INDEX IF EXISTS idx_devices_token_hash;
DROP TABLE IF EXISTS devices;
```

Notes on the schema:
- `token_hash` is `UNIQUE` to make a database integrity constraint catch any failure in the random-token generator. The UNIQUE index also doubles as the primary lookup index.
- `created_by` is `ON DELETE RESTRICT` rather than `CASCADE`: deleting an admin user MUST NOT silently delete the devices they created. (If we ever need user-deletion semantics, that is a separate spec.)
- `revoked_at` and `last_seen_at` are nullable TEXT (ISO-8601 strings, matching the project's existing time-as-text convention).

### Go Types

```go
// internal/auth/device.go
package auth

import "time"

// Device represents a registered display device.
// The raw bearer token is never stored on this struct; only the hash lives
// in the database.
type Device struct {
    ID          string
    Name        string
    TokenHash   string
    CreatedBy   string
    CreatedAt   time.Time
    LastSeenAt  *time.Time
    RevokedAt   *time.Time
}

// IsRevoked reports whether the device has been revoked.
func (d Device) IsRevoked() bool {
    return d.RevokedAt != nil
}
```

```go
// internal/auth/identity.go
package auth

import "context"

// IdentityKind distinguishes between the two ways a request can be authenticated.
type IdentityKind int

const (
    IdentityNone IdentityKind = iota
    IdentityAdmin
    IdentityDevice
)

// Identity is the unified authentication value injected into the request
// context by RequireAuth. Exactly one of User or Device is non-nil whenever
// Kind != IdentityNone.
type Identity struct {
    Kind   IdentityKind
    User   *User    // set when Kind == IdentityAdmin
    Device *Device  // set when Kind == IdentityDevice
}

// ID returns a stable string identifier for the caller, regardless of kind.
// Useful for log lines.
func (i Identity) ID() string {
    switch i.Kind {
    case IdentityAdmin:
        if i.User != nil {
            return "user:" + i.User.ID
        }
    case IdentityDevice:
        if i.Device != nil {
            return "device:" + i.Device.ID
        }
    }
    return ""
}

// IsAdmin is a convenience for handlers that gate behaviour on the caller kind.
func (i Identity) IsAdmin() bool  { return i.Kind == IdentityAdmin }
func (i Identity) IsDevice() bool { return i.Kind == IdentityDevice }
```

```go
// internal/auth/context.go (additions, not a new file)

type identityKey struct{}
type deviceKey struct{}

func ContextWithIdentity(ctx context.Context, id *Identity) context.Context
func IdentityFromContext(ctx context.Context) *Identity

func ContextWithDevice(ctx context.Context, d *Device) context.Context
func DeviceFromContext(ctx context.Context) *Device
```

The existing `ContextWithUser` / `UserFromContext` continue to work -- `RequireAuth` populates them when the identity is admin so handlers that already read the user keep working.

## API Contract

### Endpoints

| Method | Path | Request | Response | Auth |
|--------|------|---------|----------|------|
| GET    | /admin/devices | - | HTML device list | admin |
| POST   | /admin/devices | form: name, _csrf | HTML page showing raw token | admin |
| POST   | /admin/devices/{id}/revoke | form: _csrf | 302 -> /admin/devices | admin |

No new public API endpoints. Devices use existing endpoints (their first consumer is the Phase 2 Screen Display spec, which will register routes that sit behind `RequireAuth` and gate on `Identity.IsDevice()`).

### Request/Response Examples

Successful device creation -- the raw token is visible *once* in the response HTML:

```html
<div class="card" role="alert">
  <h2>Device created</h2>
  <p>Save this token now. It will not be displayed again.</p>
  <pre><code>kitchen-tablet token: 9f2a...d41c</code></pre>
</div>
```

Device authenticating to a Phase-2 protected route:

```http
GET /screen HTTP/1.1
Host: dashboard.example.com
Authorization: Bearer 9f2a3c8b...d41c
Accept: text/html
```

Failing auth on a device-flavoured request:

```http
HTTP/1.1 401 Unauthorized
Content-Type: text/plain; charset=utf-8
WWW-Authenticate: Bearer

unauthenticated
```

## Component Design

### Package Layout

```
internal/
  auth/
    device.go            -- NEW: Device type, helpers
    identity.go          -- NEW: Identity, IdentityKind
    context.go           -- MODIFY: add Identity / Device context helpers
    auth.go              -- MODIFY: add CreateDevice, ValidateDeviceToken,
                                    RevokeDevice, ListDevices, MarkDeviceSeen
  middleware/
    session.go           -- MODIFY: RequireAuth becomes the unified middleware
                                    (admin session OR device token)
    csrf.go              -- MODIFY: skip CSRF when identity is a device
    require_role.go      -- (no changes needed; relies on UserFromContext
                            which is only set for admins)
    require_device.go    -- NEW: assert caller is a device
  config/
    config.go            -- MODIFY: DeviceCookieName, DeviceLastSeenInterval
  db/
    migrations/
      005_create-devices.sql       -- NEW
    queries/
      devices.sql                  -- NEW (sqlc query file)
    devices.sql.go                 -- NEW (sqlc-generated)
views/
  devices.go             -- NEW: list / create / revoke handlers
  devices.templ          -- NEW: management page
  routes.go              -- MODIFY: register /admin/devices routes inside
                                    the admin sub-mux (RequireRole(admin))
main.go                 -- MODIFY: pass DeviceCookieName / interval into
                                   the auth service and middleware wiring
```

### Key Interfaces and Functions

#### internal/auth/auth.go (additions)

```go
// ErrDeviceNotFound is returned when an operation targets a device id that
// does not exist or is already revoked.
var ErrDeviceNotFound = errors.New("device not found")

// ErrDeviceRevoked is returned by ValidateDeviceToken when the token belongs
// to a device that has been revoked.
var ErrDeviceRevoked = errors.New("device revoked")

// CreateDevice provisions a new device. It generates a random token,
// stores its SHA-256 hash, and returns the new Device alongside the raw
// token. The caller MUST surface the raw token to the admin once and then
// discard it -- it cannot be recovered later.
func (s *Service) CreateDevice(ctx context.Context, name, createdBy string) (Device, string, error)

// ValidateDeviceToken hashes the raw token, looks it up, and returns the
// associated Device on success. Returns ErrDeviceNotFound for unknown tokens
// and ErrDeviceRevoked for revoked devices.
func (s *Service) ValidateDeviceToken(ctx context.Context, rawToken string) (*Device, error)

// MarkDeviceSeen updates last_seen_at on the device, but only if the previous
// last_seen_at is older than the configured throttle interval. Safe to call
// on every successful auth.
func (s *Service) MarkDeviceSeen(ctx context.Context, deviceID string) error

// RevokeDevice marks the device as revoked. Returns ErrDeviceNotFound if no
// device with the given id exists.
func (s *Service) RevokeDevice(ctx context.Context, deviceID string) error

// ListDevices returns all devices, including revoked ones (the UI can choose
// what to show).
func (s *Service) ListDevices(ctx context.Context) ([]Device, error)
```

The throttle for `MarkDeviceSeen` lives in `Service` config:

```go
// Config (additions)
type Config struct {
    AdminEmail              string
    SessionDuration         time.Duration
    CookieName              string
    SecureCookie            bool
    DeviceCookieName        string        // NEW
    DeviceLastSeenInterval  time.Duration // NEW
}
```

`MarkDeviceSeen` does:

```sql
UPDATE devices
   SET last_seen_at = datetime('now')
 WHERE id = ?
   AND (last_seen_at IS NULL OR last_seen_at < datetime('now', ?))
```

with the interval passed as `'-1 minutes'` (or whatever the configured throttle is) so the work happens entirely in the database; the auth path issues exactly one statement and either updates 0 or 1 rows.

#### internal/auth/device.go

```go
package auth

import (
    "time"

    "github.com/jasoncorbett/screens/internal/db"
)

type Device struct {
    ID         string
    Name       string
    TokenHash  string
    CreatedBy  string
    CreatedAt  time.Time
    LastSeenAt *time.Time
    RevokedAt  *time.Time
}

func (d Device) IsRevoked() bool { return d.RevokedAt != nil }

// deviceFromRow translates the sqlc-generated row into the domain type.
func deviceFromRow(row db.Device) (Device, error)
```

#### internal/middleware/session.go (rewritten)

```go
// RequireAuth returns middleware that authenticates the request using either
// an admin session cookie or a device bearer token (header or cookie).
//
// Order of probes:
//   1. admin session cookie (fast path: humans clicking around the UI)
//   2. Authorization: Bearer <token>
//   3. device cookie
//
// On success, an *auth.Identity is injected into the request context.
// Admin identities ALSO have the user injected via auth.ContextWithUser
// for backwards compatibility with handlers that read UserFromContext.
//
// On failure, the response depends on the request's intent:
//   - HTML navigation (GET/HEAD with Accept containing text/html): 302 to loginURL.
//   - Anything else: 401 Unauthorized with WWW-Authenticate: Bearer.
func RequireAuth(authService *auth.Service, sessionCookie, deviceCookie, loginURL string) func(http.Handler) http.Handler
```

Pseudo-implementation:

```go
func RequireAuth(svc *auth.Service, sessionCookie, deviceCookie, loginURL string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // 1. admin session
            if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
                if user, sess, vErr := svc.ValidateSession(r.Context(), c.Value); vErr == nil {
                    id := &auth.Identity{Kind: auth.IdentityAdmin, User: user}
                    ctx := auth.ContextWithUser(r.Context(), user)
                    ctx = auth.ContextWithSession(ctx, sess)
                    ctx = auth.ContextWithIdentity(ctx, id)
                    next.ServeHTTP(w, r.WithContext(ctx))
                    return
                }
                // fall through to device probe; clear bad session cookie at the end if both fail.
            }

            // 2. bearer header
            if raw := bearerToken(r); raw != "" {
                if dev, err := svc.ValidateDeviceToken(r.Context(), raw); err == nil {
                    finishDevice(w, r, svc, next, dev)
                    return
                }
            }

            // 3. device cookie
            if c, err := r.Cookie(deviceCookie); err == nil && c.Value != "" {
                if dev, err := svc.ValidateDeviceToken(r.Context(), c.Value); err == nil {
                    finishDevice(w, r, svc, next, dev)
                    return
                }
            }

            // No valid credential.
            slog.Info("auth failed", "kind", "none", "path", r.URL.Path)
            denyUnauthenticated(w, r, sessionCookie, loginURL)
        })
    }
}

func bearerToken(r *http.Request) string {
    h := r.Header.Get("Authorization")
    const prefix = "Bearer "
    if !strings.HasPrefix(h, prefix) {
        return ""
    }
    return strings.TrimSpace(h[len(prefix):])
}

func denyUnauthenticated(w http.ResponseWriter, r *http.Request, sessionCookie, loginURL string) {
    // Clear stale session cookie so the browser stops sending it.
    http.SetCookie(w, &http.Cookie{
        Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
    })

    if isHTMLNav(r) {
        http.Redirect(w, r, loginURL, http.StatusFound)
        return
    }
    w.Header().Set("WWW-Authenticate", "Bearer")
    http.Error(w, "unauthenticated", http.StatusUnauthorized)
}

func isHTMLNav(r *http.Request) bool {
    if r.Method != http.MethodGet && r.Method != http.MethodHead {
        return false
    }
    return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func finishDevice(w http.ResponseWriter, r *http.Request, svc *auth.Service, next http.Handler, dev *auth.Device) {
    _ = svc.MarkDeviceSeen(r.Context(), dev.ID) // best-effort; throttled in DB.
    id := &auth.Identity{Kind: auth.IdentityDevice, Device: dev}
    ctx := auth.ContextWithDevice(r.Context(), dev)
    ctx = auth.ContextWithIdentity(ctx, id)
    next.ServeHTTP(w, r.WithContext(ctx))
}
```

#### internal/middleware/csrf.go (modification)

```go
func RequireCSRF() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Safe methods: pass through.
            switch r.Method {
            case http.MethodGet, http.MethodHead, http.MethodOptions:
                next.ServeHTTP(w, r); return
            }

            // Devices use bearer tokens, which browsers will not auto-attach
            // cross-site, so CSRF does not apply.
            if id := auth.IdentityFromContext(r.Context()); id != nil && id.IsDevice() {
                next.ServeHTTP(w, r); return
            }

            // ... existing admin-session CSRF check unchanged ...
        })
    }
}
```

#### internal/middleware/require_device.go (new)

```go
func RequireDevice() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            id := auth.IdentityFromContext(r.Context())
            if id == nil || !id.IsDevice() {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

#### views/devices.go and views/devices.templ

Three handlers register inside the admin sub-mux (already gated by `RequireRole(RoleAdmin)`):

```go
mux.HandleFunc("GET /admin/devices",                 handleDeviceList(deps.Auth))
mux.HandleFunc("POST /admin/devices",                handleDeviceCreate(deps.Auth))
mux.HandleFunc("POST /admin/devices/{id}/revoke",    handleDeviceRevoke(deps.Auth))
```

The list handler retrieves `auth.Service.ListDevices(ctx)` and a flash message from the URL. The create handler:
1. Reads and trims `name` from the form.
2. Rejects empty names with a redirect carrying `?error=Name+is+required`.
3. Calls `authSvc.CreateDevice(ctx, name, currentUser.ID)`.
4. Renders the list page with a one-time `revealedToken` parameter so the templ component can show it once. (Deliberately not put in the URL: tokens MUST NOT appear in browser history or server logs. The handler holds the value in a function-local variable and renders it inline.)

The templ adds a "newly created" card that shows the raw token only when the handler passes one in:

```go
templ devicesPage(devices []auth.Device, currentUser *auth.User, csrfToken string,
    msg, errMsg, newName, newToken string) {
    @layout("Devices - screens") {
        if newToken != "" {
            <div class="card" role="alert">
                <h2>Device "{ newName }" created</h2>
                <p><strong>Save this token now.</strong> It will not be displayed again.</p>
                <pre><code>{ newToken }</code></pre>
            </div>
        }
        // ... list with name, last-seen, revoke button, csrf hidden field ...
    }
}
```

### Dependencies Between Components

```
main.go
  config.Load()                         -- DeviceCookieName, DeviceLastSeenInterval (NEW)
  auth.NewService(sqlDB, cfg)           -- the Service grows three new methods
  middleware.RequireAuth(svc, session, device, loginURL)  -- now accepts both cookie names
  middleware.RequireCSRF()              -- consults IdentityFromContext to skip on device
  views.AddRoutes(mux, deps)            -- deps gains DeviceCookieName
```

### main.go Wiring Changes

```go
authSvc := auth.NewService(sqlDB, auth.Config{
    AdminEmail:             cfg.Auth.AdminEmail,
    SessionDuration:        cfg.Auth.SessionDuration,
    CookieName:             cfg.Auth.CookieName,
    SecureCookie:           !cfg.Log.DevMode,
    DeviceCookieName:       cfg.Auth.DeviceCookieName,        // NEW
    DeviceLastSeenInterval: cfg.Auth.DeviceLastSeenInterval,  // NEW
})

views.AddRoutes(mux, &views.Deps{
    Auth:             authSvc,
    Google:           googleClient,
    ClientID:         cfg.Auth.GoogleClientID,
    CookieName:       cfg.Auth.CookieName,
    DeviceCookieName: cfg.Auth.DeviceCookieName, // NEW
    SecureCookie:     !cfg.Log.DevMode,
})
```

The signature of `middleware.RequireAuth` changes from
`RequireAuth(svc, cookieName, loginURL)` to
`RequireAuth(svc, sessionCookie, deviceCookie, loginURL)`. Existing call sites in `views/routes.go` are updated to thread through the new parameter.

## Storage

### sqlc Queries (internal/db/queries/devices.sql)

```sql
-- name: CreateDevice :exec
INSERT INTO devices (id, name, token_hash, created_by)
VALUES (?, ?, ?, ?);

-- name: GetDeviceByTokenHash :one
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
WHERE token_hash = ?;

-- name: GetDeviceByID :one
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
WHERE id = ?;

-- name: ListDevices :many
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at
FROM devices
ORDER BY created_at;

-- name: RevokeDevice :exec
UPDATE devices SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL;

-- name: TouchDeviceSeen :execresult
UPDATE devices
   SET last_seen_at = datetime('now')
 WHERE id = ?
   AND (last_seen_at IS NULL OR last_seen_at < datetime('now', ?));
```

After adding the file, `sqlc generate` produces `internal/db/devices.sql.go` with the corresponding methods on `*db.Queries`. The generated `db.Device` struct adopts the table's nullable columns as `sql.NullString` (or equivalent), which `deviceFromRow` translates into `*time.Time` for the domain type.

### Migration Numbering

Existing migrations: `001`, `002`, `003`, `004`. This spec adds `005_create-devices.sql`. The numbering is monotonic and unique.

## Security Considerations

### Token Strength

- 32 bytes from `crypto/rand` (256 bits), hex-encoded to 64 characters. Same primitive as `auth.GenerateToken` used for sessions, no need to invent a new one.
- The `UNIQUE` constraint on `token_hash` makes a collision -- already astronomically improbable -- a guaranteed hard error rather than a silent partition.

### Token Storage

- Only the SHA-256 hash is persisted. A database leak does not yield usable tokens.
- The raw token is returned only in the HTTP response that creates the device. The handler holds it in a function-local variable, hands it to the templ render call, and lets it leave scope. It is never written to a log line, an audit table, or a flash cookie.

### Token Comparison

- The lookup is by the hash (a primary-key index lookup), so equality is unambiguous and constant-time at the SQLite engine level.
- `auth.HashToken` already uses `sha256.Sum256` followed by `hex.EncodeToString`; we reuse it directly.

### Authorization Header Parsing

- The middleware accepts only the case-sensitive prefix `Bearer ` (capital B). Any other scheme (`Basic `, `Token `, lowercase `bearer `) is treated as no credential at all. This matches typical Bearer behaviour and avoids accidental scheme confusion.
- Empty bearer values (`Authorization: Bearer ` or `Authorization: Bearer    `) are rejected by `strings.TrimSpace` returning empty.
- Malformed headers do not panic.

### CSRF

- Device requests are authenticated by a header that the browser will NOT auto-attach cross-site. CSRF protection therefore does not apply to them, and the CSRF middleware exempts them after looking up the identity. Admin sessions remain fully CSRF-protected.

### Cookies

- The device cookie is `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when not in dev mode. Same hygiene as the admin session.
- Setting the device cookie is OUT of scope of this spec (the device is configured out-of-band -- a screen has the token written to its config). The middleware merely reads the cookie if present.

### Fail-Closed

- A database error during validation is treated as no credential present (returns 401 / 302 like an unauthenticated request). The middleware never silently grants access on an error.
- A revoked device's `revoked_at != NULL` check happens in Go (after the row is fetched) rather than via `WHERE revoked_at IS NULL` in the SQL: this lets the middleware return `ErrDeviceRevoked` separately from `ErrDeviceNotFound` for clearer logging.

### Logging

- Auth failures log one info-level slog line with `kind` (none/admin/device) and a sanitised reason. The line never includes the raw token, the cookie value, or the Authorization header.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-010.

1. **TASK-011**: Device config, migration, and sqlc queries -- (prerequisite: none).
2. **TASK-012**: Device service methods on `auth.Service` (CreateDevice, ValidateDeviceToken, RevokeDevice, ListDevices, MarkDeviceSeen) plus `Device`, `Identity` types and context helpers -- (prerequisite: TASK-011).
3. **TASK-013**: Unified `RequireAuth` middleware, CSRF exemption for devices, and `RequireDevice` middleware -- (prerequisite: TASK-012).
4. **TASK-014**: Device management views (list, create with one-time-token reveal, revoke), wiring through admin-only routes, plus main.go updates -- (prerequisite: TASK-013).

### Task Dependency Graph

```
TASK-011 (config + migration + sqlc)
    |
    v
TASK-012 (auth.Service device methods,
          Device + Identity types,
          context helpers)
    |
    v
TASK-013 (unified RequireAuth,
          CSRF device exemption,
          RequireDevice)
    |
    v
TASK-014 (admin views: list / create / revoke,
          routes.go + main.go wiring)
```

The dependency chain is strictly linear because each step depends on the previous step's exported surface area. There is no parallelism worth pursuing for this small spec.

## Alternatives Considered

See ADR-003 for the full design decision rationale. Highlights:

- **Two parallel middleware stacks (admin auth + device auth)**: rejected. Every protected endpoint would need to be wrapped with the right one, route registration would fork by caller kind, and helpers like `RequireRole` would have to know whether to look up a user or a device. A single `RequireAuth` plus a typed `Identity` keeps the boundary tidy and matches the "one mux, one wiring step" project ethos.
- **JWT for devices**: rejected. JWTs are not free to revoke (they need a deny-list, which means we are back to a database lookup). We already have a database; storing the token hash there is simpler, smaller cookies, and has no key-rotation foot-guns.
- **Self-registration ("device hits an enrolment endpoint with a one-time PIN")**: rejected for v1. A household admin can paste a token. PIN enrolment is more code, more failure modes, and not necessary for the household scale.
- **Update `last_seen_at` on every authentication**: rejected. A screen polling every 5 seconds would issue 17,000 writes per day per device. The throttle (default 1m) cuts that to ~1,400 writes per day per device, almost all of them no-op `UPDATE` statements that match zero rows. Configurable in case the dashboard becomes more chatty in the future.
- **Storing the raw token in a flash cookie so the admin can re-view it on the next page**: rejected. The whole point of "shown exactly once" is that a screenshot or a casual reload cannot leak the token. Holding it in a server-side flash store breaks that promise.
- **Encrypting tokens at rest with a server-side key**: rejected. SHA-256 hashing already guarantees that a database leak does not expose usable tokens, and unlike encryption it has no key to lose.
