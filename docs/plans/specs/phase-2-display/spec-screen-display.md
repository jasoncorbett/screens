---
id: SPEC-007
title: "Screen Display"
phase: 2
status: draft
priority: p0
created: 2026-05-25
author: pm
---

# Screen Display

## Problem Statement

Phase 2 has shipped every piece an admin needs to *describe* a dashboard -- themes (SPEC-004), the widget interface and a placeholder widget (SPEC-005), and the screen / page / widget-instance data model (SPEC-006) -- but nothing yet exists that puts those pieces on a wall. A physical device that boots up, authenticates with its bearer token, and lands on the configured `DEVICE_LANDING_URL` (`/device/` by default) currently sees a Phase 1 placeholder page that simply says "This browser is enrolled as &lt;device-name&gt;." The rendering pipeline -- the bridge between the database tree `screens.Service.GetScreenFull` returns and a fully styled, auto-rotating HTML page -- is the missing p0 in Phase 2. Without it, the household-scale dashboard that the entire project exists to deliver has no surface to render on.

This spec owns that pipeline. Four concrete things have to happen for a device to display a Screen:

1. **The device has to know which Screen to render.** A paired device has an identity (SPEC-003); a Screen is a separate entity (SPEC-006); the mapping between them is currently empty. This spec introduces a `screen_id` foreign-key column on `devices` (`ON DELETE SET NULL`) plus the admin UI to assign / clear that assignment. A device with no assignment shows a clear "no screen assigned" placeholder, not a 500.
2. **The render handler has to assemble the page.** The Phase 1 `handleDeviceLanding` is replaced with a render handler that calls `screens.Service.GetScreenFull(ctx, screen.ID)`, embeds the theme's CSS variables via `themes.Theme.CSSVariables()` in a per-render `<style>` block in the page head, and renders every page of the Screen into a sequence of page-container `<div>`s so the client-side rotator can swap visibility between them without a server round-trip.
3. **Each widget instance has to render through the registry.** The handler iterates each page's widget instances and calls `widget.Registry.Render(ctx, type, config, theme)`, which validates the per-instance JSON config and dispatches to the widget's `templ.Component`. A widget whose render fails (unknown type, malformed config) MUST NOT take down the whole page; it renders a small inline error placeholder so the rest of the dashboard keeps working. This is the "two-layer validation" property SPEC-005 / ADR-005 committed to: the writer-side validation is the first layer; the render-time re-validation is the second layer.
4. **The pages have to auto-rotate.** The Screen carries a `rotation_interval_seconds` (5..3600 from SPEC-006). The render output ships a tiny vanilla-JS rotator script that toggles `.page-current` on the page containers every interval. Single page -- no rotation. Zero pages -- a clear empty-state card. The rotator is plain stdlib JS (no framework, no htmx, no external library) to match the project's minimal-framework ethos.

A fifth concern -- how does the device pick up admin-side changes to its Screen? -- is resolved by a low-tech full-page reload on a coarse interval. A small `<meta http-equiv="refresh" content="N">` tag (where `N` defaults to roughly the rotation interval, but with a configurable minimum so a 5-second-rotation Screen does not reload every 5 seconds) drops the live-reload problem on the browser's own machinery. This avoids ServerSent Events, WebSocket, or htmx-polling endpoints that would each add code, an open connection per device, and a re-validation surface that v1 does not need. The polish phase can revisit if a real use case demands sub-minute change visibility; for now the admin who edits a Screen at the kitchen counter and looks up at the tablet within a minute sees the update.

This spec deliberately does not introduce per-device per-page state ("which page is this device currently on?"), per-device overrides, server-driven rotation, or any websocket / SSE machinery. Rotation is purely client-side; the device's database state is just its `screen_id` foreign key. That keeps the server stateless from a rendering perspective: every render is a function of the database state.

The downstream phase work this unblocks is significant: every Phase 3 widget gets to ride this pipeline on the day it ships, with no per-widget changes to the render handler. The Widget Selection UI (Phase 2, p1) and Page Backgrounds (Phase 2, p1) inherit a render surface that already understands per-instance config and per-page concerns. The PWA work in Phase 4 wraps the same render output in a service worker; the rendered HTML does not change shape.

