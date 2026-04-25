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

This architecture adds a second identity kind -- device -- alongside the existing admin user identity, and unifies both behind a single `RequireAuth` middleware. Devices are pre-provisioned by admins; provisioning issues a 256-bit random token, hashes it with SHA-256, and stores only the hash. The raw token is shown to the admin once. Devices authenticate with `Authorization: Bearer <token>` (or, for browser page loads, a cookie). The unified middleware probes for an admin session first, falls back to the device token, and injects an `auth.Identity` value into the request context that handlers can branch on. Revocation is a single column update that takes effect on the next request.

Because the dominant deployment target is a wall-mounted kiosk browser that has no convenient way to receive a cookie out-of-band, this architecture also includes a **browser enrollment** flow. An admin walks up to the kiosk, signs into the admin URL on its browser, opens the device management page, and clicks a single button that converts the kiosk's browser into a device: the server clears the admin session cookie on that browser, deletes the matching session row from the database, sets the device cookie, and redirects to a device landing URL. The admin's other sessions (on their laptop, phone, etc.) are untouched -- only the row whose cookie the kiosk was sending is removed.

Reuses the existing `auth.GenerateToken`, `auth.HashToken`, `auth.Service.Logout`, sqlc query pattern, and migration runner from SPEC-001 / SPEC-002.

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
| POST   | /admin/devices/{id}/enroll-browser | form: _csrf | 302 -> /device/ + Set-Cookie swap | admin |
| POST   | /admin/devices/enroll-new-browser | form: name, _csrf | 302 -> /device/ + Set-Cookie swap | admin |
| GET    | /device/ | - | HTML placeholder showing device name | device (or admin) |

Devices use existing endpoints once enrolled (their first content consumer is the Phase 2 Screen Display spec, which will register additional routes that sit behind `RequireAuth` and gate on `Identity.IsDevice()`). The `/device/` route is registered in Phase 1 as a placeholder so the enrollment flow has somewhere to land; Phase 2 will replace its body but keep the URL.

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

## Browser Enrollment Flow

This is the in-person flow that converts a wall-mounted kiosk's browser from "currently signed in as the admin" into "is now the device". It is the primary mechanism by which a device cookie ends up on a real kiosk.

### Sequence

```
Kiosk Browser            Screens Service               Admin User
     |                          |                           |
     |  (admin walks up to kiosk, opens admin URL)          |
     |                          |                           |
     |-- GET /admin/devices --->|                           |
     |   (no cookie yet)        |                           |
     |<-- 302 /admin/login -----|                           |
     |                          |                           |
     |   ... Google OAuth dance ...                         |
     |                          |                           |
     |<-- 302 /admin/ -----------|                          |
     |   Set-Cookie:             |                           |
     |     screens_session=ADMIN_TOKEN                       |
     |                          |                           |
     |-- GET /admin/devices --->|                           |
     |   Cookie: screens_session=ADMIN_TOKEN                 |
     |<-- HTML: device list ----|                           |
     |   + "Enroll this browser as <select existing>"        |
     |   + "Create new device and enroll this browser:"      |
     |     [name input] [submit]                             |
     |                          |                           |
     |   (admin clicks Enroll)  |                           |
     |                          |                           |
     |-- POST /admin/devices/{id}/enroll-browser ---------->|
     |   Cookie: screens_session=ADMIN_TOKEN                 |
     |   Form: _csrf=...                                     |
     |                          |                           |
     |                          | 1. RequireAuth            |
     |                          |    -> Identity{Admin}     |
     |                          | 2. RequireRole(admin) OK  |
     |                          | 3. RequireCSRF OK         |
     |                          | 4. handler:               |
     |                          |    a. Lookup target device|
     |                          |       (404 if missing,    |
     |                          |        302 error if       |
     |                          |        revoked).          |
     |                          |    b. Generate new raw    |
     |                          |       token + hash.       |
     |                          |    c. UPDATE devices SET  |
     |                          |         token_hash=...    |
     |                          |       WHERE id=?          |
     |                          |    d. authSvc.Logout(     |
     |                          |         ctx, ADMIN_TOKEN  |
     |                          |       )  -- deletes the   |
     |                          |       row from sessions.  |
     |                          |    e. Set-Cookie:         |
     |                          |       screens_session=;   |
     |                          |       Max-Age=-1          |
     |                          |       (clear admin cookie)|
     |                          |    f. Set-Cookie:         |
     |                          |       screens_device=NEW; |
     |                          |       HttpOnly;SameSite=  |
     |                          |       Lax;Secure(prod)    |
     |                          |    g. Redirect 302 to     |
     |                          |       cfg.DeviceLandingURL|
     |<-- 302 /device/ ---------|                           |
     |   Set-Cookie: screens_session=; Max-Age=-1           |
     |   Set-Cookie: screens_device=NEW_TOKEN; HttpOnly...  |
     |                          |                           |
     |-- GET /device/ --------->|                           |
     |   Cookie: screens_device=NEW_TOKEN                    |
     |   (admin cookie was just cleared, never sent)        |
     |                          |                           |
     |                          | RequireAuth probes:       |
     |                          |   1. session cookie -- gone
     |                          |   2. bearer header -- none
     |                          |   3. device cookie -- HIT |
     |                          | -> Identity{Device}       |
     |                          |                           |
     |<-- 200 HTML "This browser is enrolled as <name>..."  |
     |                          |                           |
     |   (kiosk is now permanently the device.              |
     |    A reload re-enters /device/ as the device.        |
     |    The admin walks back to their laptop -- their     |
     |    other admin sessions are unaffected because we    |
     |    only deleted the row matching the kiosk's cookie.)|
```

