---
id: ADR-007
title: "Render every Screen page server-side once per reload; client-side rotator cycles between them"
status: accepted
date: 2026-05-25
---

# ADR-007: Render every Screen page server-side once per reload; client-side rotator cycles between them

## Context

A Screen is an ordered list of Pages (SPEC-006). The Screen Display renderer (SPEC-007) has to produce HTML for the device's kiosk browser AND make the device cycle through pages on the Screen's `rotation_interval_seconds` cadence. Two clean shapes exist for the cycle:

1. **Render every page in the initial response; toggle visibility client-side.** One HTTP request returns N page-`<section>`s wrapped in a single document. A tiny client-side script rotates a `.page-current` class between them every interval.
2. **Render one page at a time; fetch the next page on the timer.** The first request returns page 1; the rotator runs an `htmx`-style fragment fetch (or a plain `fetch()`) every interval to swap in the next page's content.

Other shapes exist but are not real contenders for v1:

3. **Render every page client-side from a JSON config.** The server ships only a config document; a JS framework hydrates the page widgets. This is the spiritual opposite of the project's "server-rendered HTML, no framework" stance and is rejected as a category.
4. **Server-driven rotation via WebSocket or SSE.** The server pushes "go to page 2 now" frames; the client honours them. Reverses the control model and requires push infrastructure that does not exist in Phase 2.

The threat model is unchanged from the rest of the project: the device authenticates with its bearer token, and the rendered HTML is whatever its assigned Screen contains. Nothing about the cycle mechanism changes the auth or validation surface.

Constraints that pushed the decision:

- **The rotator must not hang on a network blip.** A wall-mounted kiosk often runs on the household Wi-Fi; transient connectivity issues are common. A rotation mechanism that requires a network round-trip per cycle will visibly stall on those blips, and "stalled wall display" is exactly the failure mode admins do not tolerate.
- **Phase 4 PWA support is on the roadmap.** A future service worker will cache the device-facing HTML for offline rendering. If the rotation mechanism requires fetching fresh fragments per cycle, the offline-PWA story becomes "cache the shell, fetch fragments at runtime", which then needs an offline-aware fragment endpoint. If the rotation mechanism is purely client-side over already-cached HTML, the offline story is trivially "cache the document; rotate within it".
- **Household scale.** Each Screen has 1-10 pages and ~10-30 widgets total. Rendering every page server-side once per reload is a tiny job (one `GetScreenFull` call, a few widget Render() invocations). The cost difference between options 1 and 2 is negligible on the server; the difference on the client / network side is large.

## Decision

### Render every page in the initial response; toggle visibility client-side.

