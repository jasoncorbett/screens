---
id: SPEC-006
title: "Screen Model"
phase: 2
status: accepted
priority: p0
created: 2026-05-13
author: pm
---

# Screen Model

## Problem Statement

The screens service is meant to drive dashboards on wall-mounted devices, but Phase 2 so far ships only the two infrastructure pieces a dashboard needs (themes and the widget interface) -- not the dashboard itself. A "dashboard" in this product is a named *Screen* whose contents are an ordered list of *Pages*, each of which is a layout of *widget instances*. Without that data model, an admin cannot tell the system "the kitchen tablet should rotate through a clock page, a weather page, and a family-calendar page"; the upcoming Screen Display spec (the next p0 item in this phase) has nothing to read; and every Phase 3 widget has nowhere to land.

This spec introduces three new entities and the admin CRUD surface around them:

1. A **Screen** is a top-level dashboard identified by a name, with a foreign-key reference to the theme it wears. Each Screen has a configurable auto-rotation interval (the actual rotation behaviour ships in Screen Display, but the interval is a property of the screen and therefore belongs in this data model).
2. A **Page** is an ordered child of a Screen. A Screen has zero or more Pages. The order is admin-controlled (drag-to-reorder is fine in a later spec; v1 supports an integer position field plus a "move up / move down" pair of actions).
3. A **WidgetInstance** is a placement of one registered widget type on one page, carrying its per-instance JSON configuration (validated against the widget type's `widget.Registration.ValidateConfig` from SPEC-005). A page has zero or more widget instances, each with a numeric position that determines its slot inside the page's layout.

The CRUD surface exposed by this spec is admin-only and follows the established `/admin/<entity>` pattern used by themes, devices, and users. It covers everything an admin needs to *manage the data*: create, list, edit, delete, reorder. The actual *visual* arrangement of widgets on a page -- the picker that lets an admin browse registered widget types, the per-widget config form, the drag-and-drop arrangement -- is owned by the Widget Selection UI spec (p1, later in this phase). This spec ships only enough widget-instance handling to wire the model end-to-end: a minimal "add a widget of type X with default config" action on a page edit page, plus delete-from-page and reorder. Anything richer waits for Widget Selection UI.

The layout *shape* of a page is the one substantive design choice this spec makes. The roadmap describes it as "widget layout", but that is open-ended. The choices in scope range from a simple ordered list ("widgets stack vertically in position order") to a grid (`row, col, row_span, col_span`) to free-form CSS positioning. The decision -- and its rationale -- lives in ADR-006. The short version: v1 uses an **ordered list (1-D, position-indexed)** layout. A widget instance has a `position INTEGER NOT NULL` column; Screen Display renders the instances in position order inside a CSS-grid container styled by the theme. This is enough for every widget Phase 3 ships and leaves a clean migration path to row/column grids if and when a real use case demands one.

This spec is the data-model owner. Screen Display (next spec) is the renderer owner. Widget Selection UI (p1) is the picker owner. The interfaces between them are explicit in this spec.

## User Stories

- As an **admin**, I want to create a named Screen (e.g., "kitchen", "bedroom") with a theme assignment, so I can configure what each physical tablet displays without editing code or config files.
- As an **admin**, I want to add Pages to a Screen and order them, so I can build a multi-page rotation (e.g., "Page 1: time; Page 2: weather; Page 3: family calendar").
- As an **admin**, I want each Screen to carry an auto-rotation interval (in seconds), so the eventual Screen Display can rotate between pages at the cadence I choose without hard-coding it.
- As an **admin**, I want to add widget instances to a page and reorder them, so I can lay out exactly what shows up on each page.
- As an **admin**, I want to delete a Screen, Page, or widget instance, so I can clean up dashboards I no longer use.
- As an **admin**, I want deleting a Screen to also delete its Pages (and their widget instances), so I do not have to manually clean up dangling children.
- As an **admin**, I want deleting a Page to delete its widget instances, so the database does not accumulate orphan widgets.
- As an **admin**, I want the system to refuse to delete a theme that any Screen is currently using, so I cannot accidentally orphan a Screen's theme reference.
- As a **Screen Display author (the next p0 spec, this spec's primary downstream consumer)**, I want a single service call that returns a Screen with all its Pages and widget instances pre-fetched in order, so the renderer is a single query and an iteration, not an N+1.
- As a **Widget Selection UI author (Phase 2, p1, deferred)**, I want the data model to already carry a per-page widget-instance list with positions and validated config blobs, so the UI is a CRUD over an existing schema rather than a new schema in its own spec.

## Functional Requirements

### Screens

1. The system MUST store screens in a `screens` table with at least: `id`, `name`, `theme_id`, `rotation_interval_seconds`, `created_at`, `updated_at`.
2. Screen IDs MUST be 32-character hex strings produced via the existing `auth.GenerateToken[:32]` primitive (mirror themes / devices / users / sessions).
3. Screen names MUST be unique across the table (UNIQUE constraint at the DB layer).
4. Screen names MUST be 1-64 characters from the character set `[A-Za-z0-9 _-]` (mirror the theme name validator: same regex, same rejection messages where practical).
5. The `theme_id` column MUST be a foreign key into `themes(id)`. The reference MUST be `ON DELETE RESTRICT`: an attempt to delete a theme that any Screen references MUST fail with a clear error. (See ADR-006 for why RESTRICT and not SET NULL or CASCADE.)
6. The system MUST extend `internal/themes` -- specifically `themes.Service.Delete` -- to detect the SQLite foreign-key-constraint violation that results from RESTRICT and translate it into a typed error (`themes.ErrThemeInUse`). The theme admin UI MUST surface that error as `error=Cannot+delete+a+theme+in+use+by+a+screen`.
7. The `rotation_interval_seconds` column MUST be an `INTEGER NOT NULL DEFAULT 30`. Range MUST be validated at the application layer: minimum 5 seconds, maximum 3600 seconds (1 hour). Out-of-range values MUST be rejected by `screens.Service.Create` / `screens.Service.Update` with a per-field validation error.
8. `created_at` and `updated_at` MUST follow the existing TEXT-as-ISO8601 convention (`datetime('now')` default + manual update on mutation).
9. The system MUST NOT seed a default screen on first start. A fresh install has zero screens; the admin creates the first one through the UI. (Contrast with themes, where a default IS seeded; the reason is that Screen Display has a clean "no screens configured" state to render, whereas themes are referenced by other tables.)

### Pages

10. The system MUST store pages in a `pages` table with at least: `id`, `screen_id`, `name`, `position`, `created_at`, `updated_at`.
11. Page IDs MUST be 32-character hex strings produced via `auth.GenerateToken[:32]`.
12. The `screen_id` column MUST be a foreign key into `screens(id)` with `ON DELETE CASCADE`: deleting a Screen MUST delete all its Pages.
13. The `name` column on pages MAY be empty. A page's display label is admin-friendly metadata only -- Screen Display renders the page's widgets, not its name. If non-empty, the name MUST satisfy the same regex as Screen names. The v1 admin UI MUST surface the page name as a small label in the page list ("Page 1 ‐ kitchen-clock") so an admin can tell pages apart at a glance.
14. Page names within a single Screen MAY be duplicate (or empty). Name uniqueness is not enforced.
15. The `position` column MUST be an `INTEGER NOT NULL`. Positions are 1-indexed integers used purely for ordering within a Screen. Gaps in positions are acceptable; the service exposes a "move up / move down" pair that swaps positions atomically.
16. The system MUST enforce uniqueness on `(screen_id, position)` via a unique index, so two pages on the same Screen cannot share a position number. The reorder operation MUST do the swap inside a transaction to avoid transient duplicate-position states.
17. The system MUST automatically assign a new page's position as `MAX(position) + 1` within the Screen at create time (so admins do not have to think about it). The first page on a Screen gets position 1.
18. The system MUST provide an admin-callable "move up" and "move down" operation per page. Move up on the top page is a no-op (302 redirect with a friendly message). Move down on the bottom page is a no-op (same).

### Widget Instances

19. The system MUST store widget instances in a `widget_instances` table with at least: `id`, `page_id`, `type`, `config` (JSON blob as TEXT), `position`, `created_at`, `updated_at`.
20. Widget instance IDs MUST be 32-character hex strings produced via `auth.GenerateToken[:32]`.
21. The `page_id` column MUST be a foreign key into `pages(id)` with `ON DELETE CASCADE`: deleting a Page MUST delete all its widget instances.
22. The `type` column MUST hold a widget type identifier as registered with `widget.Default()` (SPEC-005). The service MUST refuse to create or update an instance whose `type` is unknown to the process-wide widget registry; the response is a typed error (`screens.ErrUnknownWidgetType`).
23. The `config` column MUST hold the JSON bytes returned by the widget's `ValidateConfig`. The service MUST round-trip every create or update through `widget.Default().Validate(type, raw)` before persisting. If validation fails, the row MUST NOT be written and the validator's error MUST be returned to the caller (the admin UI surfaces it).
24. The `position` column MUST be `INTEGER NOT NULL`, 1-indexed. Uniqueness on `(page_id, position)` MUST be enforced via a unique index. Reorder operations MUST be transactional, as for pages.
25. A new widget instance's position MUST default to `MAX(position) + 1` within its page; the first widget on a page gets position 1.
26. The minimal v1 admin UI MUST expose: "Add widget of type X with that type's default config" (a button per registered widget type, sourced from `widget.Default().List()`), "Delete this widget instance", "Move up", "Move down". Editing the per-instance config is OUT of scope for this spec (see Out of Scope); it lives in Widget Selection UI.
27. The system MUST refuse to add a widget instance to a page that does not exist (404 / 302 with error). It MUST refuse to add an instance of an unregistered type (302 with error).

### CRUD API (Admin UI)

28. The system MUST register the following admin-only routes (all gated by `RequireAuth` + `RequireRole(RoleAdmin)` + `RequireCSRF`, mirroring the existing `/admin/themes` pattern):
    - `GET  /admin/screens` -- list screens with their page counts and theme names; provide a "New Screen" form.
    - `POST /admin/screens` -- create a Screen from a form (`name`, `theme_id`, `rotation_interval_seconds`, `_csrf`).
    - `GET  /admin/screens/{id}/edit` -- edit a Screen's name, theme, and rotation interval; also lists the Screen's pages with reorder / edit / delete affordances and a "New Page" form.
    - `POST /admin/screens/{id}` -- update a Screen.
    - `POST /admin/screens/{id}/delete` -- delete a Screen (cascades to pages and widget instances).
    - `POST /admin/screens/{id}/pages` -- create a page on the named Screen (`name`, `_csrf`). Position is assigned by the service.
    - `GET  /admin/screens/{id}/pages/{pageID}/edit` -- edit a single page; lists the page's widget instances with reorder / delete affordances and a "Add widget" picker driven by `widget.Default().List()`.
    - `POST /admin/screens/{id}/pages/{pageID}` -- update a page's name.
    - `POST /admin/screens/{id}/pages/{pageID}/delete` -- delete a page (cascades to its widget instances).
    - `POST /admin/screens/{id}/pages/{pageID}/move-up` -- swap with the previous page.
    - `POST /admin/screens/{id}/pages/{pageID}/move-down` -- swap with the next page.
    - `POST /admin/screens/{id}/pages/{pageID}/widgets` -- create a widget instance on the page (`type`, `_csrf`). Config is the widget type's default config; position is assigned by the service.
    - `POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete` -- delete a widget instance.
    - `POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-up` -- swap with the previous widget.
    - `POST /admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/move-down` -- swap with the next widget.
29. State-changing endpoints MUST validate `_csrf` via the existing `RequireCSRF` middleware. The existing chain wrapping the admin sub-mux handles this; no per-route CSRF wiring needed.
30. Validation errors (invalid name, out-of-range rotation interval, unknown theme, unknown widget type) MUST be surfaced via the same query-param flash pattern as `/admin/users` and `/admin/devices` (`?error=...`), NOT re-rendered inline. The reason this differs from `/admin/themes` (which renders inline): theme forms have many input fields where per-field error messages matter; screen / page / widget forms have very few fields and the flash pattern is plenty.
31. Success on any state-changing endpoint MUST flash a clear message via `?msg=...` (e.g., `msg=created`, `msg=updated`, `msg=deleted`, `msg=moved`, `msg=widget_added`, `msg=widget_deleted`, `msg=widget_moved`).
32. The Screen list page MUST show each Screen's name, the name of its theme, its page count, and its rotation interval.
33. The Screen edit page MUST show the Screen's pages in position order with a `name` (or "(no name)"), a position number, a "Move up" button (disabled / no-op on the top page), a "Move down" button (disabled / no-op on the bottom page), an "Edit" link, and a "Delete" button.
34. The Page edit page MUST show the page's widget instances in position order with the widget type's `DisplayName`, the per-instance config's JSON (rendered as monospace pre-formatted text, read-only -- editing the config is Widget Selection UI's job), a position number, "Move up" / "Move down" buttons, and a "Delete" button. Below the list, a "Add widget" form MUST render a `<select>` populated from `widget.Default().List()` showing each registration's `DisplayName` and `Description`; submitting the form creates an instance with the default config.

### Service Surface

35. The system MUST expose `screens.Service` in a new package `internal/screens/` with at minimum these methods:
    - `NewService(sqlDB *sql.DB, widgets *widget.Registry) *Service` -- the registry is injected so tests can pass a `NewRegistry` populated only with widgets they care about.
    - `CreateScreen(ctx context.Context, in ScreenInput) (Screen, error)`
    - `GetScreenByID(ctx context.Context, id string) (Screen, error)`
    - `ListScreens(ctx context.Context) ([]ScreenSummary, error)` -- the summary carries the screen plus its theme name and page count for the list page.
    - `UpdateScreen(ctx context.Context, id string, in ScreenInput) (Screen, error)`
    - `DeleteScreen(ctx context.Context, id string) error`
    - `CreatePage(ctx context.Context, screenID, name string) (Page, error)`
    - `GetPageByID(ctx context.Context, screenID, pageID string) (Page, error)` -- `screenID` is required so the URL parameter check is in the service, not the handler.
    - `UpdatePage(ctx context.Context, screenID, pageID, name string) (Page, error)`
    - `DeletePage(ctx context.Context, screenID, pageID string) error`
    - `MovePageUp(ctx context.Context, screenID, pageID string) error`
    - `MovePageDown(ctx context.Context, screenID, pageID string) error`
    - `AddWidget(ctx context.Context, screenID, pageID, widgetType string) (WidgetInstance, error)` -- always uses the widget type's default config; per-instance config editing is OUT of scope here.
    - `DeleteWidget(ctx context.Context, screenID, pageID, widgetID string) error`
    - `MoveWidgetUp(ctx context.Context, screenID, pageID, widgetID string) error`
    - `MoveWidgetDown(ctx context.Context, screenID, pageID, widgetID string) error`
    - `GetScreenFull(ctx context.Context, id string) (ScreenFull, error)` -- returns the screen plus its pages plus each page's widget instances, all in position order. This is the call Screen Display will use; pre-shipping it now keeps Screen Display's task list strictly additive.
36. `ScreenFull` MUST be a domain struct shape, NOT a DB row. The struct MUST be sufficient for Screen Display to render without going back to the database for any per-page or per-widget detail.
37. The service MUST validate `theme_id` is a real theme on Create / Update by looking it up via `themes.Service.GetByID`. The service MUST therefore take a `*themes.Service` dependency. Returning an error early (in Go) is cleaner than relying on the FK violation surfacing from SQLite -- though the FK constraint remains as the second line of defence.
38. The service MUST NOT register HTTP routes. Routes are registered by `views/screens.go` and `views/routes.go` (mirror the theme handlers).

### Deps wiring

39. The `views.Deps` struct MUST gain a `Screens *screens.Service` field. `main.go` MUST construct the service and wire it through, mirroring the `Themes` wiring.
40. `main.go` MUST pass `widget.Default()` and `themesSvc` into `screens.NewService` so that widget validation and theme lookups go through the live registry / service.

### Existing Behaviour Preserved

41. The existing `/admin/themes`, `/admin/devices`, `/admin/users`, `/admin/login`, `/health`, `/`, and device-landing routes MUST continue to behave identically.
42. The Theme System's existing acceptance criteria MUST continue to pass. The Theme System gains one new error path (`ErrThemeInUse`) on Delete; it does NOT change any of its happy paths.
43. The Widget Interface MUST continue to work unchanged. No changes to `internal/widget/` are needed; this spec is purely an additive consumer of `widget.Default()`.
44. No new third-party Go dependencies are introduced.

### Admin Landing Page

45. The admin landing page (`views/admin.templ`) MUST gain a link to `/admin/screens` for admins, mirroring the existing links to `/admin/users`, `/admin/devices`, `/admin/themes`. (One new line.)

## Non-Functional Requirements

- **Performance**: The full-render call `GetScreenFull` MUST do at most a constant number of SQL round-trips (one for the screen + theme join, one for the page list, one for the widget-instance list filtered by page IDs). N+1 query patterns are explicitly out of bounds. Household-scale dashboards have tens of widgets per screen at most, so a single query batch per render is plenty.
- **Security**: All admin routes sit behind `RequireAuth` + `RequireRole(RoleAdmin)` + `RequireCSRF`. Screen / page / widget names use the same whitelist regex as theme names. Widget config JSON is round-tripped through `widget.Default().Validate` so unknown widget types and malformed configs are rejected at write time. Theme references are FK-constrained at the DB layer with RESTRICT semantics. The widget config column accepts arbitrary JSON shapes by design (per-widget validators own the shape), but the inputs are still bounded: every widget's validator caps free-form string lengths (per SPEC-005 NFR).
- **Reliability**: All multi-row mutations (page reorder, widget reorder, screen delete, page delete) run inside a single transaction. The `ON DELETE CASCADE` semantics make screen / page deletion atomic at the DB layer. The `RESTRICT` semantics on the theme FK make theme deletion fail-fast and recoverable rather than silently orphaning a screen.
- **Testability**: Service unit tests use `db.OpenTestDB(t)` for an isolated SQLite database per test. Each test constructs its own `widget.NewRegistry` populated with the widget types it needs (typically the placeholder `text` widget). Handler tests use `httptest.NewRecorder` and the admin-context helper already present in `views/themes_test.go`. CSRF integration tests use `httptest.NewServer` to exercise the real middleware chain, as in the existing `views/themes_test.go`.
- **Backwards compatibility**: This spec adds new tables, new admin routes, and one new error (`themes.ErrThemeInUse`). No existing tables, routes, or service signatures change except for the additive `ErrThemeInUse` return from `themes.Service.Delete`. The existing `themes_test.go` happy-path delete test still passes; new tests cover the in-use rejection.

## Acceptance Criteria

### Screens

- [ ] AC-1: When an admin POSTs `/admin/screens` with `name=kitchen&theme_id=<default-theme-id>&rotation_interval_seconds=30`, then a row is inserted and a 302 to `/admin/screens?msg=created` follows.
- [ ] AC-2: When an admin POSTs `/admin/screens` with an empty `name`, then the request is rejected with `?error=Name+is+required` and no row is created.
- [ ] AC-3: When an admin POSTs `/admin/screens` with `name=screen<script>`, then the request is rejected with a clear error and no row is created.
- [ ] AC-4: When an admin POSTs `/admin/screens` with a name that already exists, then the request is rejected with `?error=...` and the original row is unchanged.
- [ ] AC-5: When an admin POSTs `/admin/screens` with `rotation_interval_seconds=2`, then the request is rejected (below the 5-second minimum); when `=3601` the request is also rejected (above the 1-hour maximum). When `=5` or `=3600` it is accepted.
- [ ] AC-6: When an admin POSTs `/admin/screens` with `theme_id=does-not-exist`, then the request is rejected and no row is created.
- [ ] AC-7: When an admin POSTs `/admin/screens/{id}/delete`, then the row is deleted, every page with `screen_id = {id}` is deleted, and every widget instance under those pages is deleted (atomic via `ON DELETE CASCADE`).
- [ ] AC-8: When an admin GETs `/admin/screens`, then every Screen is listed with its name, theme name, page count, and rotation interval visible in the response body.

### Theme FK

- [ ] AC-9: When an admin POSTs `/admin/themes/{themeID}/delete` for a theme that is referenced by at least one Screen, then the existing `themes.Service.Delete` returns `themes.ErrThemeInUse` and the row is NOT deleted.
- [ ] AC-10: When AC-9 fires, then the admin sees `?error=Cannot+delete+a+theme+in+use+by+a+screen` on the redirect target.

### Pages

- [ ] AC-11: When an admin POSTs `/admin/screens/{id}/pages` with `name=clock`, then a page row is inserted with `screen_id={id}`, `name=clock`, and `position = (MAX(position) over the screen) + 1` (1 if no other pages exist).
- [ ] AC-12: When an admin POSTs `/admin/screens/{id}/pages/{pageID}/delete`, then the page row is deleted AND every widget instance row with `page_id = {pageID}` is deleted.
- [ ] AC-13: Given a Screen with three pages at positions 1, 2, 3, when an admin POSTs move-down on the page at position 1, then after the call the pages are at positions 2 (old position-1 page), 1 (old position-2 page), 3 (unchanged).
- [ ] AC-14: Move-up on the page at position 1 is a no-op (a 302 with a friendly message; no rows mutated). Move-down on the bottom page is the same.
- [ ] AC-15: When two reorder operations interleave (simulated by running them serially under `go test -race`), then the final state has no duplicate `(screen_id, position)` pairs. (The unique index plus transactional swap guarantee this.)

### Widget Instances

- [ ] AC-16: When an admin POSTs `/admin/screens/{id}/pages/{pageID}/widgets` with `type=text` (the placeholder widget from SPEC-005), then a widget instance is created with `type=text`, `config` equal to the bytes returned by the `text` widget's `DefaultConfig()`, and `position = MAX(position) + 1`.
- [ ] AC-17: When an admin POSTs the same endpoint with `type=nonexistent`, then the request is rejected (`?error=Unknown+widget+type`) and no row is created.
- [ ] AC-18: When an admin POSTs `/admin/screens/{id}/pages/{pageID}/widgets/{widgetID}/delete`, then the row is deleted; subsequent `GetScreenFull` does not include the widget.
- [ ] AC-19: Given two widgets at positions 1 and 2 on the same page, when an admin POSTs move-up on position 2, then after the call the widgets are at positions 2 and 1 respectively (swapped).
- [ ] AC-20: When the placeholder `text` widget's `DefaultConfig()` is fed through the service's create-widget path and then retrieved via `GetScreenFull`, then the retrieved `config` bytes pass `widget.Default().Validate("text", config)` without error.

### CRUD API

- [ ] AC-21: When a member (non-admin) GETs `/admin/screens`, then the response is 403 (from `RequireRole`).
- [ ] AC-22: When a request to any state-changing route arrives without a valid `_csrf` field, then it is rejected with 403 by the existing CSRF middleware, and no rows are mutated.
- [ ] AC-23: When an admin GETs `/admin/screens/{id}/edit` for a Screen with two pages, then the response body lists both pages in position order with their names and position numbers visible.
- [ ] AC-24: When an admin GETs `/admin/screens/{id}/pages/{pageID}/edit` for a page with one `text` widget, then the response body shows the widget's `DisplayName` (`Text`), its position, and its current config JSON.
- [ ] AC-25: When an admin GETs `/admin/screens/{id}/pages/{pageID}/edit`, then the "Add widget" `<select>` is populated with every widget type from `widget.Default().List()` (at minimum, `text`).
- [ ] AC-26: When `screens.Service.GetScreenFull` is called on a Screen with 2 pages and 3 widgets total, then the returned `ScreenFull` carries the screen, the 2 pages in position order, each page's widget instances in position order, and the theme.

### Out-of-Scope Sanity

- [ ] AC-27: When the codebase is searched after this spec ships, then no per-instance config-editing form exists (Widget Selection UI ships that later).
- [ ] AC-28: When the codebase is searched, then no drag-and-drop reorder logic is present (move up / move down is the v1 reorder UX).

## Out of Scope

- The device-facing renderer. The Screen Display spec (the next p0 in this phase) owns the rendering pipeline: how a screen's pages turn into HTML, how the auto-rotation interval drives a client-side timer, how the theme's CSS variables ship to the device, how device authentication picks the right screen.
- Per-screen-instance widget config EDITING. This spec lets an admin add a widget with its type's default config and reorder / delete it; editing the per-instance config (e.g., changing the text widget's body) is owned by Widget Selection UI (p1, later in Phase 2).
- A widget-picker UI richer than the v1 "select type, click add". The full picker (showing widget thumbnails, descriptions, defaults, with a richer add experience) is Widget Selection UI.
- Drag-and-drop reorder. v1 uses "move up / move down" buttons because that is the smallest amount of UI that solves the ordering problem. A future spec can add drag-and-drop on top of the same `position` column.
- Multi-column layouts, row / column spans, free-form coordinate positioning. v1 is a 1-D ordered list. See ADR-006 for the rationale and the migration path to a row/column model.
- Per-page background images. The Page Backgrounds spec (Phase 2, p1) adds an optional background-image URL field on the `pages` table once Screen Model exists. The schema is forward-compatible (additive column).
- Per-screen theme overrides at the page level. A page inherits the Screen's theme; an admin who wants different looks on different pages makes different Screens. (If this becomes desired later, a separate spec adds a nullable `theme_id` column on `pages` with the override semantics.)
- Device-to-screen mapping. Which physical device should render which Screen is a separate concern; the Screen Display spec owns that mapping (likely a column on `devices` referencing `screens.id`, or a separate join table). This spec just builds the Screens.
- Screen templates ("create a kitchen screen with the standard 3 pages"). Roadmap Phase 5 item.
- Screen config export / import (JSON). Roadmap Phase 5 item.
- Versioning of Screens, Pages, or widget instances. Editing mutates in place. If history matters later, a separate spec adds it.
- Screen / Page audit logging beyond the existing slog lines on each mutating handler. Audit-level logging (who changed what when) is a future concern.
- Live preview of a Screen in the admin UI. The "go look at the device" loop is fine for v1.
- A separate admin permission tier ("editors" who can manage Screens but not Themes). All admin operations remain gated by `RoleAdmin`. The Auth System has only the `admin` and `member` roles in Phase 1; sub-admin tiers are a future spec if needed.

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- needs the migration runner, sqlc setup, `db.OpenTestDB(t)`, the `Queries.WithTx` pattern.
- Depends on: SPEC-002 (Admin Auth) -- needs `RequireAuth`, `RequireRole(RoleAdmin)`, `RequireCSRF`, session+CSRF helpers, `auth.GenerateToken` for IDs.
- Depends on: SPEC-004 (Theme System) -- needs `themes.Service` for FK validation on `theme_id`. Also extends `themes.Service.Delete` with the new `ErrThemeInUse` return.
- Depends on: SPEC-005 (Widget Interface) -- needs `widget.Registry`, `widget.Default()`, `widget.Registration` (Type / DisplayName / DefaultConfig / ValidateConfig). Adds an additive consumer of the registry; does not modify it.
- No new third-party Go dependencies. (SQLite still needs `PRAGMA foreign_keys = ON` -- the existing `db.Open` sets it; this spec assumes that.)

