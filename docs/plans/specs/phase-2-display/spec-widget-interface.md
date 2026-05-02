---
id: SPEC-005
title: "Widget Interface"
phase: 2
status: draft
priority: p0
created: 2026-04-30
author: pm
---

# Widget Interface

## Problem Statement

The screens service is, fundamentally, a service that puts widgets on a wall. Phase 3 of the roadmap lists at least seven concrete widget types -- time/date, weather, calendar, Home Assistant, slideshow, charts, financial tickers -- and the polish-phase work assumes more will follow. Every one of those widgets is independent code: a different data source, a different render shape, a different configuration vocabulary. Without a stable contract that all of them satisfy, the Screen Display rendering pipeline (the next p0 spec in Phase 2) has no way to ask "render this widget" without knowing about every individual widget package, and every Phase 3 widget will end up wired into Screen Display by hand. That is exactly the architecture this project is supposed to avoid.

The Widget Interface spec creates that contract. It defines:

1. A small Go interface (`Widget`) and a paired registration record that every widget type implements -- so widgets are interchangeable from the caller's perspective.
2. A process-wide registry that maps a widget-type-name string (e.g., `"text"`, eventually `"time"`, `"weather"`) to a typed registration entry -- so Screen Display can render an instance with `registry.Get("text").Render(ctx, instanceConfig, theme)` and never care which package implements it.
3. A configuration-schema convention that lets each widget type define the shape of its own per-instance configuration, validate it, and decode it from the JSON blob the database stores -- so adding a new widget type later is purely additive and never requires a database migration.
4. A placeholder `text` widget that ships in this spec to prove the contract end-to-end. The text widget renders a single block of admin-configured text styled by the active theme's CSS variables. It is the smallest possible widget that exercises every part of the contract.

The contract is the dependency point that Screen Model and Screen Display both build on. Screen Model needs to know how to store widget instances (it stores `type` plus a JSON `config` blob, and trusts the registry to validate the blob). Screen Display needs to know how to render a widget instance (it asks the registry for the renderer and hands it the validated config plus the active theme). Get this wrong and every later widget pays the cost. Get this right and every later widget is one Go file plus a templ -- no Screen Display change, no Screen Model change, no admin-UI change beyond the existing widget-picker.

A widget *type* is a code-side concept (a Go struct registered at startup). A widget *instance* is a database row positioned on a specific page with a specific configuration. The instance / placement model is owned by the Screen Model spec. This spec defines only the type-level contract and ships exactly one type (`text`) as a working example.

## User Stories

