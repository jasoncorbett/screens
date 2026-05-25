---
id: ADR-008
title: "Live-reload via <meta http-equiv=\"refresh\">; theme CSS shipped inline per render"
status: accepted
date: 2026-05-25
---

# ADR-008: Live-reload via `<meta http-equiv="refresh">`; theme CSS shipped inline per render

## Context

Two distinct delivery questions land on Screen Display's render handler:

1. **How does a device pick up admin-side changes?** An admin edits a Screen, adds a widget, or swaps the theme on a Screen the kitchen tablet is rendering. How does the kitchen tablet learn to re-render?
2. **How does the theme's CSS reach the device?** A Screen's theme carries colors, fonts, and radius (SPEC-004); the device's CSS rules reference those values via custom properties (`var(--bg)`, etc.). Where does the device's browser get the actual `:root { --bg: ... }` declarations?

The two questions are independent in principle but intertwined in practice: both are "what shape of content does the render handler emit, and what mechanism keeps the device in sync".

For live-reload, the realistic mechanisms are:

- **`<meta http-equiv="refresh" content="N">`**: a browser-native full-page reload every N seconds. Zero JS. Zero open connections. The browser handles transient network failures (retries the reload on next interval).
- **Client-side polling (`setInterval(fetch(...), N*1000)`)**: ship a JS poller that hits a "has-this-screen-changed?" endpoint and reloads on change. Requires the endpoint, requires the JS, requires a serverside "last-modified" computation across screens/pages/widget_instances.
- **htmx polling on a fragment**: similar to the above, replaces the body partial when a fresh fragment differs. Adds an htmx-specific endpoint and fragment endpoint plumbing.
- **Server-Sent Events (SSE)**: one-way push from server to device. Lower-latency than polling. Requires an open connection per device, a reconnect strategy, and event payloads.
- **WebSocket**: bidirectional push. Same connection-per-device cost as SSE. Justifiable for two-way features (Phase 4 alerts, user interactions) but heavier than v1 needs.

For theme CSS delivery, the realistic mechanisms are:

- **Inline `<style>` block per render**: the render handler emits `:root { --bg: ...; --accent: ...; }` directly in the page `<head>`. One round-trip; first paint already wears the theme.
- **Separate stylesheet endpoint** (`GET /themes/{id}.css`): the device's HTML links a per-theme stylesheet via `<link rel="stylesheet" href="/themes/abc.css">`. The browser caches it; subsequent reloads can skip the network.
- **Build-time generated stylesheet bundles**: rejected as a category; the project is a single-binary deployment and themes are admin-mutable at runtime, not at build time.

Constraints:

- **Household scale.** Per-device traffic is dominated by the initial render plus the live-reload cadence. There are no hundreds of devices; a single household has ~5-20.
- **No new framework / library.** The project ships vanilla Go, vanilla CSS, and minimal vanilla JS. Adding a long-lived push connection per device or a polling-fetch library is heavier than the problem.
- **Phase 4 PWA.** A future service worker will cache the device's HTML for offline rendering. A live-reload mechanism that needs an open server connection breaks offline rendering. The theme-CSS-delivery mechanism that needs a second HTTP request requires the service worker to cache two URLs per Screen.
- **No FOUC on first paint.** A device powered up at 6am must show the configured theme immediately, not flash unstyled content while the stylesheet loads.

## Decision

### Live-reload: `<meta http-equiv="refresh" content="N">` with a 30-second hard floor.

The render handler emits a `<meta http-equiv="refresh" content="N">` tag in the page `<head>`. `N` is computed from `LIVE_RELOAD_SECONDS` (config, default 60, hard floor 30) and the Screen's own `rotation_interval_seconds`:

- The base is `LIVE_RELOAD_SECONDS`.
- A hard floor of 30 seconds is enforced (reloading more often is hostile to the device).
- If the Screen's rotation interval is longer than the base (e.g., a 300s slow-rotation Screen), the reload interval is `max(LIVE_RELOAD_SECONDS, rotation_interval_seconds)` so the reload does not fire mid-page-view on a single-page slow Screen.

The browser does the work. No JS, no connections, no fragment endpoints. Transient network failures are absorbed by the browser's reload-retry behaviour; an offline device just keeps rendering its cached HTML until connectivity returns.

The admin-side latency is bounded: an admin who edits a Screen and looks up at the tablet within `LIVE_RELOAD_SECONDS` sees the new content. With the default 60s, that is "look up within a minute".

Rejected alternatives for live-reload:

- **Client-side polling**: adds a "has-this-screen-changed?" endpoint that has to compute a checksum across screens / pages / widget_instances / theme. Adds JS. Adds CPU on both ends. The win (slightly fresher render than `<meta refresh>` if the JS polls more often) is marginal at v1 scale.
- **htmx polling on a fragment**: same fundamental shape; the htmx layer is just sugar. Same fragment-endpoint and validation surface.
- **SSE**: one open connection per device. Reconnect logic on disconnect. Event format. The latency win (sub-second) is not justified by any v1 use case.
- **WebSocket**: same connection cost as SSE plus bidirectional plumbing. Justified in Phase 4 when push features (alerts) need bidirectional infrastructure; not justified for v1 live-reload alone.