### Cookie Mutations

The enrollment handler MUST emit exactly two `Set-Cookie` headers in addition to the 302 redirect:

```
Set-Cookie: screens_session=; Path=/; Max-Age=-1; HttpOnly
Set-Cookie: screens_device=<RAW_TOKEN>; Path=/; HttpOnly; SameSite=Lax; Secure
```

(`Secure` is included when `!cfg.Log.DevMode`, mirroring the admin session cookie hygiene from SPEC-002.)

The order matters slightly: the session-clearing cookie must be present in the response so the kiosk's browser drops it; the device cookie must be present so the next request authenticates. Both attributes use `Path=/` so they cover the full app surface.

### Database Mutations

Two writes happen in sequence; both must succeed for the swap to complete:

1. `UPDATE devices SET token_hash = ? WHERE id = ? AND revoked_at IS NULL` -- replaces the existing token (if any) with the freshly issued one. `RowsAffected == 0` means the device was revoked between the lookup and the update; the handler MUST treat this as a race-condition failure, abort, and 302 with an error flash. The admin session cookie MUST NOT be cleared in this case.
2. `auth.Service.Logout(ctx, adminRawToken)` -- existing method, deletes one row from `sessions` keyed by hash. Reuses the SPEC-002 surface; no new SQL.

If step 2 fails (database error), the handler MUST log `slog.Error` and still proceed with the cookie swap and redirect. The orphaned session row will be cleaned up by `CleanExpiredSessions` eventually; the user-visible behaviour (browser is now the device) is still correct.

### Endpoint Variants

Two POST endpoints implement the user-facing flows:

- `POST /admin/devices/{id}/enroll-browser` -- "enroll this browser as the existing device named X". The handler looks up `{id}`, runs the swap.
- `POST /admin/devices/enroll-new-browser` -- "create device named X and enroll this browser as it". The handler calls `authSvc.CreateDevice(ctx, name, currentUser.ID)` first, then runs the same swap on the freshly created device id.

Both endpoints share an internal helper:

```go
// performBrowserEnrollment swaps cookies and redirects. It MUST be called
// only after RequireAuth + RequireRole(RoleAdmin) + RequireCSRF have
// succeeded for the request, and only with a target device that is
// non-revoked. It calls authSvc.RotateDeviceToken (or equivalent: a method
// that issues a new raw token, updates the row, and returns the raw token),
// authSvc.Logout for the admin session, sets two cookies, and writes a 302.
func performBrowserEnrollment(
    w http.ResponseWriter,
    r *http.Request,
    authSvc *auth.Service,
    deps *Deps,
    deviceID string,
) error
```

