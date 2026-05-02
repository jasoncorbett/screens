---
id: TASK-019
title: "Widget interface, registration struct, and registry"
spec: SPEC-005
arch: ARCH-005
status: ready
priority: p0
prerequisites: []
skills: [add-store, green-bar]
created: 2026-04-30
author: architect
---

# TASK-019: Widget interface, registration struct, and registry

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Lay the code-level foundation for the widget system. This task creates the `internal/widget/` package containing the `Widget` interface, the `Registration` metadata + factory + validator struct, the `Instance` carrier, and the `Registry` type with all its methods (`Register`, `Get`, `List`, `Validate`, `Render`). It also ships the `Default()` process-wide singleton accessor and the `MustRegister` helper.

The package must be self-contained: it ships no concrete widgets (the `text` widget lives in TASK-020), it ships no HTTP handlers, and it ships no `views.Deps` changes. The deliverable is the contract that everything else builds on.

After this task lands, a Phase 3 widget author can read the package doc, see the `Widget` / `Registration` / `Registry` types, and have everything they need to start sketching a new widget. TASK-020 then validates the contract by implementing the `text` widget against it.

## Context

- The package mirrors the shape of `internal/themes/`: a domain-focused package with a small set of exported types, a constructor, and well-documented public methods. There is no database, no HTTP layer, and no external dependency beyond `internal/themes` (for the `themes.Theme` argument to `Render`) and `github.com/a-h/templ` (for the `templ.Component` return type).
- The registry follows the same idiom as `database/sql.Register` / `image.RegisterFormat`: a process-wide singleton populated by `init()` functions in concrete widget packages. The singleton is exposed via `widget.Default()`. A `NewRegistry()` constructor is provided so tests can build isolated registries without touching the global.
- The `Render` method on `Widget` takes `(ctx context.Context, instance Instance, theme themes.Theme)` and returns `templ.Component`. The signature is committed in the architecture doc; do not deviate.
- `Registration` is a plain struct (not an interface). Its fields are: `Type string`, `DisplayName string`, `Description string`, `New func() Widget`, `DefaultConfig func() []byte`, `ValidateConfig func(raw []byte) (Instance, error)`. All four function fields MUST be non-nil for a registration to succeed; `Register` returns an error if any are nil.
- `Instance` carries `ID string`, `Type string`, `Config any`. `ID` is set by Screen Model in production; tests typically leave it `""`. `Config` is the typed-but-erased payload returned by `ValidateConfig`. Concrete widgets type-assert it inside their `Render`.
- `Registry` uses `sync.RWMutex`. Reads (`Get`, `List`, `Validate`, `Render`) take the read lock; writes (`Register`) take the write lock. After all `init()` functions complete, no further writes occur in production -- the mutex is defensive.
- `Render` (on the registry) is the convenience wrapper Screen Display will call. Internally it calls `Get`, then `ValidateConfig`, then constructs a `Widget` via `Registration.New()`, then delegates to the widget's `Render` method. The instance returned by `ValidateConfig` carries the validated `Config`; the registry then sets `Instance.Type` to `typeName` so the widget renderer always sees a populated `Type`.
- The registry's exported `Render` returns `(templ.Component, error)`. On unknown type, it returns `(nil, fmt.Errorf("widget: unknown type %q", typeName))`. On validation failure, it returns `(nil, fmt.Errorf("widget %q: validate config: %w", typeName, err))`. Otherwise it returns the component and nil.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/testing.md`
- `.claude/skills/add-store/SKILL.md` (the package shape parallels a domain service even though there is no DB)
- `internal/themes/service.go` -- mirror the package layout, doc comment style, and exported-error pattern.
- `internal/themes/theme.go` -- mirror the simple-struct style for `Instance`.
- `internal/themes/css.go` -- mirror the pure-function style for the registry's read-only methods.
- `views/layout.templ` -- understand the templ component idiom; the registry returns `templ.Component`.
- `docs/plans/architecture/phase-2-display/arch-widget-interface.md` -- "Data Model" and "Component Design > Key Interfaces and Functions" sections. The architecture provides exact code snippets; mirror them.
- `docs/plans/specs/phase-2-display/spec-widget-interface.md` -- "Functional Requirements > Widget Type Contract", "Per-Widget-Type Configuration Schema", and "Widget Registry" sections.
- `docs/plans/architecture/decisions/adr-005-widget-interface.md` -- the rationale for the single-method interface and the init-time-registration pattern.

## Requirements

### Package layout

