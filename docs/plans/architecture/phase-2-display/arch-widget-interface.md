---
id: ARCH-005
title: "Widget Interface"
spec: SPEC-005
status: draft
created: 2026-04-30
author: architect
---

# Widget Interface Architecture

## Overview

The Widget Interface adds the code-level contract that every widget type satisfies. It introduces a new top-level package `internal/widget/` containing the `Widget` interface, the `Registration` struct, the `Instance` carrier, and the `Registry` type that maps widget-type identifiers to registrations. A second package `internal/widget/text/` ships the placeholder `text` widget that exercises the contract end-to-end. The existing `views.Deps` struct gains a `Widgets *widget.Registry` field, and `main.go` constructs the production registry via the `Default()` accessor and threads it through.

The package name is `widget` (singular), mirroring the singular naming used by `internal/themes/` and `internal/auth/`. Concrete widget types live one level deeper (`internal/widget/text/`, future `internal/widget/time/`, etc.) and self-register from `init()` functions. Importing the package is what wires the widget into the registry, mirroring the standard-library `database/sql` driver registration idiom.

The contract is small on purpose: a single-method interface (`Render`), a struct of metadata + factory + validator (`Registration`), and a map-backed registry. Phase 3 widget specs each add one widget package; nothing in `internal/widget/` itself needs to change to support them. The architecture commits to two evolution rules: `Registration` may grow new optional fields (additive), and `Widget` MUST stay one method (breaking changes go through new fields, not new methods).

## References

- Spec: `docs/plans/specs/phase-2-display/spec-widget-interface.md`
- Related ADRs: ADR-005 (this feature -- JSON-blob storage with per-type Go validation, registry-of-init-time-registrations pattern, single-method interface).
- Prerequisite architecture: ARCH-004 (Theme System) -- the renderer takes `themes.Theme` by value.
- Forward consumers: Screen Model (will add `widget_instances` table that calls `Registry.Validate`), Screen Display (will iterate page widgets and call `Registry.Render`).

## Data Model

### Code-level types

This spec defines no database schema; all persisted data is owned by Screen Model. The code-level types are:

```go
// internal/widget/widget.go
package widget

import (
    "context"

    "github.com/a-h/templ"
    "github.com/jasoncorbett/screens/internal/themes"
)

// Widget is the contract every widget type implements. It is intentionally
// minimal: a single method that produces a renderable HTML component from a
// validated per-instance configuration plus the active theme.
//
// Widget implementations MUST be safe for concurrent calls to Render. The
// typical implementation is a stateless struct, so this is free.
type Widget interface {
    // Render produces the HTML component for this widget instance. The
    // instance carries the validated configuration; the theme is the active
    // theme for the screen the widget is rendering on. Implementations MUST
    // NOT panic, MUST NOT block on external I/O without honouring ctx, and
    // MUST inherit color/font styling from theme CSS variables rather than
    // baking colors into the markup.
    Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component
}

// Instance is one concrete placement of a widget on a page. The ID is set
// by Screen Model when the row is created; this package does not own the
// generation. The Type matches Registration.Type. Config is the validated,
// type-specific payload returned by Registration.ValidateConfig -- the
// underlying concrete type is private to each widget package, and the
// caller (Screen Display, the registry's Render wrapper) treats it as an
// opaque any.
type Instance struct {
    ID     string // owned by Screen Model; "" is acceptable in tests
    Type   string // matches Registration.Type
    Config any    // produced by Registration.ValidateConfig; type-private
}
```