## Open Questions

All resolved.

- Q1 **Resolved**: Layout shape for v1 is a 1-D ordered list (position-indexed). Row/column grids are deferred to a future spec when a real use case demands them. The `position INTEGER NOT NULL UNIQUE within page` schema is the lowest-friction path that does not paint the project into a corner. See ADR-006.
- Q2 **Resolved**: Theme deletion when a Screen references it is RESTRICT, surfaced as `themes.ErrThemeInUse`. SET NULL would orphan the Screen's theme reference and force Screen Display to handle a "no theme" path (already excluded by Theme System's seed); CASCADE would let one delete blow away every Screen using that theme, which is a footgun. RESTRICT is the safe default; the admin must reassign Screens to a different theme before deleting. See ADR-006.
- Q3 **Resolved**: Page deletion cascades to widget instances; Screen deletion cascades to pages (and transitively to widget instances). `ON DELETE CASCADE` at the FK is the right tool; it is atomic, requires no application-layer code, and is supported by SQLite. See ADR-006.
- Q4 **Resolved**: Widget-instance config validation happens at write time via `widget.Default().Validate(type, raw)`. Two-layer validation (write + render) is established by SPEC-005; Screen Display will re-validate at render time. The two-layer property closes the "hand-edited bad row" hole at zero cost.
- Q5 **Resolved**: The admin UI for adding widget instances is intentionally minimal in this spec: a `<select>` of registered widget types, an Add button, and reorder / delete affordances. Editing per-instance config is Widget Selection UI's job. Drawing this line keeps this spec focused on the data model and gives Widget Selection UI a meaningful surface area to own.
- Q6 **Resolved**: `GetScreenFull` is the single call Screen Display will use. Pre-shipping it here, with a stable signature, keeps Screen Display's diff additive and saves a back-and-forth between the two specs.
- Q7 **Resolved**: Pages have a `name` for admin-side identification only. Screen Display does not render the name. Name uniqueness within a Screen is NOT enforced -- two pages labeled "clock" on the same Screen would be confusing but is not a data-integrity violation.
- Q8 **Resolved**: Reorder UI is "move up / move down" buttons, not drag-and-drop. This is the smallest UX that solves the ordering problem; drag-and-drop can be added later on top of the same `position` column.