- As a **Screen Display author (the next Phase 2 spec, this spec's primary downstream consumer)**, I want a single function call that takes a widget type name plus a configuration blob and returns a rendered HTML component, so that the page renderer can iterate over a screen's widget instances without knowing which package implements each one.
- As a **Screen Model author (the next Phase 2 spec, this spec's other primary downstream consumer)**, I want the registry to expose a "validate this config against this widget type" method, so that the database layer can reject malformed widget instances at write time and never store an instance the renderer cannot consume.
- As a **Widget Selection UI author (Phase 2, p1, deferred)**, I want the registry to expose a "list all known widget types with their human-readable display names and descriptions" call, so that the admin "add widget" picker is a one-line iteration over the registry, not a hard-coded switch statement.
- As a **Phase 3 widget author (anyone implementing time/weather/calendar/etc.)**, I want a small, well-documented `Widget` interface plus a registration helper, so that adding a new widget type means writing one Go file (plus a templ) and never touching Screen Display, Screen Model, the admin UI, or the database schema.
- As a **Typography Roles author (Phase 2, p1, deferred)**, I want the widget interface to be designed so that a future widget can declare which font role (title / body / time / etc.) it wants from the theme, so that role-aware fonts can be added without breaking existing widget implementations.
- As an **admin (eventually, once the picker UI lands)**, I want each widget type to advertise a stable type identifier, a display name, and a description, so that the picker shows "Time / Display the current time in a configurable format" and not "TIME_WIDGET_v1_internal_use_only".
- As a **placeholder-text widget user (the contract validator)**, I want to configure a single string and see it rendered on the screen with the active theme's colors and fonts, so that I have proof the entire pipeline (registration -> config decode -> render -> theme injection) is wired correctly.

## Functional Requirements

### Widget Type Contract

1. The system MUST define an exported `Widget` interface in a new package (`internal/widget`) carrying one method:
   ```go
   Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component
   ```
   Returning a `templ.Component` is mandatory (see requirement 5 for the rationale).
2. The system MUST define an exported `Registration` struct that bundles, at minimum:
   - A stable `Type` string (the widget-type identifier; lowercase ASCII; used as the JSON discriminator and database column value).
   - A human-readable `DisplayName` (e.g., `"Text"`, `"Time"`).
   - A short `Description` (one sentence, used by the future picker UI).
   - A `New` factory function that returns a `Widget` -- a fresh implementation instance, used by the registry's `Get` accessor (see requirement 9). For widget types that have no per-process state, `New` may always return the same singleton.
   - A `DefaultConfig` function returning a JSON-encoded byte slice (`[]byte`) representing the widget type's default per-instance config. Used by the picker to pre-populate a new instance form. (Returning JSON bytes rather than a typed struct keeps `Registration` non-generic; see ADR-005.)
   - A `ValidateConfig` function that takes the raw per-instance JSON blob (`[]byte`) and returns either an `Instance` (containing the decoded, validated config) or an error. This is the single chokepoint that Screen Model calls before persisting.
3. The exact field set and method shapes are defined in the architecture document. The spec commits to: a stable `Type` string, a render method returning `templ.Component`, and a validate-config function that operates on raw JSON bytes. Other fields MAY be added in later revisions (e.g., a font-role declaration -- see requirement 22).
4. The system MUST define an exported `Instance` struct carrying, at minimum, an opaque per-widget-type configuration value (an `any` or a typed payload -- see ADR-005), the widget type identifier, and a per-widget-instance `ID`. The `ID` is set by Screen Model when the instance is created; this spec does not own the ID-generation code. The `Instance` type lives alongside the `Widget` interface in `internal/widget`.
5. The `Render` method MUST return a `templ.Component` rather than a raw HTML string. This composes with the rest of `views/`, lets the widget use `@theme`-style sub-component invocations naturally, and inherits templ's HTML-escape behaviour for any string the widget renders.

### Per-Widget-Type Configuration Schema

6. The system MUST store each widget instance's configuration as a single opaque JSON blob (`[]byte` at the storage layer) rather than as per-widget-type subtables. See ADR-005 for the rationale; the short version is: per-widget-type subtables would force a database migration for every new widget type, which makes Phase 3 widgets sequentially blocking on DB work. JSON-blob plus a per-type Go validator is the design that keeps Phase 3 parallel.
7. Each widget type MUST own a Go validator function (`Registration.ValidateConfig`) that takes the raw JSON bytes and returns either a typed, validated `Instance` or an error. The validator is the single source of truth: malformed configurations are rejected here, not at render time. Validators MUST NOT panic; they MUST return errors.
8. Each widget type MUST own a default-config provider (`Registration.DefaultConfig`) that returns a JSON byte slice representing a sensible starting configuration. The picker UI uses this to pre-populate a new instance form. The default config MUST itself satisfy the type's `ValidateConfig` -- a default that fails its own validator is a bug.

### Widget Registry

9. The system MUST expose a process-wide registry (`internal/widget.Registry`) with the following operations:
   - `Register(reg Registration) error` -- called from `init()` functions in each widget type's package; returns a non-nil error if a widget with the same `Type` is already registered (defensive; in practice a duplicate type is a build-time bug). The error path MUST NOT panic; the caller (typically an `init()` function) decides how to surface registration failures.
   - `Get(typeName string) (Registration, bool)` -- returns the registration for a given type identifier, or `false` if no widget with that name is registered.
   - `Validate(typeName string, raw []byte) (Instance, error)` -- a convenience wrapper that calls `Get` then `ValidateConfig`. Returns a wrapping error if the type is unknown.
   - `Render(ctx context.Context, typeName string, raw []byte, theme themes.Theme) (templ.Component, error)` -- a convenience wrapper that calls `Get`, `ValidateConfig`, and the widget's `Render` method. This is the call Screen Display will use most.
   - `List() []Registration` -- returns every registered widget, deterministically ordered by `Type`, for the picker UI.
10. The registry MUST be safe for concurrent reads after initialisation. Writes (registrations) happen exclusively from `init()` functions, so the registry is effectively read-only after `main()` starts. The implementation MAY use a `sync.RWMutex` or a `map[string]Registration` guarded by an init-time-only lock; the architecture doc specifies the exact pattern. Tests MUST NOT need to drain or reset the global registry between cases (the architecture provides a constructor-based registry instance for testability -- see requirement 12).
11. The registry MUST NOT silently overwrite an existing registration. A duplicate `Register` call returns a non-nil error. (Real callers `panic` on registration error since this is a build-time bug, but the registry itself does not panic.)
12. The registry MUST expose both a process-wide singleton (used by production code) AND a constructor (`NewRegistry`) for tests. Tests build a fresh `Registry` per case, register only the widgets they care about, and never touch the global. This is the pattern that lets multiple parallel tests of widget logic coexist without leaking state.

### Placeholder Text Widget

13. The system MUST ship exactly one concrete widget type in this spec: `text`. This is the contract validator. Future widgets ship in their own specs (Phase 3).
14. The `text` widget MUST live in `internal/widget/text/` (one widget per package, mirror the future widget layout).
15. The `text` widget MUST define a per-instance configuration with one field:
   - `Text string` -- the text to display. Required (non-empty after trimming whitespace).
16. The `text` widget MUST define and use a JSON shape for its config:
   ```json
   {"text": "Hello, screens"}
   ```
17. The `text` widget's validator MUST:
   - Reject malformed JSON with a clear error.
   - Reject empty / whitespace-only `text` values with a clear error.
   - Reject `text` values longer than 4096 characters (defensive bound; a single placeholder display block does not need more).
   - Trim leading / trailing whitespace from accepted values before storing.
18. The `text` widget's renderer MUST emit a single HTML element (a `<div>` is acceptable) carrying the configured text as a child text node. The text MUST be HTML-escaped; templ does this automatically when rendering a `string` interpolation.
19. The `text` widget's renderer MUST style the output using the active theme's CSS custom properties (`--bg`, `--surface`, `--text`, `--font-family`, `--radius`) -- not hard-coded colors. Concrete styling comes from the existing `static/css/app.css` plus the per-page `<style>` block that Screen Display will inject. The widget's templ MUST NOT inline its own colors; it inherits from the theme.
20. The `text` widget MUST NOT consult the database, fetch external data, or have any side effects in `Render`. It is a pure function of (config, theme).
21. The `text` widget MUST register itself with the global registry from an `init()` function in its package. Importing the package is enough to register the widget. The `main.go` import list MUST include a blank import of `internal/widget/text` so the `init()` runs.

### Forward Compatibility

22. The `Registration` struct MAY grow new optional fields in later specs without breaking existing widget implementations. Specifically:
    - When the Typography Roles spec ships, `Registration` SHOULD gain a `FontRole` field (or equivalent) allowing a widget to declare which font role it wants. Existing widgets without a declared role get the global `--font-family` (the current behaviour). This spec does not implement role-aware fonts; it just commits to the additive-evolution path.
    - When alert / push features ship, the renderer signature MAY be augmented (e.g., to receive a per-instance "is this widget being overlaid by an alert?" signal). Such changes are owned by the relevant later spec.
23. The `Widget` interface itself MUST stay small. Adding methods to the interface is a breaking change; the design intent is to grow `Registration` (additive) rather than `Widget` (breaking) when new capabilities ship.

### No HTTP Endpoints

24. This spec MUST NOT register any HTTP routes. The widget interface is a code-level contract. The placeholder `text` widget has no admin UI in v1; the picker / instance-management surface is owned by Widget Selection UI (later in Phase 2).
25. The `text` widget's render output is consumable by tests calling `widget.Render(...)` directly. End-to-end use through Screen Display lands in the Screen Display spec.

### Wiring

26. The `main.go` startup path MUST construct the production registry (the singleton accessor from `internal/widget`). The act of importing the `internal/widget/text` package wires the placeholder widget into the registry; no explicit `Register` call lives in `main.go`.
27. The `views.Deps` struct MUST gain a `Widgets *widget.Registry` field threaded through `views.AddRoutes`, even though no view in this spec uses it. Screen Display (the next p0 spec in Phase 2) consumes the field; pre-wiring it here keeps Screen Display's task list strictly additive. (Alternative considered and rejected: defer the `Deps` change to Screen Display. Rejected because it forces Screen Display to ship a `Deps` change in the same task that does the rendering, which makes review noisier. Pre-wiring an unused-by-current-views field is the smaller diff.)
28. The `views.Deps` field MAY be `nil` in tests that do not need widget rendering. Production code (the `main.go` wiring) MUST set it.

### Existing Behaviour Preserved

29. The Theme System (SPEC-004) MUST continue to work unchanged. The widget renderer takes `themes.Theme` by value as an input; it does NOT call back into the theme service.
30. All existing routes (`/admin/*`, `/health`, `/`) MUST continue to behave identically. This spec adds new packages and a new field on `Deps`; it modifies no existing handlers.
31. No new third-party dependency MUST be introduced. `internal/widget` and `internal/widget/text` use only the standard library plus the existing `github.com/a-h/templ` import.

## Non-Functional Requirements

- **Performance**: `Registry.Get` and `Registry.Render` are hot-path calls (Screen Display invokes them once per widget instance per page render). They MUST be O(1) map lookups with no allocations beyond the rendered component itself. Validation runs at instance write time (rare) and is allowed to allocate.
- **Security**: The widget interface is a code-level contract; the only user input it touches is the per-instance configuration JSON, which Screen Model writes after running through the registered `ValidateConfig`. Validators MUST be strict (whitelist where practical, length-cap free-form strings, reject malformed JSON). The placeholder `text` widget renders text via templ's automatic HTML escape; it does NOT use `templ.Raw` or string concatenation.
- **Concurrency**: The registry is read-mostly. After all `init()` functions have run, no further writes occur. Reads MUST be safe across goroutines without external synchronisation. The architecture document specifies the exact synchronisation pattern.
- **Testability**: The registry constructor (`NewRegistry`) returns a fresh, empty registry for tests. Each widget package's tests build a registry, register the widget under test, and exercise the validator + renderer directly. Tests MUST NOT mutate the global singleton.
- **Documentation**: Every exported identifier in `internal/widget` MUST have a doc comment, including the `Widget` interface, the `Registration` struct, every `Registry` method, and the `Instance` type. The expectation is that a Phase 3 widget author reads the package doc + one existing widget (the `text` widget) and has enough to ship a new widget.
- **Backwards compatibility**: This spec adds new packages, a `views.Deps` field, and one main.go import. No existing Go types, function signatures, routes, or config defaults change.

## Acceptance Criteria

### Widget Interface and Registration

- [ ] AC-1: When a developer reads `internal/widget/widget.go`, then they find an exported `Widget` interface with exactly one method `Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component`.
- [ ] AC-2: When a developer reads `internal/widget/registration.go` (or wherever the architecture places it), then they find an exported `Registration` struct with at least the fields `Type`, `DisplayName`, `Description`, `New`, `DefaultConfig`, and `ValidateConfig`.
- [ ] AC-3: When a developer reads the package, then they find an exported `Instance` struct carrying a per-instance ID, the widget type identifier, and the validated config payload.

### Registry

- [ ] AC-4: When `NewRegistry().Register(reg)` is called once with a valid registration, then the registration succeeds (returns nil).
- [ ] AC-5: When `Register` is called twice with the same `Type` on the same registry, then the second call returns a non-nil error containing the duplicate type identifier.
- [ ] AC-6: When `Get("nonexistent-type")` is called on a registry, then it returns the zero `Registration` and `false`.
- [ ] AC-7: When `Get("text")` is called on a registry that has the `text` widget registered, then it returns that registration and `true`.
- [ ] AC-8: When `List()` is called on a registry with three registrations (`a`, `b`, `c`), then it returns those three registrations in alphabetical order by `Type`.
- [ ] AC-9: When `Render(ctx, "text", validJSON, theme)` is called, then it returns a non-nil `templ.Component` and a nil error.
- [ ] AC-10: When `Render(ctx, "text", invalidJSON, theme)` is called, then it returns a nil component and a non-nil error wrapping the validator's error.
- [ ] AC-11: When `Render(ctx, "nonexistent", anyJSON, theme)` is called, then it returns a nil component and a non-nil error indicating the type is unknown.
- [ ] AC-12: When two goroutines concurrently call `Get("text")` on a registry that has `text` registered (and no concurrent `Register` calls are happening), then both calls succeed and return the same `Registration`. (Tested with `go test -race`.)
- [ ] AC-13: When the global singleton registry is read, then it contains exactly the widget types whose packages have been imported (specifically: `text`, after the `main.go` blank import lands).

### Text Widget Validation

- [ ] AC-14: When `ValidateConfig([]byte(`{"text":"hello"}`))` is called on the text widget, then it returns an `Instance` whose validated config has `Text == "hello"` and a nil error.
- [ ] AC-15: When `ValidateConfig([]byte(`{"text":"  hello  "}`))` is called, then the resulting `Instance` has `Text == "hello"` (trimmed).
- [ ] AC-16: When `ValidateConfig([]byte(`{"text":""}`))` is called, then it returns a non-nil error mentioning the empty / whitespace value.
- [ ] AC-17: When `ValidateConfig([]byte(`{"text":"   "}`))` is called, then it returns a non-nil error.
- [ ] AC-18: When `ValidateConfig([]byte(`not-json`))` is called, then it returns a non-nil error mentioning JSON.
- [ ] AC-19: When `ValidateConfig` is called with a `text` value 4097 characters long, then it returns a non-nil error mentioning the length cap.
- [ ] AC-20: When `DefaultConfig()` is called on the text widget, then the returned bytes parse as valid JSON and pass `ValidateConfig` without error (the default-must-validate property).

### Text Widget Rendering

- [ ] AC-21: When the text widget's renderer is invoked with a valid `Instance` containing `Text == "hello"` and rendered to a string, then the output contains the literal substring `hello`.
- [ ] AC-22: When the text widget's renderer is invoked with `Text == "<script>"`, then the rendered output contains the HTML-escaped form (`&lt;script&gt;`) and does NOT contain a raw `<script>` tag.
- [ ] AC-23: When the text widget's renderer is invoked, then the rendered HTML contains no `style="..."` attributes hard-coding colors. (The widget inherits from theme CSS variables; it MUST NOT bake colors into the markup.)

### Registration Wiring

- [ ] AC-24: When `internal/widget/text` is imported (anywhere -- including by tests), then the `text` widget is present in the global singleton registry. Verified by importing the package and calling `widget.Default().Get("text")`.
- [ ] AC-25: When `main.go` is built and `Registry.List()` is called on the running process, then the returned slice contains a registration with `Type == "text"`.

### Out-of-Scope Sanity

- [ ] AC-26: When the codebase is searched for widget-related HTTP routes, then no new routes are registered by this spec. (Search verifies `widget.Register` calls and that no `views/widget*.go` file with route registration was added.)
- [ ] AC-27: When `views.Deps` is constructed in tests, then the `Widgets` field MAY be omitted (zero value); existing view tests do not break.

## Out of Scope

- Widget instance storage (the `widget_instances` table, the per-page placement model, instance CRUD). Owned by Screen Model.
- The admin UI for picking, configuring, and arranging widgets on a page (the picker, the per-instance config form). Owned by Widget Selection UI (Phase 2, p1).
- Live preview of a widget's rendered output as the admin types config values. (Could be useful; deferred.)
- Per-screen or per-page widget overrides that compose configs at render time.
- Concrete data-fetching widgets (time / weather / calendar / Home Assistant / slideshow / charts / financial). Each ships as its own Phase 3 spec.
- Per-role font selection on the theme (`title`, `body`, `time`, etc.). Owned by Typography Roles (Phase 2, p1). This spec keeps every widget on the global `--font-family` and commits to the additive evolution path so role-aware fonts can be added without breaking the interface.
- Widget-private CSS files. The placeholder `text` widget styles itself purely via the theme's existing variables; future widgets MAY ship a small CSS file in `static/css/widgets/<type>.css` if needed. The conventions for that are owned by individual widget specs.
- Widget-private JavaScript. htmx attributes are fine; bespoke JS modules are not in scope.
- A widget capability negotiation system (e.g., "this widget refuses to render at sizes below 200x200"). Layout sizing is owned by Screen Model / Screen Display.
- Authorization differentiation between widget types (e.g., "some widgets are admin-only"). Every widget that an admin can configure is renderable on a screen.
- Hot-reloading of widget types at runtime. Widgets register at process start via `init()`; restarting the binary is the upgrade story.
- Database persistence of registry state. The registry is a code-only construct; it is rebuilt every process boot from the imported widget packages.
- Versioning of widget configurations. If a widget type evolves its JSON shape, that widget's spec owns the migration story. The interface stays the same.
- Per-widget logging and metrics conventions beyond the project's existing slog setup. (Future cross-cutting concern; out of scope here.)

## Dependencies

- Depends on: SPEC-004 (Theme System) -- the renderer signature takes `themes.Theme` by value. The widget interface needs the `themes` package to compile.
- Depends on: ADR-001 (Storage Engine) -- this spec doesn't write to the database, but the JSON-blob choice (per ADR-005) inherits the SQLite-only constraint.
- Depends on: existing `views.Deps` and `main.go` wiring established in Phase 1.
- No external dependencies. No new third-party Go modules.
- Forward dependency note: Screen Model will ADD a `widget_instances` table (or equivalent) with a `type TEXT NOT NULL` column and a `config TEXT NOT NULL` (JSON) column, plus the ID/positioning columns it owns. Screen Model calls `widget.Default().Validate(typeName, raw)` before persisting. That work is in Screen Model's spec, not here.

## Open Questions

All resolved.

- Q1 **Resolved**: Configurations are stored as opaque JSON blobs per instance in a single column; per-widget-type subtables are rejected. See ADR-005 for the trade-off analysis. This keeps Phase 3 widgets parallelisable and matches how every other dynamic-shape entity in the codebase is going to grow.
- Q2 **Resolved**: The renderer returns a `templ.Component`, not a string. This composes with the rest of `views/`, inherits HTML escape, and lets widgets compose sub-components naturally.
- Q3 **Resolved**: The registry is a process-wide singleton populated by `init()` functions in widget packages, with a `NewRegistry` constructor exposed for tests. This matches the package-init idiom used by `database/sql` driver registration and `image` format registration in the standard library.
- Q4 **Resolved**: The `text` widget is the only widget shipped in this spec. It is purely a contract validator; concrete widgets ship in their own Phase 3 specs.
- Q5 **Resolved**: The `views.Deps` struct gains a `Widgets *widget.Registry` field in this spec, even though no current view consumes it. Pre-wiring keeps Screen Display's diff smaller.
- Q6 **Resolved**: Per-role font support (Typography Roles, Phase 2, p1) is a future additive extension to the `Registration` struct, not a breaking change to the `Widget` interface. This spec commits to that evolution path and notes it in Forward Compatibility.
- Q7 **Resolved**: The widget interface package lives at `internal/widget/`; concrete widget types live at `internal/widget/<type>/`. Mirrors the existing `internal/themes/` layout for the interface and gives each concrete widget its own package for tests and (eventually) its own templ.