```go
// internal/widget/registration.go
package widget

// Registration is the metadata + behaviour bundle for one widget type. Every
// widget package builds a Registration in its init() function and passes it
// to Default().Register(...) (or to a fresh registry in tests).
//
// Forward-compatibility note: this struct may gain new optional fields in
// later specs (e.g., FontRole when Typography Roles ships). New fields MUST
// be optional with sensible zero-value semantics so existing widgets keep
// working.
type Registration struct {
    // Type is the stable identifier used as the JSON discriminator and the
    // database column value. Lowercase ASCII, no spaces. Examples: "text",
    // "time", "weather". Must be unique within a registry.
    Type string

    // DisplayName is the human-readable label shown in the picker UI.
    // Example: "Text".
    DisplayName string

    // Description is a one-sentence explanation shown alongside the picker
    // entry. Example: "Display a configurable block of text.".
    Description string

    // New returns a Widget implementation. For stateless widget types this
    // typically returns a pointer to a singleton; for widgets that hold
    // per-instance state (e.g., a per-process HTTP client), it MAY return
    // a fresh value per call. The registry calls New each time it needs to
    // render, so the call MUST be cheap.
    New func() Widget

    // DefaultConfig returns the JSON bytes representing a sensible starting
    // configuration. Used by the picker UI to pre-populate a new instance
    // form. Returning JSON bytes (rather than a typed struct) keeps the
    // Registration struct non-generic. The returned bytes MUST satisfy
    // ValidateConfig without error -- a default that fails validation is
    // a build-time bug. Tests assert this property.
    DefaultConfig func() []byte

    // ValidateConfig parses and validates the per-instance JSON config.
    // On success it returns an Instance whose Config field carries the
    // typed, validated payload (the concrete type is private to the
    // widget's own package). On failure it returns the zero Instance and
    // a non-nil error. ValidateConfig MUST NOT panic.
    //
    // Screen Model calls this before persisting a widget instance row.
    // Screen Display calls it (indirectly via Registry.Render) before
    // every render -- the cost is one JSON unmarshal per widget per page,
    // which is acceptable for a household-scale dashboard.
    ValidateConfig func(raw []byte) (Instance, error)
}
```

```go
// internal/widget/registry.go
package widget

import (
    "context"
    "fmt"
    "sort"
    "sync"

    "github.com/a-h/templ"
    "github.com/jasoncorbett/screens/internal/themes"
)

// Registry maps widget-type identifiers to Registrations. Production code
// uses the singleton returned by Default(); tests build a fresh Registry
// via NewRegistry to avoid global state.
//
// The Registry is read-mostly: writes (registrations) happen exclusively
// from init() functions in widget packages. After init(), reads are safe
// across goroutines without external synchronisation. The internal mutex
// is a defensive guard against accidental late registrations -- the
// expected pattern is "everything registers in init(), then nothing
// registers ever again".
type Registry struct {
    mu    sync.RWMutex
    items map[string]Registration
}

// NewRegistry returns an empty Registry suitable for tests. Each test that
// touches a widget should build its own registry, register the widget(s)
// under test, and exercise the registry methods directly. Tests MUST NOT
// mutate the global singleton.
func NewRegistry() *Registry {
    return &Registry{items: map[string]Registration{}}
}

// Register adds reg to the registry. Returns a non-nil error if a widget
// with the same Type is already registered. The error is descriptive
// enough to be panicked by the calling init() function -- a duplicate
// type is a build-time bug, not a runtime condition.
func (r *Registry) Register(reg Registration) error {
    if reg.Type == "" {
        return fmt.Errorf("widget registration: Type must not be empty")
    }
    if reg.New == nil {
        return fmt.Errorf("widget registration %q: New must not be nil", reg.Type)
    }
    if reg.DefaultConfig == nil {
        return fmt.Errorf("widget registration %q: DefaultConfig must not be nil", reg.Type)
    }
    if reg.ValidateConfig == nil {
        return fmt.Errorf("widget registration %q: ValidateConfig must not be nil", reg.Type)
    }
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, exists := r.items[reg.Type]; exists {
        return fmt.Errorf("widget registration: type %q already registered", reg.Type)
    }
    r.items[reg.Type] = reg
    return nil
}

// Get returns the Registration for the given type, or the zero
// Registration and false if no such type is registered. Safe for
// concurrent use.
func (r *Registry) Get(typeName string) (Registration, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    reg, ok := r.items[typeName]
    return reg, ok
}

// List returns every registered widget, deterministically ordered by Type.
// The picker UI iterates this slice. Returns an empty slice (never nil)
// when the registry is empty.
func (r *Registry) List() []Registration {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]Registration, 0, len(r.items))
    for _, reg := range r.items {
        out = append(out, reg)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
    return out
}

// Validate is a convenience wrapper that resolves the type, runs its
// ValidateConfig, and returns the resulting Instance. Returns a wrapping
// error if the type is unknown.
func (r *Registry) Validate(typeName string, raw []byte) (Instance, error) {
    reg, ok := r.Get(typeName)
    if !ok {
        return Instance{}, fmt.Errorf("widget: unknown type %q", typeName)
    }
    return reg.ValidateConfig(raw)
}

// Render is the call Screen Display will make per widget instance per page
// render. It resolves the type, validates the config, constructs a Widget
// via Registration.New, and delegates to the Widget's Render method.
// Returns a nil component plus a wrapping error if the type is unknown or
// the config fails validation.
func (r *Registry) Render(ctx context.Context, typeName string, raw []byte, theme themes.Theme) (templ.Component, error) {
    reg, ok := r.Get(typeName)
    if !ok {
        return nil, fmt.Errorf("widget: unknown type %q", typeName)
    }
    inst, err := reg.ValidateConfig(raw)
    if err != nil {
        return nil, fmt.Errorf("widget %q: validate config: %w", typeName, err)
    }
    inst.Type = typeName
    return reg.New().Render(ctx, inst, theme), nil
}
```

