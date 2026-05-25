---
id: TASK-030
title: "Device render handler + templates (rendered Screen, unassigned, admin picker, screen-not-found) + device.css + inline rotator script + live-reload meta tag"
spec: SPEC-007
arch: ARCH-007
status: ready
priority: p0
prerequisites: [TASK-027, TASK-028]
skills: [add-view, green-bar]
created: 2026-05-25
author: architect
---

# TASK-030: Device render handler + templates + device.css + inline rotator script + live-reload meta tag

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Replace the Phase 1 placeholder `handleDeviceLanding` with a new render handler `handleDeviceRender` that, for a device identity, looks up the device's `screen_id`, calls `screens.Service.GetScreenFull`, iterates the pages and widgets to render through `widget.Registry.Render`, and emits a single self-contained HTML document with: the theme's CSS variables inline in `<style>`, every page rendered as a `<section class="page-container">`, the inline vanilla-JS rotator script, the `<meta http-equiv="refresh">` tag for live-reload, and the `Cache-Control: no-store` headers. For an admin identity, the same handler renders a Screen-picker page (no `?screen=` parameter) or the previewed Screen (`?screen=<id>`). A new `static/css/device.css` carries the page-rotation visibility rules and widget layout. Adversarial / failure-mode tests are deferred to TASK-031; this task's tests cover the happy paths and the deliberate empty-state / unassigned branches.

## Context

- The existing Phase 1 handler `views/device.go::handleDeviceLanding` renders a tiny placeholder page (`deviceLandingPage` templ). The new handler replaces it. The route registration in `views/routes.go` (`mux.Handle("GET "+deps.DeviceLandingURL, landingHandler)`) keeps the same `RequireAuth`-only chain; only the handler body changes.
- `screens.Service.GetScreenFull(ctx, id)` (shipped by TASK-022, SPEC-006) returns a `screens.ScreenFull` carrying the Screen + Theme + ordered Pages + each Page's ordered WidgetInstances. The handler uses ONE call.
- `widget.Registry.Render(ctx, type, configBytes, theme)` (shipped by SPEC-005) returns `(templ.Component, error)`. The handler iterates each widget instance and calls this method.
- `themes.Theme.CSSVariables()` (shipped by SPEC-004) returns the `:root { ... }` CSS block; the handler embeds it in `<style>`.
- The admin landing flow (SPEC-003 / Phase 1) lands at `/device/`. The new handler keeps that URL working. An admin reaching this URL without `?screen=` sees a picker; a device reaches this URL by virtue of its device cookie.
- `Deps.LiveReloadSeconds` (shipped by TASK-028) holds the config value; pass it into the handler factory.

### Files to Read Before Starting

- `.claude/rules/http.md` -- handler conventions.
- `.claude/rules/testing.md` -- testing conventions.
- `.claude/skills/add-view/SKILL.md` -- the add-view scaffold.
- `views/device.go` -- the existing Phase 1 placeholder handler (`handleDeviceLanding`).
- `views/device.templ` -- the existing Phase 1 placeholder template.
- `views/device_test.go` -- existing test patterns (small, mostly auth-flow assertions).
- `views/routes.go` -- where the landing handler is registered; modify to call the new factory.
- `views/screens.go` and `views/screens.templ` -- existing patterns for rendering screen-related views; mirror the layout / hero / card conventions where they make sense in the unassigned / picker pages.
- `views/layout.templ` -- the existing layout templ; the device render uses its OWN layout (not the admin one) because the device pages are rendered WITHOUT the admin chrome. Read it to understand the conventions and copy the `<!DOCTYPE>`, `<head>` structure.
- `internal/screens/service.go` -- `GetScreenFull`, `ListScreens`, `GetScreenByID`, `ErrScreenNotFound`, `ScreenFull`, `Page`, `WidgetInstance`.
- `internal/widget/registry.go` -- the `Render` method signature.
- `internal/themes/css.go` -- `CSSVariables()`.
- `static/css/app.css` -- the existing CSS vocabulary (`.hero`, `.card`, `--bg`, etc.). The new `device.css` reuses these custom properties.
- `docs/plans/specs/phase-2-display/spec-screen-display.md` -- requirements 8-32; AC-9 through AC-25, AC-28, AC-29.
- `docs/plans/architecture/phase-2-display/arch-screen-display.md` -- "Component Design" through "API Contract" sections.
- `docs/plans/architecture/decisions/adr-007-rendering-strategy.md` -- the rationale for server-render-all + client-side rotation.
- `docs/plans/architecture/decisions/adr-008-live-reload-and-theme-css.md` -- the rationale for `<meta refresh>` + inline `<style>`.

