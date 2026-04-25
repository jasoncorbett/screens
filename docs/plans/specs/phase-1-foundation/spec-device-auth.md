---
id: SPEC-003
title: "Device Auth"
phase: 1
status: draft
priority: p0
created: 2026-04-25
author: pm
---

# Device Auth

## Problem Statement

The screens service is meant to drive physical wall-mounted dashboards (tablets, low-power monitors, kiosks). Those devices are not human users -- they cannot complete a Google OAuth flow, do not have a keyboard for sign-in, and need to come up automatically after power loss with no human intervention. They also must be addressable individually so that an admin can revoke a single compromised device without disrupting every other display in the household.

The system therefore needs a second authentication mechanism that lives alongside admin auth: a long-lived, pre-shared bearer token bound to a specific named device. An admin generates the token in the management UI, copies it once to the device's local storage / config, and the device uses it to authenticate every subsequent request. If a device is lost, replaced, or compromised, the admin revokes its token and the next request from that device is rejected.

This spec also folds in the cross-cutting **auth middleware** work that the roadmap originally listed as a separate item. Admin sessions and device tokens are two ways of identifying a caller, but every protected endpoint needs to ask the same question: "is this caller authenticated, and which kind?". A single unified middleware layer that accepts either credential and injects a typed `Identity` into the request context is far simpler than two parallel stacks. It also keeps handlers honest: a handler that calls `auth.IdentityFromContext(ctx)` does not have to care whether the caller is a human admin or a wall display, only what the caller is allowed to do.

Without this spec, no device-facing feature in Phase 2 (Screen Display, page rotation) can be built securely -- either every screen route would be public or every device would have to log in with a real Google account.

## User Stories

- As an **admin**, I want to register a new physical device by giving it a friendly name (e.g., "kitchen tablet") so that I can tell my devices apart in the management UI.
- As an **admin**, I want the device's authentication token to be displayed exactly once at provisioning time so that I can copy it into the device's configuration without it being recoverable later from the database.
- As an **admin**, I want to revoke a device's token immediately so that a stolen or repurposed tablet can no longer reach the service.
- As an **admin**, I want to see when each device last contacted the service so that I can identify dead or unplugged screens at a glance.
- As a **device**, I want to authenticate with a single long-lived bearer token in the `Authorization` header so that I can silently reconnect after every reboot without user input.
- As a **device**, I want my token to also be accepted via a cookie when I am loading a full HTML page so that the browser running on the wall display can render screen content directly without needing JavaScript to attach a header.
- As a **handler author**, I want one `RequireAuth` middleware that accepts either an admin session or a device token so that I do not have to write two protection paths for every endpoint.
- As a **handler author**, I want to read a typed `Identity` value from the request context (admin user, device, or none) so that I can branch on caller type without re-parsing cookies or headers.

## Functional Requirements

### Device Records

1. The system MUST store devices in a `devices` table with at least: `id`, `name`, `token_hash`, `created_by` (admin user id), `created_at`, `revoked_at` (nullable), `last_seen_at` (nullable).
2. The system MUST allow the admin to assign a human-readable `name` to each device. Names are not required to be unique, but the system MUST reject empty names.
3. The system MUST generate device tokens using `crypto/rand` with at least 32 bytes of entropy and hex-encode them.
4. The system MUST store only the SHA-256 hash of the device token (same construction as `auth.HashToken` already used for sessions); the raw token MUST never be persisted.
5. The system MUST return the raw token to the admin **exactly once** at creation time in the HTTP response that creates the device. Subsequent reads of the device record MUST NOT contain the raw token.
6. The system MUST set `revoked_at` to the current time when an admin revokes a device, rather than deleting the row, so that audit information is preserved.
7. The system MUST treat any device whose `revoked_at` is non-NULL as authentication-failed.
8. The system SHOULD update `last_seen_at` on every successful device-token authentication. The update MAY be coalesced (e.g., at most once per minute per device) to avoid write amplification.
9. The system MAY allow an admin to "rotate" a device by issuing a new token and invalidating the old one in a single step. This is OPTIONAL for v1; the equivalent (revoke + create) is acceptable.

### Token Presentation

10. Devices MUST be able to authenticate by sending the raw token in an `Authorization: Bearer <token>` request header.
11. The system MUST also accept the raw token in a cookie (default name `screens_device`) for page-load requests where the browser cannot easily attach a custom header (e.g., a tablet browser navigating to `/`).
12. The system MUST accept the token from the cookie and the header equivalently. If both are present, the header takes precedence.
13. The system MUST treat tokens as opaque, case-sensitive strings and MUST NOT trim or normalise them.
14. The system MUST compare the candidate token's hash to the stored hash; raw-token comparison MUST NOT happen.

### Unified Auth Middleware

