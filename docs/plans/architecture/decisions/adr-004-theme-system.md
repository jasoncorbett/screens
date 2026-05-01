---
id: ADR-004
title: "Database-stored themes with server-rendered CSS variable injection"
status: accepted
date: 2026-04-30
---

# ADR-004: Database-stored themes with server-rendered CSS variable injection

## Context

Phase 2 introduces themes -- named bundles of color, font, and spacing values that drive the look of a screen. The roadmap entry is "Theme data model (colors, fonts, spacing), CRUD API, CSS variable injection". Three independent design questions shake out:

1. **Where do themes live?** Possible answers:
   - Static configuration files (TOML / JSON / YAML) shipped with the binary or mounted as a volume.
   - Environment variables (`THEME_KITCHEN_BG=...`).
   - The same SQLite database as users / sessions / devices.
2. **How does a theme's value reach the browser?** Possible answers:
   - A server-rendered `<style>` block in the page `<head>`.
   - A dynamic CSS endpoint (`GET /themes/{id}.css`) referenced by `<link rel="stylesheet">`.
   - Inline `style` attributes on every element.
   - A class on `<body>` plus a stylesheet that ships every theme as its own scoped rule set.
3. **How are color values stored and validated?** Possible answers:
   - As raw `string` columns with no validation (let CSS sort it out).
   - As structured types (R/G/B integers, optional alpha) with a custom serialization.
   - As hex strings (`#rrggbb`, `#rgb`) with strict validation.
   - As any valid CSS color (named, `rgb()`, `hsl()`, `oklch()` ...) with a permissive parser.

The downstream consumers (Screen Model, Screen Display) need a stable answer to all three before they can be designed. Screen Model holds a `theme_id` foreign key; Screen Display has to render a theme's values into a real device's HTML response on the hot path. Both depend on the choices made here.

The threat model is the same as for the rest of the admin UI: an authenticated admin can store theme values that flow back out through HTTP responses. CSS injection is a real risk class -- a malicious value containing `</style><script>...` would break out of the embedded `<style>` block. The validation policy chosen here is the only defence; HTML escaping does not apply inside `<style>`.

## Decision

### Themes live in the database

We store themes as rows in a new `themes` table in the existing SQLite database, not in static files or environment variables.

Rationale:
- Admins already manage other long-lived configuration (users, devices, invitations) through the admin UI. Themes are the same shape: low-volume, mutable, audited via `created_at` / `updated_at`. Putting them anywhere else creates two parallel "configuration" surfaces.
- A static-file approach forces a redeploy (or a volume remount and restart) for every color tweak. The household admin tweaks themes from the same browser they use to manage devices; the deploy cycle is the wrong granularity.
- The existing migration runner, sqlc setup, and test helpers already give us everything we need to add a table and CRUD it. There is essentially no incremental cost.
- Environment variables do not scale to multiple themes (one set per theme is unwieldy) and require a process restart to pick up changes.

### CSS variables are injected via a server-rendered `<style>` block in `<head>`

For each rendered screen page, the server includes the theme's values as a `<style>` block at the top of the page setting the same CSS custom properties (`--bg`, `--text`, `--accent`, etc.) that `static/css/app.css` already references.

Rejected alternatives:
- **Dynamic CSS endpoint**: would add a second round-trip on every page load and a flash of unstyled content while the stylesheet loads. A device kiosk reloads on every page rotation; an extra request per rotation is gratuitous.
- **Inline `style` attribute on every element**: requires every templ component to know about every theme variable, defeats the existing CSS-variable architecture, and bloats the HTML.
- **`<body class="theme-kitchen">` + a static stylesheet that ships every theme**: works but pre-bakes every theme into the binary and requires a recompile to add or change one. Loses the "edit through the admin UI" property.

The chosen approach is also the simplest implementation: a single helper function `func (t Theme) CSSVariables() string` returns the `:root { --foo: bar; ... }` block, and every page template embeds it inside a `<style>...</style>` element rendered into `<head>`. Phase 2 Screen Display is the consumer; this spec ships only the helper.

