---
phase: 3
title: "Content Widgets"
status: planning
milestone: "real-widgets"
---

# Phase 3: Content Widgets

## Goal

Implement all widget types using the interface established in Phase 2. Each widget is independent and can be developed in parallel. At the end of this phase, screens display real content: time, weather, calendars, home automation controls, slideshows, charts, and financial data.

## Specs in This Phase

| ID | Title | Status | Priority |
|----|-------|--------|----------|
| | Time/Date Widget | | p0 |
| | Weather Widget | | p1 |
| | Calendar Widget | | p1 |
| | Home Assistant Widget | | p1 |
| | Slideshow Widget | | p1 |
| | Charts Widget | | p2 |
| | Financial Tickers Widget | | p2 |

## Dependencies

Requires Phase 2 (Display Framework) -- widget interface and registry must exist.

## Exit Criteria

1. All p0 widgets are functional and configurable
2. All p1 widgets are functional and configurable
3. Each widget renders correctly within the screen layout
4. Each widget's configuration is manageable through the admin UI
5. External data sources (weather API, iCal, Home Assistant) connect and refresh
6. All green-bar checks pass
