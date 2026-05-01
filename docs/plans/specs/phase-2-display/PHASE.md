---
phase: 2
title: "Display Framework"
status: planning
milestone: "screen-infrastructure"
---

# Phase 2: Display Framework

## Goal

Build the screen, page, and widget infrastructure without specific widget implementations. Establish the extensible pattern (widget interface, registry, renderer) that Phase 3 fills in with real widgets. Admin can configure screens, themes, and layouts. Devices can display and auto-rotate through pages.

## Specs in This Phase

| ID | Title | Status | Priority |
|----|-------|--------|----------|
| SPEC-004 | Theme System | draft | p0 |
| | Widget Interface | | p0 |
| | Screen Model | | p0 |
| | Screen Display | | p0 |
| | Widget Selection UI | | p1 |
| | Theme Preview | | p1 |
| | Typography Roles | | p1 |
| | Page Backgrounds | | p1 |
| | Card Theming | | p1 |

## Dependencies

Requires Phase 1 (Foundation) -- all admin endpoints need auth, all data needs storage.

## Exit Criteria

1. Admin can create, edit, and delete themes with color/font/spacing settings
2. Widget interface is defined with a placeholder "text" widget
3. Admin can create screens with multiple pages and assign widgets
4. Device displays screen pages and auto-rotates between them
5. Admin can select which widgets appear on each page
6. All green-bar checks pass
