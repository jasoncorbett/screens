---
id: SPEC-004
title: "Theme System"
phase: 2
status: draft
priority: p0
created: 2026-04-30
author: pm
---

# Theme System

## Problem Statement

The screens service displays dashboards on physical wall-mounted devices in a household. Different rooms, families, and use cases want different looks: a kitchen tablet might use a bright, high-contrast theme during the day; a bedroom display might use a warm, dim palette at night; a kid's room might pick playful colors. Hard-coding the dashboard colors in CSS forces every device to look identical and forces every visual change to ship through a code release.

The roadmap therefore introduces themes as first-class admin-managed entities. An admin defines one or more themes through the management UI, each carrying a small palette of colors, font choices, and spacing scale. Subsequent specs in Phase 2 -- specifically Screen Model and Screen Display -- attach a theme to each screen and inject the theme's values as CSS custom properties when the screen renders. From the device's perspective, "wearing" a theme is just a `<style>` block at the top of the page setting `--bg`, `--text`, `--accent`, etc., which the existing CSS already references.

This spec lays the foundation: the data model, the CRUD API and admin UI, a reusable CSS-variable rendering helper, and a guarantee that the system always has at least one usable theme (the default) so that a freshly installed instance has something to render before an admin has touched anything. It does not implement the Screen entity, the per-screen theme assignment, or the Screen Display rendering pipeline -- those are Screen Model and Screen Display, both of which depend on this spec.

The static demo page already uses CSS custom properties (`--bg`, `--surface`, `--accent`, etc.) defined in `static/css/app.css`. Themes carry the same variable names, which means a theme can override the defaults at the page level without rewriting any stylesheet rules.

## User Stories

