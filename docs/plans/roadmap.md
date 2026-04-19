# Screens Roadmap

## Phases

```
Phase 1 (Foundation)
  └── Phase 2 (Display Framework)
        ├── Phase 3 (Content Widgets)  ── all widgets parallel
        ├── Phase 4 (Delivery & PWA)
        └── Phase 5 (Polish)
```

## Phase 1: Foundation

**Goal**: Establish storage, authentication, and the security boundary. Nothing user-facing ships without auth.

**Why first**: Every subsequent feature needs persistent data and authenticated requests. Building this first prevents rework.

| Spec | Description | Priority |
|------|-------------|----------|
| Storage Engine | SQLite via `database/sql`, migration system, connection management, health check | p0 |
| Admin Auth | Username/password login, bcrypt hashing, session cookies, CSRF protection, login/logout UI | p0 |
| Device Auth | Pre-shared token generation, token-based auth middleware, token revocation | p0 |
| Auth Middleware | Enforce admin-session or device-token on protected routes, context injection of identity | p0 |

**ADRs needed**: Storage engine choice, auth approach (sessions vs JWTs)

**Exit criteria**: An admin can log in, create a device token, and a device can authenticate with that token. All subsequent endpoints can be protected.

**Estimated tasks**: 8-12

---

## Phase 2: Display Framework

**Goal**: Build the screen/page/widget infrastructure without specific widget implementations. Establish the extensible pattern that Phase 3 fills in.

| Spec | Description | Priority |
|------|-------------|----------|
| Theme System | Theme data model (colors, fonts, spacing), CRUD API, CSS variable injection | p0 |
| Widget Interface | Widget type registry, renderer interface, configuration schema, placeholder "text" widget | p0 |
| Screen Model | Screen entity (name, pages, theme ref), page entity (widget layout), CRUD API | p0 |
| Screen Display | Device-facing renderer, page layout engine, auto-rotation with configurable interval | p0 |
| Widget Selection UI | Admin UI to assign widgets to screen pages | p1 |
| Theme Preview | Live preview of theme changes in admin UI | p1 |

**ADRs needed**: Widget interface design, screen layout approach

**Exit criteria**: Admin can create a screen with multiple pages, assign a theme, add placeholder widgets. A device can display and auto-rotate through pages.

**Estimated tasks**: 14-18

---

## Phase 3: Content Widgets

**Goal**: Implement actual widget types. Each widget is independent and follows the Phase 2 pattern.

| Spec | Description | Priority |
|------|-------------|----------|
| Time/Date Widget | Configurable format, timezone, digital/analog styles | p0 |
| Weather Widget | External API integration, current conditions, forecast | p1 |
| Calendar Widget | iCal URL integration, upcoming events, configurable days ahead | p1 |
| Home Assistant Widget | HA REST API, entity state display, toggle/dimmer/unlock controls | p1 |
| Slideshow Widget | Image upload/URL list, configurable interval, transitions, idle screensaver mode | p1 |
| Charts Widget | Data source config (HA, Grafana), line/bar/pie, auto-refresh | p2 |
| Financial Tickers | Stock/crypto prices, configurable symbols, refresh interval | p2 |

**Parallelism**: All widgets can be developed in parallel.

**Exit criteria**: All p0 and p1 widgets are functional and configurable through admin UI.

**Estimated tasks**: 14-21

---

## Phase 4: Delivery & PWA

**Goal**: Make devices proper PWAs with offline capability and push notifications for real-time alerts.

| Spec | Description | Priority |
|------|-------------|----------|
| PWA Manifest | `manifest.json`, service worker, offline shell, installability, in-app install instructions | p0 |
| Alerts & Messaging | Admin creates alert messages ("mom says"), overlay on devices, must-acknowledge, renamable by admin | p0 |
| Push Notifications | Web Push API (VAPID keys), subscription management, alert delivery via push, acknowledgement | p1 |

**ADRs needed**: PWA strategy, push notification approach

**Exit criteria**: Device display works as installable PWA. Admins can send alerts that appear on devices in real-time and require acknowledgement.

**Estimated tasks**: 8-12

---

## Phase 5: Polish

**Goal**: Refinements, admin experience improvements, deferred items.

| Spec | Description | Priority |
|------|-------------|----------|
| Widget Selection per Screen | Enhanced per-screen widget config, screen templates | p1 |
| Device Management | Admin view of connected devices, last-seen, force-refresh | p1 |
| Config Export/Import | JSON export/import of screen configs for backup/migration | p2 |

**Exit criteria**: Full admin management experience is polished. System is production-ready.

**Estimated tasks**: 6-10

---

## Total Estimated Tasks: 50-73
