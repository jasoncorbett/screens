---
id: ADR-006
title: "Screen / Page / WidgetInstance model: 1-D layout, RESTRICT theme FK, CASCADE child deletes"
status: accepted
date: 2026-05-13
---

# ADR-006: Screen / Page / WidgetInstance model: 1-D layout, RESTRICT theme FK, CASCADE child deletes

## Context

Phase 2 introduces the dashboard data model. SPEC-006 (Screen Model) is the roadmap entry: "Screen entity (name, pages, theme ref), page entity (widget layout), CRUD API". Three independent design questions shake out:

1. **What is the layout shape of a Page?** Possible answers:
   - A 1-D ordered list (`position INTEGER`): widgets stack in position order.
   - A 2-D grid (`row INTEGER, col INTEGER, row_span INTEGER, col_span INTEGER`): widgets place into named cells.
   - Free-form CSS positioning (`x, y, width, height` in pixels or percentages): widgets float anywhere.
   - A named-slot model (`slot TEXT`): a Page has a fixed layout template ("hero + 3 cards"), each slot is a named drop zone.

2. **How does the theme FK behave when an admin tries to delete a theme that a Screen references?** Possible answers:
   - `ON DELETE CASCADE`: the theme delete also nukes every Screen using it.
   - `ON DELETE SET NULL`: the Screen's `theme_id` becomes NULL; Screen Display falls back to the default theme.
   - `ON DELETE RESTRICT`: the delete is rejected with a clear error; the admin must reassign Screens first.
   - No FK at all (`theme_id TEXT NOT NULL`, but no `REFERENCES`): the database silently allows orphaned references.

3. **What happens to a Screen's children (pages) and a page's children (widget instances) when the parent is deleted?** Possible answers:
   - `ON DELETE CASCADE`: deleting the parent atomically deletes the children at the DB layer.
   - Application-layer cascade: the service explicitly issues DELETE statements for children before the parent.
   - `ON DELETE RESTRICT`: the parent cannot be deleted while children exist; the admin must delete children first.
   - No FK / no cleanup: the database accumulates orphans.

The downstream consumers (Screen Display for rendering, Widget Selection UI for the picker, Phase 3 widgets for instances) all depend on stable answers. Performance is not the bottleneck (household-scale dashboards have tens of widgets); the bottleneck is admin UX and developer cognitive load.

The threat model is the same as the rest of the admin UI: authenticated admins can store layout data that flows through the renderer onto every device. The validation already established (whitelist regex for names, widget validators for per-instance config, FK constraints for IDs) is what keeps it safe. The decisions here do not introduce new attack surfaces; they choose among several safe options.

## Decision

### Layout: 1-D ordered list (position-indexed) in v1

We model the page layout as an ordered list. A page row carries no layout metadata of its own. Each widget instance has a `position INTEGER NOT NULL` column with `UNIQUE (page_id, position)`. The renderer iterates instances in position order and stacks them inside a single CSS-grid container styled by the theme.

Rationale:
- Every widget Phase 3 ships (time, weather, calendar, slideshow, charts, Home Assistant, financial tickers) renders as a single card. A vertical stack of cards is a sensible default rendering for every one of them.
- The lowest-friction admin UX. "Page 1 is a clock; Page 2 is weather; Page 3 is the family calendar" reads naturally and matches how a household actually wants to use a kitchen tablet. Two-dimensional grids force the admin to think about cell sizes when most kitchens want "show me one thing big".
- The schema is the smallest possible (one integer column on widget instances). Future grid migration is additive: add `row`, `col`, `row_span`, `col_span` columns with sensible defaults (`row = position, col = 1, row_span = 1, col_span = 12`); the existing data stays meaningful (it becomes "one widget per row, full-width", which is the existing rendering).
- CSS grid lets the renderer evolve from "single column" to "multi-column at wider viewports" via media queries without any schema change.