1. Create `internal/widget/widget.go` containing:
   - A doc comment for the package (`// Package widget defines the contract every widget type implements...`).
   - The `Widget` interface with a single method `Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component`. Doc-comment the interface and the method.
   - The `Instance` struct with exported fields `ID string`, `Type string`, `Config any`. Doc-comment the struct and each field.
   - The imports needed: `"context"`, `"github.com/a-h/templ"`, `"github.com/jasoncorbett/screens/internal/themes"`.

2. Create `internal/widget/registration.go` containing:
   - The `Registration` struct with the six fields documented in the architecture doc. Doc-comment the struct and each field.
   - No methods on `Registration` in this task. (Validation of the registration itself happens inside `Registry.Register`.)

3. Create `internal/widget/registry.go` containing:
   - The `Registry` struct with private fields `mu sync.RWMutex`, `items map[string]Registration`. Doc-comment the struct.
   - `NewRegistry() *Registry` -- returns a registry with a non-nil empty `items` map. Doc-comment.
   - `(r *Registry) Register(reg Registration) error`:
     - Reject `reg.Type == ""` with `fmt.Errorf("widget registration: Type must not be empty")`.
     - Reject `reg.New == nil` with `fmt.Errorf("widget registration %q: New must not be nil", reg.Type)`.
     - Reject `reg.DefaultConfig == nil` with `fmt.Errorf("widget registration %q: DefaultConfig must not be nil", reg.Type)`.
     - Reject `reg.ValidateConfig == nil` with `fmt.Errorf("widget registration %q: ValidateConfig must not be nil", reg.Type)`.
     - Take the write lock. If `r.items[reg.Type]` already exists, return `fmt.Errorf("widget registration: type %q already registered", reg.Type)`.
     - Otherwise, store the registration and return nil.
   - `(r *Registry) Get(typeName string) (Registration, bool)`:
     - Take the read lock. Return `r.items[typeName]` and the comma-ok result.
   - `(r *Registry) List() []Registration`:
     - Take the read lock. Allocate `out := make([]Registration, 0, len(r.items))`. Append every value. Sort by `Type` ascending (`sort.Slice`). Return.
     - Empty slice (not nil) when the registry is empty.
   - `(r *Registry) Validate(typeName string, raw []byte) (Instance, error)`:
     - Calls `r.Get(typeName)`. On unknown type, returns `Instance{}, fmt.Errorf("widget: unknown type %q", typeName)`.
     - Otherwise calls `reg.ValidateConfig(raw)` and returns its result.
   - `(r *Registry) Render(ctx context.Context, typeName string, raw []byte, theme themes.Theme) (templ.Component, error)`:
     - Calls `r.Get(typeName)`. On unknown type, returns `nil, fmt.Errorf("widget: unknown type %q", typeName)`.
     - Calls `reg.ValidateConfig(raw)`. On error, returns `nil, fmt.Errorf("widget %q: validate config: %w", typeName, err)`.
     - Sets `inst.Type = typeName` (so callers always see a populated Type even if ValidateConfig forgot).
     - Calls `reg.New().Render(ctx, inst, theme)` and returns the component plus nil.
   - All registry methods MUST be safe for concurrent calls. Reads use `RLock`/`RUnlock`; writes use `Lock`/`Unlock`. Do NOT hold a lock across a call to `reg.ValidateConfig` or `Widget.Render` -- those are user code and could call back into the registry, deadlocking. Read the registration out under the read lock, then release the lock before calling user code.

4. Create `internal/widget/default.go` containing:
   - A package-level `var defaultOnce sync.Once` and `var defaultReg *Registry`.
   - `Default() *Registry`:
     - Uses `defaultOnce.Do` to lazily initialise `defaultReg = NewRegistry()` on first call.
     - Returns `defaultReg`.
   - `MustRegister(reg Registration)`:
     - Calls `Default().Register(reg)`. On non-nil error, panics with the error.
     - Doc-comment that this is intended to be called from widget package `init()` functions where a registration error is a build-time bug.

### Tests

