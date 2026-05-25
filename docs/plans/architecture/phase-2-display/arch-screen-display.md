---
id: ARCH-007
title: "Screen Display"
spec: SPEC-007
status: draft
created: 2026-05-25
author: architect
---

# Screen Display Architecture

## Overview

Screen Display is the renderer that ties Phase 2 together. It replaces the Phase 1 placeholder body at `DEVICE_LANDING_URL` (`/device/` by default) with a real render handler that pulls a `screens.ScreenFull` tree from `screens.Service.GetScreenFull`, embeds the active theme's CSS variables in an inline `<style>` block, renders every page's widget instances through the widget registry, and ships a tiny vanilla-JS rotator script plus a `<meta http-equiv="refresh">` tag for live-reload. The device's identity is mapped to a Screen via a new nullable `devices.screen_id` column (added by a SPEC-007 migration); admins manage the assignment via one new endpoint and form on the existing `/admin/devices` page. An admin viewing `/device/` from a non-kiosk browser sees a Screen-picker page; with `?screen=<id>` they get a live preview.

Three ADRs accompany this architecture: ADR-007 (server-render-all-pages + client-side rotation), ADR-008 (`<meta refresh>` live-reload + inline theme CSS), and ADR-009 (device-to-Screen mapping via nullable FK column with SET NULL).

No new external dependencies. All new code is vanilla Go and roughly 30 lines of vanilla JS.

## References

- Spec: `docs/plans/specs/phase-2-display/spec-screen-display.md`
- Related ADRs: ADR-007 (rendering strategy), ADR-008 (live-reload + theme CSS delivery), ADR-009 (device-to-Screen mapping)
- Prerequisite ADRs: ADR-001 (Storage Engine), ADR-003 (Device Auth), ADR-004 (Theme System), ADR-005 (Widget Interface), ADR-006 (Screen Model)
- Prerequisite architecture: ARCH-003 (Device Auth), ARCH-004 (Theme System), ARCH-005 (Widget Interface), ARCH-006 (Screen Model)

## Data Model

### Database schema change

```sql
-- 010_add-device-screen-id.sql
-- +up
-- SQLite supports adding a NOT NULL-able FK column via ALTER TABLE on a
-- table that already exists. The FK is enforced at write time when the
-- PRAGMA foreign_keys = ON connection setting is active (which db.Open
-- already configures). Existing rows get NULL by definition (no
-- backfill).
ALTER TABLE devices ADD COLUMN screen_id TEXT
    REFERENCES screens(id) ON DELETE SET NULL;

CREATE INDEX idx_devices_screen_id ON devices(screen_id);

-- +down
DROP INDEX IF EXISTS idx_devices_screen_id;
-- SQLite cannot DROP COLUMN before 3.35; modernc.org/sqlite ships >= 3.40
-- so DROP COLUMN works. We use it here.
ALTER TABLE devices DROP COLUMN screen_id;
```

Notes:

- `ON DELETE SET NULL` is the FK behaviour for Screen deletes. When an admin deletes a Screen that two devices reference, both devices' `screen_id` columns become NULL in the same transaction as the DELETE.
- The migration is additive (`ALTER TABLE ... ADD COLUMN`). No data migration is required: existing devices have `screen_id IS NULL`, which the handler treats as "no Screen assigned".
- The index on `devices.screen_id` is for fast "which devices reference this Screen?" lookups -- not used by Screen Display directly, but useful for future admin-side "devices displaying this screen" widgets and for the FK cascade.
- The `ALTER TABLE ... ADD COLUMN ... REFERENCES ...` syntax IS supported by SQLite for tables that exist, but the FK is enforced only on rows inserted / updated AFTER the column is added; SQLite does not re-validate existing rows. Existing rows all have NULL (which trivially satisfies the FK), so this is fine.

### Go domain type changes

```go
// internal/auth/device.go (existing file, additive change)

type Device struct {
    ID         string
    Name       string
    TokenHash  string
    CreatedBy  string
    CreatedAt  time.Time
    LastSeenAt *time.Time
    RevokedAt  *time.Time
    ScreenID   *string  // NEW: nil = no Screen assigned; pointer matches LastSeenAt/RevokedAt convention
}

// deviceFromRow gains:
if row.ScreenID.Valid {
    s := row.ScreenID.String
    dev.ScreenID = &s
}
```

### New error variable

```go
// internal/auth/auth.go (additive)

// ErrScreenNotFoundForAssignment is returned by AssignDeviceToScreen when the
// caller-supplied screenID does not resolve to an existing Screen. Distinct
// from screens.ErrScreenNotFound to keep package boundaries clean; the admin
// handler translates both to the same user-visible "Screen not found" flash.
var ErrScreenNotFoundForAssignment = errors.New("screen not found for device assignment")
```

### New service method

