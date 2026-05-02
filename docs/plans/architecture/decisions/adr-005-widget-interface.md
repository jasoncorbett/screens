---
id: ADR-005
title: "Widget interface, registry-of-init-time-registrations, and JSON-blob per-instance configuration"
status: accepted
date: 2026-04-30
---

# ADR-005: Widget interface, registry-of-init-time-registrations, and JSON-blob per-instance configuration

## Context

Phase 2 introduces the widget abstraction. The roadmap entry is "Widget type registry, renderer interface, configuration schema, placeholder text widget". Three independent design questions shake out:

1. **What is the runtime contract a widget type satisfies?** Possible answers:
   - A multi-method interface (e.g., separate `Validate`, `Render`, `Describe` methods).
   - A single-method interface (`Render`) plus a metadata struct (`Registration`) carrying the rest.
   - A struct with function-typed fields (e.g., `WidgetType{Render func(...), Validate func(...), ...}`) and no Go interface.
2. **How are widget types registered with the runtime?** Possible answers:
   - A central registry-list file (`internal/widget/all.go`) that imports every widget package and explicitly calls `Register`.
   - `init()` functions in each widget package that self-register.
   - A YAML / JSON manifest file parsed at startup.
   - Constructor injection: `main.go` builds a `[]Registration` and passes it to `widget.NewRegistry(...)`.
3. **How is per-instance configuration stored and validated?** Possible answers:
   - One database table per widget type (typed columns).
   - One database column per widget type on a single `widget_instances` table (sparsely populated).
   - A single JSON-blob column on `widget_instances`, validated by a Go function the widget type owns.
   - A single text-blob column with no validation (let the widget figure it out at render time).

Phase 3 of the roadmap lists at least seven concrete widget types -- time/date, weather, calendar, Home Assistant, slideshow, charts, financial tickers -- and the polish-phase work assumes more will follow. The downstream consumers (Screen Model writes instances, Screen Display renders them, Widget Selection UI lists types) all depend on stable answers to the three questions above.

The threat model is the same as for the rest of the admin UI: an authenticated admin can store arbitrary widget instance configurations, which then flow through the renderer onto every device's HTML response. A bad configuration must not crash the page or open an XSS vector. Validation is the only defence; HTML escape applies inside the rendered components but not to the configuration JSON itself.

The performance model is "household-scale dashboard": a single admin manages tens of widget instances, devices render a page every few seconds. Per-render allocation is acceptable; a per-render database round-trip per widget would be measurable.

## Decision

### Widget contract: a single-method `Widget` interface plus a `Registration` metadata struct

We define `Widget` as a single-method interface (`Render`) and put everything else (type identifier, display name, description, factory, default-config, validate-config) on a plain `Registration` struct of function-typed fields and metadata.

```go
type Widget interface {
    Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component
}

type Registration struct {
    Type           string
    DisplayName    string
    Description    string
    New            func() Widget
    DefaultConfig  func() []byte
    ValidateConfig func(raw []byte) (Instance, error)
}
```

Rationale:
- A single-method interface is the easiest contract to extend additively. Adding a method is a breaking change; adding a field on `Registration` is not.
- Splitting `Validate` off the interface and onto `Registration` cleanly resolves the "instantiation chicken-and-egg" problem: you can't construct a `Widget` without a config, can't validate a config without a `Widget`. With `ValidateConfig` on `Registration`, validation runs before any `Widget` instance is constructed.
- The pattern matches `database/sql.Driver` (a small interface) plus `database/sql.DriverContext` / various optional add-on interfaces -- but here we put the optional parts on a plain struct rather than as opt-in interfaces, because every widget needs all of them.
- Returning a `templ.Component` from `Render` matches the project's established pattern (every existing view does this) and inherits templ's automatic HTML escape.