### Theme CSS: inline `<style>` block per render.

The render handler embeds `themes.Theme.CSSVariables()` (already shipped by SPEC-004) directly in a `<style>` block in the page `<head>`. The first byte of the response carries the theme; the first paint wears it. No separate stylesheet endpoint; no separate HTTP request; no cache header strategy; no flash of unstyled content.

The existing SPEC-004 validation (R7-R14: whitelist regex for hex colors, length-capped font strings, no `;`/`{`/`}`/`<`/`>`/backslash in font values) is what makes the inline embedding safe. The renderer is a pure function of the theme value; the bytes that reach the `<style>` block are the same bytes that passed validation at write time.

Rejected alternatives for theme CSS:

- **Separate `/themes/{id}.css` endpoint**: would save inline bytes on subsequent loads at the cost of a second HTTP request, a cache-control strategy, a per-theme route, and a per-render-context theme-lookup that the inline mechanism does in zero additional code. Two HTTP requests instead of one is the wrong direction; the bytes saved are trivial.
- **Per-theme generated CSS file at theme-create time**: complicates theme mutation (every theme update has to regenerate the file or invalidate it). The inline mechanism rebuilds the CSS on every render at zero noticeable cost.
- **CSS variables via JS at runtime** (`document.documentElement.style.setProperty(...)`): adds JS, defers theme application until JS runs, and causes a visible FOUC. Worse on every axis.

### Why the same ADR

The two questions share the same constraint: the device's connection to the server is intermittent and the device should render correctly with whatever it last fetched. `<meta refresh>` for live-reload and inline `<style>` for theme CSS share the property "every render is self-contained; the document is the unit of caching, transmission, and offline rendering". Phase 4's PWA spec inherits this property for free.

## Consequences

**Accepted trade-offs:**

- The browser-driven reload happens at a coarse cadence (default 60s, minimum 30s). Admin edits take up to `LIVE_RELOAD_SECONDS` to show up. We accept this for v1 -- if a use case demands sub-30s update visibility, a future spec adds SSE on top of the same render handler. The change is purely additive: keep `<meta refresh>` as the fallback; add SSE as the fast path.
- The reload is a full-page reload, not a fragment swap. The user sees a brief flicker if they happen to be watching the kiosk at the moment of reload. We accept this -- the alternative (htmx fragment swap with a "should I swap?" check) is more code with no real UX win on a kiosk that nobody is actively staring at.
- Inline CSS adds bytes to every render. For a typical theme (~10 variables, ~300 bytes of CSS), this is negligible. The benefit (no second HTTP request, no FOUC, no cache-invalidation strategy) far outweighs the cost.
- A `<meta refresh>` tag is not interruptible; once the browser scheduled it, the reload fires. We accept this. The only reason to interrupt a reload would be "the user is currently interacting" -- but the device-render path has no interactive widgets in v1.
- The hard floor of 30 seconds means `LIVE_RELOAD_SECONDS` cannot be set lower. We document this and reject configs at startup that try.

**Benefits:**

- Zero JavaScript for live-reload. Zero open connections per device. Zero new endpoints to gate. Zero fragment-endpoint validation surface.
- The render output is a self-contained HTML document. Phase 4 PWA caches it verbatim; offline rendering "just works".
- The theme is correct on first paint. No FOUC. No separate stylesheet endpoint to manage.
- The implementation is "two extra `<head>` elements" -- a `<meta>` tag and a `<style>` block. Roughly 10 lines of templ.
- Failure modes are obvious. The kiosk shows what it last rendered until the next reload. The admin can manually refresh the tablet's browser if they want immediate feedback.

**Risks accepted:**

- A pathological device clock could in principle make the `<meta refresh>` fire on a wildly different schedule than configured. Browsers honour the value via their own timer, not via wall-clock comparison, so we believe this is not a real risk. Worst case: the reload fires at the configured interval relative to load time, which is exactly what we want.
- A reverse proxy that ignores `Cache-Control: no-store` and caches the render response could serve stale renders to multiple devices. We document the cache header expectations; configuration of the proxy is the operator's responsibility (same posture as the rest of the admin UI).
- An admin who wants instant feedback during live configuration has to manually refresh the kiosk's browser, or wait up to `LIVE_RELOAD_SECONDS`. We accept the friction; a future "watch this screen" admin tool (which already has access to `?screen=<id>` preview) can layer SSE on top of the same render handler if it becomes a real productivity issue.
- The CSS variables ship inline per render, so two devices on the same Screen download the same CSS twice. This is fine -- 300 bytes per render, household-scale traffic, no measurable cost.
- A future widget that wants to dynamically change the theme (per-widget themed regions) would have to invent its own CSS variable shadowing. We accept this; per-widget theming is explicitly out of scope (SPEC-005 / SPEC-007).