### New Service Method

`auth.Service` gains one new method to support enrollment without breaking the "token shown exactly once" invariant for the create flow:

```go
// RotateDeviceToken generates a fresh random token for the given device,
// stores its hash (replacing any prior hash), and returns the raw token.
// Returns ErrDeviceNotFound if the device id is unknown or already revoked
// (the handler MUST treat both cases identically: refuse to swap cookies).
// The returned raw token is the only opportunity for the caller to capture
// it; it is not persisted in plaintext anywhere.
func (s *Service) RotateDeviceToken(ctx context.Context, deviceID string) (string, error)
```

Implementation:

```go
func (s *Service) RotateDeviceToken(ctx context.Context, deviceID string) (string, error) {
    rawToken, err := GenerateToken()
    if err != nil {
        return "", fmt.Errorf("generate device token: %w", err)
    }
    res, err := s.queries.RotateDeviceToken(ctx, db.RotateDeviceTokenParams{
        TokenHash: HashToken(rawToken),
        ID:        deviceID,
    })
    if err != nil {
        return "", fmt.Errorf("rotate device token: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return "", fmt.Errorf("rotate device token rows: %w", err)
    }
    if n == 0 {
        return "", ErrDeviceNotFound
    }
    return rawToken, nil
}
```

The corresponding sqlc query (added to `internal/db/queries/devices.sql`):

```sql
-- name: RotateDeviceToken :execresult
UPDATE devices
   SET token_hash = ?
 WHERE id = ?
   AND revoked_at IS NULL;
```

`RowsAffected == 0` cleanly distinguishes "no such device" and "device revoked" from "row was updated" without a second SELECT, and the database-side `WHERE revoked_at IS NULL` clause guarantees that we cannot accidentally rotate a revoked device's token.

### Device Landing URL Handler

A minimal templ page lives at `views/device.templ` and is wired at `cfg.Auth.DeviceLandingURL` (default `/device/`). Its handler:

```go
func handleDeviceLanding(authSvc *auth.Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id := auth.IdentityFromContext(r.Context())
        if id == nil {
            // RequireAuth admitted us, but the identity is somehow missing.
            // Fail closed.
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        var name string
        switch {
        case id.IsDevice() && id.Device != nil:
            name = id.Device.Name
        case id.IsAdmin() && id.User != nil:
            // An admin can hit this URL too (e.g., navigating directly).
            // Show a friendly message that this is the device landing page.
            name = "(viewing as admin: " + id.User.Email + ")"
        default:
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        deviceLandingPage(name).Render(r.Context(), w)
    }
}
```