Rejected alternatives:
- **Multi-method `Widget` interface (`Validate`, `Render`, `Describe`)**: forces every widget to instantiate before validation can run, and entangles construction with config decoding. Splitting validation onto `Registration` is cleaner.
- **Plain function-typed struct with no interface**: works, but loses the implicit "this type implements Widget" check the compiler gives us. The single-method interface is enforced at registration time (`reg.New().Render(...)` would fail to compile if the returned type didn't satisfy `Widget`).
- **Generic `Registration[T]` parameterised by config type**: the registry has to be type-erased at the boundary anyway (a heterogeneous map of registrations), the ergonomic gain inside each widget package is small (the widget still type-asserts inside `Render`), and the noisier signature is worse for the next widget author. The current shape -- a non-generic `Registration` whose `ValidateConfig` returns an `Instance` carrying an `any` -- is simpler.

### Registration: process-wide singleton populated by `init()` functions

We expose a `widget.Default()` singleton plus a `widget.NewRegistry()` constructor. Concrete widget packages call `widget.MustRegister(Registration())` from their `init()` functions. `main.go` blank-imports each widget package to trigger its `init()`. Tests build their own `*Registry` via `NewRegistry()` and never touch the global.

Rationale:
- The init-time-registration idiom is what `database/sql` (driver registration), `image` (format registration), `encoding/gob` (concrete-type registration), and many other standard-library packages use. Phase 3 widget authors recognise the pattern instantly.
- Adding a new widget type in Phase 3 is a two-line `main.go` diff (one blank import) plus a new package. There is no central widget-list file to keep in sync.
- The constructor (`NewRegistry`) makes the registry testable without global mutation. Each test builds its own registry, registers only the widgets under test, and gets clean isolation.
- A `MustRegister` helper that panics on registration error matches the "duplicate type is a build-time bug, not a runtime condition" assumption -- but the underlying `Register` method returns an error, so the panic path is opt-in for callers that want it.

Rejected alternatives:
- **Central registry-list file (`internal/widget/all.go`)**: requires a separate edit per new widget; couples the central file to every widget package; complicates "remove widget X" cleanup. The blank-import pattern in `main.go` is a smaller, more local change per widget.
- **Constructor injection only (no singleton)**: forces `main.go` to import every widget package and explicitly build the slice. Two-line diff per new widget instead of one. Loses parity with idiomatic Go.
- **YAML / JSON manifest file parsed at startup**: introduces a non-trivial parser, doesn't validate types at compile time, doesn't survive cross-compilation as cleanly, and adds a runtime failure mode (manifest typo) for what should be a build-time concern.
- **No singleton (every consumer builds its own registry)**: works for tests but bloats `main.go` (every widget would have to be threaded explicitly through `views.Deps`). The singleton is the standard Go answer; the constructor lets tests opt out.

### Per-instance configuration: a single JSON-blob column with a per-widget Go validator

We store each widget instance's configuration as a single JSON column (TEXT) on `widget_instances` (the table is owned by Screen Model). Each widget type owns a Go validator that decodes the blob into the type's typed config, validates field-by-field, and returns either a typed `Instance` or an error. Screen Model calls the validator before persisting; Screen Display calls it again before rendering.

Rationale:
- **Phase 3 parallelism**: a JSON blob means adding the `time` widget, the `weather` widget, etc. requires zero schema work. Each Phase 3 widget is one Go package; specs can run in parallel without sequencing on database migrations.
- **Schema stability**: the `widget_instances` table is fixed at `(id, page_id, type, config, position, created_at, updated_at)`. Widget shape changes are versioned inside the JSON; the database doesn't care.
- **Validation strictness**: per-widget Go validators are strict (whitelist where possible, length-cap free-form strings, reject malformed JSON, never panic). The two-layer call (Screen Model + Screen Display) means even a hand-edited bad row is caught before reaching a device.
- **Default-must-validate property**: each widget's `DefaultConfig()` returns JSON bytes that satisfy its own `ValidateConfig` -- a default that fails validation is a build-time bug. Tests assert this; the picker UI relies on it.

Rejected alternatives:
- **Per-widget-type subtables**: every new widget type is a new migration. Sequentially blocks Phase 3 specs on schema work. "Rename widget X" or "change config shape of widget X" becomes a multi-table migration. The cost is incurred once per widget (in 7+ widgets) for benefits (typed columns, SQL queryability) that the application doesn't need -- the application reads the row, decodes the JSON, and renders. SQL queryability of a widget's config fields is not a requirement.
- **Sparse columns on a single table**: every column is nullable, table grows by one column per widget, and most rows have most columns null. Worst of both worlds.
- **Untyped `map[string]any` config (no validator at all)**: pushes validation to render time, where errors are silent and the config is already in the database. Loses the "reject malformed at write time" property.
- **Reflection-based JSON-schema validation**: the standard library has no JSON-schema implementation; rolling our own is out of proportion with what we need. Each widget's hand-rolled validator is more code per widget but zero shared infrastructure.

### `views.Deps` gains a `Widgets *widget.Registry` field even though no current view consumes it

We add the field now, in this spec, even though Screen Display (the consumer) ships in the next spec.

Rationale:
- Pre-wiring keeps Screen Display's diff strictly additive on the views side. Screen Display is already a non-trivial spec (page layout, auto-rotation, theme injection, widget instance fetch + render); adding a `Deps` change to it bloats the diff and the review burden.
- The cost here is minimal: one field on `Deps`, one line in `main.go`, no code consumes it yet so there is no behavioural risk.

Rejected alternative: defer the `Deps` change to Screen Display. Cost-benefit doesn't change much either way; pre-wiring is simply where the architecture has chosen to put the diff.

### `Render` returns `templ.Component`, not a string

Rationale:
- templ is the project's standard for HTML rendering. Returning a `templ.Component` lets widgets compose sub-components (`@subPart()`), inherits automatic HTML escape on string interpolation (`{ body }`), and matches every other view in `views/`.
- Returning a string forces every widget to manage its own buffer and escape strategy; the templ approach is strictly easier and safer.

Rejected alternative: return `[]byte` or `string` for max flexibility. The flexibility is illusory: every widget would re-implement what templ already does. We'd lose escape-by-default and gain nothing.

### `Render` takes `context.Context`, not `*http.Request`

Rationale:
- Cancellation is the only request-scoped concern a widget legitimately needs. `context.Context` carries it.
- Coupling widgets to `*http.Request` makes them harder to render in tests, in a future preview feature, in a future "render this page to a static screenshot" task, or in a queue-driven "pre-render and ship to device" mode.
- The architecture explicitly does NOT pass `r.URL.Query()` etc. into the widget; if a widget needs runtime parameters it gets them through its persisted config, not through the request URL.

### Two-layer validation (write time + render time)

Screen Model calls `Registry.Validate` before persisting. Screen Display calls `Registry.Render` (which validates again internally) before rendering.

Rationale:
- Two-layer validation closes the loophole where someone hand-edits the SQLite database, changes a widget's config to malformed JSON, and the render path silently breaks the whole page.
- The cost is one JSON unmarshal per widget per page render. For a household-scale dashboard with tens of widgets per page, this is sub-millisecond overhead.

### Concurrency: registry uses `sync.RWMutex`, write-once-read-many

The registry's `items` map is guarded by an `sync.RWMutex`. Writes happen only from `init()` functions before `main()` reaches the registry; reads happen on every render. The mutex protects against accidental late registrations and is benchmarked / `-race`-tested.

Rejected alternative: lock-free read after a one-time init. Marginal performance gain for substantial complexity (atomic.Value, careful publication semantics). The RWMutex is the plain idiom and has been adequate for every Go program of this scale.

## Consequences

**Accepted trade-offs:**

- A new top-level package (`internal/widget/`) is added. Concrete widgets live in subpackages (`internal/widget/text/`, future `internal/widget/time/`, etc.). We accept the package-per-widget shape because each widget will eventually own a templ file and a tests file, and one-package-per-widget is the cleanest home for both.
- The `widget.Default()` singleton is process-wide global state. We accept this because the standard library has the same pattern (database/sql driver registration), tests have a clean opt-out (`NewRegistry`), and the singleton is read-only after `init()`.
- JSON-blob config means widget config fields are NOT directly SQL-queryable. We accept this -- the application never queries config fields; it reads the row, decodes the JSON, and renders. If a future spec needs cross-widget queryability (e.g., "list all weather widgets pointing at zip 90210"), it can add a generated index column or denormalised metadata.
- Each widget owns a hand-written validator. Some boilerplate per widget (JSON-unmarshal, field checks, length caps). We accept the boilerplate in exchange for keeping the type-aware validation logic next to the type-aware Render logic, in one package.
- `Widget` is a single-method interface. Any future cross-cutting capability (font role, capability flags, lifecycle hooks) goes on `Registration`, not on `Widget`. We commit to this rule; deviating would mean breaking every existing widget.
- `Render` takes `themes.Theme` by value. Themes are small structs; copying them is cheap and avoids any concern about a widget mutating a shared theme value.
- `views.Deps.Widgets` is set by `main.go` but consumed by no view in this spec. The field is `*widget.Registry`; tests that don't render widgets can pass `nil`. We accept the unused-by-current-views field.

**Benefits:**

- Phase 3 widgets are independent. Each new widget is one package + one blank-import line in `main.go`. No central widget-list file. No database migration. No `views.Deps` change.
- The contract is small enough to fit in one architecture-doc snippet. Phase 3 widget authors can read the package doc + the `text` widget and ship a new widget the same day.
- Validation is strict and lives next to the type. The `text` widget's validator and renderer are in the same file; a future widget reviewer sees both at once.
- The registry has a clean test path. `NewRegistry()` per test means concurrent test execution doesn't fight over the global.
- The `Default()` singleton plus blank-import pattern is the idiomatic Go way; new contributors recognise it.
- The two-layer validation closes the "hand-edited bad row" hole at zero performance cost.
- Forward-compatibility is explicit: `Registration` may grow optional fields (e.g., `FontRole` for Typography Roles); the `Widget` interface stays one method. Future specs can extend without breaking.
- The text widget proves the contract end-to-end. If the contract is wrong, the text widget breaks first (and visibly).

**Risks accepted:**

- A widget package's `init()` failing (e.g., MustRegister panicking on duplicate type) crashes the process at startup. We accept this -- a duplicate widget type is a build-time bug, not a runtime condition; failing fast at startup is the right behaviour.
- JSON-blob config shifts schema-typo bugs from "rejected at INSERT" to "rejected at validate-call time". We accept this because validators run synchronously at write time, so the error reaches the admin UI before the bad row is persisted.
- A widget's `Render` could in theory take an unbounded amount of time. We accept this and rely on the screen-display task to wrap renders with timeouts at the page level. Per-widget timeouts would require expanding the contract; we defer that to a later spec if it becomes a problem.
- Hand-written validators have to stay in sync with the typed Config struct. We accept this and rely on tests (each widget asserts default-validates and exercises every validator branch) to catch drift.
- The registry uses an RWMutex on every read. Negligible cost for a household-scale dashboard, but if widget rendering ever became a hot path with millions of QPS we would need to revisit. Out of scope for the project's target scale.