15. The system MUST provide a single `RequireAuth` middleware that succeeds if **either** a valid admin session **or** a valid (non-revoked) device token is present.
16. The middleware MUST inject a typed `Identity` value into the request context that distinguishes admin from device callers.
17. The injected `Identity` MUST expose at minimum: kind (admin or device), the underlying user (for admin) or device (for device), and a stable string ID.
18. Handlers MUST be able to retrieve the identity via a context accessor (`auth.IdentityFromContext(ctx)`).
19. The middleware MUST short-circuit on the first valid credential it finds, in this order: admin session cookie, then bearer header, then device cookie. (This avoids a database lookup for the device path when the caller is clearly an admin.)
20. The middleware MUST return the existing 302 redirect to the configured login URL when neither credential is present **and** the request is an HTML navigation (`Accept` header includes `text/html` and method is GET / HEAD). This preserves the existing admin browser experience.
21. The middleware MUST return a 401 Unauthorized JSON response (with `WWW-Authenticate: Bearer` header) when neither credential is present **and** the request is a likely device/API call (anything that is not the HTML navigation case in requirement 20).
22. The middleware MUST log one structured slog line per failed auth attempt at `info` level with `kind` (admin/device/none) and a sanitised reason. It MUST NOT log raw token or cookie values.

### Role-Aware Middleware Updates

23. The existing `RequireRole` middleware MUST continue to require an admin user. Device identities are not assigned roles and MUST be rejected with 403 by `RequireRole` even though they are otherwise authenticated.
24. The system MUST provide a separate `RequireDevice` middleware (or equivalent) that asserts the caller is a device, for endpoints that are exclusively for screen display.

### Existing Endpoints Continue to Work