### Color values are stored as hex strings, validated strictly

Colors are stored as strings in the database, normalised to lower case, and validated against a strict regex matching `#rgb` or `#rrggbb` (case-insensitive on input). No other CSS color forms are accepted in v1.

Rejected alternatives:
- **Permissive parsing of any CSS color form**: a single permissive parser that accepts `red`, `rgb(255 0 0 / 0.5)`, `oklch(60% 0.15 200)`, etc. is real work and a real attack surface. CSS color syntax has grown over the years; a permissive parser invites validation gaps.
- **Structured RGB triples**: clean for storage but adds friction in the admin UI (three inputs per color). Most palette pickers (designers, browser dev tools, Tailwind / shadcn presets) emit hex; meeting admins where they are wins.
- **Raw strings with no validation**: opens a CSS-injection hole. Given the values are rendered into a `<style>` block on every device response, the cost of a bad string is potentially every screen rendering broken (or worse, a malicious admin storing `</style>` and breaking out into the page DOM).

Strict hex was chosen because:
- It is the lowest-friction format for human admins (most palette pickers output hex).
- Validation is a one-line regex.
- Equality comparisons are predictable after lower-case normalisation.
- The character set is restricted to `[0-9a-f#]`, which is provably safe to embed in a `<style>` block.
- Adding richer color forms later is additive (add new fields, keep the existing hex columns), so we are not painting into a corner.

### Font and radius validation is whitelist-based, not blacklist-based

`font_family` is validated by:
- Length cap (256 chars).
- Rejection of `;`, `{`, `}`, `<`, `>`, backslash, and any control / newline characters.

`radius` is validated by a regex matching `0` or one of `<positive-int>px` / `<non-negative-decimal>(rem|em)`.

Rejected alternative: a single permissive "is this valid CSS?" check (e.g., parsing the whole `<style>` block in a CSS parser). The dependency cost is too high for one entity, and the failure mode of a permissive parser ("yes, technically valid, but contains `</style>`") is exactly the failure mode we are trying to prevent.

The whitelist approach trades expressiveness (no fancy `clamp()` for radius, no exotic font features) for safety. A future spec can add new fields with their own typed validators when there is a concrete use case.

### Exactly one default theme, enforced at two layers

The `themes` table has an `is_default INTEGER NOT NULL DEFAULT 0` column with a partial unique index `CREATE UNIQUE INDEX themes_one_default ON themes(is_default) WHERE is_default = 1;`. The application-layer `SetDefault` method runs both the "unset old default" and "set new default" `UPDATE`s inside a single transaction.

Rejected alternative: a separate `system_settings` table with a `default_theme_id` row. This works but introduces a second table for one column and forces every theme read in Screen Display to JOIN. The per-row boolean is the same shape as the existing `users.active` column and reads identically.

### A default theme is always seeded on first start

On every startup, an idempotent seed step inserts a theme named `default` (or `THEME_DEFAULT_NAME` if set) with `is_default = 1` if and only if no row in the `themes` table currently has `is_default = 1`. The seeded color values match the constants currently in `static/css/app.css`.

This guarantees the database is never in a state where Screen Display has no theme to render with, even on a fresh install before any admin has visited the management UI. The check is "no row has `is_default = 1`" rather than "no row exists at all" because the partial unique index already guarantees the two are equivalent (zero or one default row), but the latter is more robust if a future migration ever transiently empties `is_default`.

### Reuse, don't reinvent

