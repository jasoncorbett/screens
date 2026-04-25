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
- Devices share the same `auth.Service` as admins. The service grows new methods (`CreateDevice`, `ValidateDeviceToken`, `RevokeDevice`, `ListDevices`, `MarkDeviceSeen`) and the type starts to do "more than session management". We accept this; an `internal/auth` package is the right home for both admin and device identity. Splitting devices into a sibling package would just create circular import temptations later.
- `last_seen_at` adds write traffic on every successful device auth. We mitigate with a server-configurable throttle (`DEVICE_LAST_SEEN_INTERVAL`, default 1 minute) implemented as a single SQL `UPDATE ... WHERE last_seen_at < datetime('now', '-1 minutes')`.
- Tokens are NOT recoverable after creation. If an admin forgets to copy the token, they have to revoke and recreate. We accept this in exchange for the security guarantee that a database snapshot does not leak any usable credentials.

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