```go
// internal/auth/auth.go (additive)

// AssignDeviceToScreen sets the device's screen_id to the given screenID, or
// clears it if screenID == "". If screenID is non-empty, the caller MUST have
// already validated the Screen exists -- the auth package does not depend on
// the screens package (would create an import cycle). The handler tier
// (views/devices.go) performs the existence check via screens.Service.GetByID
// before calling this method.
//
// Returns ErrDeviceNotFound when the device id is unknown. Note: the FK
// constraint at the DB layer ALSO catches an invalid screenID, but the
// pre-check in the handler keeps the user-visible flow clean.
func (s *Service) AssignDeviceToScreen(ctx context.Context, deviceID, screenID string) error {
    var screenIDArg sql.NullString
    if screenID != "" {
        screenIDArg = sql.NullString{String: screenID, Valid: true}
    }
    res, err := s.queries.AssignDeviceScreen(ctx, db.AssignDeviceScreenParams{
        ScreenID: screenIDArg,
        ID:       deviceID,
    })
    if err != nil {
        return fmt.Errorf("assign device screen: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return fmt.Errorf("assign device screen rows: %w", err)
    }
    if n == 0 {
        return ErrDeviceNotFound
    }
    return nil
}
```

The handler tier calls `screens.Service.GetScreenByID` before this method when `screenID != ""` to convert the "Screen does not exist" case into the user-visible flash before the database mutation runs.

## API Contract

### Endpoints

| Method | Path                                       | Request Body                          | Response                                                  | Auth                |
|--------|--------------------------------------------|---------------------------------------|-----------------------------------------------------------|---------------------|
| GET    | `/device/`                                 | -                                     | HTML rendered Screen (or unassigned/picker placeholder)   | device OR admin     |
| GET    | `/device/?screen=<id>`                     | -                                     | HTML rendered Screen (admin live preview)                 | admin (device-id with `?screen=` is rejected as 403; see Component Design > handleDeviceRender) |
| POST   | `/admin/devices/{id}/assign-screen`        | `screen_id`, `_csrf`                  | 302 → `/admin/devices?msg=assigned` / `?msg=unassigned`   | admin               |

The `/device/` URL path is whatever `DEVICE_LANDING_URL` is set to (default `/device/`); the table uses the default. The render handler replaces the existing `handleDeviceLanding` body but keeps the same route registration in `views/routes.go`.

### Request / response examples

A device GETting `/device/`:

```http
GET /device/ HTTP/1.1
Cookie: screens_device=<raw-device-token>

HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
Cache-Control: no-store, must-revalidate
Pragma: no-cache

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta http-equiv="refresh" content="60">
  <title>kitchen — screens</title>
  <link rel="stylesheet" href="/static/css/app.css">
  <link rel="stylesheet" href="/static/css/device.css">
  <style>
:root {
  --bg: #0b0d11;
  --surface: #14171f;
  ...
}
  </style>
</head>
<body data-rotation-seconds="30">
  <section class="page-container page-current" aria-label="Page 1 of 3: clock">
    <div class="widget widget-text">12:30 PM</div>
  </section>
  <section class="page-container" aria-label="Page 2 of 3: weather">
    ...
  </section>
  <section class="page-container" aria-label="Page 3 of 3: calendar">
    ...
  </section>
  <script>
    (function () {
      document.addEventListener('DOMContentLoaded', function () { /* rotator */ });
    })();
  </script>
</body>
</html>
```

An unassigned device GETting `/device/`:

```http
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8

<!DOCTYPE html>
<html lang="en">
<head>...minimal head, no theme...</head>
<body>
  <div class="hero">
    <h1>kitchen-tablet</h1>
  </div>
  <div class="card">
    <p>No screen assigned. Visit <a href="/admin/devices">/admin/devices</a> to assign one.</p>
  </div>
</body>
</html>
```

An admin POSTing the assignment form:

```http
POST /admin/devices/abc123/assign-screen HTTP/1.1
Content-Type: application/x-www-form-urlencoded

screen_id=def456&_csrf=...

HTTP/1.1 302 Found
Location: /admin/devices?msg=assigned
```

An admin clearing the assignment:

```http
POST /admin/devices/abc123/assign-screen HTTP/1.1
Content-Type: application/x-www-form-urlencoded

screen_id=&_csrf=...

HTTP/1.1 302 Found
Location: /admin/devices?msg=unassigned
```

## Component Design

### Package Layout