The render handler at `DEVICE_LANDING_URL` produces a single HTML document containing one `<section class="page-container">` per page of the Screen. The first page also has the `page-current` class; the others are hidden by `device.css`. The body carries `data-rotation-seconds="<N>"` (set from the Screen's `rotation_interval_seconds`).

A small inline `<script>` runs on `DOMContentLoaded`, reads `data-rotation-seconds`, finds all `.page-container` elements, and rotates `page-current` between them every N seconds via `setInterval`. The script is ~30 lines of vanilla JS, no framework, no dependencies.

For the live-reload concern (admin edits the Screen and wants the device to pick up the change), see ADR-008. That's a separate mechanism (`<meta http-equiv="refresh">`) layered on the same render output.

Critical properties of the chosen shape:

- **Zero network round-trips after first paint.** The kiosk renders the first page immediately; subsequent rotations are pure CSS class toggles. A network blip lasting an hour does not interrupt the rotation.
- **PWA-ready.** Phase 4's service worker will cache one HTML document per Screen; rotation works offline trivially because everything is already in the document.
- **Browser-throttled in background tabs**, which is *good* on a kiosk that occasionally shows a `popup` over the dashboard -- background timers slow down but resume cleanly.
- **No per-rotation database query.** The server's render cost is "once per reload interval" (`LIVE_RELOAD_SECONDS`, default 60s, hard floor 30s), not "once per rotation interval" (`rotation_interval_seconds`, can be as fast as 5s). At a 5s rotation, that is a 12x reduction in render load per device.
- **Single document is the unit of testing.** The render handler emits one complete HTML page; tests use `httptest.NewRecorder` against the handler and assert on the body. No fragment-endpoint plumbing or assertion infrastructure required.

Rejected alternatives:

- **One-page-per-request with client-side fragment fetches**: forces a network round-trip per rotation, fails noisily under transient network failures, blocks the offline-PWA story, and adds a fragment endpoint with its own auth + validation surface. Saves no measurable bytes; costs material UX robustness.
- **Server-driven rotation via SSE / WebSocket**: requires an open connection per device, a reconnect strategy, and a serialisation format. Adds significant infrastructure for a small UX gain (server can tell the device "advance now"). Defers to Phase 4 when push infrastructure exists for a different reason (alerts).
- **Client-side hydration from a JSON config**: contradicts the project's framework-free stance. The widget interface (SPEC-005) is `Render(ctx, instance, theme) templ.Component`, not "produce a JSON-serialisable representation". Switching to client-side hydration would require every widget to have a second rendering implementation in JS, doubling the per-widget code footprint.

## Consequences

**Accepted trade-offs:**

- The first paint loads every widget on every page, even though only the first page is visible. For a Screen with 10 pages of 3 widgets each, that is 30 widget renders in the initial response. We accept this: at household scale (~30 widgets max), the render cost is small, and the "no flash of unstyled content when rotating" property is worth the extra work.
- The document HTML grows roughly linearly with the number of pages. A 10-page Screen ships ~10 page-containers worth of markup. Acceptable.
- A widget that polls an external API on Render (e.g., a weather widget hitting OpenWeather) will hit that API for every page on every reload, even though only one page is visible at a time. The future weather widget (Phase 3) MUST cache its API responses; this is a per-widget concern and the widget interface already commits to "Render is pure (config, theme) in v1". Polling widgets are out of scope for this ADR.
- The rotator is just `setInterval`; it does NOT pause on tab background (browser does that for us) and does NOT honour the Page Visibility API explicitly. We accept the default behaviour; if a future use case demands explicit pause-on-hidden, that is a few lines of additive code.
- A future "user clicks on a widget" interaction (none planned for v1) would happen on whatever page is currently visible. The model handles that cleanly because `page-current` is the only visible page.

**Benefits:**

- Rotation survives network failures of arbitrary duration.
- The PWA offline story (Phase 4) is "cache the document"; nothing more.
- The server's render cost is bounded by `LIVE_RELOAD_SECONDS`, not by the much smaller `rotation_interval_seconds`. At 5s rotation + 60s reload, that is a 12x reduction in server-side work per device.
- The client-side rotator is ~30 lines of vanilla JS with no dependencies. New contributors can understand it at a glance.
- Tests use `httptest.NewRecorder` against the render handler; no fragment-endpoint test infrastructure required.
- The render output is a single complete HTML document that a service worker can cache verbatim (Phase 4 alignment).
- The widget interface (SPEC-005) does NOT need a second rendering implementation; the server-side templ Render() is the only render path.

**Risks accepted:**

- A Screen with 50+ pages would ship a large HTML document. We do not see any v1 use case for 50-page Screens; the admin UX would be unwieldy long before the document size mattered. If it ever becomes a concern, lazy-loading via `<template>` elements + a more elaborate rotator is an additive change.
- A widget that secretly mutates the DOM in Render (e.g., a future widget that captures a canvas) might behave differently in a hidden page-container vs the visible one. Widgets are contracted to be pure renderers (SPEC-005 R20); a widget that violates the contract is a widget bug. We accept the risk and rely on the widget contract.
- The `setInterval` timer could be paused indefinitely by a buggy browser or a system sleep. We do not actively defend against this. If a future spec wants resilience, it can layer a "if rotation hasn't fired in N intervals, kick it" watchdog -- but the per-`LIVE_RELOAD_SECONDS` full-page reload already provides a coarse-grained safety net.