```go
// internal/widget/default.go
package widget

import "sync"

// defaultRegistry is the process-wide singleton populated by init()
// functions in widget packages.
var (
    defaultOnce sync.Once
    defaultReg  *Registry
)

// Default returns the process-wide registry. Widget packages call
// Default().Register(...) from init(). Application code (main.go, Screen
// Display) reads from it. Tests SHOULD use NewRegistry() instead.
func Default() *Registry {
    defaultOnce.Do(func() {
        defaultReg = NewRegistry()
    })
    return defaultReg
}

// MustRegister registers reg with the default registry, panicking on
// error. Intended to be called from widget package init() functions where
// a duplicate-type or missing-field error is a build-time bug. Plain
// Register is also fine; this is sugar.
func MustRegister(reg Registration) {
    if err := Default().Register(reg); err != nil {
        panic(err)
    }
}
```

### Text widget types (in `internal/widget/text/`)

```go
// internal/widget/text/text.go
package text

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "github.com/a-h/templ"
    "github.com/jasoncorbett/screens/internal/themes"
    "github.com/jasoncorbett/screens/internal/widget"
)

// Type is the widget-type identifier for the text widget.
const Type = "text"

// MaxTextLength caps the size of a text widget's body. A single placeholder
// display block does not need more.
const MaxTextLength = 4096

// Config is the per-instance configuration shape for the text widget. The
// JSON shape is {"text": "..."}.
type Config struct {
    Text string `json:"text"`
}

// widgetImpl satisfies widget.Widget. Stateless; one global value is fine.
type widgetImpl struct{}

var singleton = &widgetImpl{}

// Render implements widget.Widget. The active theme's CSS variables drive
// styling; this template embeds no inline colors.
func (w *widgetImpl) Render(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
    cfg, _ := instance.Config.(Config) // ValidateConfig guarantees the type
    return textComponent(cfg.Text)
}

// validate parses raw bytes and returns a widget.Instance carrying the
// validated Config payload. Trims whitespace from text. Rejects empty
// values and oversized values.
func validate(raw []byte) (widget.Instance, error) {
    var cfg Config
    if err := json.Unmarshal(raw, &cfg); err != nil {
        return widget.Instance{}, fmt.Errorf("text widget: invalid JSON: %w", err)
    }
    cfg.Text = strings.TrimSpace(cfg.Text)
    if cfg.Text == "" {
        return widget.Instance{}, fmt.Errorf("text widget: text must not be empty or whitespace-only")
    }
    if len(cfg.Text) > MaxTextLength {
        return widget.Instance{}, fmt.Errorf("text widget: text must be %d characters or fewer", MaxTextLength)
    }
    return widget.Instance{Type: Type, Config: cfg}, nil
}

// defaultConfig returns a JSON blob representing a sensible starting config.
// MUST validate cleanly under validate() -- this is asserted by tests.
func defaultConfig() []byte {
    b, _ := json.Marshal(Config{Text: "Hello, screens"})
    return b
}

// Registration returns the widget.Registration for the text widget. Tests
// call this directly to register the widget on a fresh widget.NewRegistry().
func Registration() widget.Registration {
    return widget.Registration{
        Type:           Type,
        DisplayName:    "Text",
        Description:    "Display a configurable block of text.",
        New:            func() widget.Widget { return singleton },
        DefaultConfig:  defaultConfig,
        ValidateConfig: validate,
    }
}

func init() {
    widget.MustRegister(Registration())
}
```

```go
// internal/widget/text/text.templ
package text

templ textComponent(body string) {
    <div class="widget widget-text">
        { body }
    </div>
}
```

The CSS class names (`widget`, `widget-text`) are hooks for `static/css/app.css` (or a future per-widget CSS file) -- this spec does NOT add a new CSS file; the class names just provide a styling target Screen Display can hook into later. The point of the spec is to prove the contract; Screen Display will tighten the visual presentation when it lands.