```
internal/
  auth/
    auth.go               -- MODIFY: add AssignDeviceToScreen; add ErrScreenNotFoundForAssignment (small)
    device.go             -- MODIFY: add ScreenID *string to Device; update deviceFromRow
    auth_test.go          -- MODIFY: add tests for AssignDeviceToScreen
  config/
    config.go             -- MODIFY: add LiveReloadSeconds int field; parse + validate
  db/
    migrations/
      010_add-device-screen-id.sql                    -- NEW
    queries/
      devices.sql                                     -- MODIFY: add AssignDeviceScreen; update existing SELECTs to include screen_id
    devices.sql.go                                    -- REGEN: sqlc generate
    models.go                                         -- REGEN: Device gains ScreenID sql.NullString
    screens_schema_test.go                            -- (existing) -- still passes
views/
  device.go               -- REWRITE: handleDeviceLanding -> handleDeviceRender
  device.templ            -- REWRITE: replace placeholder with deviceRenderedScreenPage + deviceUnassignedPage + deviceAdminPickerPage
  devices.go              -- MODIFY: add handleDeviceAssignScreen handler; List handler now fetches screens for the <select>
  devices.templ           -- MODIFY: add Assigned Screen column + assignment form per active device
  routes.go               -- MODIFY: register POST /admin/devices/{id}/assign-screen
  device_test.go          -- REWRITE: tests for the new render handler (existing skeleton is small)
  devices_test.go         -- MODIFY: add assignment-endpoint tests
  device_adversarial_test.go         -- NEW: edge cases (deleted Screen, widget-render failure, malformed config row)
static/
  css/
    device.css            -- NEW: page-container layout, rotation transition rules, widget-error placeholder styling
  js/
    device-rotator.js     -- NEW: vanilla-JS rotator script (embedded inline via templ, OR served as a separate file -- see Decision below)
main.go                   -- MODIFY: thread cfg.HTTP.LiveReloadSeconds into views.Deps
```

### Rotator script: inline or file?

ADR-007 chooses **inline**. Three reasons:

1. Cache atomicity. The rotator references the page-container DOM structure that this specific render emitted. Shipping the script inline means the rotator and the DOM are versioned together -- a stale cached `device-rotator.js` could not break a freshly rendered DOM that includes a markup change.
2. PWA story. The Phase 4 service worker caches one HTML document; an inline script is part of that document for free.
3. Size. The script is ~30 lines / ~700 bytes. Saving HTTP request count beats saving bytes at this scale.

If a future spec adds a much larger device-side script (e.g., an alert overlay receiver in Phase 4), that script ships as a separate file because its size justifies the second HTTP request, and it can be cached at the service-worker level.

### Templates (views/device.templ)

Three new templ components:

```go
// deviceRenderedScreenPage is the full Screen render: theme CSS inline, every
// page as a <section class="page-container">, every widget rendered through
// the registry, the rotator script inline, the live-reload <meta> tag.
templ deviceRenderedScreenPage(
    full screens.ScreenFull,
    pageBodies []templ.Component,    // pre-rendered page content (one per page) -- includes the widget templ components composed in
    rotationSeconds int,
    reloadSeconds int,
)

// deviceUnassignedPage is the placeholder shown when a device has no
// screen_id assigned (or the screen_id resolved to a missing Screen). The
// page is plain (no theme CSS), so a stale device whose Screen was deleted
// still displays clearly.
templ deviceUnassignedPage(deviceName string)

// deviceAdminPickerPage is the page shown when an admin GETs /device/
// without a ?screen= query parameter. Lists available Screens with links to
// /device/?screen=<id>.
templ deviceAdminPickerPage(screens []screens.ScreenSummary, currentUser *auth.User)

// deviceScreenNotFoundPage is the error-card page for /device/?screen=<unknown>.
templ deviceScreenNotFoundPage(currentUser *auth.User)
```

The page bodies are composed in the handler (not the templ) because each page's widgets are rendered by iterating `full.Pages` and calling the registry. The handler builds a `[]templ.Component`, one per page, then passes the slice into the templ.

A small templ helper renders a single widget instance (used inside the per-page composition):

```go
templ widgetSlot(component templ.Component) {
    @component
}

templ widgetErrorSlot(typeName string) {
    <div class="widget widget-error" role="alert">
        Widget { typeName } failed to render
    </div>
}
```

### The render handler (views/device.go)