25. The CSRF middleware MUST be unchanged in behaviour for admin sessions (it still validates the per-session CSRF token on POST/PUT/PATCH/DELETE).
26. The CSRF middleware MUST exempt requests authenticated solely by a device bearer token. (Bearer tokens are not subject to CSRF; the attack model -- a malicious site forging a request with the user's cookies -- does not apply to a header that the browser will not auto-attach.) The middleware MAY skip CSRF entirely when the identity is a device.

### Admin UI

27. The system MUST provide a `/admin/devices` page accessible only to admin users that lists all non-revoked devices with: name, ID, created date, last-seen date.
28. The page MUST allow an admin to create a new device by submitting a form with a name. On successful creation, the page MUST display the raw token to the admin once, with a clear instruction that it cannot be recovered later.
29. The page MUST allow an admin to revoke a device with a single POST (CSRF-protected). After revocation the device MUST appear in a separate "Revoked" section (or be hidden, at the implementation's discretion).
30. The page MUST display the list of revoked devices at least optionally so the admin can still see recent revocations.

### Configuration

31. The system MUST add a `DEVICE_COOKIE_NAME` config setting with default `screens_device`.
32. The system MUST add a `DEVICE_LAST_SEEN_INTERVAL` config setting (duration, default `1m`) controlling how often `last_seen_at` is allowed to be re-written for the same device.
33. No new secret-bearing config is required (device tokens live in the database).

## Non-Functional Requirements

- **Performance**: Device-token authentication is on the hot path for every device request (every page rotation, every htmx fragment). It MUST be a single indexed primary-key lookup on `devices.token_hash` plus an in-context check, with no N+1 pattern. The `last_seen_at` write MUST be coalesced (see requirement 8) so that a screen that polls every 5 seconds does not generate 17,000 writes per day per device.
- **Security**: Tokens are 256 bits of entropy, hex-encoded, never persisted in plaintext, and constant-time-compared via the SHA-256 hash. Tokens are never logged. Revocation is enforced server-side on every request; there is no token cache that could serve a revoked device. The unified middleware fails closed: any database error during validation is treated as unauthenticated.
- **Reliability**: Device authentication MUST NOT panic on malformed `Authorization` headers, missing schemes, empty bearer values, or weird cookie contents. It returns a clean 401.
- **Operability**: An admin can identify which physical screen a token belongs to (via name) and revoke it in one click. Last-seen helps locate dead displays.
- **Backwards compatibility**: Existing admin-session-only routes (everything currently under `/admin/`) MUST continue to function with no behaviour change for human users.

## Acceptance Criteria

### Token Lifecycle

- [ ] AC-1: When an admin POSTs a name to the create-device form, then a new device row is created with a hashed token and the response renders the raw token exactly once.
- [ ] AC-2: When the same device is fetched again (page reload, list view), then the raw token is **not** present anywhere in the response.
- [ ] AC-3: When the create-device form is submitted with an empty or whitespace-only name, then the request is rejected with a user-visible error and no device row is created.
- [ ] AC-4: When two devices are created in rapid succession, then they receive distinct tokens (no collisions, no re-use).
- [ ] AC-5: When the database is inspected after device creation, then the `token_hash` column matches `sha256(rawToken)` and the raw token does **not** appear in any column.

### Token Authentication

- [ ] AC-6: Given a non-revoked device with token `T`, when a GET request arrives with `Authorization: Bearer T`, then the request is authenticated as that device and the handler can read the device identity from context.
- [ ] AC-7: Given the same device, when a GET request arrives with the cookie `screens_device=T` and no Authorization header, then the request is authenticated identically.
- [ ] AC-8: Given a request with both an `Authorization: Bearer T1` header and a cookie `screens_device=T2`, when both tokens are valid for different devices, then the device identified in the Authorization header is used.
- [ ] AC-9: When a request arrives with `Authorization: Bearer <unknown-token>`, then the response is 401 Unauthorized.
- [ ] AC-10: When a request arrives with `Authorization: Basic <anything>`, then the device middleware ignores it (does not crash, does not authenticate as a device).
- [ ] AC-11: When a request arrives with `Authorization: Bearer ` (empty value), then the response is 401 Unauthorized.

### Revocation

- [ ] AC-12: Given a device authenticated successfully at time T, when an admin revokes the device at T+1 and the device makes another request at T+2, then the second request is rejected (401) and the `revoked_at` column is non-NULL.
- [ ] AC-13: When an admin revokes a device, then no other device's authentication is affected.
- [ ] AC-14: When `RequireAuth` validates a device whose `revoked_at` is non-NULL, then it logs an info-level slog line with kind=device and reason indicating revocation, without including the raw token.

### Last-Seen Tracking

- [ ] AC-15: When a device authenticates successfully and its previous `last_seen_at` was more than `DEVICE_LAST_SEEN_INTERVAL` ago (or NULL), then `last_seen_at` is updated to the current time.
- [ ] AC-16: When a device authenticates twice within `DEVICE_LAST_SEEN_INTERVAL`, then the second authentication does NOT issue a new UPDATE (verified by query count or by stable timestamp).

### Unified Middleware

- [ ] AC-17: When an admin with a valid session cookie navigates to a route protected by `RequireAuth`, then the request succeeds and `IdentityFromContext` returns an admin identity carrying the user.
- [ ] AC-18: When a device with a valid bearer token calls a route protected by `RequireAuth`, then the request succeeds and `IdentityFromContext` returns a device identity carrying the device.
- [ ] AC-19: When a request has neither credential and `Accept: text/html`, then the response is a 302 redirect to the login URL.
- [ ] AC-20: When a request has neither credential and `Accept: application/json` (or no Accept), then the response is 401 with a `WWW-Authenticate: Bearer` header.
- [ ] AC-21: When a device hits a route protected by `RequireRole(RoleAdmin)`, then the response is 403 (device identity is authenticated but does not satisfy the role check).

### CSRF Behaviour

- [ ] AC-22: When a device sends `POST` requests with a valid bearer token but no `_csrf` field, then the request succeeds (CSRF is exempt for device identities).
- [ ] AC-23: When an admin sends a `POST` with a valid session but no `_csrf`, then the request is rejected with 403 (existing behaviour preserved).

### Admin UI

- [ ] AC-24: When a non-admin (member) navigates to `/admin/devices`, then the response is 403.
- [ ] AC-25: When an admin GETs `/admin/devices`, then the page lists all non-revoked devices showing name and last-seen timestamp.
- [ ] AC-26: When an admin POSTs the create-device form with a valid name, then the resulting page contains the raw token in copyable form along with explicit "save this now -- it will not be shown again" wording.

### Configuration

- [ ] AC-27: When `DEVICE_COOKIE_NAME` is not set, then the cookie name defaults to `screens_device`.
- [ ] AC-28: When `DEVICE_LAST_SEEN_INTERVAL` is set to `5m`, then `last_seen_at` is throttled to once per 5 minutes per device.

## Out of Scope

- Self-registration of devices (admin-driven only).
- Per-screen assignment of devices to specific screens (lives in Phase 2 Screen Model).
- mTLS or client-cert auth (token-based is sufficient for a household).
- Token expiry / rotation policy (devices live for years; manual revocation is enough).
- Push commands from the server to a specific device (lives in Phase 4 Push Notifications).
- Device groups / fleets / labels.
- A device API for the device itself to fetch its own configuration (that is the Phase 2 Screen Display spec).

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- needs the migration runner and `db.OpenTestDB` test helper.
- Depends on: SPEC-002 (Admin Auth) -- needs `auth.GenerateToken`, `auth.HashToken`, the `auth.Service` and existing middleware composition; also reuses the admin-only UI shell, layout templ, and CSRF mechanism for the admin pages added here.
- No external dependencies (no new third-party Go modules required).

## Open Questions

All resolved.

- Q1 **Resolved**: Auth Middleware is folded into this spec. The roadmap entry is satisfied here. PHASE.md is updated to remove the separate row.
- Q2 **Resolved**: A single device cookie is acceptable in addition to bearer header. The cookie is set out-of-band by the admin (or pasted in by the device's browser) and is HttpOnly. We do not gate it behind dev-mode the way the admin session cookie is gated, because device displays are typically on local-network HTTPS-via-reverse-proxy or plain HTTP on a trusted LAN; cookie `Secure` follows the same `!cfg.Log.DevMode` rule as the admin session.
- Q3 **Resolved**: Token presentation uses 64-character hex (32 bytes). This matches the existing `auth.GenerateToken` so the same crypto primitive can be reused.
- Q4 **Resolved**: `last_seen_at` is throttled at the application layer (not via DB trigger) so the behaviour is testable in Go.
