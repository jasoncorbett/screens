---
id: ADR-003
title: "Pre-shared bearer tokens with a unified auth middleware for devices"
status: accepted
date: 2026-04-25
---

# ADR-003: Pre-shared bearer tokens with a unified auth middleware for devices

## Context

The screens service has two very different kinds of clients:

1. **Admins** (humans): browse to `/admin/...`, sign in via Google OAuth, get a session cookie, click around. Already implemented in SPEC-002.
2. **Devices** (wall displays / tablets): power on, immediately need to load a screen, have no keyboard, are not a Google user. They have to authenticate using a credential that lives in the device's local config.

Two design questions:

1. **What credential do devices present?** Options included
   - JWT signed with a server key,
   - Pre-shared random token issued by an admin,
   - mTLS with per-device client certs,
   - Magic-link enrolment with a one-time PIN.
2. **How do protected endpoints know which kind of caller they are talking to?** Options included
   - Two separate middleware stacks (one for `/admin/`, one for `/device/`), with handlers wrapped manually,
   - One unified middleware that accepts either credential and tells the handler what it found,
   - Deferring the distinction entirely (every protected endpoint validates whatever it wants).

The threat model is a household: trusted local network most of the time, but the dashboard is reachable over the internet (via a reverse proxy / Cloudflare Tunnel / similar). A stolen tablet is a realistic threat; a sophisticated MITM on the LAN is not.

## Decision

### Devices use pre-shared bearer tokens

- An admin creates a device record with a friendly name. The service generates a 32-byte random token via `crypto/rand`, hex-encodes it (64 chars), and SHA-256-hashes it for storage. The raw token is shown to the admin **exactly once** in the response and never persisted.
- The device sends the token on every request as `Authorization: Bearer <token>`. For browser page loads where attaching a header is awkward, the same token is also accepted in a cookie (`screens_device`).
- Revocation is a single column update (`revoked_at = now`) that takes effect on the next request. No revocation list, no deny cache, no key rotation.

### Browser enrollment: admin-bootstrap, not QR/PIN

The dominant deployment target is a wall-mounted browser kiosk. A kiosk has no keyboard for an admin to paste a 64-character token into a config file, and the cookie cannot be set out-of-band. We therefore add an in-person enrollment flow:

1. Admin walks up to the kiosk and opens the admin URL in the kiosk's browser.
2. Browser bounces through `/admin/login` -> Google OAuth -> back. Admin is now signed into the admin UI **on the kiosk's browser**.
3. Admin opens `/admin/devices` and clicks one of two buttons: "enroll this browser as <existing device name>" or "create new device named <X> and enroll this browser".
4. Server-side, the handler calls `RotateDeviceToken` (issuing a fresh token for the target device, replacing any prior token), calls `auth.Service.Logout` with the admin cookie value to delete that single session row from the database, clears the admin session cookie on the response, sets the device cookie with the new raw token, and 302-redirects to a configured device landing URL (default `/device/`).

We considered QR-code pairing and PIN pairing and rejected both: they ship a new unauthenticated claim endpoint, a poll endpoint on the device side, a TTL on pairing tokens, and rate-limiting. Five new attack-surface knobs to replace a flow that the admin can do by walking up to the screen they are configuring. The household model means an admin physically reaching the kiosk is realistic; this is a household dashboard, not a fleet of remote signage.

Critical properties of the chosen flow:

- **POST-only, CSRF-protected.** A GET-triggered enrollment would be a single-click downgrade attack against an admin's main browser. The admin clicks a malicious link, their session cookie is sent, the server swaps cookies, the admin is locked out of `/admin/`. POST + CSRF closes that hole using the same defence we already use for every other state-changing admin endpoint.
- **Surgical session deletion.** `Logout(rawToken)` deletes exactly one row -- the row keyed by the `token_hash` of the cookie this request sent. Other admin sessions for the same user (laptop, phone) are untouched because they have different token values. The admin does not get punted out of every browser they own.
- **Atomic from the user's perspective.** `RotateDeviceToken` runs first; if the device is missing or revoked, the handler 302s with a flash error BEFORE clearing any cookies or deleting any session row. A failed enrollment leaves the admin still signed in.
- **Token rotation, not reuse.** Even when enrolling against an existing un-revoked device, we issue a NEW token and overwrite the row's `token_hash`. Any previously distributed copy of the token becomes invalid. This is the property that makes "re-enrolling a kiosk after taking it down" safe: the old token cannot also be used.
- **Landing URL is device-friendly.** Post-enrollment the browser arrives at `/device/` (configurable). The route is registered under `RequireAuth` only -- no `RequireRole` -- so the device identity is sufficient. Phase 1 ships a placeholder template; Phase 2 Screen Display replaces the body with the real screen content. The URL itself is stable across phases.

### Reuse, don't reinvent

- The token primitive is `auth.GenerateToken` and `auth.HashToken`, the same functions that power admin session tokens. Same entropy, same hashing, same encoding. One surface to audit, one set of tests for token-generation correctness.
- Device records live in their own table (`devices`), but the SQL pattern -- TEXT id, TEXT-as-ISO8601 timestamps, sqlc-generated queries -- matches the existing `users`, `sessions`, and `invitations` tables.