```go
// handleDeviceRender replaces handleDeviceLanding. The route registration in
// views/routes.go continues to use RequireAuth ONLY (no RequireRole, no
// RequireCSRF). The handler branches on identity kind.
func handleDeviceRender(
    authSvc *auth.Service,
    screensSvc *screens.Service,
    widgets *widget.Registry,
    reloadSeconds int,
) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        id := auth.IdentityFromContext(ctx)
        if id == nil {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }

        // No-cache headers (R17) apply to every render path.
        w.Header().Set("Cache-Control", "no-store, must-revalidate")
        w.Header().Set("Pragma", "no-cache")

        switch {
        case id.IsDevice() && id.Device != nil:
            renderForDevice(w, r, id.Device, screensSvc, widgets, reloadSeconds)

        case id.IsAdmin():
            renderForAdmin(w, r, id.User, screensSvc, widgets, reloadSeconds)

        default:
            // Should not happen given RequireAuth, but defend in depth.
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
    }
}

func renderForDevice(
    w http.ResponseWriter, r *http.Request, dev *auth.Device,
    screensSvc *screens.Service, widgets *widget.Registry, reloadSeconds int,
) {
    ctx := r.Context()
    if dev.ScreenID == nil {
        deviceUnassignedPage(dev.Name).Render(ctx, w)
        return
    }
    full, err := screensSvc.GetScreenFull(ctx, *dev.ScreenID)
    if err != nil {
        if errors.Is(err, screens.ErrScreenNotFound) {
            // Race with delete; FK SET NULL should have prevented this but
            // defend in depth (R10b).
            slog.Info("device screen missing",
                "device_id", dev.ID,
                "screen_id", *dev.ScreenID)
            deviceUnassignedPage(dev.Name).Render(ctx, w)
            return
        }
        slog.Error("device get screen full", "err", err,
            "device_id", dev.ID, "screen_id", *dev.ScreenID)
        http.Error(w, "Internal server error", http.StatusInternalServerError)
        return
    }
    pageBodies := renderPageBodies(ctx, full, widgets)
    rotation := computeRotation(full.Screen.RotationIntervalSeconds)
    reload := computeReload(rotation, reloadSeconds)
    deviceRenderedScreenPage(full, pageBodies, rotation, reload).Render(ctx, w)
}

func renderForAdmin(
    w http.ResponseWriter, r *http.Request, user *auth.User,
    screensSvc *screens.Service, widgets *widget.Registry, reloadSeconds int,
) {
    ctx := r.Context()
    screenID := r.URL.Query().Get("screen")
    if screenID == "" {
        summaries, err := screensSvc.ListScreens(ctx)
        if err != nil {
            slog.Error("admin /device list screens", "err", err)
            http.Error(w, "Internal server error", http.StatusInternalServerError)
            return
        }
        deviceAdminPickerPage(summaries, user).Render(ctx, w)
        return
    }
    full, err := screensSvc.GetScreenFull(ctx, screenID)
    if err != nil {
        if errors.Is(err, screens.ErrScreenNotFound) {
            deviceScreenNotFoundPage(user).Render(ctx, w)
            return
        }
        slog.Error("admin /device get screen full",
            "err", err, "screen_id", screenID)
        http.Error(w, "Internal server error", http.StatusInternalServerError)
        return
    }
    pageBodies := renderPageBodies(ctx, full, widgets)
    rotation := computeRotation(full.Screen.RotationIntervalSeconds)
    reload := computeReload(rotation, reloadSeconds)
    deviceRenderedScreenPage(full, pageBodies, rotation, reload).Render(ctx, w)
}
```

### Page body rendering

```go
// renderPageBodies iterates each page in the full tree, renders each widget
// through the registry, and produces one templ.Component per page that
// composes that page's widgets. The widget-render error path produces an
// inline placeholder rather than failing the whole page.
func renderPageBodies(ctx context.Context, full screens.ScreenFull, widgets *widget.Registry) []templ.Component {
    out := make([]templ.Component, 0, len(full.Pages))
    for _, pw := range full.Pages {
        slots := make([]templ.Component, 0, len(pw.Widgets))
        for _, inst := range pw.Widgets {
            comp, err := widgets.Render(ctx, inst.Type, inst.Config, full.Theme)
            if err != nil || comp == nil {
                if err == nil {
                    err = fmt.Errorf("widget render returned nil component")
                }
                slog.Warn("widget render failed",
                    "type", inst.Type,
                    "page_id", inst.PageID,
                    "screen_id", full.Screen.ID,
                    "err", err)
                slots = append(slots, widgetErrorSlot(inst.Type))
                continue
            }
            slots = append(slots, widgetSlot(comp))
        }
        out = append(out, pageBody(pw.Page, slots))
    }
    return out
}

templ pageBody(page screens.Page, widgets []templ.Component) {
    if len(widgets) == 0 {
        <p class="empty-widgets">(no widgets on this page)</p>
    } else {
        for _, w := range widgets {
            @w
        }
    }
}
```

### Rotation + reload helpers

```go
// computeRotation returns the effective rotation interval in seconds. The
// service already validates 5..3600 (SPEC-006 R7), so this is a pass-through
// with a defensive zero-floor.
func computeRotation(seconds int) int {
    if seconds < 1 {
        return 30
    }
    return seconds
}

// computeReload applies the live-reload policy from SPEC-007 R16:
//   - The minimum is 30 seconds (hard floor).
//   - The base is LIVE_RELOAD_SECONDS from config.
//   - If the rotation interval is longer than the base, reload happens at
//     max(base, rotation) so a single-page slow-rotation Screen does not get
//     a reload mid-page-view.
func computeReload(rotationSeconds, reloadSeconds int) int {
    const hardFloor = 30
    if reloadSeconds < hardFloor {
        reloadSeconds = hardFloor
    }
    if rotationSeconds > reloadSeconds {
        return rotationSeconds
    }
    return reloadSeconds
}
```