## Requirements

### Templates

1. Rewrite (or substantially extend) `views/device.templ`. Replace the existing `deviceLandingPage` with four new components:

   ### a. `deviceRenderedScreenPage` (the main render)

   ```go
   templ deviceRenderedScreenPage(full screens.ScreenFull, pageBodies []templ.Component, rotationSeconds int, reloadSeconds int)
   ```

   The templ emits a full HTML document with:
   - `<!DOCTYPE html><html lang="en">`.
   - `<head>` containing:
     - `<meta charset="UTF-8">`.
     - `<meta name="viewport" content="width=device-width, initial-scale=1.0">`.
     - `<meta http-equiv="refresh" content="{reloadSeconds}">`.
     - `<title>{full.Screen.Name} — screens</title>`.
     - `<link rel="stylesheet" href="/static/css/app.css">`.
     - `<link rel="stylesheet" href="/static/css/device.css">`.
     - `<style>` containing the result of `full.Theme.CSSVariables()`. Use `templ.Raw` ONLY on the CSS string returned by `CSSVariables()` (which is the documented safe-to-embed contract from SPEC-004).
   - `<body data-rotation-seconds="{rotationSeconds}">` containing:
     - If `len(full.Pages) == 0`: a single `<section class="page-container page-current empty-screen"><div class="card"><h1>{full.Screen.Name}</h1><p>This screen has no pages yet. Visit <a href="/admin/screens">/admin/screens</a> to add one.</p></div></section>`.
     - Otherwise: one `<section class="page-container [page-current if i==0]" aria-label="Page {i+1} of {N}: {page name or page-N}">` per page; the body of the section is the corresponding `pageBodies[i]`.
     - The inline rotator script (see Requirement 3).

   ### b. `deviceUnassignedPage` (no Screen)

   ```go
   templ deviceUnassignedPage(deviceName string)
   ```

   Plain HTML document (no theme CSS, no rotator, but DOES include the `<meta refresh>` so a re-assignment shows up on the next reload):
   - `<head>` with charset, viewport, `<title>` (e.g., `<title>{deviceName} — unassigned — screens</title>`), `<meta http-equiv="refresh" content="60">` (use a hard-coded 60 here; the unassigned page does not have a rotation interval, and we want the reload cadence to match the default), `<link rel="stylesheet" href="/static/css/app.css">`.
   - `<body>` with a `.hero` and a `.card` containing the device name and the message "No screen assigned. Visit /admin/devices to assign one." (link the URL).

   ### c. `deviceAdminPickerPage` (admin viewing `/device/` with no `?screen=`)

   ```go
   templ deviceAdminPickerPage(summaries []screens.ScreenSummary, currentUser *auth.User)
   ```

   Plain HTML document (admin chrome OK):
   - `<head>` similar to the unassigned page.
   - `<body>` with:
     - `.hero` "Device Preview" + a back link to `/admin/screens`.
     - A `.card` explaining "You are viewing the device URL as an admin. Choose a Screen to live-preview as a device would see it.".
     - A `<ul>` of links: one `<li><a href="/device/?screen={s.ID}">{s.Name}</a></li>` per Screen.
     - An empty-state message if `len(summaries) == 0`: "No screens configured. Visit <a href="/admin/screens">/admin/screens</a> to create one.".

   ### d. `deviceScreenNotFoundPage` (admin viewing `/device/?screen=<unknown>`)

   ```go
   templ deviceScreenNotFoundPage(currentUser *auth.User)
   ```

   Plain HTML document:
   - `<head>` similar.
   - `<body>` with `.hero` "Screen Not Found" + a back link to `/device/` (picker) and to `/admin/screens`.

   ### e. Page-body composition helpers

   ```go
   templ pageBody(page screens.Page, widgets []templ.Component) {
       if len(widgets) == 0 {
           <p class="empty-widgets">(no widgets on this page)</p>
       } else {
           for _, w := range widgets {
               @w
           }
       }
   }

   templ widgetSlot(component templ.Component) {
       @component
   }

   templ widgetErrorSlot(typeName string) {
       <div class="widget widget-error" role="alert">
           Widget { typeName } failed to render
       </div>
   }
   ```