Rejected alternatives:
- **2-D grid with `row, col, row_span, col_span`**: forces every widget instance to specify a cell, which is more cognitive load than v1 needs. Most v1 layouts would just be "row = position, col = 1" anyway. Premature.
- **Named slots (`slot TEXT`)**: requires a separate "Page layout template" entity (the slot definitions). That is a meaningful new concept and is not justified by any v1 use case. Adds a second design problem on top of the one we are solving.
- **Free-form CSS positioning (`x, y, width, height`)**: gives admins a level of control that produces fragile layouts on the variable-pixel-density tablets in question. A clock at `x=120, y=80` on one tablet is a clock at `x=160, y=110` on another with a different DPI. Avoid.
- **No layout at all (page has a single widget)**: doesn't satisfy the spec's "page entity (widget layout)" requirement and gives admins no way to combine widgets on one page.

Chosen because it is the smallest design that solves the v1 problem AND has a clean extension path. Multi-column layouts are not impossible later; they are just deferred to when a real use case demands them.

### Theme FK: ON DELETE RESTRICT, surfaced as `themes.ErrThemeInUse`

The `screens.theme_id` column is `REFERENCES themes(id) ON DELETE RESTRICT`. The Theme System's `Service.Delete` method gains a typed return (`themes.ErrThemeInUse`) when the SQLite FK violation surfaces from the underlying DELETE statement. The Theme admin UI translates that error into `?error=Cannot+delete+a+theme+in+use+by+a+screen` and renders it on the redirect target.

Rationale:
- RESTRICT is fail-fast. The admin sees a clear error message ("a Screen is using this theme"), reassigns the Screens, then deletes the theme. The system's state is always coherent.
- CASCADE would let one wrong click delete every Screen using the theme. That is catastrophic for an admin UX in a household-scale dashboard where Screens are configured by hand over weeks.
- SET NULL would leave the Screen with a NULL `theme_id`. The Theme System's default-theme seed and `GetDefault` machinery would have to be threaded into every render path as a fallback. Theme System's contract is "every Screen has a theme"; SET NULL contradicts that.
- No FK at all is a footgun: a delete of a referenced theme leaves Screens pointing at non-existent rows, and the bug surfaces only at render time on a device, not at delete time in the admin UI.

The application-layer cost is tiny: `themes.Service.Delete` already inspects the row before issuing the DELETE; it now also catches the FK-violation error from the modernc.org/sqlite driver (the same string-match pattern already used for UNIQUE-constraint detection on theme name) and converts it to `ErrThemeInUse`. One new error variable, one new error path, one new test.

Rejected alternative: rely solely on the application layer to check `SELECT 1 FROM screens WHERE theme_id = ? LIMIT 1` before issuing the DELETE. This works but is racy (a concurrent INSERT into `screens` between the check and the DELETE would defeat it) and adds a query that the FK constraint already does for free. RESTRICT plus error translation is the cleaner shape.

### Children: ON DELETE CASCADE for pages and widget instances

The `pages.screen_id` column is `REFERENCES screens(id) ON DELETE CASCADE`. The `widget_instances.page_id` column is `REFERENCES pages(id) ON DELETE CASCADE`. Deleting a Screen atomically deletes its Pages, which atomically deletes their widget instances.

Rationale:
- Atomicity at the DB layer. No half-deleted state is reachable, even on a crash mid-delete.
- Zero application code. The service's `DeleteScreen` is just `DELETE FROM screens WHERE id = ?`; the cascade is enforced by SQLite.
- Matches admin intent. An admin who clicks "delete this Screen" wants the Screen and everything inside it gone. RESTRICT would force them to delete every widget and page first, which is a worse UX with no upside.
- Page widgets are not shared across pages (the widget interface ships per-instance config; an instance belongs to exactly one page). Cascading is semantically correct.

Rejected alternatives:
- **Application-layer cascade**: more code, more places to forget a child entity, and requires the service to know about every descendant table. Adding a new child table later (Page Backgrounds spec adds an image-asset reference per page) would mean updating the application cascade code; the DB-layer cascade is automatic.
- **RESTRICT on children**: an admin has to manually delete every widget instance, then every page, then the Screen. Tedious; no value.