### The rotator script (inline in deviceRenderedScreenPage)

```html
<script>
(function () {
  function start() {
    var pages = document.querySelectorAll('.page-container');
    if (pages.length === 0) return;
    pages.forEach(function (p, i) {
      p.classList.toggle('page-current', i === 0);
    });
    if (pages.length < 2) return;
    var rotationMs = (parseInt(document.body.dataset.rotationSeconds, 10) || 30) * 1000;
    var current = 0;
    // Clean up any prior interval (idempotent re-init).
    if (window.__screensRotator) clearInterval(window.__screensRotator);
    window.__screensRotator = setInterval(function () {
      pages[current].classList.remove('page-current');
      current = (current + 1) % pages.length;
      pages[current].classList.add('page-current');
    }, rotationMs);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
</script>
```

### Device admin list -- new assign form (views/devices.templ)

```html
<!-- inside the Active Devices table, in the existing row for each device: -->
<td>
  if d.ScreenID != nil {
    { lookupScreenName(screensList, *d.ScreenID) }
  } else {
    (none)
  }
</td>
<td>
  <form method="POST" action={ templ.SafeURL("/admin/devices/" + d.ID + "/assign-screen") }>
    <input type="hidden" name="_csrf" value={ csrfToken }/>
    <select name="screen_id">
      <option value="">(none)</option>
      for _, s := range screensList {
        <option
          value={ s.ID }
          if d.ScreenID != nil && *d.ScreenID == s.ID { selected }
        >{ s.Name }</option>
      }
    </select>
    <button type="submit">Assign</button>
  </form>
</td>
```

`screensList []screens.ScreenSummary` is passed into the existing `devicesPage` templ; the list handler fetches it once and threads it through (one extra service call per page load, well under the performance envelope).

`lookupScreenName` is a small helper that returns the Screen's name for a given ID, or the ID itself if the Screen was somehow not in the list (defence in depth):

```go
func lookupScreenName(screens []screens.ScreenSummary, id string) string {
    for _, s := range screens {
        if s.ID == id {
            return s.Name
        }
    }
    return id
}
```

### Assign-Screen handler (views/devices.go)

```go
func handleDeviceAssignScreen(authSvc *auth.Service, screensSvc *screens.Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        user := auth.UserFromContext(ctx)
        if user == nil {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        deviceID := r.PathValue("id")
        if deviceID == "" {
            http.Redirect(w, r, "/admin/devices?error=Missing+device+ID", http.StatusFound)
            return
        }
        screenID := strings.TrimSpace(r.FormValue("screen_id"))

        // Empty screenID means "clear the assignment".
        if screenID != "" {
            // Pre-check the Screen exists so the user sees a friendly flash
            // rather than relying on the FK constraint surfacing.
            if _, err := screensSvc.GetScreenByID(ctx, screenID); err != nil {
                if errors.Is(err, screens.ErrScreenNotFound) {
                    http.Redirect(w, r, "/admin/devices?error=Screen+not+found", http.StatusFound)
                    return
                }
                slog.Error("assign-screen lookup", "err", err, "screen_id", screenID)
                http.Redirect(w, r, "/admin/devices?error=Could+not+assign+screen", http.StatusFound)
                return
            }
        }

        if err := authSvc.AssignDeviceToScreen(ctx, deviceID, screenID); err != nil {
            if errors.Is(err, auth.ErrDeviceNotFound) {
                http.Redirect(w, r, "/admin/devices?error=Device+not+found", http.StatusFound)
                return
            }
            slog.Error("assign-screen", "err", err, "device_id", deviceID, "screen_id", screenID)
            http.Redirect(w, r, "/admin/devices?error=Could+not+assign+screen", http.StatusFound)
            return
        }

        msg := "assigned"
        if screenID == "" {
            msg = "unassigned"
        }
        slog.Info("device screen assigned",
            "device_id", deviceID,
            "screen_id", screenID,
            "assigned_by", user.Email)
        http.Redirect(w, r, "/admin/devices?msg="+msg, http.StatusFound)
    }
}
```

### Route registration (views/routes.go)

```go
// inside deviceMux:
deviceMux.HandleFunc("POST /admin/devices/{id}/assign-screen", handleDeviceAssignScreen(deps.Auth, deps.Screens))
```

The existing wildcard mount `adminMux.Handle("/admin/devices/", middleware.RequireRole(auth.RoleAdmin)(deviceMux))` covers the new path.

For the render handler:

```go
// Replace the existing landing handler with the new render handler. Same
// chain (RequireAuth only).
landingHandler := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
    http.HandlerFunc(handleDeviceRender(deps.Auth, deps.Screens, deps.Widgets, deps.LiveReloadSeconds)),
)
mux.Handle("GET "+deps.DeviceLandingURL, landingHandler)
```