## API Contract

This spec adds NO HTTP endpoints. It is a code-level contract.

The implicit Go API contract that downstream specs depend on:

| Symbol | Owner | Stability |
|--------|-------|-----------|
| `widget.Widget` interface | this spec | Stable. New methods are a breaking change. |
| `widget.Registration` struct | this spec | Stable for existing fields. New fields may be added; they MUST be optional. |
| `widget.Instance` struct | this spec | Stable. |
| `widget.NewRegistry()` | this spec | Stable. |
| `widget.Default()` | this spec | Stable. |
| `(*Registry).Register / Get / List / Validate / Render` | this spec | Stable. |
| `widget.MustRegister` | this spec | Stable. |

The expected call shapes downstream:

```go
// Screen Model (write path)
inst, err := widget.Default().Validate(typeName, rawJSON)
if err != nil {
    return fmt.Errorf("invalid widget config: %w", err)
}
// persist typeName and rawJSON in the widget_instances table
```

```go
// Screen Display (render path)
component, err := widget.Default().Render(ctx, instance.Type, instance.Config, theme)
if err != nil {
    // render an error fallback component instead of failing the whole page
    component = errorPlaceholder(instance.Type, err)
}
component.Render(ctx, w)
```

```go
// Widget Selection UI (picker)
for _, reg := range widget.Default().List() {
    // render reg.DisplayName / reg.Description; the form's "config" field
    // pre-populates from reg.DefaultConfig().
}
```

## Component Design

### Package Layout

```
internal/
  widget/
    widget.go           -- NEW: Widget interface, Instance type
    registration.go     -- NEW: Registration struct
    registry.go         -- NEW: Registry type + methods (Register, Get, List, Validate, Render)
    default.go          -- NEW: Default() singleton accessor + MustRegister
    registry_test.go    -- NEW: registry tests (concurrent reads, duplicate registration, etc.)
    text/
      text.go           -- NEW: text widget impl + Registration + init()
      text.templ        -- NEW: textComponent templ
      text_test.go      -- NEW: validator + render + DefaultConfig-validates tests
main.go               -- MODIFY: blank-import _ "github.com/jasoncorbett/screens/internal/widget/text",
                                  pass widget.Default() into views.Deps
views/
  routes.go           -- MODIFY: add Widgets *widget.Registry field on Deps
```

The `internal/widget/` package contains ONLY the contract types and the registry. Concrete widget types live in subpackages. This keeps the root `widget` package importable from anywhere without circular-dependency risk: the root has no widget-specific knowledge, only the contract.

### Key Interfaces and Functions

See the type definitions in the Data Model section above. The exact method signatures are committed there; deviations require a spec amendment.

### Dependencies Between Components

```
internal/widget/                       <- depends only on internal/themes + a-h/templ + stdlib
internal/widget/text/                  <- depends on internal/widget + internal/themes + a-h/templ + stdlib
main.go                                <- blank-imports internal/widget/text (registers via init)
                                       <- imports internal/widget for Default()
                                       <- threads widget.Default() through views.Deps
views/routes.go                        <- imports internal/widget for the Deps field type
```

The dependency graph is acyclic. `internal/widget/text/` depends on `internal/widget/` (for the `Registration` and `Widget` interface), but NOT vice versa. Adding more widgets (Phase 3) extends this fan-out: each new widget package imports `internal/widget/` and is blank-imported by `main.go`.

### main.go Wiring Changes

```go
// existing imports
import (
    // ...
    "github.com/jasoncorbett/screens/internal/widget"
    _ "github.com/jasoncorbett/screens/internal/widget/text" // registers the placeholder text widget
)

// in main()
views.AddRoutes(mux, &views.Deps{
    Auth:             authSvc,
    Google:           googleClient,
    ClientID:         cfg.Auth.GoogleClientID,
    CookieName:       cfg.Auth.CookieName,
    DeviceCookieName: cfg.Auth.DeviceCookieName,
    DeviceLandingURL: cfg.Auth.DeviceLandingURL,
    SecureCookie:     !cfg.Log.DevMode,
    Themes:           themesSvc,
    Widgets:          widget.Default(), // NEW
})
```

The blank import is the wiring point. Adding a new widget in Phase 3 is purely:
1. Implement the widget package.
2. Add one blank import line to `main.go`.

There is no central widget-list file to maintain.

### views.Deps changes