The CASCADE behaviour requires `PRAGMA foreign_keys = ON` on the connection (SQLite's default is OFF). The existing `db.Open` already sets this; this ADR confirms the requirement and the existing setup satisfies it.

### Widget instance config: JSON blob validated through `widget.Default().Validate`

We continue the design established by SPEC-005 / ADR-005: per-instance config is a single TEXT column holding JSON bytes. The service round-trips the bytes through `widget.Default().Validate(type, raw)` before persisting. This is not new; this ADR just records that Screen Model adopts the contract.

The reason this is here at all: Screen Model is the *first* persistent consumer of the widget registry. SPEC-005 committed to the validator contract; this spec proves it in production by writing rows that round-trip.

### Service depends on `themes.Service` AND `widget.Registry`

`screens.NewService` takes `*sql.DB`, `*themes.Service`, and `*widget.Registry`. The first is the storage handle. The second lets the service validate theme references via `themes.Service.GetByID` before INSERT (returning a friendly typed error if the theme is missing) and serves as a logical dependency declaration. The third is the registry the service uses to validate widget types and look up default configs.

Rationale:
- Explicit dependencies make the wiring readable in `main.go` and the tests obvious. A test constructs its own `widget.NewRegistry`, registers exactly the widgets the test needs, and hands the registry to the service.
- Threading `*themes.Service` (rather than redoing theme lookups via raw SQL inside `screens.Service`) keeps the validation logic in one place. The Theme System owns "what does a valid theme look like"; Screen Model owns "does this Screen reference a real theme".

Rejected alternative: inject the `widget.Default()` singleton implicitly inside the service. Cleaner-looking but harder to test (every test would have to register against the global). The constructor injection costs one extra argument; the readability and testability win is large.

### Reorder: explicit "move up / move down" operations, transactional swap

Reordering a Page or a widget instance happens via two service methods (`MovePageUp/Down`, `MoveWidgetUp/Down`) that:
1. Look up the target row's position.
2. Look up the row at `position - 1` (or `+ 1`) within the same parent.
3. Swap the two positions inside a single transaction.

Top-row-move-up and bottom-row-move-down are no-ops (the lookup at step 2 returns no rows).

Rationale:
- Two transactional UPDATEs is simpler than reshuffling N rows. With a unique index on `(parent_id, position)`, a naive swap would briefly violate the constraint between the two UPDATEs; running both inside a transaction with `BEGIN IMMEDIATE` (the default in modernc.org/sqlite when a write is the first statement) is sufficient. SQLite checks unique constraints at COMMIT time only if the constraint is `DEFERRABLE INITIALLY DEFERRED`, which SQLite does not support; for immediate constraints SQLite checks per-statement. To avoid that, the transaction does the swap via a temporary "negative position" trick: `UPDATE row A SET position = -position; UPDATE row B SET position = (A's old position); UPDATE row A SET position = (B's old position); COMMIT`. This is the same trick used in many SQL schemas to swap unique values.
- Drag-and-drop is a different UI primitive that can be added later on top of the same `position` column without any schema change. v1 ships the smallest UX that works.

Rejected alternatives:
- **Fractional positions (`position REAL`)**: lets you insert anywhere without renumbering. Adds floating-point comparison concerns and is overkill for a few dozen items per page.
- **Linked-list (`previous_id` / `next_id`)**: works in theory; in practice requires graph traversal for ordered iteration and is harder to read in a SQL inspector.
- **Renumber-all-rows-on-every-move**: a few extra UPDATEs per reorder. Acceptable but uglier than the swap. The swap is one transaction; the renumber is N transactions or N rows mutated.

### Pre-shipped `GetScreenFull` for Screen Display

`screens.Service.GetScreenFull(ctx, id)` returns the Screen + theme + ordered pages + each page's ordered widget instances, in a single domain struct, using a constant number of SQL round-trips (one for the Screen + theme join, one for the pages, one for the widget instances filtered by page IDs).

Rationale:
- Screen Display is the next spec. Pre-shipping the call here means Screen Display's task list is "render the struct" rather than "design and build the query, then render". The diff stays additive.
- A single domain struct with everything pre-fetched eliminates N+1 query risk in the renderer hot path. Devices rotate pages every few seconds; the database is hit per rotation; doing it in one batch of three queries is plenty.
- Tests can construct a Screen + pages + widget instances via the service's public CRUD methods and assert `GetScreenFull` returns them in the right shape. No private fixture code needed.

Rejected alternatives:
- **Defer GetScreenFull to Screen Display**: works but bloats Screen Display's diff and risks discovering an N+1 problem at the last minute. Pre-shipping is cheap and consolidates the query work.
- **Stream pages and widgets via separate calls in the renderer**: forces the renderer to manage three calls and handle partial failures. The aggregated call hides that complexity behind one error.

## Consequences

**Accepted trade-offs:**

- A new top-level package (`internal/screens/`) is added. We accept this as the right home for the dashboard model -- it is not auth, not themes, not widgets, and is large enough (Screen + Page + WidgetInstance + service + queries) to warrant its own boundary. The "themes" and "widget" packages set the precedent; "screens" follows it.
- The widget-instance config column is a JSON blob, not typed columns. We inherit the trade-offs from ADR-005: schema-stable across widget types, hand-rolled per-widget validators, NOT directly SQL-queryable for config fields. This is the right trade for a household-scale dashboard.
- The 1-D layout means v1 cannot do multi-column dashboards on a single page. We accept this for v1; an admin who wants two side-by-side widgets makes two pages today and waits for the grid-layout spec for true side-by-side.
- The "move up / move down" UX is functional but not glamorous. Drag-and-drop ships later if the household demands it.
- The RESTRICT theme FK means an admin who wants to delete a theme has to first reassign every Screen using it. That is one extra step but a clearly-explained one (the error message says exactly what to do).
- The CASCADE behaviour on Screen / Page deletes is silent in the admin UI (no "are you sure?" confirmation in v1). We accept this; the Theme System's existing delete action has the same property, and a confirmation modal can be added cross-cutting later as an admin polish item.
- The page-name field is non-unique and may be empty. Two pages labeled "clock" on the same Screen is confusing but legal. We accept the looseness because pages are addressed by ID, not by name, and stricter validation would block admins who legitimately want to rename pages incrementally.
- The reorder swap uses the temporary-negative-position trick. It is a known SQL idiom but it does mean a brief window of an "invalid" position value exists inside the transaction. Outside the transaction nothing observes it. We accept it because the alternative (renumber-all) is N times more writes.
- `screens.Service` takes both `*themes.Service` and `*widget.Registry` as explicit dependencies. Constructor signature is wider than the existing services. We accept this for readability and testability.

**Benefits:**

- The data model is small. Three new tables, all with the same shape (id, parent, name, position, timestamps). New contributors recognise the pattern instantly.
- The 1-D layout is the smallest design that solves the v1 problem and has a clean migration path to 2-D grids.
- RESTRICT on the theme FK turns a class of dangerous bugs (deleted-theme orphans) into a clear admin-UI error.
- CASCADE on the child FKs turns a class of tedious admin work (manually delete every widget before deleting a page) into a single click.
- The widget-instance config column reuses the SPEC-005 contract; Screen Model is the first persistent consumer of the registry and validates the contract end-to-end.
- `GetScreenFull` is the single Screen-Display-consumable surface. Pre-shipping it here keeps the next spec's diff additive.
- The service-layer dependency on `themes.Service` keeps the validation logic in one place: Theme System owns "what is a valid theme", Screen Model asks "does this Screen reference a real theme".
- Forward compatibility for Page Backgrounds (additive column on `pages`), Typography Roles (additive field on widget Registration), and Widget Selection UI (CRUD over existing widget_instances table). No schema changes will be needed for those.

**Risks accepted:**

- The 1-D layout means future grid demands will require a migration plus a new admin UI. We accept this because the alternative is over-designing v1.
- The reorder swap depends on `PRAGMA foreign_keys = ON` and on transactions being honoured by the SQLite driver. The existing `db.Open` configures both; this ADR confirms the assumption.
- The widget-config column accepts arbitrary JSON shapes (with per-widget validators) -- a future widget could store a config that, after some refactor, no longer matches its validator. We accept this and rely on tests (each widget has tests for its DefaultConfig-validates property, and Screen Display will re-validate at render time).
- A bad migration could orphan rows (e.g., a forgotten `ON DELETE CASCADE` clause). The migration is reviewed via the standard process and tested against `db.OpenTestDB`.
- The admin UI's lack of "are you sure?" confirmation means a misclick deletes a Screen. We accept this for v1 (matches the existing Theme delete behaviour) and treat confirmation modals as a cross-cutting admin polish item.
- The page-name uniqueness looseness means an admin could create two pages named "clock" on one Screen. Confusing but legal. A future spec can add a uniqueness constraint or a UI warning if this becomes a real problem.