`Deps` gains a `LiveReloadSeconds int` field threaded from `main.go`.

### Dependencies Between Components

```
main.go
  cfg.HTTP.LiveReloadSeconds (NEW)
  views.AddRoutes(..., &Deps{
      ...,
      LiveReloadSeconds: cfg.HTTP.LiveReloadSeconds,
  })

views/routes.go
  registerAuthRoutes:
    deviceMux.HandleFunc("POST /admin/devices/{id}/assign-screen", handleDeviceAssignScreen(deps.Auth, deps.Screens))
    landingHandler = RequireAuth(...)(handleDeviceRender(deps.Auth, deps.Screens, deps.Widgets, deps.LiveReloadSeconds))

views/device.go
  handleDeviceRender -> branches on identity:
    device  -> auth.Device.ScreenID? -> screens.Service.GetScreenFull -> widget.Registry.Render per instance -> deviceRenderedScreenPage templ
    admin   -> r.URL.Query().Get("screen") -> screens.Service.GetScreenFull / ListScreens -> deviceRenderedScreenPage or deviceAdminPickerPage

internal/auth/auth.go
  AssignDeviceToScreen(ctx, deviceID, screenID) -> db.AssignDeviceScreen
```

### main.go Wiring Changes

```go
// new field on Deps:
views.AddRoutes(mux, &views.Deps{
    ...,
    Screens:           screensSvc,
    LiveReloadSeconds: cfg.HTTP.LiveReloadSeconds, // NEW
})
```

```go
// new config:
type HTTPConfig struct {
    ...
    LiveReloadSeconds int  // NEW
}

// in Load():
HTTP: HTTPConfig{
    ...,
    LiveReloadSeconds: envInt("LIVE_RELOAD_SECONDS", 60),
},

// in Validate():
if c.HTTP.LiveReloadSeconds < 30 {
    errs = append(errs, "LIVE_RELOAD_SECONDS must be at least 30 seconds")
}
```

## Storage

### sqlc Queries (additive)

#### internal/db/queries/devices.sql (additions)

```sql
-- name: AssignDeviceScreen :execresult
UPDATE devices
   SET screen_id = ?
 WHERE id = ?;
```

Existing SELECT queries (`GetDeviceByID`, `GetDeviceByTokenHash`, `ListDevices`) need to include the new `screen_id` column. After the migration, sqlc regenerates these to include `screen_id sql.NullString` in their return struct. The `auth.Device` mapper picks it up in `deviceFromRow`.

Concretely, every SELECT becomes (no change to the WHERE / ORDER BY, just the column list):

```sql
SELECT id, name, token_hash, created_by, created_at, last_seen_at, revoked_at, screen_id
FROM devices
WHERE ...
```

The implementer regenerates with `sqlc generate` after updating the SQL files.

### Why ON DELETE SET NULL (and not CASCADE or RESTRICT)?

Recorded in ADR-009. Short version:

- **CASCADE** would delete the device row when its Screen is deleted -- that loses the device record entirely, which is wrong (the admin still wants to manage / revoke / re-assign the device).
- **RESTRICT** would block Screen deletion when any device references it -- that is the wrong UX (an admin deleting a Screen does not want to be told "first reassign 4 devices"; they want the Screen gone and the devices back to unassigned).
- **SET NULL** is the right shape: devices outlive Screens; deleting a Screen returns its devices to the unassigned state, which the render handler already handles cleanly.

### Migration Ordering

Existing migrations: 001 - 009. This spec adds 010. Numbering remains monotonic.

The migration assumes `PRAGMA foreign_keys = ON` (already set by `db.Open`). The `ALTER TABLE` runs after the existing `screens` table exists (migration 007 created it), so the FK target is already valid.

### Test helper update

`db.OpenTestDB(t)` already runs every migration; no test-helper change. Existing tests that create devices and never touch `screen_id` keep passing because the new column is nullable.

## Configuration

### New setting

| Key                   | Type | Default | Validation                  | Description                                                                                                |
|-----------------------|------|---------|-----------------------------|------------------------------------------------------------------------------------------------------------|
| `LIVE_RELOAD_SECONDS` | int  | 60      | `>= 30`                     | The base interval (seconds) for the device's `<meta http-equiv="refresh">` tag. Hard floor 30s.            |