- Theme IDs use the same primitive as user / device / session IDs: `auth.GenerateToken[:32]` (16 bytes of entropy, hex-encoded).
- Timestamps are TEXT-as-ISO8601 like every other table.
- sqlc queries follow the same `:exec` / `:one` / `:many` annotation style as `devices.sql` and `users.sql`.
- Admin views mirror `views/devices.go` / `views/devices.templ`: a single page templ, handler factories taking `*auth.Service`, route registration inside the `RequireRole(RoleAdmin)` sub-mux.
- Validation errors are surfaced inline on the form (mirror the user-management form's existing error rendering), not via the `?error=...` query-param flash. The flash pattern is fine for "device revoked" but unhelpful for "your `color_bg` value is invalid: please use a hex code like #1a2b3c".

### Themes service lives in `internal/themes/`, not `internal/auth/`

Themes are not an authentication concern. The `auth` package already grew to handle devices alongside users; bolting themes onto it would cement an "auth.go is the dumping ground" pattern. A new `internal/themes/` package keeps the domain boundary clean.

The themes service follows the same constructor pattern as `auth.NewService`: it takes the `*sql.DB` (or its sqlc `Queries` wrapper) and a small `Config` struct, exposing CRUD methods. Views import `internal/themes` for the `Theme` type and the `Service`; main.go wires it into the existing `views.Deps` struct alongside `Auth`.

## Consequences

**Accepted trade-offs:**

- A new top-level package (`internal/themes/`) is added. We accept this as the right home for a domain that is not auth and not storage-engine. Future Phase 2 specs (Widget Interface, Screen Model) will likely add their own packages in the same idiom.
- The `views.Deps` struct grows a `Themes *themes.Service` field. Existing handlers do not have to be changed because they read only the fields they need from `Deps`; the threading is one new line in `main.go`.
- The seed step runs on every startup (it is a single conditional `INSERT`). Cost is one extra `SELECT COUNT(*) FROM themes WHERE is_default = 1` per process boot.
- The strict hex-only color policy means an admin cannot type `red` and have it work. The validation error message is explicit ("hex like #1a2b3c"), and admins can paste from any palette picker. We accept the friction.
- The whitelist `font_family` validator rejects some valid CSS (e.g., a font name containing `<`). No real font name uses these characters; the trade-off is acceptable.
- The CSS-rendering helper is part of the package contract. If the theme model evolves (more fields, structured colors, etc.), the helper signature is what downstream Screen Display depends on. We commit to keeping the function signature stable: `func (t Theme) CSSVariables() string`. New fields are additive; the returned string can grow but the function signature does not.
- The optional `font_family_mono` field is in the schema from day one but is not exposed in the v1 admin UI. The default theme seeds it; later specs can surface a control. We accept the small amount of unused-by-UI-yet column rather than ship a migration later just to add it.
- The "exactly one default" invariant is enforced at the DB layer with a partial unique index. SQLite supports partial indexes; this is not a portability problem (we are SQLite-only by ADR-001).

**Benefits:**

- Themes are first-class admin-managed entities, editable through the UI without a redeploy.
- One round-trip per page load: the theme's values ship in the same response as the HTML, so no flash of unstyled content.
- Strict validation closes the CSS-injection hole that a permissive parser would open. The character sets allowed in fields are provably safe to embed in `<style>`.
- The CSS-rendering helper has a stable, testable contract that Screen Display can wire in with one line.
- The default-theme seed guarantees Screen Display always has something to render, even on a fresh install. There is no "no theme defined" branch to handle in downstream code.
- Reusing `auth.GenerateToken`, the migration runner, sqlc, and the existing admin sub-mux means there is no new infrastructure to audit.
- The two-layer default invariant (transaction + partial unique index) makes accidental "two defaults at once" or "zero defaults" states impossible at the storage layer.
- A future "import / export themes as JSON" spec (Phase 5) can be implemented by serialising / deserialising rows -- no schema or rendering changes needed.

**Risks accepted:**

- Storing themes in the database means a corrupted database file destroys configured themes. This is the same risk the rest of the application accepts for users, sessions, and devices; backups (a file copy) handle it.
- The strict color format means later support for richer color systems (P3, OKLCH, light/dark adaptive) requires schema additions. We accept the future migration cost in exchange for the v1 simplicity.
- A `<style>` block in every page response slightly bloats responses (a few hundred bytes). Acceptable for a household-scale dashboard.
- Validation is server-side only. There is no client-side preview in v1 (Theme Preview is a separate p1 spec). Admins see validation errors after submit, not as they type.