The route is registered OUTSIDE the admin sub-mux but INSIDE the `RequireAuth` wrapper (no `RequireRole`, no CSRF -- it's a GET). Concretely, the routes.go change is to move the unified `RequireAuth` chain to wrap a parent mux that includes both `/admin/` and `/device/`:

```go
// Authenticated mux: anything that requires either an admin or a device.
authedMux := http.NewServeMux()
authedMux.Handle("/admin/", adminCSRFChain(adminMux))
authedMux.HandleFunc("GET "+cfg.Auth.DeviceLandingURL, handleDeviceLanding(deps.Auth))

// Wrap once with RequireAuth.
mux.Handle("/", middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(authedMux))
```

(See "main.go Wiring Changes" below for the full updated wiring.)

## Component Design

### Package Layout

```
internal/
  auth/
    device.go            -- NEW: Device type, helpers
    identity.go          -- NEW: Identity, IdentityKind
    context.go           -- MODIFY: add Identity / Device context helpers
    auth.go              -- MODIFY: add CreateDevice, ValidateDeviceToken,
                                    RevokeDevice, ListDevices, MarkDeviceSeen,
                                    RotateDeviceToken
  middleware/
    session.go           -- MODIFY: RequireAuth becomes the unified middleware
                                    (admin session OR device token)
    csrf.go              -- MODIFY: skip CSRF when identity is a device
    require_role.go      -- (no changes needed; relies on UserFromContext
                            which is only set for admins)
    require_device.go    -- NEW: assert caller is a device
  config/
    config.go            -- MODIFY: DeviceCookieName, DeviceLastSeenInterval,
                                    DeviceLandingURL
  db/
    migrations/
      005_create-devices.sql       -- NEW
    queries/
      devices.sql                  -- NEW (sqlc query file, includes
                                          RotateDeviceToken)
    devices.sql.go                 -- NEW (sqlc-generated)
views/
  devices.go             -- NEW: list / create / revoke + enroll-browser +
                                 enroll-new-browser handlers
  devices.templ          -- NEW: management page (also renders the
                                 "Enroll this browser as <X>" controls)
  device.go              -- NEW: device landing handler
  device.templ           -- NEW: device landing placeholder page
  routes.go              -- MODIFY: register /admin/devices routes inside
                                    the admin sub-mux (RequireRole(admin)),
                                    register /device/ landing route under
                                    RequireAuth only
main.go                 -- MODIFY: pass DeviceCookieName / interval /
                                   landing URL into the auth service,
                                   middleware, and view deps
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

// RotateDeviceToken issues a fresh raw token for the given device, replacing
// any existing token_hash. Returns the new raw token. Returns ErrDeviceNotFound
// if the device does not exist or has been revoked. The handler that calls
// this is the only opportunity to capture the raw token; it is not persisted
// in plaintext anywhere.
func (s *Service) RotateDeviceToken(ctx context.Context, deviceID string) (string, error)
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
    DeviceLandingURL        string        // NEW (e.g. "/device/")
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

Five handlers register inside the admin sub-mux (already gated by `RequireRole(RoleAdmin)`):

```go
mux.HandleFunc("GET /admin/devices",                          handleDeviceList(deps.Auth))
mux.HandleFunc("POST /admin/devices",                         handleDeviceCreate(deps.Auth))
mux.HandleFunc("POST /admin/devices/{id}/revoke",             handleDeviceRevoke(deps.Auth))
mux.HandleFunc("POST /admin/devices/{id}/enroll-browser",     handleDeviceEnrollExisting(deps))
mux.HandleFunc("POST /admin/devices/enroll-new-browser",      handleDeviceEnrollNew(deps))
```

The two enroll handlers take `*Deps` (not just `*auth.Service`) because they need the device cookie name and the secure-cookie flag to set the device cookie correctly.

The list handler retrieves `auth.Service.ListDevices(ctx)` and a flash message from the URL. The create handler:
1. Reads and trims `name` from the form.
2. Rejects empty names with a redirect carrying `?error=Name+is+required`.
3. Calls `authSvc.CreateDevice(ctx, name, currentUser.ID)`.
4. Renders the list page with a one-time `revealedToken` parameter so the templ component can show it once. (Deliberately not put in the URL: tokens MUST NOT appear in browser history or server logs. The handler holds the value in a function-local variable and renders it inline.)

The templ adds a "newly created" card that shows the raw token only when the handler passes one in, plus an "Enroll this browser" form group:

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

        <section>
            <h2>Enroll This Browser as a Device</h2>
            <p>Use this when standing at a wall display: it will sign you out
               of the admin session on THIS browser only and set this browser
               as the named device.</p>

            <form method="POST" action="/admin/devices/enroll-new-browser">
                <input type="hidden" name="_csrf" value={ csrfToken } />
                <label>Device name <input type="text" name="name" required /></label>
                <button type="submit">Create new device and enroll this browser</button>
            </form>

            if len(devices) > 0 {
                <p>Or enroll this browser as an existing un-revoked device:</p>
                <ul>
                    for _, d := range devices {
                        if !d.IsRevoked() {
                            <li>
                                <form method="POST" action={ templ.SafeURL("/admin/devices/" + d.ID + "/enroll-browser") }>
                                    <input type="hidden" name="_csrf" value={ csrfToken } />
                                    <button type="submit">Enroll this browser as "{ d.Name }"</button>
                                </form>
                            </li>
                        }
                    }
                </ul>
            }
        </section>

        // ... list with name, last-seen, revoke button, csrf hidden field ...
    }
}
```

The enroll handler bodies are small and share the cookie-swap helper:

```go
func handleDeviceEnrollExisting(deps *Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        deviceID := r.PathValue("id")
        if deviceID == "" {
            http.Redirect(w, r, "/admin/devices?error=Missing+device+ID", http.StatusFound)
            return
        }
        if err := performBrowserEnrollment(w, r, deps, deviceID); err != nil {
            slog.Info("enroll-browser rejected", "device_id", deviceID, "err", err)
            http.Redirect(w, r, "/admin/devices?error="+url.QueryEscape(err.Error()), http.StatusFound)
            return
        }
        // performBrowserEnrollment wrote the redirect on success.
    }
}

func handleDeviceEnrollNew(deps *Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        currentUser := auth.UserFromContext(ctx)
        if currentUser == nil {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        name := strings.TrimSpace(r.FormValue("name"))
        if name == "" {
            http.Redirect(w, r, "/admin/devices?error=Name+is+required", http.StatusFound)
            return
        }
        dev, _, err := deps.Auth.CreateDevice(ctx, name, currentUser.ID)
        if err != nil {
            slog.Error("enroll-new-browser create device", "err", err)
            http.Redirect(w, r, "/admin/devices?error=Could+not+create+device", http.StatusFound)
            return
        }
        // Discard the raw token from CreateDevice -- we'll re-issue one in
        // performBrowserEnrollment via RotateDeviceToken so the same
        // helper handles both endpoints.
        if err := performBrowserEnrollment(w, r, deps, dev.ID); err != nil {
            slog.Error("enroll-new-browser swap", "device_id", dev.ID, "err", err)
            http.Redirect(w, r, "/admin/devices?error="+url.QueryEscape(err.Error()), http.StatusFound)
            return
        }
    }
}

// performBrowserEnrollment is the cookie-swap workhorse.
func performBrowserEnrollment(w http.ResponseWriter, r *http.Request, deps *Deps, deviceID string) error {
    ctx := r.Context()

    // Re-validate target device with the rotation; if revoked or missing,
    // ErrDeviceNotFound is returned and we abort BEFORE touching cookies.
    rawToken, err := deps.Auth.RotateDeviceToken(ctx, deviceID)
    if err != nil {
        if errors.Is(err, auth.ErrDeviceNotFound) {
            return errors.New("Device not found or revoked")
        }
        return errors.New("Could not enroll browser")
    }

    // Best-effort: delete the admin session row backing this request so we
    // don't leave an orphan in the database. We rely on the fact that the
    // admin's session cookie is still on this request -- RequireAuth read it.
    if c, cErr := r.Cookie(deps.CookieName); cErr == nil && c.Value != "" {
        if logoutErr := deps.Auth.Logout(ctx, c.Value); logoutErr != nil {
            slog.Error("enroll-browser logout admin session", "err", logoutErr)
            // Continue: the swap is more important than orphan cleanup.
        }
    }

    // Clear the admin session cookie on this browser only.
    http.SetCookie(w, &http.Cookie{
        Name: deps.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
        Expires: time.Unix(0, 0),
    })

    // Set the device cookie.
    http.SetCookie(w, &http.Cookie{
        Name:     deps.DeviceCookieName,
        Value:    rawToken,
        Path:     "/",
        HttpOnly: true,
        Secure:   deps.SecureCookie,
        SameSite: http.SameSiteLaxMode,
    })

    currentUser := auth.UserFromContext(ctx)
    enrolledBy := ""
    if currentUser != nil {
        enrolledBy = currentUser.Email
    }
    slog.Info("device enrolled via browser",
        "device_id", deviceID, "enrolled_by", enrolledBy)

    http.Redirect(w, r, deps.DeviceLandingURL, http.StatusFound)
    return nil
}
```

Ordering note: `RotateDeviceToken` runs FIRST. If it fails (device revoked / missing), we return the error before clearing any cookies, so a failed enrollment leaves the admin still signed in.

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
    DeviceLandingURL:       cfg.Auth.DeviceLandingURL,        // NEW
})