5. Create `internal/widget/registry_test.go` with the following tests. Use only the standard `testing` package, no testify.

   Test scaffolding helper:
   ```go
   // newTestRegistration returns a minimal valid Registration for tests.
   // Callers may override fields after the call.
   func newTestRegistration(t *testing.T, typeName string) widget.Registration {
       t.Helper()
       return widget.Registration{
           Type:           typeName,
           DisplayName:    typeName,
           Description:    "test widget",
           New:            func() widget.Widget { return &fakeWidget{} },
           DefaultConfig:  func() []byte { return []byte(`{}`) },
           ValidateConfig: func(raw []byte) (widget.Instance, error) {
               return widget.Instance{Type: typeName}, nil
           },
       }
   }

   // fakeWidget is a stub Widget implementation used in registry tests. Its
   // Render returns a templ.Component that renders an empty string.
   type fakeWidget struct{}
   func (*fakeWidget) Render(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component {
       return templ.Raw("") // OR a small inline component; either is fine
   }
   ```
   (Use whatever templ-component construction is idiomatic in the codebase. Read `views/demo.templ` and `views/demo.go` for examples; an inline `templ.ComponentFunc` may also be fine -- the test just needs a non-nil Component.)

6. Tests required (each uses `NewRegistry()`, never the global):
   - **Register-then-Get**: register a widget with type `"alpha"`, call `Get("alpha")`, assert `ok == true` and `reg.Type == "alpha"`.
   - **Get unknown type**: call `Get("nonexistent")` on a fresh registry, assert `ok == false` and the returned Registration is the zero value.
   - **Duplicate registration rejected**: register `"alpha"` twice. The second call returns a non-nil error whose message contains `"alpha"` and the word `"already"`.
   - **Register rejects empty Type**: `reg.Type = ""`. The call returns a non-nil error mentioning `"Type"`.
   - **Register rejects nil New**: a registration with `New: nil` returns a non-nil error mentioning `"New"`.
   - **Register rejects nil DefaultConfig**: a registration with `DefaultConfig: nil` returns a non-nil error mentioning `"DefaultConfig"`.
   - **Register rejects nil ValidateConfig**: a registration with `ValidateConfig: nil` returns a non-nil error mentioning `"ValidateConfig"`.
   - **List ordering**: register types `"c"`, `"a"`, `"b"` in that order. Assert `List()` returns them in order `a`, `b`, `c`.
   - **List on empty registry**: assert `List()` returns a non-nil, len-0 slice.
   - **Validate unknown type**: call `Validate("nope", []byte("{}"))` on a fresh registry. Assert error is non-nil and message contains `"unknown type"` and `"nope"`.
   - **Validate forwards errors**: register a widget whose `ValidateConfig` returns `errors.New("boom")`. Call `Validate("alpha", ...)`, assert the returned error is the validator's error (use `errors.Is` if the architecture wraps; the architecture above uses direct return so `==` or `errors.Is` both work).
   - **Render unknown type**: call `Render(ctx, "nope", []byte("{}"), themes.Theme{})`. Assert returned component is nil and the error mentions `"unknown type"`.
   - **Render with failing validator**: register a widget whose `ValidateConfig` returns `errors.New("bad config")`. Call `Render`. Assert nil component, non-nil error wrapping the validator's error (`errors.Is` finds `"bad config"`'s sentinel, OR the message contains `"bad config"`).
   - **Render happy path**: register a widget whose `ValidateConfig` succeeds and whose `New().Render(...)` returns a component. Assert non-nil component and nil error. The test does NOT need to actually render the component to bytes; verifying it's non-nil is enough at this layer.
   - **Render sets Instance.Type**: register a widget whose `ValidateConfig` returns `widget.Instance{Type: ""}` (deliberately blank), and whose `New().Render(...)` records the Instance.Type it was given on a captured variable. Assert the captured Type equals the type name passed to `Render`. (Verifies the registry's "set inst.Type" guard.)
   - **Concurrent Get is race-free**: launch N goroutines (e.g., 64) each calling `Get("alpha")` 1000 times, after the registration has happened. Run with `go test -race ./internal/widget/...`. The race detector must be clean.

7. Test the `Default()` singleton in a separate file or test:
   - **Default returns the same registry across calls**: `widget.Default() == widget.Default()` (pointer equality).
   - **Default starts empty in test isolation**: do NOT assert this -- by the time tests run, other test files in the same `_test` binary may have imported a widget package and triggered its init(). Asserting "empty" couples the test to import order. Skip.
   - **MustRegister panics on duplicate**: register a unique type via `MustRegister`, then attempt to `MustRegister` the same type again on the SAME default registry. Recover from the panic, assert non-nil. To avoid polluting the global between tests: pick a clearly-test-only type name (e.g., `"__widget_test_dup__"`) AND use a sub-test that runs first registration via `widget.Default().Register(...)` (returns error), then asserts a follow-up `MustRegister` panics. This avoids leaving a duplicate registration in the global. Alternative: skip this test; the panic-on-error path is trivial and is exercised by reading the code. Architect's guidance: ship the simple version that uses a unique test-only type and recovers the panic; then call `widget.Default().Register(reg)` first (returns nil), then `widget.Default().Register(reg)` again (returns an error, but doesn't panic), then assert `MustRegister(reg)` panics. The MustRegister call panics with the error returned by Register. This is a 10-line test.

