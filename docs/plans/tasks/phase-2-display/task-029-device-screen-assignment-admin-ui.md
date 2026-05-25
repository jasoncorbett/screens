---
id: TASK-029
title: "Device-to-Screen assignment admin UI: assignment column + form on /admin/devices, POST /admin/devices/{id}/assign-screen handler"
spec: SPEC-007
arch: ARCH-007
status: ready
priority: p0
prerequisites: [TASK-027]
skills: [add-view, add-endpoint, green-bar]
created: 2026-05-25
author: architect
---

# TASK-029: Device-to-Screen assignment admin UI: assignment column + form on /admin/devices, POST /admin/devices/{id}/assign-screen handler

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Surface the device-to-Screen assignment in the existing `/admin/devices` admin UI: add a new "Assigned Screen" column per active device, an inline `<select>` form for picking a Screen (or "(none)") with an Assign button, the corresponding POST handler `handleDeviceAssignScreen`, the route registration, and the flash-text helper extensions. This task does NOT touch the device render handler -- that is TASK-030. It only equips the admin to set the assignment.

## Context

- The existing admin device list page (`views/devices.templ::devicesPage`) renders a table of active devices and a separate table of revoked devices, plus enrollment forms. This task extends the active-devices table by one column (Assigned Screen) and adds a per-row assignment form. Revoked devices are NOT given an assignment form.
- The existing flash pattern uses `?msg=...` for success and `?error=...` for failure (see `handleDeviceRevoke`, `handleDeviceEnrollExisting`). The new handler follows the same pattern: success → 302 to `/admin/devices?msg=assigned` (or `msg=unassigned`), failure → 302 to `/admin/devices?error=Screen+not+found` (or `Device+not+found`).
- The handler MUST pre-check that a non-empty `screen_id` form value resolves to a real Screen via `screens.Service.GetScreenByID`. This is because the `internal/auth` package does NOT depend on `internal/screens` (would create an import cycle), so the existence check has to live in the handler tier. The FK constraint at the DB layer is the second defence; the pre-check is the first.
- The route registration in `views/routes.go::deviceMux` already mounts under `/admin/devices/` with `RequireRole(RoleAdmin)`. Add the new handler to that mux.
- The `Deps.Screens *screens.Service` field already exists (added by TASK-025 for the existing screen-management admin UI). The handler and the list page reuse it.

### Files to Read Before Starting

- `.claude/rules/http.md` -- handler conventions.
- `.claude/rules/testing.md` -- testing conventions.
- `.claude/skills/add-view/SKILL.md`, `.claude/skills/add-endpoint/SKILL.md` -- the scaffolds.
- `views/devices.go` -- existing handler patterns (especially `handleDeviceList`, `handleDeviceRevoke`, and `performBrowserEnrollment`).
- `views/devices.templ` -- the page template to extend.
- `views/devices_test.go` -- existing test patterns (admin context helpers, CSRF cases).
- `views/routes.go` -- the `deviceMux` to extend with the new route.
- `internal/auth/auth.go` -- the `AssignDeviceToScreen` method shipped by TASK-027.
- `internal/screens/service.go` -- the `GetScreenByID` method (used for the pre-check).
- `docs/plans/specs/phase-2-display/spec-screen-display.md` -- requirements 5-7, 33-35; AC-2 through AC-8, AC-29.
- `docs/plans/architecture/phase-2-display/arch-screen-display.md` -- "Component Design > Device admin list -- new assign form" and "Assign-Screen handler" sections.

## Requirements

### Handler

1. Add a new handler factory in `views/devices.go`:
   ```go
   func handleDeviceAssignScreen(authSvc *auth.Service, screensSvc *screens.Service) http.HandlerFunc
   ```
   that:
   - Reads `user := auth.UserFromContext(ctx)`; 403 if nil.
   - Reads `deviceID := r.PathValue("id")`; if empty, 302 to `/admin/devices?error=Missing+device+ID`.
   - Reads `screenID := strings.TrimSpace(r.FormValue("screen_id"))`.
   - If `screenID != ""`, calls `screensSvc.GetScreenByID(ctx, screenID)`. On `errors.Is(err, screens.ErrScreenNotFound)`, 302 to `/admin/devices?error=Screen+not+found`. On any other error, log slog.Error and 302 to `?error=Could+not+assign+screen`.
   - Calls `authSvc.AssignDeviceToScreen(ctx, deviceID, screenID)`. On `errors.Is(err, auth.ErrDeviceNotFound)`, 302 to `?error=Device+not+found`. On any other error, log + 302 to `?error=Could+not+assign+screen`.
   - On success: log `slog.Info("device screen assigned", "device_id", deviceID, "screen_id", screenID, "assigned_by", user.Email)` and 302 to `/admin/devices?msg=assigned` (or `?msg=unassigned` if `screenID == ""`).

### Route registration

2. Edit `views/routes.go::registerAuthRoutes` to add the new route inside the existing `deviceMux`:
   ```go
   deviceMux.HandleFunc("POST /admin/devices/{id}/assign-screen", handleDeviceAssignScreen(deps.Auth, deps.Screens))
   ```
   The existing wildcard mount `adminMux.Handle("/admin/devices/", ...)` covers the new path.

### List handler / templ updates

3. Update `views/devices.go::handleDeviceList` to also fetch the screen list (for the `<select>` options):
   ```go
   summaries, err := screensSvc.ListScreens(ctx)
   if err != nil { ... }
   ```
   and pass `summaries` (typed as `[]screens.ScreenSummary`) into the templ. Change the signature of `handleDeviceList` to take `screensSvc *screens.Service` in addition to `authSvc`, and update its call site in `views/routes.go` to pass `deps.Screens`.