views.AddRoutes(mux, &views.Deps{
    Auth:             authSvc,
    Google:           googleClient,
    ClientID:         cfg.Auth.GoogleClientID,
    CookieName:       cfg.Auth.CookieName,
    DeviceCookieName: cfg.Auth.DeviceCookieName, // NEW
    DeviceLandingURL: cfg.Auth.DeviceLandingURL, // NEW
    SecureCookie:     !cfg.Log.DevMode,
})
```

The signature of `middleware.RequireAuth` changes from
`RequireAuth(svc, cookieName, loginURL)` to
`RequireAuth(svc, sessionCookie, deviceCookie, loginURL)`. Existing call sites in `views/routes.go` are updated to thread through the new parameter.

### views/routes.go Wiring Changes

The current registration wraps only the admin sub-mux in `RequireAuth`. After this change, both `/admin/` and the device landing URL share `RequireAuth` (admin or device identity is sufficient at the outer layer). `RequireRole(RoleAdmin)` continues to gate everything under `/admin/...` so device identities are 403'd from the admin surface, while the device landing handler accepts either kind:

```go
func registerAuthRoutes(mux *http.ServeMux, deps *Deps) {
    // Public routes (no auth required).
    mux.HandleFunc("GET /admin/login", handleLogin(deps.Auth, deps.CookieName))
    mux.HandleFunc("GET /auth/google/start", handleGoogleStart(deps.Google))
    mux.HandleFunc("GET /auth/google/callback", handleGoogleCallback(deps))

    // Admin sub-mux (requires admin role on top of RequireAuth).
    adminMux := http.NewServeMux()
    adminMux.HandleFunc("GET /admin/{$}", handleAdmin)
    adminMux.HandleFunc("POST /admin/logout", handleLogout(deps.Auth, deps.CookieName))

    // Existing user-management mux gated by RequireRole(admin).
    userMux := http.NewServeMux()
    userMux.HandleFunc("GET /admin/users", handleUserList(deps.Auth))
    userMux.HandleFunc("POST /admin/users/invite", handleInvite(deps.Auth))
    userMux.HandleFunc("POST /admin/users/{id}/deactivate", handleDeactivate(deps.Auth))
    userMux.HandleFunc("POST /admin/invitations/{id}/revoke", handleRevokeInvitation(deps.Auth))

    // Device-management mux gated by RequireRole(admin).
    deviceMux := http.NewServeMux()
    deviceMux.HandleFunc("GET  /admin/devices",                      handleDeviceList(deps.Auth))
    deviceMux.HandleFunc("POST /admin/devices",                      handleDeviceCreate(deps.Auth))
    deviceMux.HandleFunc("POST /admin/devices/{id}/revoke",          handleDeviceRevoke(deps.Auth))
    deviceMux.HandleFunc("POST /admin/devices/{id}/enroll-browser",  handleDeviceEnrollExisting(deps))
    deviceMux.HandleFunc("POST /admin/devices/enroll-new-browser",   handleDeviceEnrollNew(deps))

    adminMux.Handle("/admin/users",         middleware.RequireRole(auth.RoleAdmin)(userMux))
    adminMux.Handle("/admin/users/",        middleware.RequireRole(auth.RoleAdmin)(userMux))
    adminMux.Handle("/admin/invitations/",  middleware.RequireRole(auth.RoleAdmin)(userMux))
    adminMux.Handle("/admin/devices",       middleware.RequireRole(auth.RoleAdmin)(deviceMux))
    adminMux.Handle("/admin/devices/",      middleware.RequireRole(auth.RoleAdmin)(deviceMux))

    // Authenticated routes (admin OR device).
    authedMux := http.NewServeMux()
    authedMux.Handle("/admin/", middleware.RequireCSRF()(adminMux))
    authedMux.HandleFunc("GET "+deps.DeviceLandingURL, handleDeviceLanding(deps.Auth))

    mux.Handle("/admin/", middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
        // For the /admin/ subtree we still want CSRF; reuse the chain.
        middleware.RequireCSRF()(adminMux),
    ))

    // The device landing route lives outside /admin/ and uses RequireAuth only.
    landingHandler := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
        http.HandlerFunc(handleDeviceLanding(deps.Auth)),
    )
    mux.Handle("GET "+deps.DeviceLandingURL, landingHandler)
}
```

Note: the two `RequireAuth` instantiations are functionally identical and could be deduplicated by composing into a single outer mux that internally dispatches `/admin/` vs `/device/`. The implementer is free to refactor as long as the observable contract is preserved (admin-only routes still 403 device callers; the landing page still accepts either kind).

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

-- name: RotateDeviceToken :execresult
UPDATE devices
   SET token_hash = ?
 WHERE id = ?
   AND revoked_at IS NULL;
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
- Successful enrollments log one info-level slog line with `device_id` and `enrolled_by` (admin email). The raw device token is NEVER logged.

### Browser Enrollment

The browser-enrollment endpoints are the single most security-sensitive new addition:

- **POST only.** A GET-triggered enrollment would be a one-click downgrade attack: an attacker who can get an admin to click an `<a>` or trigger a navigation could convert the admin's current browser into a device, locking the admin out of the admin UI from that browser. The handlers are registered as `POST /admin/devices/...`; a GET to those paths does not match.
- **CSRF-protected.** The endpoints sit behind the existing `RequireCSRF` middleware in the admin chain. A cross-site form submission cannot trigger enrollment because the attacker cannot read the per-session `_csrf` token.
- **Admin-role-required.** `RequireRole(RoleAdmin)` gates the admin sub-mux, so a `member` cannot enroll a browser as a device. (Members can manage screens but cannot mutate the device fleet.)
- **Atomic-from-the-user-perspective.** `RotateDeviceToken` runs first; if it returns `ErrDeviceNotFound` (device missing or revoked), the handler aborts BEFORE clearing any cookies or deleting any session row. A failed enrollment leaves the admin still authenticated.
- **Session cleanup, not just cookie clearing.** The handler calls `auth.Service.Logout(ctx, adminRawToken)` to delete the session row from the database BEFORE clearing the cookie. If the call fails, we still clear the cookie -- the user-visible state is correct -- and the orphan is reaped by `CleanExpiredSessions` later. We do not want to leave a long-lived session row that nobody is authenticating against.
- **Scope of session deletion.** Only the row whose `token_hash` equals `HashToken(<the cookie value the kiosk sent>)` is deleted. Other admin sessions for the same user (e.g., on a laptop) are unaffected -- they have different `token_hash` values.
- **Token rotation, not reuse.** Even when enrolling against an existing device, the handler issues a NEW token. Any previously distributed token for that device becomes invalid (its hash is no longer the row's `token_hash`). This is a deliberate property: a kiosk that was enrolled, taken away, and re-enrolled does not retain a working old token.
- **The device cookie carries the raw token.** The same SHA-256 round-trip used for sessions applies: the cookie value is the raw token, the database stores only the hash, and the middleware compares hashes. A leaked database snapshot does not yield a usable cookie.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-010.

1. **TASK-011**: Device config, migration, and sqlc queries (now also includes `DEVICE_LANDING_URL` config and the `RotateDeviceToken` query) -- (prerequisite: none).
2. **TASK-012**: Device service methods on `auth.Service` (CreateDevice, ValidateDeviceToken, RevokeDevice, ListDevices, MarkDeviceSeen, RotateDeviceToken) plus `Device`, `Identity` types and context helpers -- (prerequisite: TASK-011).
3. **TASK-013**: Unified `RequireAuth` middleware, CSRF exemption for devices, and `RequireDevice` middleware -- (prerequisite: TASK-012).
4. **TASK-014**: Device management views (list, create with one-time-token reveal, revoke) and admin landing-page link -- (prerequisite: TASK-013).
5. **TASK-015**: Browser-enrollment endpoints, device landing URL handler, route wiring, main.go updates -- (prerequisite: TASK-014).

### Task Dependency Graph

```
TASK-011 (config + migration + sqlc,
          incl. RotateDeviceToken query
          and DEVICE_LANDING_URL)
    |
    v
