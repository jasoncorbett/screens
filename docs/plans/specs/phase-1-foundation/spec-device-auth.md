---
id: SPEC-003
title: "Device Auth"
phase: 1
status: accepted
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
- As an **admin standing in front of a wall display**, I want to enroll the browser running on that screen as a device in a single click so that I do not have to manually copy a 64-character token onto a kiosk that has no keyboard.
- As an **admin**, I want enrolling a browser as a device to immediately log my admin session out **of that browser** (and only that browser) so that the wall display cannot accidentally be used as an admin terminal and so my admin session on my laptop is not affected.
- As a **device**, I want to authenticate with a single long-lived bearer token in the `Authorization` header so that I can silently reconnect after every reboot without user input.
- As a **device**, I want my token to also be accepted via a cookie when I am loading a full HTML page so that the browser running on the wall display can render screen content directly without needing JavaScript to attach a header.
- As a **device kiosk browser**, I want a stable URL to land on after enrollment that does not require admin auth so that a refresh of the page does not bounce me back into a login flow.
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
9. The system MUST allow an admin to "rotate" a device's token (issue a new token and invalidate the old one in a single step) without revoking and recreating the device. This is required by the browser-enrollment flow (requirement 31): enrolling a browser as an existing device replaces the device's token in place, so any previously issued token for that device becomes invalid. The rotated token MUST be returned to the immediate caller exactly once (used to set the device cookie) and never persisted in plaintext.

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
28. The page MUST allow an admin to create a new device by submitting a form with a name. On successful creation, the page MUST display the raw token to the admin once, with a clear instruction that it cannot be recovered later. (This "copy the token" flow is retained for programmatic clients and for cases where the admin wants the token without converting their current browser into a device.)
29. The page MUST allow an admin to revoke a device with a single POST (CSRF-protected). After revocation the device MUST appear in a separate "Revoked" section (or be hidden, at the implementation's discretion).
30. The page MUST display the list of revoked devices at least optionally so the admin can still see recent revocations.

### Browser Enrollment

The dominant deployment target is a wall-mounted kiosk browser. Cookies cannot be set on that browser out of band, so the system MUST provide a flow whereby an admin standing in front of the kiosk converts the kiosk's currently-authenticated admin browser session into a device session in a single click.

31. The system MUST provide a POST endpoint (e.g. `POST /admin/devices/{id}/enroll-browser`) that, given a current admin caller, performs the following actions atomically from the caller's perspective:
    - Generate a fresh device token for the target device (replacing any prior token for that device).
    - Delete the admin session backing the caller's current request from the database (call the existing `auth.Service.Logout` so the row is gone, not orphaned).
    - Clear the admin session cookie on the response (MaxAge -1, same attributes as logout).
    - Set the device cookie on the response with the freshly generated raw token.
    - Redirect (302) the caller to the configured device landing URL (see requirement 38).
32. The endpoint MUST require an admin caller. The middleware chain MUST be `RequireAuth` then `RequireRole(RoleAdmin)` then `RequireCSRF` then this handler. A non-admin (member) caller MUST receive 403; an unauthenticated caller MUST receive the standard `RequireAuth` failure (302 to login for HTML, 401 otherwise).
33. The endpoint MUST be POST only. A GET MUST NOT trigger enrollment, because a GET-triggered enrollment would let an attacker convert an admin's browser into a device by getting the admin to click a link.
34. The endpoint MUST consume a valid CSRF token from the form (existing CSRF middleware behaviour). Without this, an attacker who tricks an admin into submitting a cross-site form could downgrade the admin's browser to a device.
35. The system MUST also provide an admin UI affordance that creates a new device and enrolls the current browser as that device in a single user action (one POST, two server-side operations: create then enroll). This combined flow MAY be implemented as a separate endpoint (e.g. `POST /admin/devices/enroll-new-browser` with a `name` form field) or as the create-then-redirect chain implemented client-side; either is acceptable as long as the admin only has to fill in one form on the kiosk.
36. The system MUST reject `enroll-browser` for a target device whose `revoked_at` is non-NULL with a flash error (302 back to `/admin/devices?error=...`). The admin's session MUST NOT be terminated in this rejection case -- the swap only happens if the target device is valid.
37. The system MUST tolerate enrollment when the caller's browser already has a device cookie set: the new device cookie REPLACES the old one (same cookie name, new value), and any old admin session cookie is cleared. The browser ends up with exactly one auth cookie -- the freshly issued device cookie.
38. The system MUST add a `DEVICE_LANDING_URL` config setting (default `/device/`) controlling where the browser is redirected after enrollment. The endpoint at this URL MUST NOT require admin auth (the admin session is gone by the time the browser arrives).
39. The system MUST register a placeholder handler at the device landing URL (default `/device/`) that:
    - Is gated by `RequireAuth` only (NOT `RequireRole(RoleAdmin)`), so a device identity is sufficient.
    - Renders a minimal HTML page identifying the browser as the named device (e.g., `"This browser is enrolled as <device-name>. The screen display will appear here once the screen feature ships."`).
    - This handler is a placeholder; Phase 2 (Screen Display) will replace its body with the actual screen content. Its existence in Phase 1 is required so that enrollment has somewhere to land.
40. The system MUST log one `slog.Info` line per successful enrollment with attributes `device_id`, `device_name`, and `enrolled_by` (admin email). It MUST NOT log the raw token.

### Configuration

41. The system MUST add a `DEVICE_COOKIE_NAME` config setting with default `screens_device`.
42. The system MUST add a `DEVICE_LAST_SEEN_INTERVAL` config setting (duration, default `1m`) controlling how often `last_seen_at` is allowed to be re-written for the same device.
43. The system MUST add a `DEVICE_LANDING_URL` config setting with default `/device/`. Validation MUST reject empty values and values that do not start with `/`.
44. No new secret-bearing config is required (device tokens live in the database).

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

### Browser Enrollment

- [ ] AC-27: Given an admin caller with a valid session cookie and a valid CSRF token, when they POST to the enroll-browser endpoint for an existing non-revoked device, then the response is a 302 to the device landing URL, the response sets the device cookie to the freshly issued raw token, and the response clears the admin session cookie (MaxAge -1).
- [ ] AC-28: After AC-27, when the database is inspected, then the admin session row used for the enrolling request is no longer present (orphan-free).
- [ ] AC-29: After AC-27, when the same browser issues a follow-up request to the device landing URL with only the device cookie, then the request is authenticated as the enrolled device (`IdentityFromContext.IsDevice() == true`) and the device's name appears in the response.
- [ ] AC-30: Given the admin from AC-27 also has a session on a SECOND browser (e.g., their laptop), when AC-27 completes, then the second browser's session is unaffected and continues to authenticate (the only session deleted is the one used to call enroll-browser).
- [ ] AC-31: When an admin POSTs the enroll-browser endpoint targeting a device whose `revoked_at` is non-NULL, then the response is a 302 to `/admin/devices?error=...`, the admin session cookie is NOT cleared, the device cookie is NOT set, and the admin's session row is NOT deleted.
- [ ] AC-32: When an unauthenticated client POSTs the enroll-browser endpoint, then the response is the standard `RequireAuth` failure (302 to login for HTML, 401 otherwise) and no cookies are mutated.
- [ ] AC-33: When a member (non-admin) authenticated user POSTs the enroll-browser endpoint, then the response is 403 and no cookies are mutated.
- [ ] AC-34: When a GET request is sent to the enroll-browser path, then the response is 405 Method Not Allowed (or the route simply does not match GET, which yields 404). The admin session is NOT terminated and the device cookie is NOT set.
- [ ] AC-35: When an admin POSTs enroll-browser without a valid `_csrf` field, then the request is rejected with 403 by the existing CSRF middleware and no cookies are mutated.
- [ ] AC-36: When an admin POSTs enroll-browser from a browser that already has a device cookie for some OTHER device, then the response sets the device cookie to the newly enrolled device's token (replacing the previous value) and clears the admin session cookie.
- [ ] AC-37: When an admin POSTs the "create-and-enroll-this-browser" form with a valid device name, then a new device row is created AND the same response performs the cookie swap and 302-redirect to the device landing URL.
- [ ] AC-38: When a request without any auth cookie hits the device landing URL, then the response is the standard `RequireAuth` failure (302 to login for HTML, 401 otherwise). (This makes the landing URL safe to publish.)

### Configuration

- [ ] AC-39: When `DEVICE_COOKIE_NAME` is not set, then the cookie name defaults to `screens_device`.
- [ ] AC-40: When `DEVICE_LAST_SEEN_INTERVAL` is set to `5m`, then `last_seen_at` is throttled to once per 5 minutes per device.
- [ ] AC-41: When `DEVICE_LANDING_URL` is not set, then the default landing URL is `/device/`. When set to a non-`/`-prefixed string, validation fails.

## Out of Scope

- Self-registration of devices without an admin present (admin-driven only -- either via token-copy or via in-person browser enrollment).
- QR-code or PIN-based pairing flows. The browser-enrollment flow assumes the admin can physically reach the kiosk and authenticate on it directly. A remote/headless pairing flow is not in scope.
- Per-screen assignment of devices to specific screens (lives in Phase 2 Screen Model).
- mTLS or client-cert auth (token-based is sufficient for a household).
- Token expiry / rotation policy (devices live for years; manual revocation is enough).
- Push commands from the server to a specific device (lives in Phase 4 Push Notifications).
- Device groups / fleets / labels.
- The actual screen display content at the device landing URL. Phase 1 ships a placeholder page; Phase 2 Screen Display replaces its body.
- A device API for the device itself to fetch its own configuration (that is the Phase 2 Screen Display spec).
- "Un-enrolling" a browser back into an admin session (the admin signs into the admin URL again on a different browser if they need admin access; revoking the device on an enrolled browser leaves that browser unauthenticated, and the admin can sign in fresh from there).

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- needs the migration runner and `db.OpenTestDB` test helper.
- Depends on: SPEC-002 (Admin Auth) -- needs `auth.GenerateToken`, `auth.HashToken`, the `auth.Service` and existing middleware composition; also reuses the admin-only UI shell, layout templ, and CSRF mechanism for the admin pages added here.
- No external dependencies (no new third-party Go modules required).

## Open Questions

All resolved.

- Q1 **Resolved**: Auth Middleware is folded into this spec. The roadmap entry is satisfied here. PHASE.md is updated to remove the separate row.
- Q2 **Resolved**: A single device cookie is acceptable in addition to bearer header. The cookie is HttpOnly. The primary mechanism for setting it on a real wall-mounted kiosk is the in-person browser-enrollment flow (see "Browser Enrollment" requirements 31-40). Programmatic clients can still copy the raw token shown at create time. Cookie `Secure` follows the same `!cfg.Log.DevMode` rule as the admin session.
- Q3 **Resolved**: Token presentation uses 64-character hex (32 bytes). This matches the existing `auth.GenerateToken` so the same crypto primitive can be reused.
- Q4 **Resolved**: `last_seen_at` is throttled at the application layer (not via DB trigger) so the behaviour is testable in Go.
- Q5 **Resolved**: The browser-enrollment endpoint is POST-only and CSRF-protected. A GET-triggered or non-CSRF-protected enrollment would let an attacker downgrade an admin's browser into a device by tricking the admin into clicking a link or submitting a forged form. POST + CSRF is the same defence we use for every other admin state-changing endpoint, so it imposes no new burden.
- Q6 **Resolved**: The admin's session in the database is keyed by the cookie that the kiosk's browser was sending. Calling `auth.Service.Logout` with that cookie value before the cookie is cleared deletes only THAT row. Other sessions for the same admin (e.g., on a laptop) are unaffected.
- Q7 **Resolved**: Post-enrollment landing URL is `/device/` by default and is configurable via `DEVICE_LANDING_URL`. Phase 1 ships a placeholder template at that URL; Phase 2 Screen Display will replace the body. The handler is gated by `RequireAuth` only (no `RequireRole`), so a device identity is sufficient.