```go
// views/routes.go
type Deps struct {
    Auth             *auth.Service
    Google           *auth.GoogleClient
    ClientID         string
    CookieName       string
    DeviceCookieName string
    DeviceLandingURL string
    SecureCookie     bool
    Themes           *themes.Service
    Widgets          *widget.Registry // NEW
}
```

No view in this spec consumes `Deps.Widgets`. Screen Display (the next p0 spec) will. Pre-wiring the field here keeps that spec's diff strictly additive.

## Storage

This spec adds no migrations, no tables, and no SQL queries. The widget interface is a pure code-level construct; there is nothing to persist.

The forward-looking JSON storage shape (owned by Screen Model, NOT this spec):

```sql
-- (in a future Screen Model migration)
CREATE TABLE widget_instances (
    id          TEXT PRIMARY KEY,
    page_id     TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,    -- matches Registration.Type
    config      TEXT NOT NULL,    -- JSON blob; validated against the type's ValidateConfig before INSERT
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
```

The Screen Model spec owns the exact column set, the indexes, and the migration. This spec just commits the contract: a widget instance's persisted shape is `(type, raw-JSON-config)`.

## Configuration

No new environment-driven configuration. The widget interface is code; the registry is built at process start from imported widget packages.

A future config knob (NOT in scope for this spec) would be `WIDGETS_ENABLED` -- a comma-separated list of widget types to include in the registry. Useful for trimming the binary for a kiosk that does not need the financial-tickers widget. Deferred to a later spec; mentioned here as a future extension point.

## Security Considerations

### Configuration JSON validation

The single security-relevant entry point is `Registration.ValidateConfig`. Every widget type owns a validator; the registry never bypasses it. Screen Model calls `Registry.Validate` before persisting, and Screen Display calls `Registry.Render` (which validates again) before rendering. Two-layer validation is by design: the database may already contain rows from older code paths or hand-edited rows.

Validators MUST be:
- **Total**: never panic.
- **Strict**: reject malformed JSON, reject missing required fields, reject out-of-bounds values.
- **Bounded**: cap free-form string lengths (the `text` widget caps at 4096 chars).
- **Deterministic**: same input -> same output.

The `text` widget's validator follows all four rules. Future widget validators MUST do the same; the architecture document for each Phase 3 widget restates the rules.

### HTML escape in templ

The `text` widget's templ uses a string interpolation `{ body }` which templ escapes automatically. There are no `templ.Raw` or `unsafe` constructs anywhere in this spec. Future widgets that want to render rich HTML MUST do so through templ components (composing existing safe primitives) rather than bypassing escape.

### No XSS via configuration

The validator-rejects-bad-input pattern means an admin who somehow gets bad JSON into the database (via direct SQLite editing) sees a render error rather than an XSS. The `text` widget's templ HTML-escapes the body; even if a malformed `<script>` blob bypassed validation, it would render as text.

### No filesystem or network access

This spec's two packages (`internal/widget/` and `internal/widget/text/`) make zero filesystem or network calls. The placeholder `text` widget is a pure function of (config, theme). Future widgets that DO fetch external data inherit the rules in the existing `add-widget` skill: pass an `*http.Client` with timeouts, accept `context.Context`, fail open with a fallback rendering.

### Registry concurrency

The `Registry` uses a `sync.RWMutex`. After all `init()` registrations complete, the registry is read-only. The mutex makes a hypothetical late `Register` call safe but not silent (it returns an error). Tests run with `-race` to verify the read path is data-race-free.

## Task Breakdown

This architecture decomposes into the following tasks. Numbering continues from TASK-018 (the last accepted Theme System task):

1. **TASK-019**: `internal/widget/` package -- Widget interface, Registration struct, Instance type, Registry implementation (with NewRegistry, Register, Get, List, Validate, Render), Default() singleton, MustRegister helper, plus the registry's tests. (Prerequisite: none -- this is the contract; nothing else depends on it from this spec.)
2. **TASK-020**: `internal/widget/text/` placeholder text widget -- `text.go` with Config, validator, default-config, render, init() registration; `text.templ` with the visual component; tests for validator, render, default-validates property. Plus the `main.go` blank-import wiring AND the `views.Deps.Widgets` field. (Prerequisite: TASK-019 -- text needs the Registration / Widget interface.)

### Task Dependency Graph

```
TASK-019 (interface + registry, no concrete widgets)
    |
    v
TASK-020 (text widget + main.go + Deps wiring)
```