8. Skip integration with the rest of the codebase. This task touches no `main.go`, no `views/`, no other package. The next task wires the package into production.

## Acceptance Criteria

From SPEC-005:

- [ ] AC-1: `internal/widget/widget.go` defines the `Widget` interface with exactly one method `Render(ctx context.Context, instance Instance, theme themes.Theme) templ.Component`.
- [ ] AC-2: `internal/widget/registration.go` defines the `Registration` struct with at least the fields `Type`, `DisplayName`, `Description`, `New`, `DefaultConfig`, `ValidateConfig`.
- [ ] AC-3: `internal/widget/widget.go` (or `instance.go`) defines the `Instance` struct with `ID`, `Type`, `Config`.
- [ ] AC-4: `NewRegistry().Register(validReg)` returns nil.
- [ ] AC-5: A second `Register` with the same `Type` returns a non-nil error containing the duplicate type name and the word `"already"`.
- [ ] AC-6: `Get("nonexistent-type")` returns a zero Registration and `false`.
- [ ] AC-7: `Get("alpha")` after registering `"alpha"` returns the registration and `true`.
- [ ] AC-8: `List()` returns registrations sorted alphabetically by `Type`.
- [ ] AC-9: `Render(ctx, "alpha", validJSON, theme)` returns a non-nil component and nil error when `"alpha"` is registered with a passing validator.
- [ ] AC-10: `Render(ctx, "alpha", invalidJSON, theme)` returns a nil component and a non-nil error wrapping the validator's error.
- [ ] AC-11: `Render(ctx, "nonexistent", anyJSON, theme)` returns a nil component and a non-nil error mentioning `"unknown type"`.
- [ ] AC-12: Concurrent `Get` calls under `go test -race` produce no data races.

## Skills to Use

- `add-store` -- the `internal/themes/` shape (constructor, mutex-guarded read-mostly state, exported sentinel-style APIs) is the closest existing template, even though `widget` is not a database-backed package.
- `green-bar` -- run before marking complete; include `-race` for the registry tests.

## Test Requirements

1. All tests in `internal/widget/registry_test.go` use `widget.NewRegistry()` to build a fresh registry per test. The global singleton (`widget.Default()`) is touched ONLY in the dedicated `Default()` tests described above.

2. Use table-driven tests for the validator-rejection branches of `Register` (one row per "missing field" case). Each row has a `name`, the registration to register, and a `wantErrSubstring`.

3. The concurrency test uses `sync.WaitGroup` plus N goroutines to call `Get`. Race-free is the assertion; there is no behavioural property to check beyond "tests pass under `-race`".

4. Tests follow `.claude/rules/testing.md`: each test has a single, clear failure reason; the test name documents the invariant; tests test contracts not implementations.

5. Do NOT assert sqlc-generated method signatures (there are none here -- this is a pure-Go package), do NOT assert struct field order, do NOT lock the test to a specific error message word-for-word beyond what the AC requires (use `strings.Contains` for substring matches).

6. Run `go test -race ./internal/widget/...` and ensure it passes.

## Definition of Done

- [ ] `internal/widget/widget.go` created with the `Widget` interface and `Instance` struct.
- [ ] `internal/widget/registration.go` created with the `Registration` struct.
- [ ] `internal/widget/registry.go` created with the `Registry` type and all five methods (`Register`, `Get`, `List`, `Validate`, `Render`).
- [ ] `internal/widget/default.go` created with the `Default()` singleton accessor and `MustRegister` helper.
- [ ] `internal/widget/registry_test.go` covers every AC (1-12), all validator branches, list ordering, and concurrent reads.
- [ ] All exported identifiers have doc comments.
- [ ] `go test -race ./internal/widget/...` passes.
- [ ] green-bar passes (`gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./...`).
- [ ] No new third-party dependencies (the package's only non-stdlib imports are `internal/themes` and `github.com/a-h/templ`, both already in `go.mod`).
- [ ] No HTTP handlers, no templ files, no `views/` changes, no `main.go` changes -- TASK-020 owns those.