2. After editing `views/device.templ`, run `templ generate` and commit the regenerated `views/device_templ.go`.

3. The inline rotator script (embedded inside `deviceRenderedScreenPage`'s body, AFTER the last `<section>`):

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

   Use `templ.Raw` to emit this script verbatim (the content is fixed and safe to embed).

### Handler

4. Rewrite `views/device.go`:
   - Remove `handleDeviceLanding`.
   - Add `handleDeviceRender(authSvc *auth.Service, screensSvc *screens.Service, widgets *widget.Registry, reloadSeconds int) http.HandlerFunc`. Body:
     - Read `id := auth.IdentityFromContext(ctx)`; 403 if nil.
     - Set `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache` headers (for all branches that return 200).
     - If `id.IsDevice() && id.Device != nil`: call `renderForDevice(w, r, id.Device, screensSvc, widgets, reloadSeconds)`.
     - Else if `id.IsAdmin()`: call `renderForAdmin(w, r, id.User, screensSvc, widgets, reloadSeconds)`.
     - Else: 403.

5. Add the internal helpers:

   ```go
   func renderForDevice(w http.ResponseWriter, r *http.Request, dev *auth.Device, screensSvc *screens.Service, widgets *widget.Registry, reloadSeconds int) {
       ctx := r.Context()
       if dev.ScreenID == nil {
           deviceUnassignedPage(dev.Name).Render(ctx, w)
           return
       }
       full, err := screensSvc.GetScreenFull(ctx, *dev.ScreenID)
       if err != nil {
           if errors.Is(err, screens.ErrScreenNotFound) {
               slog.Info("device screen missing", "device_id", dev.ID, "screen_id", *dev.ScreenID)
               deviceUnassignedPage(dev.Name).Render(ctx, w)
               return
           }
           slog.Error("device get screen full", "err", err, "device_id", dev.ID, "screen_id", *dev.ScreenID)
           http.Error(w, "Internal server error", http.StatusInternalServerError)
           return
       }
       pageBodies := renderPageBodies(ctx, full, widgets)
       rotation := computeRotation(full.Screen.RotationIntervalSeconds)
       reload := computeReload(rotation, reloadSeconds)
       deviceRenderedScreenPage(full, pageBodies, rotation, reload).Render(ctx, w)
   }

   func renderForAdmin(w http.ResponseWriter, r *http.Request, user *auth.User, screensSvc *screens.Service, widgets *widget.Registry, reloadSeconds int) {
       ctx := r.Context()
       screenID := r.URL.Query().Get("screen")
       if screenID == "" {
           summaries, err := screensSvc.ListScreens(ctx)
           if err != nil {
               slog.Error("admin device list screens", "err", err)
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
           slog.Error("admin device get screen full", "err", err, "screen_id", screenID)
           http.Error(w, "Internal server error", http.StatusInternalServerError)
           return
       }
       pageBodies := renderPageBodies(ctx, full, widgets)
       rotation := computeRotation(full.Screen.RotationIntervalSeconds)
       reload := computeReload(rotation, reloadSeconds)
       deviceRenderedScreenPage(full, pageBodies, rotation, reload).Render(ctx, w)
   }

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

   func computeRotation(seconds int) int {
       if seconds < 1 {
           return 30
       }
       return seconds
   }

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

### Route registration

6. Edit `views/routes.go::registerAuthRoutes`:
   - Replace the existing `handleDeviceLanding(deps.Auth)` call with `handleDeviceRender(deps.Auth, deps.Screens, deps.Widgets, deps.LiveReloadSeconds)`.
   - Keep the `RequireAuth` chain unchanged. Do NOT add `RequireRole` or `RequireCSRF` -- the handler is GET-only and must serve both devices and admins.

### CSS

7. Create `static/css/device.css` with at minimum:
   ```css
   /* Device render CSS. Inherits theme custom properties from the inline
      <style> block; sets up page-container layout and rotation rules. */

   body {
     margin: 0;
     padding: 0;
     min-height: 100vh;
     background: var(--bg);
     color: var(--text);
     font-family: var(--font-family, -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif);
     overflow: hidden;
   }

   .page-container {
     position: fixed;
     inset: 0;
     padding: 2rem;
     display: none;
     overflow: auto;
   }

   .page-container.page-current {
     display: block;
   }

   .widget {
     background: var(--surface);
     border: 1px solid var(--border);
     border-radius: var(--radius, 10px);
     padding: 1rem;
     margin-bottom: 1rem;
     color: var(--text);
   }

   .widget-error {
     border-color: var(--red, #f87171);
     color: var(--red, #f87171);
   }

   .empty-widgets {
     color: var(--text-muted);
     font-style: italic;
   }

   .empty-screen .card {
     max-width: 520px;
     margin: 4rem auto;
     background: var(--surface);
     border: 1px solid var(--border);
     border-radius: var(--radius, 10px);
     padding: 1.5rem;
   }
   ```

   The static-file handler in `static-handler.go` serves files from `static/` automatically; no route registration change is required for the new CSS file.

### Phase 1 placeholder cleanup

8. Delete (or repurpose) the Phase 1 `deviceLandingPage` templ component in `views/device.templ`. If existing tests in `views/device_test.go` reference it, update them to reference the new components instead. The Phase 1 spec-003 acceptance criteria around enrollment landing (AC-29 in this spec) MUST continue to pass: a newly enrolled device GETting `/device/` sees the unassigned page, not a 500.

## Acceptance Criteria

From SPEC-007:

- [ ] AC-9: Device with `screen_id IS NULL` GETs `/device/` → 200, body contains device name and "No screen assigned".
- [ ] AC-10: Device with valid `screen_id` (Screen has 2 pages, 3 widgets total) GETs `/device/` → 200, body contains `<style>` with `--bg:`, two `<section class="page-container">`, rendered widget output for each.
- [ ] AC-12: Successful render includes `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache` response headers.
- [ ] AC-13: Device with Screen `rotation_interval_seconds=15` GETs `/device/` → body has `data-rotation-seconds="15"`.
- [ ] AC-14: Device GETs `/device/` with `LIVE_RELOAD_SECONDS=60` and Screen rotation=15 → `<meta http-equiv="refresh">` content is `60`.
- [ ] AC-15: Device's Screen has zero pages → 200, body contains the Screen's name and "no pages yet".
- [ ] AC-16: Admin GETs `/device/` (no `?screen=`) → 200, body lists the admin's Screens with links to `/device/?screen=<id>`.
- [ ] AC-17: Admin GETs `/device/?screen=<valid>` → 200, renders the Screen the same as a device would.
- [ ] AC-18: Admin GETs `/device/?screen=does-not-exist` → 200 with a "Screen not found" card.
- [ ] AC-19: Device's Screen has one page with one `text` widget body "hello" → rendered HTML contains literal `hello`.
- [ ] AC-22: Device's Screen has one page with a `text` widget with valid config → rendered HTML contains the widget output, NOT the error placeholder.
- [ ] AC-23: Successful render contains a `<script>` body referencing `page-container`, `page-current`, and `setInterval`.
- [ ] AC-24: Single-page Screen render contains the rotator script with the `< 2` early-exit branch present in the source.
- [ ] AC-25: Screen with pages but no widgets → rendered HTML contains `(no widgets on this page)` inside each page-container.
- [ ] AC-28: Existing AC-8 of SPEC-006 (GET `/admin/screens` shows the screen list) still passes (this task does not touch that handler).
- [ ] AC-29: A newly enrolled device GETs `/device/` and sees the unassigned page, NOT a 500.

(Failure-mode ACs AC-11, AC-20, AC-21 are covered in TASK-031.)

## Skills to Use

- `add-view` -- templ + handler scaffolding (though this is more of an extensive rewrite of an existing file than a fresh scaffold).
- `green-bar` -- run before marking review (includes `templ generate`).

## Test Requirements

Tests live in `views/device_test.go` (rewrite as needed). Use `httptest.NewRecorder` and the existing helpers for building admin / device contexts. Construct a test widget registry via `widget.NewRegistry()` with the `text` widget registered (via `text.Registration()`) so the happy-path render exercises the real registry path.

1. **Unassigned device renders placeholder**: build a `*auth.Device` with `ScreenID == nil`, inject it via the identity context, GET `/device/`. Assert 200, body contains the device's name and "No screen assigned". Assert response headers include `Cache-Control: no-store, must-revalidate`.

2. **Device with Screen renders happy path**: open test DB, create a default theme (via the seeded value), create a screen via `screens.Service.CreateScreen`, add two pages and one `text` widget to each via the service, assign the screen to the device. GET `/device/`. Assert 200, body contains `--bg:`, two `<section class="page-container">` elements, the `text` widget's body text, the `data-rotation-seconds="<N>"` attribute.

3. **Empty screen renders the empty-state card**: create a screen with no pages; GET `/device/` as that device. Assert 200, body contains the screen name and "no pages yet".

4. **Page with no widgets renders the empty-widgets message**: create a screen with one page and zero widgets; GET. Assert 200, body contains "(no widgets on this page)".

5. **Live-reload computation**: parameterised cases ((rotationSeconds, reloadSecondsConfig) → expectedMetaRefresh). At minimum:
   - (15, 60) → 60
   - (300, 60) → 300 (rotation wins)
   - (45, 30) → 45 (reload-config below floor; rotation longer than floor wins)
   - (15, 30) → 30 (reload-config at floor; rotation shorter)
   Each case: build a Screen with the given rotation, set `Deps.LiveReloadSeconds` (or pass directly into the handler factory), render, assert the `<meta http-equiv="refresh" content="..."` value.

6. **Admin picker page**: admin identity, no `?screen=` param. Assert 200, body lists each configured Screen with a link to `/device/?screen=<id>`.

7. **Admin picker page with zero screens**: same as above but with no screens in the DB. Assert 200, body contains a message linking to `/admin/screens`.

8. **Admin preview of valid screen**: admin identity, `?screen=<valid-id>`. Assert 200, body contains the Screen's name and the same `<style>` block with `--bg:`.

9. **Admin preview of invalid screen**: admin identity, `?screen=does-not-exist`. Assert 200 (not 404), body contains "Screen Not Found".

10. **Rotator script presence**: any successful render. Assert the body contains the literal substrings `page-container`, `page-current`, and `setInterval`. Also assert the body contains the early-exit substring (e.g., `pages.length < 2`) so AC-24 is satisfied.

11. **CSP / safety smoke test**: after a render, assert the body does NOT contain raw `<script>` injection or `</style>` substrings inside the inline `<style>` block. (The theme validation guarantees this, but a smoke test pins the behaviour.)

The failure-mode tests (deleted-Screen race, widget-render-failure, malformed-config row, identity confusion) are deliberately covered in TASK-031, not this task.

Follow `.claude/rules/testing.md`: table-driven, `t.Helper()`, no third-party assertion libraries.

## Definition of Done

- [ ] `views/device.go` has `handleDeviceRender` and helpers; the old `handleDeviceLanding` is removed.
- [ ] `views/device.templ` (and regenerated `views/device_templ.go`) has the four new templ components plus `pageBody`, `widgetSlot`, `widgetErrorSlot`.
- [ ] `views/routes.go` registers the new handler under the same `RequireAuth`-only chain at `DEVICE_LANDING_URL`.
- [ ] `static/css/device.css` exists with the page-container + widget + empty-state rules.
- [ ] Inline rotator script is present in the rendered output of `deviceRenderedScreenPage`.
- [ ] All listed acceptance criteria tests pass.
- [ ] green-bar passes (`templ generate` was run; committed `_templ.go` matches `.templ`).
- [ ] No new third-party dependencies.
- [ ] The inline `<style>` block uses `themes.Theme.CSSVariables()` ONLY -- no hand-rolled CSS values per render.
- [ ] The rotator script and inline CSS are NOT served as separate files; they are embedded per-render.
- [ ] `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache` headers are set on every successful 200 response.
- [ ] The Phase 1 `deviceLandingPage` placeholder is no longer referenced anywhere.