The split is deliberate: TASK-019 ships the testable contract with no dependence on any concrete widget. TASK-020 ships the contract validator (the `text` widget) AND the integration wiring (`main.go` blank import, `Deps` field, end-to-end registry-contains-text test).

A three-task split was considered (separate the Deps wiring into its own task) and rejected. The Deps change is two lines, pre-wires a field that nothing in this spec consumes, and is small enough to land alongside the text widget. The blank import in `main.go` is similarly small. Splitting them out would be ceremony without value.

## Alternatives Considered

See ADR-005 for the full design rationale. Architectural alternatives evaluated during this design pass:

- **Per-widget-type subtables (one table per widget type)**: rejected. Forces a database migration for every new widget type, sequentially blocks Phase 3 widget specs on schema work, and makes "widget type rename" or "widget shape change" a multi-table migration. JSON-blob keeps the schema stable.
- **Untyped `map[string]any` per-instance config (no validator)**: rejected. Pushes validation to render time, where errors are silent; loses the "reject malformed at write time" property; makes the `text` widget's `Render` method full of type assertions.
- **Generic `Registration[T]` parameterised by config type**: rejected. Requires the registry to be type-erased at the boundary anyway (a `map[string]Registration[any]` is non-trivial in Go's current generics), the ergonomic gain inside each widget package is small (the widget already type-asserts in its own `Render`), and the cost is a noticeably worse package signature for new widget authors. The current shape -- a non-generic `Registration` whose `ValidateConfig` returns an `Instance` carrying an `any` -- is simpler and matches `database/sql`'s driver-interface idiom.
- **Reflection-based config schema (declare a struct, get JSON-schema validation for free)**: rejected. The standard library does not ship a JSON-schema implementation; rolling our own is out of proportion with the placeholder `text` widget's needs. Each widget owning a hand-written validator is more code per widget but zero shared infrastructure to maintain.
- **Multi-method `Widget` interface (e.g., separate `Validate`, `Render`, `Describe` methods)**: rejected. Splitting the contract across multiple methods on the interface entangles instantiation order (you can't `Validate` until you have a Widget, you can't construct a Widget until you have a config, you can't get a config without `Validate`). Pulling `Validate` and `Describe` onto `Registration` (a plain data struct) and keeping `Widget` as the render-only interface cleanly separates "what the widget IS" from "what the widget DOES".
- **Renderer returns a string instead of `templ.Component`**: rejected. Loses templ's automatic HTML escape, prevents widgets from composing sub-components naturally, and forces every widget to manage its own buffer. Returning `templ.Component` is the project's established pattern (every other view does this).
- **Renderer takes `*http.Request`**: rejected. Couples widgets to the HTTP layer and makes them harder to render in tests or in a future "preview" feature. Passing `context.Context` is sufficient for cancellation.
- **Skipping the `Default()` singleton and forcing main.go to construct the registry explicitly**: rejected. The init-time-registration idiom (used by `database/sql` driver registration, by `image` format registration, etc.) is the idiomatic Go way to do this. A constructor-based registry alone makes adding a Phase 3 widget a two-step change (write the package + register in main.go); the singleton makes it a one-step change (write the package + blank-import). The constructor still exists for tests.
- **Putting the placeholder widget in `internal/widget/` itself rather than a subpackage**: rejected. Mixes the contract (which Phase 3 widgets import) with a concrete widget (which Phase 3 widgets do NOT need to import). Subpackage cleanly separates the two.
- **Registering widgets via a YAML / JSON manifest file rather than `init()`**: rejected. The manifest would have to be parsed at startup, validated against build-time-known types, and would not survive cross-compilation cleanly. Code-level registration via `init()` is the simpler, type-safe path and is what every `database/sql` driver does today.
- **Storing each widget's configuration schema separately and validating at the registry level**: rejected. Schema definitions become a non-trivial sub-language; validation logic ends up duplicated between the schema and the Go code that consumes the validated payload. Hand-rolled validators per widget are more code per widget but the per-widget code is also where the type-aware logic lives, so consolidation is the natural shape.
- **Widget capability flags on Registration (e.g., "needs-network", "expensive-render")**: deferred. Useful for a future "render budget" or "offline mode" feature, but not needed by SPEC-005 / Screen Display. Adding it later is additive (new optional field on `Registration`).
- **Per-instance widget IDs generated by the registry**: rejected. Screen Model owns instance creation; the registry never sees an instance until Screen Model has assigned it an ID. Putting ID generation in `internal/widget/` couples it to the persistence layer.