- As an **admin**, I want to create a named theme (e.g., "kitchen-day", "bedroom-night") with a color palette, font, and spacing scale so that I can give different screens different looks without editing CSS.
- As an **admin**, I want one theme to be marked the system default so that a screen that does not explicitly pick a theme still has something to render.
- As an **admin**, I want a default theme to exist out of the box (matching the dark palette already in `static/css/app.css`) so that the very first time I open the admin UI there is at least one theme to inspect or duplicate.
- As an **admin**, I want to edit a theme's colors and fonts after it has been created so that I can tune the look without deleting and recreating.
- As an **admin**, I want to delete a theme that is no longer in use so that the list does not accumulate stale entries.
- As an **admin**, I want the system to refuse to delete the default theme so that I cannot accidentally leave the system without any theme to fall back on.
- As an **admin**, I want the system to validate color values are real CSS color hex strings so that a typo does not silently render a broken-looking page.
- As a **screen rendering pipeline (Phase 2 Screen Display, this spec's downstream consumer)**, I want a single helper function that takes a `Theme` and returns a CSS `<style>` snippet so that injecting the theme into a page is one line of code regardless of how the theme model evolves.
- As a **Screen Model author (the next Phase 2 spec)**, I want a stable theme ID I can store as a foreign key on the screen row so that linking a screen to a theme is a simple referential constraint.
- As a **device kiosk browser**, I want the CSS variable values to ship in the page HTML (rather than as a separate request) so that the theme is applied on first paint with no flash of unstyled content.

## Functional Requirements

### Theme Records

1. The system MUST store themes in a `themes` table with at least: `id`, `name`, `is_default`, color fields, font fields, spacing/radius fields, `created_at`, `updated_at`.
2. The system MUST enforce unique theme names at the database level (case-sensitive equality, mirroring the existing `users.email` UNIQUE constraint pattern).
3. The system MUST reject names that are empty or whitespace-only.
4. The system MUST reject names longer than 64 characters and names containing characters outside `[A-Za-z0-9 _-]`. (This keeps names safe to reference in URLs and admin UI without escaping considerations.)
5. The system MUST store theme IDs as TEXT primary keys generated the same way the existing tables generate IDs (32-character hex from `auth.GenerateToken[:32]` -- reuse, do not reinvent).
6. The system MUST set `created_at` to the row insert time and `updated_at` to the most recent mutation time, both as ISO-8601 TEXT (matching the project's existing time-as-text convention).

### Color Palette

7. Each theme MUST carry the following color fields, each stored as a string and validated as a CSS hex color (`#rrggbb` or `#rgb`, case-insensitive):
   - `color_bg` -- page background
   - `color_surface` -- card / panel background
   - `color_border` -- border color
   - `color_text` -- primary text color
   - `color_text_muted` -- secondary / muted text color
   - `color_accent` -- accent color used for buttons, links, highlights
8. Hex validation MUST accept both 3-digit (`#abc`) and 6-digit (`#aabbcc`) forms and MUST reject anything else (no `rgb()`, no named colors, no opacity in v1).
9. The system MUST normalise hex values to lower case at write time so equality comparisons between themes are predictable.

### Fonts

10. Each theme MUST carry a `font_family` field: a CSS `font-family` value as a string.
11. The system MUST validate `font_family` is non-empty and MUST limit length to 256 characters.
12. The system MUST reject `font_family` values containing characters that would break out of a CSS declaration: `;`, `{`, `}`, `<`, `>`, backslash, or newline / control characters. (Validation is server-side; it is the only defence -- the value is rendered into a `<style>` block, so the usual HTML escape is not in play.)
13. The system MAY support a separate `font_family_mono` field for monospace text. (This is the only optional palette field; it is OK for the v1 default theme to set it to a sensible system stack and never expose it in the admin UI as a separate input.)

### Spacing / Radius

14. Each theme MUST carry a `radius` field: a CSS length value (e.g., `10px`, `0.5rem`, `0`) as a string, validated as one of: a non-negative integer followed by `px`, a non-negative decimal followed by `rem` or `em`, or the literal `0`.
15. Each theme MAY carry a `spacing_scale` field: a positive decimal (e.g., `1.0`, `1.25`) used as a multiplier downstream. If absent in the v1 schema, it defaults to `1.0` and SHOULD be added in a later spec when widgets actually consume it. (Keeping it out of v1 reduces the surface area the admin UI has to expose, and the schema can grow when needed.) For v1, MUST: include `radius`. MAY: include `spacing_scale` -- defer.

### Default Theme

16. The system MUST track exactly one theme as the system default via the `is_default` boolean column.
17. The system MUST enforce the "exactly one default" invariant at the database level via a partial unique index on `is_default` where `is_default = 1`.
18. On first startup with an empty `themes` table, the system MUST seed a single default theme named `default` whose color values match the values currently hard-coded in `static/css/app.css` (`--bg: #0b0d11`, `--surface: #14171f`, `--border: #23273a`, `--text: #dfe2ed`, `--text-muted: #6b7084`, `--accent: #7b93ff`, `--radius: 10px`). This guarantees the database is never in a state where downstream code has no theme to render with.
19. The seeding step MUST be idempotent: running it twice MUST NOT produce two themes nor mutate an existing theme named `default`. Implementation MAY check for an existing default before inserting.
20. The system MUST allow an admin to mark a different theme as the default. Marking a theme as default MUST atomically clear `is_default` on the previously-default theme (single transaction).
21. The system MUST NOT permit `is_default = 0` on every theme; the partial unique index combined with the "set new default in a transaction" semantics keeps exactly one row marked as default at all times after seeding.
22. The system MUST refuse to delete a theme whose `is_default = 1`. The admin must mark another theme as default first.

### CRUD API (Admin UI)

23. The system MUST register the following admin-only routes (all gated by `RequireAuth` + `RequireRole(RoleAdmin)` + `RequireCSRF`, mirroring the existing `/admin/users` and `/admin/devices` patterns):
    - `GET  /admin/themes` -- list themes, render the management page.
    - `POST /admin/themes` -- create a theme from a form submission.
    - `GET  /admin/themes/{id}/edit` -- render an edit form pre-populated with the theme's current values.
    - `POST /admin/themes/{id}` -- update a theme from a form submission.
    - `POST /admin/themes/{id}/delete` -- delete the theme.
    - `POST /admin/themes/{id}/set-default` -- mark the theme as the system default.
24. The system MUST render the theme management page using the existing `views/` templ layout pattern (mirror `views/devices.templ`).
25. State-changing endpoints MUST validate the CSRF token in the existing `_csrf` form field via the existing `RequireCSRF` middleware.
26. The system MUST render an "edit" affordance on the list page (a link or button to `/admin/themes/{id}/edit`) for each theme.
27. The system MUST render the active default with a visual marker (e.g., "(default)" suffix or a badge) so the admin can tell at a glance which theme is in effect.
28. The system MUST display validation errors back to the admin on the same page (re-render the form with the rejected values and an error message), NOT redirect to an opaque "error" flash. (Validation feedback per field is more useful when fixing typos in colors than a generic flash.)
29. The system MUST flash a success message on the list page after a create / update / delete / set-default action completes successfully, using the same `?msg=...` query-param convention as `/admin/users` and `/admin/devices`.

### CSS Variable Rendering Helper

30. The system MUST expose a function in the `internal/themes` package (e.g., `func (t Theme) CSSVariables() string`) that returns the theme's values as a `:root { ... }` CSS block ready to be embedded in an HTML `<style>` tag. This is the contract Screen Display will use.
31. The CSS block MUST set, at minimum, these custom properties matching the names in `static/css/app.css`: `--bg`, `--surface`, `--border`, `--text`, `--text-muted`, `--accent`, `--radius`, plus `--font-family` for the chosen font.
32. The output MUST be safe to embed in a `<style>` block: the validation in requirements 7-14 ensures values cannot contain `<`, `>`, `;` (other than the natural CSS terminator), or `}` characters. The renderer MAY still re-validate at render time as defence in depth.
33. The renderer MUST be a pure function: same theme in => same string out. Order of declarations MUST be deterministic so output is testable without normalising.
34. The renderer MUST NOT depend on any HTTP context, request, or `*sql.DB`. It is a domain-level helper.

### Configuration

35. The system MAY add a `THEME_DEFAULT_NAME` config setting (string, default `default`) to control the seed-time default theme name. This makes test-mode bootstrapping (e.g., a different default in tests) possible without forking the seed code. Validation MUST reject empty values.
36. No new secret-bearing config is required. The default theme's color palette is hard-coded in the seed function, not in environment variables.

### Existing Behaviour Preserved

37. The existing `/admin/users` and `/admin/devices` flows MUST continue to work unchanged.
38. The existing `static/css/app.css` MUST remain in place. Theme rendering augments the page with a `<style>` block; it does NOT replace the static stylesheet. The static stylesheet provides structural / layout rules; the theme block overrides only the color / font / spacing custom properties.
39. The existing migration runner MUST apply the new `006_create-themes.sql` migration on startup with no manual intervention.

## Non-Functional Requirements

- **Performance**: Theme reads happen at most once per page render in Screen Display. The `themes` table is small (likely <50 rows), so a simple primary-key lookup is sufficient. No caching layer is required in v1.
- **Security**: All admin endpoints sit behind `RequireAuth` + `RequireRole(RoleAdmin)` + `RequireCSRF`. Theme values are user input that flows into a `<style>` block on rendered pages -- the validation in requirements 7-14 is the only defence against CSS injection. The validation is strict (whitelist character sets and known formats), not blacklist-based. The default theme is seeded by code, not from any user input, so the seed cannot inject malicious CSS.
- **Reliability**: The "exactly one default theme" invariant is enforced both at the application layer (transaction in `SetDefault`) and the database layer (partial unique index). Seeding is idempotent so service restart never duplicates the default. If validation rejects a theme update, the existing theme row is unchanged.
- **Testability**: Theme validation, CSS rendering, and the default-seeding step are pure-Go and tested without HTTP. Handler tests mirror the existing `views/devices_test.go` pattern using `httptest.NewRecorder` and `db.OpenTestDB(t)`.
- **Backwards compatibility**: This spec adds new tables, new admin routes, and one config setting. No existing tables, routes, or config defaults change. The static stylesheet is unaltered.

## Acceptance Criteria

### Theme Records

- [ ] AC-1: When the service starts on a database with no `themes` rows, then exactly one row is inserted with `is_default = 1` and the seeded color values match the constants in `static/css/app.css`.
- [ ] AC-2: When the service starts on a database that already has a theme named `default`, then no new row is inserted and no existing row is mutated (idempotent seed).
- [ ] AC-3: When an admin POSTs `/admin/themes` with a name containing only whitespace, then the request is rejected with a user-visible error and no row is created.
- [ ] AC-4: When an admin POSTs `/admin/themes` with a name containing characters outside `[A-Za-z0-9 _-]` (e.g., `theme<script>`), then the request is rejected with a user-visible error and no row is created.
- [ ] AC-5: When an admin POSTs `/admin/themes` with a name longer than 64 characters, then the request is rejected with a user-visible error and no row is created.
- [ ] AC-6: When an admin POSTs `/admin/themes` with a name that already exists, then the request is rejected with a user-visible error and the original theme is unchanged.

### Color and Font Validation

- [ ] AC-7: When a theme is submitted with `color_bg=#0b0d11`, then the row is created and the stored value is `#0b0d11`.
- [ ] AC-8: When a theme is submitted with `color_bg=#FFF`, then the row is created and the stored value is normalised to `#fff`.
- [ ] AC-9: When a theme is submitted with `color_bg=red` (a CSS named color), then the request is rejected with a user-visible error and no row is created.
- [ ] AC-10: When a theme is submitted with `color_bg=rgb(11,13,17)`, then the request is rejected with a user-visible error and no row is created.
- [ ] AC-11: When a theme is submitted with `color_bg=#zzzzzz`, then the request is rejected with a user-visible error.
- [ ] AC-12: When a theme is submitted with `font_family` containing `;` (e.g., `Arial;}<script>`), then the request is rejected with a user-visible error and no row is created.
- [ ] AC-13: When a theme is submitted with `font_family` longer than 256 characters, then the request is rejected with a user-visible error.
- [ ] AC-14: When a theme is submitted with `radius=10px`, then the row is created with `radius` stored as `10px`.
- [ ] AC-15: When a theme is submitted with `radius=10` (no unit), then the request is rejected with a user-visible error.

### Default Theme

- [ ] AC-16: When an admin POSTs `/admin/themes/{id}/set-default` for a non-default theme, then that theme's `is_default` is `1` and the previously-default theme's `is_default` is `0`.
- [ ] AC-17: When AC-16 runs and the SQL transaction encounters an error mid-way (simulated), then both rows retain their pre-transaction `is_default` values (atomic update, no half-applied state).
- [ ] AC-18: When an admin POSTs `/admin/themes/{id}/delete` for the theme whose `is_default` is `1`, then the request is rejected with a user-visible error and the row is not deleted.
- [ ] AC-19: When an admin POSTs `/admin/themes/{id}/delete` for a non-default theme, then the row is deleted and the previously-default theme is still the default.
- [ ] AC-20: When the database is inspected after AC-16, then exactly one row has `is_default = 1` (verified by `SELECT COUNT(*) FROM themes WHERE is_default = 1`).

### CRUD API

- [ ] AC-21: When a member (non-admin) GETs `/admin/themes`, then the response is 403.
- [ ] AC-22: When an admin GETs `/admin/themes`, then the page lists every theme with a clear marker on the row whose `is_default = 1`.
- [ ] AC-23: When an admin POSTs a valid create form, then a 302 redirect to `/admin/themes?msg=...` follows and the next GET shows the new theme in the list.
- [ ] AC-24: When an admin GETs `/admin/themes/{id}/edit` for an existing theme, then the form is pre-populated with the theme's current name, color values, font, and radius.
- [ ] AC-25: When an admin POSTs `/admin/themes/{id}` with valid edits, then the row's `updated_at` advances, the modified columns reflect the new values, and a 302 redirect to the list follows.
- [ ] AC-26: When an admin POSTs `/admin/themes/{id}` for a theme ID that does not exist, then the response is 404 (or a 302 to `/admin/themes?error=...`).
- [ ] AC-27: When an admin POSTs `/admin/themes/{id}/delete` without a valid `_csrf` field, then the existing CSRF middleware rejects with 403 and the row is not deleted.

### CSS Variable Rendering

- [ ] AC-28: When `Theme.CSSVariables()` is called on a theme, then the returned string contains a `:root { ... }` block with `--bg:`, `--surface:`, `--border:`, `--text:`, `--text-muted:`, `--accent:`, `--radius:`, and `--font-family:` declarations.
- [ ] AC-29: When `Theme.CSSVariables()` is called twice on the same theme value, then both calls return byte-identical strings (deterministic output).
- [ ] AC-30: When `Theme.CSSVariables()` is called on a theme whose `color_accent` is `#7b93ff`, then the returned string contains the literal substring `--accent: #7b93ff;` (case-insensitive on the property name, exact on the value).
- [ ] AC-31: When `Theme.CSSVariables()` is called, then the returned string MUST NOT contain `<`, `>`, or `</style>`. (This is a sanity check; the field validation is what actually prevents these characters from reaching this point.)

### Configuration

- [ ] AC-32: When `THEME_DEFAULT_NAME` is not set, then the seeded default theme's name is `default`.
- [ ] AC-33: When `THEME_DEFAULT_NAME=onyx` is set on a fresh database, then the seeded theme is named `onyx` and is the system default.

## Out of Scope

- The `screens` table (lives in SPEC: Screen Model). This spec does not create the `screens.theme_id` foreign key; Screen Model adds it.
- Per-screen theme selection UI (lives in Screen Display / Widget Selection UI).
- A live "preview" of a theme as the admin edits it (lives in the Theme Preview spec, p1, later in Phase 2).
- Per-role fonts (title, heading, body, time, money, alert). v1 carries a single `font_family` plus an optional `font_family_mono`. The Typography Roles spec (Phase 2, p1) extends the schema with role-specific font fields once widgets exist to pick a role.
- Page background images (URL-referenced). v1 backgrounds are solid colors only -- the Page Backgrounds spec (Phase 2, p1) adds a per-page background-image field on the Page entity once Screen Model exists.
- Card surface controls beyond `radius`: transparency/opacity, border color, border thickness, and a "no border" option. The Card Theming spec (Phase 2, p1) adds these fields to the theme schema.
- Light / dark / auto mode switching driven by `prefers-color-scheme`. Themes are explicit picks; an admin who wants both makes two themes.
- Importing / exporting themes as JSON (deferred to Phase 5 Config Export/Import).
- A theme marketplace, gallery, or community sharing.
- Per-widget theming. A widget inherits the screen's theme; it does not pick its own colors. (If this becomes desired later, a separate spec adds widget-level overrides.)
- Image / pattern backgrounds. v1 backgrounds are solid colors only. (See the Page Backgrounds line above for the deferred follow-up.)
- Animated theme transitions.
- Spacing scale beyond `radius` in v1 (see requirement 15). A future spec may add a multiplier when widgets actually consume one.
- A separate "theme version" or "revision" concept. Editing a theme mutates the row in place; downstream screens pick up the change on the next render.
- An admin UI for the optional `font_family_mono` field. The default theme seeds it; later specs may surface a control for it.
- Validation of whether a `font_family` value resolves to an actually installed font on the device. The browser handles the fallback; we just store the string.

## Dependencies

- Depends on: SPEC-001 (Storage Engine) -- needs the migration runner, sqlc setup, and `db.OpenTestDB` test helper.
- Depends on: SPEC-002 (Admin Auth) -- needs `RequireAuth`, `RequireRole(RoleAdmin)`, `RequireCSRF`, the existing admin layout and CSRF token-on-form pattern, and `auth.GenerateToken` (reused for theme IDs).
- Depends on: SPEC-003 (Device Auth) -- needs the unified `RequireAuth` because the admin sub-mux is wrapped in it; also reuses the route registration and `Deps` pattern in `views/routes.go`.
- No external dependencies (no new third-party Go modules required).

## Open Questions

All resolved.

- Q1 **Resolved**: Themes live in the database, not in static files. An admin needs to be able to add and tune themes through the management UI without a redeploy. See ADR-004.
- Q2 **Resolved**: CSS variables are injected via a server-rendered `<style>` block in the page `<head>`, not via a dynamic CSS endpoint or inline `style` attribute on `<body>`. The `<style>` block ships in the same response as the HTML, so there is no flash-of-unstyled-content. See ADR-004.
- Q3 **Resolved**: Color values are stored as hex strings (`#rrggbb` or `#rgb`), not as structured RGB triples or named CSS colors. Hex is the lowest-friction format for both human admins (most palette pickers output hex) and machines (deterministic equality and easy validation). See ADR-004.
- Q4 **Resolved**: Exactly one theme is the system default at any time. The "exactly one" invariant is enforced both in code (transactional `SetDefault`) and in the database (partial unique index on `is_default = 1`). The system seeds a default theme named `default` (configurable via `THEME_DEFAULT_NAME`) on first startup so a freshly installed instance is never in a state with zero themes.
- Q5 **Resolved**: Editing a theme mutates the existing row. There is no version history. If an admin wants to keep an old palette around, they duplicate the theme via the admin UI before editing the new copy. This is consistent with how `users` and `devices` work and avoids carrying revision-history machinery for a low-volume entity.
- Q6 **Resolved**: This spec lays the foundation only. The downstream consumers (Screen Model, Screen Display, Widget Selection UI, Theme Preview) each depend on this spec but are out of its scope. The CSS rendering helper is exported with a stable signature so Screen Display can wire it in without changes here.