### Auth Middleware is folded into this spec, not deferred

- The roadmap originally listed "Auth Middleware" as a separate spec. We are folding it into this device-auth spec because the work is the same work: unifying admin-session validation and device-token validation behind a single `RequireAuth` is the unified middleware. Designing them apart would create two parallel call paths that have to converge anyway when handlers ask "who is calling me?".

### One middleware, typed identity

- A single `RequireAuth` middleware probes admin session first (fast cookie-only path), then `Authorization: Bearer`, then the device cookie. The first valid credential wins. No credential => the response is a 302 to `/admin/login` for HTML navigations, or a 401 with `WWW-Authenticate: Bearer` for everything else (so devices and API clients get a meaningful failure).
- On success, the middleware injects an `auth.Identity` value into the request context. The Identity carries a `Kind` enum (`IdentityAdmin` / `IdentityDevice`) plus the underlying `*User` or `*Device`. Handlers branch on `id.IsAdmin()` or `id.IsDevice()`; they do not need to look at cookies or headers themselves.
- For backwards compatibility with the handlers shipped in SPEC-002, when the identity is admin we also continue to populate `auth.ContextWithUser` and `auth.ContextWithSession`. Existing handlers that call `auth.UserFromContext` keep working without modification.

### CSRF behaviour

- The CSRF middleware continues to validate `_csrf` for admin sessions on state-changing methods (existing AC-15..AC-17 from SPEC-002).
- Device requests authenticated by a bearer token are exempt from CSRF. Browsers do not auto-attach `Authorization` headers cross-site, so the CSRF threat model does not apply.

## Consequences

**Accepted trade-offs:**

- The `RequireAuth` signature changes: it now takes both the session cookie name and the device cookie name. All existing call sites in `views/routes.go` and `main.go` are updated -- this is a one-time cost.
- Devices share the same `auth.Service` as admins. The service grows new methods (`CreateDevice`, `ValidateDeviceToken`, `RevokeDevice`, `ListDevices`, `MarkDeviceSeen`, `RotateDeviceToken`) and the type starts to do "more than session management". We accept this; an `internal/auth` package is the right home for both admin and device identity. Splitting devices into a sibling package would just create circular import temptations later.
- `last_seen_at` adds write traffic on every successful device auth. We mitigate with a server-configurable throttle (`DEVICE_LAST_SEEN_INTERVAL`, default 1 minute) implemented as a single SQL `UPDATE ... WHERE last_seen_at < datetime('now', '-1 minutes')`.
- Tokens are NOT recoverable after creation. If an admin forgets to copy the token, they have to revoke and recreate. We accept this in exchange for the security guarantee that a database snapshot does not leak any usable credentials.
- The `/device/` landing URL is reserved by Phase 1 (placeholder content) and replaced by Phase 2 (Screen Display). Reserving the URL early means the enrollment flow does not have to change shape when Phase 2 ships -- the cookie swap and redirect target are stable.
- Browser enrollment works against a single browser at a time. Enrolling twenty kiosks means walking to twenty kiosks. We accept this; the alternative is a bulk-enroll flow which requires PIN/QR machinery we have explicitly rejected. A future spec could add bulk enrollment if the household ever reaches a scale where one-by-one is painful.

**Benefits:**

- Devices need zero interactive setup: paste a token into the config, reboot, done.
- Revocation is immediate -- the next request after `revoked_at` is non-NULL fails 401.
- One middleware, one mental model. Handlers ask "who's calling?" via `IdentityFromContext` and branch as needed; they do not have to think about which authentication mechanism produced the answer.
- No new third-party dependencies. Reuses `crypto/rand`, `crypto/sha256`, `database/sql`, sqlc-generated queries, and the existing slog logger.
- No JWT footguns: no algorithm-confusion attacks, no key rotation, no issuer/audience claims to validate, no clock skew handling.
- The "unified middleware" approach scales to future identity kinds (e.g., a future API key for an external integration) by adding a new `IdentityKind` and a new probe arm. Handlers do not change.

**Risks accepted:**

- A token in `Authorization` is observable to any reverse proxy that sits in front of the service. We assume the operator deploys behind TLS and trusts their own proxy. This is the same assumption admin sessions make.
- A device token is bearer-style and confers full device privileges. There is no per-route fine-grained capability scheme. For a household dashboard this is appropriate; if richer scoping is ever needed, a future ADR can revisit.
- An admin who enrolls a kiosk and then forgets which kiosk maps to which device will have to look at the device-management page (which shows last-seen timestamps) or revoke and re-enroll. We do not show the device cookie value back to the admin after enrollment -- the same "shown exactly once" rule applies to the rotated token as to the create-time token. This is acceptable: the admin does not need the raw token; the kiosk's browser holds it.
- If a kiosk is stolen with a valid device cookie still in its browser storage, an attacker can authenticate as the device until the admin revokes it. This is the same risk profile as a stolen laptop with a valid admin session cookie. The mitigation is the existing one-click revoke on `/admin/devices`.