Added to `config.HTTPConfig` (it's a render-time interval served via HTTP, so `HTTPConfig` is the right home).

### No new secret-bearing config

The render handler is purely a function of existing state.

## Security Considerations

### Authentication

The render handler sits behind the existing `RequireAuth` middleware, which accepts admin sessions or device tokens. The handler then branches:

- **Device identity**: renders its assigned Screen (or the unassigned placeholder).
- **Admin identity**: renders the picker or the previewed Screen.
- **No identity** (should not reach the handler): 403.

A device cannot see another device's assigned Screen because the handler reads `dev.ScreenID` from the identity in context; there is no path through the handler that lets a device claim a different Screen.

An admin can preview any Screen via `?screen=<id>`. This is intentional: admins are admins. There is no per-Screen ACL in v1; the existing role gate is the only authorization tier.

### CSRF

The render handler is GET-only; CSRF does not apply.

The new `POST /admin/devices/{id}/assign-screen` endpoint sits inside the existing admin sub-mux that is already wrapped in `RequireCSRF`, so CSRF is automatic. No per-route CSRF wiring needed.

### Cache Headers

The render output sets `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache`. A reverse proxy / CDN that respects these headers will NOT cache the response. This is correct: each render is a function of the current database state, and a stale cached render would show an outdated Screen.

A proxy that ignores these headers is operator-misconfiguration; we accept the risk (same as the project's existing admin endpoints).

### Theme CSS Injection

The theme's color / font / radius fields are embedded verbatim into a `<style>` block via `themes.Theme.CSSVariables()`. The existing SPEC-004 R7-R14 validation (whitelist character sets, hex regex for colors, length bounds on fonts, reject `;`, `{`, `}`, `<`, `>`, backslash in font values) is the only defence against CSS injection. ARCH-004 already confirms `CSSVariables()` is safe to embed given those preconditions.

The render handler does NOT re-validate the theme at render time. The Theme service guarantees `GetByID` only returns themes that passed validation at write time; the FK from `screens.theme_id` plus `themes.ErrThemeInUse` plus the seed-default policy together guarantee `GetScreenFull` always returns a valid theme. (Defence-in-depth: if a future schema change loosens this, the inline `<style>` would still only render whatever bytes the theme contains, and CSS-injection risk is contained by the per-field validation.)

### Widget Config Bytes

The render path NEVER emits widget config bytes into the HTML directly. The widget package's `Render` method receives the bytes, validates them, and produces a `templ.Component`. The component is what reaches the HTML, and templ auto-escapes any interpolated string. The widget-error placeholder includes the widget *type* identifier (a controlled string like `"text"`) but NOT the bytes or the error message; this contains any incidental information that could leak through an error-message channel.

### Live-Reload Header

The `<meta http-equiv="refresh">` value is a number computed from validated config plus validated rotation interval; no user input flows through.

### Identity Confusion

The render handler branches strictly on `id.IsDevice()` vs `id.IsAdmin()`. The `?screen=<id>` admin preview is only honoured for admin identities; a device GETting `/device/?screen=<id>` ignores the query parameter and renders its assigned Screen (or unassigned placeholder). This prevents a device from rendering an arbitrary Screen by appending a query parameter.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-026 (the last task in SPEC-006).

1. **TASK-027**: Schema migration + sqlc query updates for `devices.screen_id`; `auth.Device.ScreenID` field; `auth.Service.AssignDeviceToScreen`. -- (prerequisite: none)
2. **TASK-028**: `LIVE_RELOAD_SECONDS` config setting; `views.Deps.LiveReloadSeconds`; main.go wiring; README config-table update. -- (prerequisite: none; parallel with TASK-027)
3. **TASK-029**: Device admin UI: assignment column + `<select>` form on `/admin/devices`; `POST /admin/devices/{id}/assign-screen` handler and route registration; tests. -- (prerequisite: TASK-027)
4. **TASK-030**: New device render handler `handleDeviceRender` + `views/device.templ` rewrite (rendered-screen page, unassigned placeholder, admin picker, screen-not-found error); `static/css/device.css` page-layout rules; route registration swap from `handleDeviceLanding`. -- (prerequisite: TASK-027, TASK-028; parallel with TASK-029)
5. **TASK-031**: Adversarial / edge-case tests for the render handler: deleted-screen race, widget render failure (unknown type), malformed-config row, empty Screen, identity confusion (device with `?screen=`), `LIVE_RELOAD_SECONDS` boundary conditions. -- (prerequisite: TASK-030)

### Task Dependency Graph

```
TASK-027 (devices.screen_id schema + sqlc + auth.Device.ScreenID + AssignDeviceToScreen)
    |
    +--------- TASK-029 (admin UI: assignment form + handler)
    |
TASK-028 (LIVE_RELOAD_SECONDS config)
    |
    +--------- TASK-030 (device render handler + templates + CSS)
                            |
                            v
                       TASK-031 (adversarial tests)
```

TASK-027 and TASK-028 are independent and can be developed in parallel.
TASK-029 depends only on TASK-027; can run in parallel with TASK-030 once both prerequisites are met.
TASK-031 is the last task and locks in the edge-case coverage.

### Sizing Notes

- **TASK-027** is a focused data-layer + service-layer task: one migration, sqlc regen, one new query, one new service method, ScreenID pointer plumbing, a handful of tests. Single coding session.
- **TASK-028** is the smallest task: one config field, one validation rule, one Deps field, one main.go wire, one README row update.
- **TASK-029** is medium: the assignment column on the existing list, a new `<select>` form, a new handler, route registration, tests. Some templ/CSS work but tightly scoped.
- **TASK-030** is the biggest task: the render handler with two identity branches, three new templ components, the inline rotator script, the live-reload computation, page-body composition, CSS, AND the existing `handleDeviceLanding` replacement. Within a single coding session if scoped tightly. The adversarial tests are deliberately split off into TASK-031 so this task's diff stays focused on the happy paths.
- **TASK-031** is small-to-medium: covers the failure modes the render handler must defend against, but the testing surface is bounded by R10b / R19-R21 / R23-R27.

A five-task split is intentional: it isolates the additive data layer (TASK-027) from the config-only change (TASK-028), separates the admin-side concern (TASK-029) from the device-side render (TASK-030), and keeps the adversarial-test surface as its own deliverable (TASK-031) so the green-bar coverage is reviewable independently.

## Alternatives Considered

See ADR-007, ADR-008, ADR-009 for full rationale on the three significant design choices.

Other architectural alternatives evaluated during this design pass:

- **htmx fragment endpoint per page; the rotator swaps `<div hx-get>`**: rejected. Adds a network round-trip per rotation, adds a fragment-validation surface, and moves "the rotator works offline" from "trivially true" to "needs caching code". Server-render-all-pages plus client-side toggle is simpler.
- **Render the active page only; reload the page on rotation**: rejected. Each rotation would be a full HTTP request + database query + render. At rotation_interval=5s, that is 12 renders per minute per device just for rotation. Server-render-all-pages plus client-side toggle eliminates that cost entirely.
- **Separate `/preview/{screenID}` admin route for live preview**: rejected. The `?screen=<id>` query parameter on `/device/` does the same job with no new route to maintain. Admins can bookmark `/device/?screen=<id>` for any Screen they want to preview live.
- **WebSocket-driven server push for live reload**: rejected for v1. The `<meta refresh>` tag is browser-native, has no open-connection cost, and survives transient network failures. A WebSocket layer would add a connection-per-device, a reconnect dance, and a serialisation format. Phase 4 alerts / push notifications justify that infrastructure; the live-reload story does not.
- **SSE (Server-Sent Events) for live reload**: rejected for v1, same rationale. SSE is lighter than WebSocket but still adds an open connection per device and a reconnect strategy. A future spec can add SSE on top of the same render handler if sub-30s update visibility becomes a real need.
- **Device polls a `?lastModified=...` endpoint for change detection, then full-reloads if changed**: rejected. Adds an admin-facing endpoint, a per-screen `updated_at` aggregator (a JOIN across screens/pages/widget_instances), and a per-poll database query. The `<meta refresh>` tag does the same job in zero code.
- **Cache the rendered HTML by screen ID at the server**: rejected. The cache invalidation surface (every admin mutation touching the screen, its pages, its widget instances, or the theme) is broad. The per-render cost is already small (one `GetScreenFull` + N widget Renders), and the cache would only help if the same Screen is being rendered for multiple devices in rapid succession. Premature.
- **Per-device rotation phase offset (so two devices on the same Screen don't both rotate at the same moment)**: rejected. The client-side timer starts at `DOMContentLoaded`, which already varies per device. The architecturally cleaner option would be a phase offset based on the device ID; the practical option (do nothing) is fine for v1 and households.
- **Combine TASK-029 and TASK-030 into one larger task**: rejected. Reviewing the admin-side change (which has a clear test surface in `views/devices_test.go`) and the device-side render (which has a different test surface in `views/device_test.go`) separately keeps each diff focused. They have independent failure modes; splitting them keeps reviews focused.
- **Skip the live-reload entirely and require manual reload**: rejected. The "edit Screen at the kitchen counter and look up at the tablet" use case is exactly the configure-loop the spec optimises for. Even a coarse 60s reload is dramatically better than "go to the kitchen tablet and refresh by hand".
- **Render the `<meta refresh>` tag client-side based on a JS-set interval**: rejected. The browser-native `<meta refresh>` is more robust against the page being in a backgrounded tab (most browsers throttle JS timers in background tabs but honour `<meta refresh>` regardless). Wall-mounted kiosks usually have one active tab; backgrounded behaviour is not a primary concern, but the simpler mechanism is the better default.
- **Per-Screen `live_reload_seconds` column** (override the global default): rejected for v1. Adding the column is additive; we just don't see a v1 use case that justifies it. A future spec can add a nullable per-Screen override against `LIVE_RELOAD_SECONDS` as a default.
- **Render-time per-instance widget cache to skip re-validation**: rejected. Validation is fast (per-widget JSON unmarshal + a handful of string checks); caching adds an invalidation surface; the simpler thing is to always re-validate.