## User Stories

- As an **admin**, I want to assign a Screen to a paired device from the device-management UI, so that the device starts displaying that Screen's pages on its next reload without me touching the device itself.
- As an **admin**, I want to clear a device's Screen assignment, so that a device I am repurposing returns to a clear "no screen assigned" placeholder instead of continuing to render stale content.
- As an **admin**, I want a device whose assigned Screen has been deleted to land back on a clear "no screen assigned" placeholder rather than a 500 page, so that a misclick on a Screen delete does not silently brick every device displaying it.
- As an **admin**, I want to look at a wall display within a minute of editing the Screen, page list, or widget configuration in the admin UI and see the new content, so that the configure-look-tweak loop in front of the tablet is bearable without a manual reload.
- As an **admin**, I want each Screen's `rotation_interval_seconds` to be the cadence at which the device cycles through pages, so that I can tune fast-rotating dashboards (5-second pulses on a clock-only Screen) versus slow-rotating dashboards (5-minute holds on a family-calendar Screen) per Screen without touching code.
- As a **device kiosk browser**, I want each page's HTML to ship in the initial render, so the rotator can cycle pages with zero network round-trips after the first load, and a brief network blip never blanks the wall display.
- As a **device kiosk browser**, I want the active theme's CSS variables (colors, fonts, radius) to ship inline in the page head, so the very first paint already wears the correct theme with no flash of unstyled content.
- As a **device kiosk browser**, I want a periodic full-page reload (default ~60s, capped sensibly relative to rotation interval) so that admin edits show up without me being online for an SSE stream or running a heavyweight polling client.
- As a **device kiosk browser**, I want a single widget's render failure to produce a small inline error placeholder rather than blanking the entire page, so that one bad config row never costs me the whole dashboard.
- As an **admin (eventually, once Phase 3 widgets ship)**, I want each widget on the page rendered through the registry without any per-widget handler code, so that adding a new widget type to the admin's vocabulary requires zero changes to the device render handler.
- As a **future Phase 4 PWA author**, I want the render output to be a single complete HTML page (with embedded CSS, the rotator script, and every page's widgets) that a service worker can cache verbatim, so that offline rendering is a function of cached HTML rather than a separate offline-mode code path.

## Functional Requirements

### Device-to-Screen Assignment

1. The `devices` table MUST gain a nullable `screen_id TEXT` column. The migration runs against the existing schema (`devices` table from SPEC-003); SQLite supports `ALTER TABLE devices ADD COLUMN screen_id TEXT` without a table rebuild.
2. The `screen_id` column MUST be `REFERENCES screens(id) ON DELETE SET NULL`: deleting a Screen MUST clear every device's `screen_id` that referenced it, leaving the devices unassigned rather than orphaned. SQLite's `ALTER TABLE ... ADD COLUMN` cannot add `REFERENCES` directly in every dialect; the architecture document specifies the migration's exact SQL shape and the application-layer safeguard that fires if the FK enforcement falls through. The functional contract is "deleting a Screen leaves no device pointing at a non-existent row".
3. The system MUST expose a service method `auth.Service.AssignDeviceToScreen(ctx, deviceID, screenID string) error` that updates `devices.screen_id`. `screenID == ""` MUST be the documented way to CLEAR the assignment (set the column to NULL). Returns `auth.ErrDeviceNotFound` on a missing device; returns `screens.ErrScreenNotFound` (or an analogous `auth`-package error -- see Open Questions) when `screenID` is non-empty and does not resolve to a real Screen.
4. The `auth.Device` Go struct MUST gain a `ScreenID *string` field. Nil indicates "no Screen assigned".
5. The admin device list page MUST surface, per non-revoked device, the assigned Screen's name (or "(none)") and a small form that lets the admin choose any existing Screen (or "(none)" to clear) and POST the change to a new endpoint.
6. The new endpoint MUST be `POST /admin/devices/{id}/assign-screen`, form field `screen_id` (string, possibly empty for clear), gated by the existing admin chain (`RequireAuth` → `RequireRole(RoleAdmin)` → `RequireCSRF`). On success it redirects to `/admin/devices?msg=assigned` (or `msg=unassigned` for the clear case). On unknown Screen ID it redirects to `?error=Screen+not+found`.
7. Revoked devices MAY display their last `screen_id` (read-only) in the revoked section; the assignment form MUST NOT be rendered for revoked devices.

### Device Render Handler

8. The handler at `DEVICE_LANDING_URL` (default `/device/`) currently implemented by `views/device.go::handleDeviceLanding` MUST be replaced by a Screen Display render handler. The route registration in `views/routes.go` MUST continue to use the existing chain (`RequireAuth` only -- no `RequireRole`, no `RequireCSRF`); the only change is what the handler does once auth has succeeded.
9. The new handler MUST require an authenticated identity: a device identity is the production case; an admin identity (the admin viewing the page from a non-kiosk browser, e.g., during configuration) MUST also be allowed to view the rendered Screen, but only when the URL carries an explicit `?screen=<id>` query parameter selecting which Screen to render. (Admins do not have a "their" Screen the way devices do; the explicit selector is the simplest UX that keeps the handler usable for an admin doing live verification.)
10. For a device identity:
    - If `device.ScreenID` is nil, the handler MUST render an "unassigned" placeholder card (device name + message: "No screen assigned. Visit /admin/devices to assign one.") and return 200. This is NOT an error state and MUST NOT log at warn / error level.
    - If `device.ScreenID` is non-nil but the Screen no longer exists (e.g., race between Screen delete and the device's next reload, even though FK `SET NULL` should prevent this), the handler MUST also render the "unassigned" placeholder. It MUST log `slog.Info("device screen missing", "device_id", ..., "screen_id", ...)` once per render. (The FK should keep this branch unreachable in practice; the handler still defends against it for the case where a future schema change loosens the FK or the cleanup raced.)
    - Otherwise the handler MUST call `screensSvc.GetScreenFull(ctx, *device.ScreenID)` and render via the rules below.
11. For an admin identity:
    - If `?screen=<id>` is present and resolves, render that Screen.
    - If `?screen=<id>` is missing, render an "admin viewing the device URL" landing card listing the admin's available Screens with links to `/device/?screen=<id>` for live preview.
    - If `?screen=<id>` is present but does not resolve, render an error card ("Screen not found").
12. The render output MUST be a single complete HTML document. No fragments, no htmx attributes on the device render path. The PWA service worker (Phase 4) will cache this document verbatim, so it MUST be self-contained.
13. The render output MUST embed the active theme's CSS variables in a `<style>` block in `<head>` via `themes.Theme.CSSVariables()`. This SHIPS in the same HTTP response as the page body so the first paint already wears the theme. There MUST NOT be a separate stylesheet endpoint for theme variables in v1.
14. The render output MUST link the existing `/static/css/app.css` (for the structural rules the project's existing CSS already provides), plus a new device-specific stylesheet (TBD path in the architecture document) that defines the page-container, page-rotation transition rules, and widget-stack layout.
15. The render output MUST include a small inline vanilla-JS rotator script. The script MUST:
    - Run on `DOMContentLoaded`.
    - Find all elements with the class `page-container`.
    - If there are zero pages: do nothing (the empty-state card is the body).
    - If there is exactly one page: ensure that page has `page-current`; do not start a timer.
    - If there are two or more pages: ensure the first has `page-current`; start an interval at `data-rotation-seconds * 1000` ms (from a `data-rotation-seconds` attribute on the body) that advances `page-current` round-robin.
    - Use `setInterval` (not a recursive `setTimeout` chain). Stop and re-start cleanly if the function is invoked again (idempotent against a double-run from cached scripts in Phase 4).
    - Be plain ES2015 or newer; no external library; no framework; no module loader. Fits in roughly 30 lines.
16. The render output MUST include a `<meta http-equiv="refresh" content="N">` tag in `<head>` where `N` is computed from a `LIVE_RELOAD_SECONDS` config (default 60). If the rotation interval is shorter than `LIVE_RELOAD_SECONDS`, the reload interval is `LIVE_RELOAD_SECONDS`. If the rotation interval is longer (e.g., 300s rotation on a single-page calendar Screen), the reload interval is `max(LIVE_RELOAD_SECONDS, rotation_interval_seconds)` so the reload does not interrupt a single page's first view. The minimum reload interval MUST be 30 seconds even if `LIVE_RELOAD_SECONDS` is configured lower; reloading more often than that is hostile to the device.
17. The handler MUST set `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache` headers so a stale render is never served from a downstream cache.

### Widget Rendering

18. For each page in the Screen, the handler MUST render each widget instance in position order by calling `widget.Default().Render(ctx, instance.Type, instance.Config, theme)`. The returned `templ.Component` is composed into the page's container.
19. If `widget.Default().Render` returns an error (unknown type, validation failure), the handler MUST render a small inline error placeholder (`<div class="widget widget-error">Widget &lt;type&gt; failed to render</div>`) in that widget's slot and log `slog.Warn("widget render failed", "type", ..., "page_id", ..., "screen_id", ..., "err", ...)`. The error placeholder MUST NOT include the raw error message or any of the widget's config bytes (defence in depth; widget config is admin-supplied but might in principle carry information that should not leak to the device kiosk browser).
20. A widget that returns a `nil` component (which the contract forbids but the handler still defends against) MUST be treated the same as an error and render the inline placeholder.
21. The handler MUST NOT short-circuit the page render on a single widget failure. Subsequent widgets on the same page and all subsequent pages MUST still render.
22. The handler MUST NOT issue any additional database queries per widget. The `ScreenFull` shape from SPEC-006 already carries every widget's `Type` and `Config`; the widget package is a pure function over (type, config, theme).

### Empty States

23. A Screen with zero pages MUST render a single "empty" page-container card showing the Screen's name and the message "This screen has no pages yet. Visit /admin/screens to add one." The rotator script handles this (zero pages -> no rotation).
24. A Screen with pages but where every page has zero widgets MUST render each page-container as a card containing the page's name (or "(no name)") and a small muted message "(no widgets on this page)" so the rotation still has visible content.

### Configuration

25. The system MUST add a `LIVE_RELOAD_SECONDS` config setting (integer, default 60, minimum 30). Out-of-range values fail validation at startup.
26. No new secret-bearing config is required.

### Existing Behaviour Preserved

27. The Phase 1 placeholder `views/device.templ::deviceLandingPage` MAY be deleted or repurposed by this spec; the route at `DEVICE_LANDING_URL` MUST still resolve, but its body changes per the rules above.
28. The existing `/admin/devices` page MUST continue to work; the new "assign screen" form is additive on the per-device row.
29. The existing `/admin/screens` CRUD from SPEC-006 MUST continue to work unchanged. (This spec adds a new consumer of `GetScreenFull` but does not alter the screens service surface.)
30. The existing `RequireAuth` chain at the device landing URL MUST be preserved -- no new middleware on the render handler.
31. The existing `/admin/screens` delete behaviour MUST continue to atomically delete pages and widget instances via the FK CASCADE; the new SET NULL on `devices.screen_id` MUST also fire for the same delete (in the same transaction implied by `DELETE FROM screens WHERE id = ?`).
32. No new third-party Go dependencies are introduced. The rotator script is hand-written vanilla JS.

### Admin UI

33. The `/admin/devices` page MUST gain a per-device "Assigned Screen" column showing the assigned Screen's name (or "(none)").
34. The `/admin/devices` page MUST gain a per-device "Assign Screen" form: a `<select>` populated with `(none)` plus every existing Screen, with the current assignment pre-selected; a CSRF hidden input; an "Assign" button POSTing to `/admin/devices/{id}/assign-screen`.
35. The `/admin/devices` page MUST flash a success message (`?msg=assigned` -> "Device assigned to screen." / `?msg=unassigned` -> "Device assignment cleared.") and an error flash (`?error=Screen+not+found`) on the assignment endpoint.
36. The `/admin/screens` and `/admin/screens/{id}/edit` pages MAY (but are not required to) display "Devices currently displaying this screen" as a small metadata row. This is a polish item; v1 ships without it and the architecture leaves a clean extension point.

## Non-Functional Requirements

- **Performance**: The render handler MUST complete in a single `GetScreenFull` call (three SQL queries, established by SPEC-006) plus zero per-widget queries (widget rendering is pure). The rotator script is client-side and consumes no server CPU per rotation. The full-page reload at the configured cadence does mean one render per device per reload interval; with `LIVE_RELOAD_SECONDS=60` and 20 household devices, that is 20 renders per minute, which is well within the project's scale envelope. The render handler MUST NOT do any work proportional to the number of devices, only proportional to the size of one Screen.
- **Security**: The render handler sits behind `RequireAuth`, which accepts either an admin session or a device token. The theme CSS variables are injected verbatim into a `<style>` block; the existing theme field validation (SPEC-004 R7-R14) is what keeps the embedded values safe. The widget config bytes never reach the rendered HTML; only the widget's `templ.Component` does, which auto-escapes interpolated strings. The new `screen_id` column on `devices` is admin-mutable only via the CSRF-protected endpoint; devices cannot reassign themselves. The render endpoint MUST NOT leak the assigned Screen's ID to an unauthenticated request (the existing `RequireAuth` enforces this -- no new code path).
- **Reliability**: A single widget render failure MUST NOT take down the rest of the page (R19-R21). The render handler MUST NOT panic on a missing theme (defence in depth: SPEC-006 RESTRICT FK keeps it impossible to reach here, but a defensive nil check + error placeholder is the right shape). The "device with assigned-but-deleted Screen" branch MUST render the unassigned placeholder, not a 500 (R10b). The auto-reload uses the browser's built-in `<meta refresh>` which survives transient network failures gracefully.
- **Testability**: The render handler is tested via `httptest.NewRecorder` with a populated database (helper: `db.OpenTestDB(t)` + `screens.Service` constructed against a test-only widget registry). The rotator script is unit-tested as plain JS by emitting it and asserting on its presence and on key attributes (a smoke test); end-to-end behaviour testing of the rotator is out of scope for v1 (browser drivers are not in the project's dep set). The widget-error path is tested by registering a test-only widget that always fails validation.
- **Accessibility**: The rendered page MUST set `<html lang="en">` (mirrors the existing layout). Each page container MUST be a `<section>` with an `aria-label="Page N of M: <name or page-N>"` so screen readers announce rotations meaningfully. The error placeholder MUST be marked `role="alert"` so it is announced if a widget fails after the first paint.
- **Backwards compatibility**: This spec adds one nullable column, one new config setting, one new admin endpoint, one new admin UI control, and replaces one placeholder handler body. No existing tables, routes, services, or config defaults change. The `/admin/devices` list grows one column; the admin's existing assignment-free workflow (create device, copy token, revoke device) is unchanged.

## Acceptance Criteria

### Device-to-Screen Assignment

- [ ] AC-1: After the migration runs, the `devices` table has a nullable `screen_id` column, and existing device rows (created before this spec) have `screen_id IS NULL`.
- [ ] AC-2: When an admin POSTs `/admin/devices/{deviceID}/assign-screen` with `screen_id=<valid-screen-id>` and a valid CSRF token, then the device row's `screen_id` is updated, and the response is 302 to `/admin/devices?msg=assigned`.
- [ ] AC-3: When an admin POSTs `/admin/devices/{deviceID}/assign-screen` with `screen_id=` (empty), then the device row's `screen_id` is set to NULL, and the response is 302 to `/admin/devices?msg=unassigned`.
- [ ] AC-4: When an admin POSTs `/admin/devices/{deviceID}/assign-screen` with `screen_id=does-not-exist`, then the device row is unchanged and the response is 302 to `/admin/devices?error=Screen+not+found`.
- [ ] AC-5: When an admin POSTs `/admin/devices/nonexistent/assign-screen` for an unknown device, then the response is 302 to `/admin/devices?error=Device+not+found` and no row is mutated.
- [ ] AC-6: When an admin DELETEs a Screen that two devices reference, then both devices' `screen_id` columns become NULL atomically (FK SET NULL).
- [ ] AC-7: When a member (non-admin) POSTs `/admin/devices/{id}/assign-screen`, then the response is 403 from `RequireRole`.
- [ ] AC-8: When a request to `/admin/devices/{id}/assign-screen` arrives without a valid `_csrf` field, then the response is 403 and no row is mutated.

### Device Render Handler -- Device Identity

- [ ] AC-9: When a device with `screen_id IS NULL` GETs `/device/`, then the response is 200, the body contains the device's name, and the body contains the phrase "No screen assigned".
- [ ] AC-10: When a device whose `screen_id` references an existing Screen with two pages and three total widgets GETs `/device/`, then the response is 200, contains a `<style>` block with `--bg:` (theme CSS variable embedded), contains two `<section class="page-container">` elements, and contains rendered output for each widget.
- [ ] AC-11: When the same device GETs `/device/` a second time after the admin deletes the Screen between requests, then the response is 200 (NOT 500), the body contains "No screen assigned" (the FK SET NULL brought the device back to the unassigned state), and an info log line `"device screen missing"` is NOT emitted (because the SET NULL happened cleanly so the handler does not see a non-nil-but-dangling pointer).
- [ ] AC-12: When a device GETs `/device/` and the handler successfully renders, then the response includes `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache` headers.
- [ ] AC-13: When a device GETs `/device/` and the Screen has `rotation_interval_seconds=15`, then the rendered HTML's `<body>` carries `data-rotation-seconds="15"` (the rotator script reads this attribute).
- [ ] AC-14: When a device GETs `/device/` and `LIVE_RELOAD_SECONDS=60`, rotation interval is 15, then the rendered HTML's `<meta http-equiv="refresh">` content attribute is `60` (the larger of the two with the 30s floor applied).
- [ ] AC-15: When a device GETs `/device/` and the Screen has zero pages, then the response is 200, contains the Screen's name, and contains the phrase "no pages yet".

### Device Render Handler -- Admin Identity

- [ ] AC-16: When an admin GETs `/device/` (no `?screen=` query parameter), then the response is 200, the body lists the admin's available Screens with links to `/device/?screen=<id>`.
- [ ] AC-17: When an admin GETs `/device/?screen=<valid-id>`, then the response is 200 and renders that Screen's content the same as a device would see it.
- [ ] AC-18: When an admin GETs `/device/?screen=does-not-exist`, then the response is 200 with a "Screen not found" error card. (NOT a 404 -- the URL itself is valid; the query parameter is invalid; the user-facing error is what matters.)

### Widget Rendering

- [ ] AC-19: When a device's Screen has a single page containing a `text` widget with body "hello", then the rendered HTML contains the literal substring `hello` (the text widget's output -- HTML-escaped by templ, which leaves bare ASCII unchanged).
- [ ] AC-20: When a device's Screen has a page containing a widget whose `type` is not registered (simulated by setting `widget_instances.type='nonexistent'` directly in the test DB), then the rendered HTML contains a `widget-error` placeholder for that widget, the other widgets on the same page still render, and a `slog.Warn` line is emitted with the unknown type.
- [ ] AC-21: When a device's Screen has a page containing a `text` widget whose stored config is malformed JSON (also simulated via direct DB write), then the rendered HTML contains a `widget-error` placeholder and the response is still 200.
- [ ] AC-22: When `widget_instances.config` contains JSON that *would* round-trip cleanly through the widget's validator (e.g., a valid `text` config), then the rendered HTML contains the widget's normal output, NOT the error placeholder. (Confirms the render path does not over-trigger the placeholder.)

### Auto-Rotation Script

- [ ] AC-23: When any device or admin-preview render succeeds, then the rendered HTML contains a `<script>` element whose body references `page-container` and `page-current` and uses `setInterval` (verified by string search; deeper JS behaviour testing is out of scope per Non-Functional > Testability).
- [ ] AC-24: When the Screen has exactly one page, then the body still contains the rotator script (it is unconditional) but is verified to early-exit (script body contains the conditional that handles `length === 1` or `< 2`).

### Empty States

- [ ] AC-25: When a Screen has pages but no widgets on any page, then the rendered HTML contains a `(no widgets on this page)` muted message inside each page container.

### Live Reload

- [ ] AC-26: When `LIVE_RELOAD_SECONDS=45` is set on startup, the config validation accepts it (it is >= 30).
- [ ] AC-27: When `LIVE_RELOAD_SECONDS=10` is set on startup, the config validation rejects it (below 30s floor) and the service fails to start.

### Existing Behaviour Preserved

- [ ] AC-28: After this spec ships, GET `/admin/screens` still returns the Screen list (Spec-006 AC-8 still passes).
- [ ] AC-29: After this spec ships, the device enrollment flow (POST to enroll-browser, redirect to `/device/`) still completes without error. The kiosk browser arrives at `/device/`; an unassigned newly enrolled device sees the "No screen assigned" placeholder, NOT a 500.
- [ ] AC-30: After this spec ships, `/admin/themes/{themeID}/delete` still rejects deletion of a theme in use by a screen (Spec-006 AC-9 still passes).

## Out of Scope

- Server-Sent Events, WebSocket, htmx polling, or any other server-push live-reload mechanism. The full-page `<meta refresh>` is the v1 mechanism; a future spec can layer SSE / WebSocket on top if a real use case demands sub-30s update visibility.
- Per-device per-page state (server-side tracking of which page each device is currently on). The rotator is purely client-side; the server is stateless from a rendering perspective.
- Server-driven rotation (the admin clicks "go to page 2 on the kitchen tablet"). That is push-to-device territory, more naturally owned by the Phase 4 push-notifications spec.
- Drag-to-rearrange page order on the device side. Reorder happens in the admin (SPEC-006 MovePageUp/Down); the device just renders.
- Per-widget refresh independent of the page. v1 widgets either accept what the page-level reload gives them (every `LIVE_RELOAD_SECONDS`) or are stateless. Phase 3 widgets that need finer-grained refresh own that in their own spec.
- Per-device theme override. The Screen's theme is the device's theme. A future spec can add per-device overrides if a real use case appears.
- Per-device rotation-interval override. The Screen's `rotation_interval_seconds` is the device's rotation interval. Two devices on the same Screen rotate in sync (modulo their independent client-side timers' phase).
- A separate `/preview/{screenID}` admin route for live preview. The `?screen=<id>` query parameter on `/device/` is the lighter-weight equivalent (admins type that URL, no new route to maintain). A dedicated preview UX with a chrome around the rendered Screen (a "view as device kiosk" frame) is polish-phase work.
- PWA service worker, manifest, install instructions. Owned by Phase 4 PWA spec; this spec's render output is the input to that spec, not a substitute for it.
- A "fullscreen / kiosk-mode" CSS toggle. The device browser is assumed to be in kiosk mode at the OS / browser level; the rendered HTML occupies the full viewport via CSS but does not call the Fullscreen API.
- Real-time admin notifications when a device disconnects or shows an error. The existing `last_seen_at` tracking from SPEC-003 is enough; the admin can look at `/admin/devices` and see which devices have stale `last_seen_at` timestamps.
- Per-screen alert overlays ("mom says"). Owned by the Phase 4 Alerts spec; the Screen Display pipeline will integrate as a Phase 4 additive change once that spec lands.
- Per-screen analytics (which widgets render most often, which pages spend the most time visible). Out of scope.
- A `?test=true` mode that injects fake widgets for visual regression testing. Out of scope; the existing widget tests cover render correctness.
- Per-page background images. Owned by the Phase 2 Page Backgrounds spec (p1) which adds an additive column on `pages`. The Screen Display renderer will pick it up automatically when the column is populated; this spec does not need to know about it.
- Per-page custom layouts (multi-column, named slots). The 1-D layout from SPEC-006 / ADR-006 is what v1 renders. A future grid spec extends both the data model and the renderer additively.

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- needs the migration runner and `db.OpenTestDB(t)` test helper.
- Depends on: SPEC-002 (Admin Auth) -- needs `RequireAuth`, `RequireRole(RoleAdmin)`, `RequireCSRF` for the new device-assignment endpoint.
- Depends on: SPEC-003 (Device Auth) -- needs the existing `devices` table to extend with `screen_id`, the existing device identity flowing through `RequireAuth`, the device cookie / bearer token flow, and the existing `/admin/devices` admin UI to extend with the new assignment form.
- Depends on: SPEC-004 (Theme System) -- needs `themes.Theme.CSSVariables()` (already shipped) for the inline `<style>` block.
- Depends on: SPEC-005 (Widget Interface) -- needs `widget.Registry.Render` for the per-widget render dispatch.
- Depends on: SPEC-006 (Screen Model) -- needs `screens.Service.GetScreenFull` (already shipped) for the per-render data fetch, plus the `screens` table existence for the `devices.screen_id` FK.
- No new external dependencies.

## Open Questions

All resolved.

- Q1 **Resolved**: Pages render server-side once per reload; the rotator cycles client-side without re-fetching. The alternative (fetch one page at a time on the timer) would require either an htmx-style fragment endpoint or a client-side AJAX call, each with its own validation surface, its own per-fetch network cost, and a "what if the request fails?" branch the kiosk has to handle. Bundling every page into one render eliminates the network round-trip per rotation and makes the offline-PWA story trivial in Phase 4 (cache one document; rotate within it). See ADR-007.
- Q2 **Resolved**: Live reload is a `<meta http-equiv="refresh">` tag, NOT SSE / WebSocket / htmx polling. The mechanism is browser-native, has no open-connection cost, survives transient network failures, and matches the project's minimal-framework ethos. The downside (admin edits take up to `LIVE_RELOAD_SECONDS` to show up) is acceptable for v1 -- if a use case demands sub-minute visibility, a future spec adds SSE on top of the same render handler. See ADR-008.
- Q3 **Resolved**: Theme CSS variables are inline in a `<style>` block per render, NOT a separate stylesheet endpoint. The trade-off mirrors SPEC-004's existing rendering helper: theme values are validated at write time, rendering is pure (deterministic, cheap), and inline shipping avoids the flash-of-unstyled-content. A separate stylesheet endpoint would add a route, a cache header strategy, and a per-render-context theme lookup, all to save bytes that are already small. See ADR-008.
- Q4 **Resolved**: Device-to-Screen mapping is a nullable `screen_id` column on `devices` with `ON DELETE SET NULL`, not a separate join table. The relationship is exactly 1:0..1 (a device renders at most one Screen at a time; a Screen has zero or more devices); a column is the smallest representation. A join table would imply N:M (one device renders multiple Screens at once -- which is not the model -- or a Screen has rotation history per device -- also not the model). The SET NULL on theme-delete-equivalent (Screen-delete) keeps devices safely unassigned rather than orphaned or hard-failing on a render. See ADR-009.
- Q5 **Resolved**: The render handler is at the existing `DEVICE_LANDING_URL` (`/device/` by default), replacing the Phase 1 placeholder body. A separate `/render/...` URL was considered and rejected: the device's bookmark is the landing URL, the enrollment flow already redirects there, and adding a second device-facing URL is more code with no benefit. The admin "preview" path is the `?screen=<id>` query parameter on the same URL.
- Q6 **Resolved**: A widget render failure renders an inline error placeholder; it does NOT take down the page. Two-layer validation (write + render) from SPEC-005 / ADR-005 closes the "hand-edited bad row" hole; this spec implements the render-side layer. The placeholder is generic ("Widget &lt;type&gt; failed to render") and does NOT include error details to prevent any incidental information leak through the device kiosk browser.
- Q7 **Resolved**: An admin viewing the device URL without `?screen=` lands on a Screen-picker page rather than a 404 or a "this URL is for devices" error. Admins use the URL during configuration; giving them a usable surface there saves a context switch. The picker links to `/device/?screen=<id>` for live preview, mirroring how the device itself would render.
- Q8 **Resolved**: `LIVE_RELOAD_SECONDS` is a config setting with a hard floor of 30 seconds. Reloading more often than that risks interrupting the user's view of a page mid-content and adds load that v1 doesn't justify. If a future spec needs sub-30s reload, it should switch to SSE or WebSocket, not reduce the floor.
