---
phase: 4
title: "Delivery & PWA"
status: planning
milestone: "pwa-and-alerts"
---

# Phase 4: Delivery & PWA

## Goal

Make the device display a proper Progressive Web App with offline capability, installability, and push notifications. Add the alerts/messaging system ("mom says") that requires acknowledgement and delivers via push notifications.

## Specs in This Phase

| ID | Title | Status | Priority |
|----|-------|--------|----------|
| | PWA Manifest & Service Worker | | p0 |
| | Alerts & Messaging | | p0 |
| | Push Notifications | | p1 |

## Dependencies

Requires Phase 2 (Display Framework) for the screen rendering pipeline. Benefits from Phase 3 widgets but does not strictly require them.

## Exit Criteria

1. Device display is installable as a PWA on mobile devices
2. In-app instructions guide users through PWA installation
3. Admin can create alert messages with custom label (default "mom says")
4. Alerts display as overlays on devices and require acknowledgement
5. Push notifications deliver alerts to devices even when app is backgrounded
6. Acknowledgements are visible to admin
7. All green-bar checks pass