4. Update `views/devices.templ::devicesPage` signature to accept the new screens slice and to render:
   - A new column header "Assigned Screen" between "Last Seen" and "Actions" in the Active Devices table.
   - A new column body per active device row: shows the assigned screen's name (or "(none)") from a small helper `lookupScreenName(screensList, *d.ScreenID)` (returns "(none)" when `d.ScreenID == nil`).
   - A new `<form>` inside the Actions column (placed alongside the existing Revoke form) with:
     - `method="POST"`, `action="/admin/devices/{d.ID}/assign-screen"`.
     - Hidden CSRF input.
     - A `<select name="screen_id">` with `<option value="">(none)</option>` plus one `<option value="{s.ID}" [selected if d.ScreenID == s.ID]>{s.Name}</option>` per screen.
     - A `<button type="submit">Assign</button>`.

5. Add the small Go helper `lookupScreenName(screens []screens.ScreenSummary, id string) string` in `views/devices.go`. If `id` matches a screen's `ID`, return `s.Name`; otherwise return `id` (defence in depth: shows the raw ID if the screen was somehow not in the list).

6. Update the `handleDeviceList` flash-message branch to also handle `msg=assigned` (text: "Device assigned to screen.") and `msg=unassigned` (text: "Device assignment cleared.").

### Templ regen

7. Run `templ generate` after editing `views/devices.templ`. Commit the regenerated `views/devices_templ.go`.

### Revoked-device read-only display

8. The revoked-devices table MAY show the last `screen_id` value (read-only) but MUST NOT render the assignment form. The simplest correct shape is to leave the revoked-devices table unchanged (the spec leaves this as MAY); only the active-devices section gets the new column and form. Pick the simpler option: do not change the revoked-devices table.

## Acceptance Criteria

From SPEC-007:

- [ ] AC-2: POST `/admin/devices/{deviceID}/assign-screen` with `screen_id=<valid>` and a valid CSRF → 302 to `/admin/devices?msg=assigned`; the device row's `screen_id` is updated.
- [ ] AC-3: POST `/admin/devices/{deviceID}/assign-screen` with `screen_id=` (empty) → 302 to `?msg=unassigned`; the device row's `screen_id` is NULL.
- [ ] AC-4: POST `/admin/devices/{deviceID}/assign-screen` with `screen_id=does-not-exist` → 302 to `?error=Screen+not+found`; the device row is unchanged.
- [ ] AC-5: POST `/admin/devices/nonexistent/assign-screen` → 302 to `?error=Device+not+found`; no row mutated.
- [ ] AC-7: Member (non-admin) POST → 403 from `RequireRole`.
- [ ] AC-8: POST without `_csrf` → 403 from `RequireCSRF`; no row mutated.

Plus task-internal:

- [ ] AC-T1: GET `/admin/devices` as admin shows the new "Assigned Screen" column for every active device.
- [ ] AC-T2: GET `/admin/devices` as admin shows the `<select>` form per active device with `(none)` plus every screen as an option; the current assignment (if any) is `selected`.
- [ ] AC-T3: The success flash text for `msg=assigned` reads "Device assigned to screen." and for `msg=unassigned` reads "Device assignment cleared.".

## Skills to Use

- `add-view` -- templ + handler updates.
- `add-endpoint` -- the new POST route.
- `green-bar` -- run before marking review (includes `templ generate`).

## Test Requirements

Tests live in `views/devices_test.go` (extend existing) and possibly a new `views/devices_assign_screen_test.go`. Mirror the existing helpers (admin context, CSRF cases, `httptest.NewRecorder`).

1. **Assign happy path**: create a device + a screen, POST `/admin/devices/{deviceID}/assign-screen` with `screen_id=<id>` + `_csrf`. Assert 302 to `/admin/devices?msg=assigned`. Assert `ListDevices` returns the device with `ScreenID != nil && *ScreenID == screenID`.

2. **Clear assignment**: create a device with an assignment (via direct service call), POST with `screen_id=` + `_csrf`. Assert 302 to `?msg=unassigned`. Assert `ScreenID == nil`.

3. **Unknown screen**: POST with `screen_id=does-not-exist`. Assert 302 to `?error=Screen+not+found`. Assert the device's `ScreenID` is unchanged.

4. **Unknown device**: POST to `/admin/devices/nonexistent/assign-screen` with `screen_id=<valid>`. Assert 302 to `?error=Device+not+found`. (Note: by this point the screen pre-check has already passed; the failure is in the auth service.)

5. **CSRF rejected**: full `httptest.NewServer` exercising the chain; POST WITHOUT `_csrf`. Assert 403. Assert the device's `ScreenID` is unchanged.

6. **Member 403**: full server, log in as a non-admin user (use the existing `createTestMember` helper); POST. Assert 403.

7. **List page renders assignment column**: create two devices, assign one of them to a screen. GET `/admin/devices` as admin. Assert the body contains the screen's name for the assigned device and `(none)` for the unassigned one. Assert the body contains a `<select name="screen_id">` for each active device.

Follow `.claude/rules/testing.md`: table-driven cases where setup is shared, `t.Helper()` in helpers.

## Definition of Done

- [ ] `views/devices.go` has the new handler + the `lookupScreenName` helper + updated `handleDeviceList` signature.
- [ ] `views/devices.templ` (regenerated `views/devices_templ.go`) renders the new column and form.
- [ ] `views/routes.go` registers `POST /admin/devices/{id}/assign-screen` and updates the `handleDeviceList` call to pass `deps.Screens`.
- [ ] All acceptance criteria tests pass.
- [ ] green-bar passes (`templ generate` was run; committed `_templ.go` matches `.templ`).
- [ ] No new third-party dependencies.
- [ ] No raw input is logged; the slog line for a successful assignment includes only IDs and the actor's email.