TASK-012 (auth.Service device methods
          incl. RotateDeviceToken,
          Device + Identity types,
          context helpers)
    |
    v
TASK-013 (unified RequireAuth,
          CSRF device exemption,
          RequireDevice)
    |
    v
TASK-014 (admin views: list / create / revoke)
    |
    v
TASK-015 (browser enrollment endpoints,
          device landing handler,
          routes.go + main.go wiring)
```

The dependency chain is strictly linear because each step depends on the previous step's exported surface area. The split between TASK-014 and TASK-015 keeps the "traditional copy-the-token" UI work separate from the "swap cookies in-browser" work; each has its own focused acceptance criteria and can be reviewed independently.

## Alternatives Considered

See ADR-003 for the full design decision rationale. Highlights:

- **Two parallel middleware stacks (admin auth + device auth)**: rejected. Every protected endpoint would need to be wrapped with the right one, route registration would fork by caller kind, and helpers like `RequireRole` would have to know whether to look up a user or a device. A single `RequireAuth` plus a typed `Identity` keeps the boundary tidy and matches the "one mux, one wiring step" project ethos.
- **JWT for devices**: rejected. JWTs are not free to revoke (they need a deny-list, which means we are back to a database lookup). We already have a database; storing the token hash there is simpler, smaller cookies, and has no key-rotation foot-guns.
- **PIN-based pairing ("device shows a 6-digit code, admin types it into the admin UI")**: rejected. Requires a device-side handler that knows nothing yet (cannot be authenticated to fetch a PIN), an unauthenticated PIN-claim endpoint, a short TTL on PINs, and rate-limiting against guessing. Five new attack-surface knobs to ship a flow that the household admin can replace by walking up to the screen.
- **QR-code pairing ("device shows a QR, admin scans on phone, phone POSTs the device's id and a fresh token, device polls a self-info endpoint")**: rejected. Same surface concerns as PIN pairing plus a polling endpoint. The household admin's phone is already the kiosk's keyboard surrogate -- they can just sign into the kiosk's browser directly.
- **Admin walks up to kiosk, signs in as admin, clicks "enroll this browser as device X" (the chosen flow)**: accepted. Reuses every piece of the existing admin auth (Google OAuth, sessions, CSRF). Only one new endpoint and one new sqlc query (`RotateDeviceToken`). The mental model is intuitive ("this browser is now the device"). Crucially, the admin's other sessions are untouched because session deletion is keyed by the cookie value the kiosk was sending, not by user id.
- **Manual cookie-pasting in dev tools**: rejected as a primary flow but available as a fallback (the create-device page shows the raw token; an advanced user can set the device cookie manually). For programmatic clients (a screen running its own HTTP client), the bearer header is the right path.
- **Self-registration ("device hits an enrolment endpoint with a one-time PIN")**: rejected for v1 (see PIN-based pairing above).
- **Update `last_seen_at` on every authentication**: rejected. A screen polling every 5 seconds would issue 17,000 writes per day per device. The throttle (default 1m) cuts that to ~1,400 writes per day per device, almost all of them no-op `UPDATE` statements that match zero rows. Configurable in case the dashboard becomes more chatty in the future.
- **Storing the raw token in a flash cookie so the admin can re-view it on the next page**: rejected. The whole point of "shown exactly once" is that a screenshot or a casual reload cannot leak the token. Holding it in a server-side flash store breaks that promise.
- **Encrypting tokens at rest with a server-side key**: rejected. SHA-256 hashing already guarantees that a database leak does not expose usable tokens, and unlike encryption it has no key to lose.
- **Redirect post-enrollment to `/admin/login`**: rejected. The admin cookie was just cleared on this browser, so the browser would arrive at `/admin/login` with no session. If the operator clicks anything that requires admin auth, they re-enter the OAuth dance -- but on the kiosk, not on their personal device. Worse, a refresh on `/admin/login` from a wall-mounted screen looks broken. Landing on `/device/` (a dedicated device-friendly URL) is unambiguous and survives reload.
