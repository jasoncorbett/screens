---
id: TASK-020
title: "Placeholder text widget, registry wiring through main.go and views.Deps"
spec: SPEC-005
arch: ARCH-005
status: review
priority: p0
prerequisites: [TASK-019]
skills: [add-widget, green-bar]
created: 2026-04-30
author: architect
---

# TASK-020: Placeholder text widget, registry wiring through main.go and views.Deps

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Validate the widget contract delivered in TASK-019 by implementing the placeholder `text` widget and wiring the registry into the application's startup path. This task ships:

1. The `internal/widget/text/` package: a `Config` struct, a `validate` function, a `defaultConfig` provider, a `Render` method that returns a templ component, a `Registration()` factory, and an `init()` function that registers the widget with the global singleton via `widget.MustRegister`.
2. The `text.templ` file with a `textComponent(body string)` templ that renders a single themed `<div class="widget widget-text">` carrying the configured text.
3. A blank import of `internal/widget/text` in `main.go` so that importing the binary registers the widget.
4. A new `Widgets *widget.Registry` field on `views.Deps`, threaded through `views.AddRoutes` from `main.go` (`Widgets: widget.Default()`).
5. Tests that prove every AC for the text widget plus the wiring (`Default()` contains `text`, etc.).

This task is the contract validator. If the contract from TASK-019 is wrong, this task fails first.

## Context

- The widget interface, `Registration` struct, `Instance` carrier, and `Registry` (with `Default()` and `MustRegister`) are delivered by TASK-019. Read `internal/widget/widget.go`, `internal/widget/registration.go`, and `internal/widget/registry.go` before starting; the function signatures the text widget plugs into are defined there verbatim.
- The text widget's per-instance JSON shape is `{"text": "..."}`. Its single field is `Text`, capped at 4096 characters, must be non-empty after trimming whitespace.
- The text widget is stateless; one global singleton struct is enough. `Registration().New` returns a pointer to that singleton, not a new instance per call.
- The text widget's renderer styles its output via theme CSS variables -- it MUST NOT inline `style="color: ..."` etc. The CSS class names `widget` and `widget-text` are styling hooks for `static/css/app.css` (which Screen Display will tighten later); do NOT add a new CSS file in this task.
- The blank import in `main.go` is the wiring primitive: `import _ "github.com/jasoncorbett/screens/internal/widget/text"`. The `init()` inside the `text` package runs at import time and self-registers with `widget.MustRegister`.
- `views.Deps` lives in `views/routes.go`. Add the `Widgets *widget.Registry` field after `Themes` (mirroring the alphabetic-by-domain ordering used elsewhere in the struct -- the existing struct currently lists fields in initialisation order; either keep that or sort. Architect's guidance: append `Widgets` after `Themes` to match the existing flow).
- No view in this spec consumes `Deps.Widgets`. Tests for existing views (`views/themes_test.go`, etc.) MUST continue to pass without setting the field. Confirm by running the full test suite.
- The `add-widget` skill predates the formal widget interface; it documents widget conventions but does NOT specify the file layout this task uses. Read it for the per-widget patterns (HTML escape, theme inheritance, no global JS), then follow the architecture doc for the file layout.

### Files to Read Before Starting

- `.claude/rules/go-style.md`
- `.claude/rules/http.md` -- for the `views.Deps` change pattern.
- `.claude/rules/testing.md`
- `.claude/skills/add-widget/SKILL.md`
- `.claude/skills/green-bar/SKILL.md`
- `internal/widget/widget.go` -- the `Widget` interface definition (post TASK-019).
- `internal/widget/registration.go` -- the `Registration` struct definition (post TASK-019).
- `internal/widget/registry.go` -- the `Registry` methods, especially `Render` and `Validate` (post TASK-019).
- `internal/widget/default.go` -- `Default()` and `MustRegister` (post TASK-019).
- `internal/themes/theme.go` -- the `Theme` struct that the renderer receives.
- `views/demo.templ` and `views/demo.go` -- minimal templ + handler shape for reference.
- `views/routes.go` -- where the `Widgets` field is added on `Deps`.
- `main.go` -- where the blank import goes and where `widget.Default()` is threaded into `views.AddRoutes`.
- `docs/plans/architecture/phase-2-display/arch-widget-interface.md` -- "Component Design > Package Layout" and "main.go Wiring Changes" sections.
- `docs/plans/specs/phase-2-display/spec-widget-interface.md` -- "Functional Requirements > Placeholder Text Widget" and "Wiring" sections.

## Requirements

### Text widget package

1. Create `internal/widget/text/text.go` containing:
   - The `Type` constant: `const Type = "text"`.
   - The `MaxTextLength` constant: `const MaxTextLength = 4096`.
   - The `Config` struct: a single field `Text string \`json:"text"\``. Doc-comment.
   - A package-private `widgetImpl` struct (no exported fields). Doc-comment.
   - A package-private `singleton = &widgetImpl{}` value.
   - The method `func (w *widgetImpl) Render(ctx context.Context, instance widget.Instance, theme themes.Theme) templ.Component`:
     - Type-asserts `cfg, _ := instance.Config.(Config)`. The validator guarantees the type, so the comma-ok is defensive; either branch returns `textComponent(cfg.Text)` (the empty-text case is unreachable in production but the templ handles it gracefully).
     - Calls `textComponent(cfg.Text)` and returns the result.
     - Note the `theme` parameter is unused at the Go layer; the active theme drives the rendered HTML's CSS variables transparently via the `<style>` block Screen Display will inject. Do NOT name the parameter `_` -- keep it named `theme` so future widgets that DO consult fields of the theme have a clean diff.
   - The package-private `validate(raw []byte) (widget.Instance, error)` function:
     - `var cfg Config`. `if err := json.Unmarshal(raw, &cfg); err != nil` return `widget.Instance{}, fmt.Errorf("text widget: invalid JSON: %w", err)`.
     - `cfg.Text = strings.TrimSpace(cfg.Text)`.
     - If `cfg.Text == ""`, return `widget.Instance{}, fmt.Errorf("text widget: text must not be empty or whitespace-only")`.
     - If `len(cfg.Text) > MaxTextLength`, return `widget.Instance{}, fmt.Errorf("text widget: text must be %d characters or fewer", MaxTextLength)`.
     - Return `widget.Instance{Type: Type, Config: cfg}, nil`.
   - The package-private `defaultConfig() []byte` function:
     - `b, _ := json.Marshal(Config{Text: "Hello, screens"})`. (Marshalling a known-valid struct cannot fail; ignoring the error is safe.)
     - Return `b`.
   - The exported `Registration() widget.Registration` factory:
     - Returns a `widget.Registration` populated with `Type`, `DisplayName: "Text"`, `Description: "Display a configurable block of text."`, `New: func() widget.Widget { return singleton }`, `DefaultConfig: defaultConfig`, `ValidateConfig: validate`.
   - The `init()` function: `widget.MustRegister(Registration())`.

2. Create `internal/widget/text/text.templ` containing:
   ```go
   package text

   templ textComponent(body string) {
       <div class="widget widget-text">
           { body }
       </div>
   }
   ```
   The `{ body }` interpolation invokes templ's automatic HTML escape; the renderer relies on this for AC-22.

3. Run `templ generate` to compile the `.templ` file. Verify `internal/widget/text/text_templ.go` is created and compiles.

### Tests

4. Create `internal/widget/text/text_test.go` with the following tests (no testify, standard `testing` package only):

   - **validate happy path** (table-driven):
     | input | want_text |
     | `{"text":"hello"}` | `"hello"` |
     | `{"text":"  hello  "}` | `"hello"` (trimmed) |
     | `{"text":"line\nbreak"}` | `"line\nbreak"` (newlines preserved -- the cap is bytes, not lines) |

   - **validate rejection** (table-driven, each asserts a non-nil error and a substring of the error message):
     | input | want_err_substring |
     | `not-json` | `"JSON"` or `"invalid JSON"` |
     | `{"text":""}` | `"empty"` |
     | `{"text":"   "}` | `"empty"` |
     | `{"text":"<4097-char string>"}` | `"4096"` |

   - **defaultConfig validates**: `cfg := defaultConfig(); inst, err := validate(cfg)`. Assert `err == nil` and `inst.Type == Type` and `inst.Config.(Config).Text != ""`. (The default-must-validate property; AC-20.)

   - **Render produces a non-nil component**: build a Registration via `Registration()`, call `New()`, call `Render(context.Background(), widget.Instance{Type: Type, Config: Config{Text: "hello"}}, themes.Theme{})`. Render the returned component to a buffer using `component.Render(ctx, &buf)`. Assert `buf.String()` contains the literal `"hello"`. (AC-21.)

   - **Render HTML-escapes**: Same as above with `Text: "<script>alert('x')</script>"`. Assert the buffer:
     - Contains the substring `"&lt;script&gt;"` (or `"&#34;"` style HTML-escape entities).
     - Does NOT contain a raw `"<script>"` substring.
     (AC-22.)

   - **Render does not inline colors**: Same as above with any valid Text. Assert the buffer does NOT contain the substring `style="`. (AC-23.)

   - **Render through the registry**: build a fresh `widget.NewRegistry()`. Register the text widget via `r.Register(text.Registration())`. Call `r.Render(ctx, "text", []byte(\`{"text":"hi"}\`), themes.Theme{})`. Assert non-nil component, nil error, and the rendered output contains `"hi"`.

5. Create `internal/widget/text/registration_test.go` (or merge into `text_test.go`) testing the global singleton:

   - **Default contains text after import**: this test file already imports `internal/widget/text`, so the `init()` has run. Call `widget.Default().Get("text")`. Assert `ok == true` and `reg.Type == "text"`. (AC-24.)
   - **Default's text registration matches**: Same as above; assert `reg.DisplayName == "Text"` and `reg.Description != ""` and all four function fields are non-nil.

### main.go and views.Deps wiring

6. In `main.go`:
   - Add the import: `import "github.com/jasoncorbett/screens/internal/widget"` (regular import; `widget.Default()` is called below).
   - Add the blank import: `import _ "github.com/jasoncorbett/screens/internal/widget/text"` (registers the placeholder text widget at startup).
   - In the `views.AddRoutes(mux, &views.Deps{...})` call, add the field `Widgets: widget.Default(),`. Place it after `Themes: themesSvc,` (mirroring the field-ordering convention).

7. In `views/routes.go`:
   - Add the import: `"github.com/jasoncorbett/screens/internal/widget"`.
   - Add the field `Widgets *widget.Registry` to `Deps` struct after `Themes`.
   - No handler in this task consumes `deps.Widgets`. The field is for Screen Display; pre-wiring keeps that diff minimal.

8. Verify existing view tests still pass without setting `Deps.Widgets`. Run `go test ./views/...` and confirm. Tests that construct `Deps{}` literals SHOULD continue to compile because `Widgets` is a pointer with a nil zero value; if any test asserts strict struct equality on `Deps`, accept that this task's diff includes updating that test (none are expected to do so based on the read of `views/`).

### Integration test (recommended; counts toward AC-25)

9. Create `views/widget_wiring_test.go` (or place in any package that imports both `widget` and the text widget). Test:
   - Verifies the whole binary's wiring: import `internal/widget` and `_ "internal/widget/text"`. Call `widget.Default().List()`. Assert the slice contains a Registration with `Type == "text"`.
   - This is a stand-in for "the binary built with main.go has the text widget registered" -- since `init()` runs at import time, importing both packages in the test file is equivalent.

## Acceptance Criteria

From SPEC-005:

- [ ] AC-13: `widget.Default().List()` (in a process / test file that imports `internal/widget/text`) contains a Registration with `Type == "text"`.
- [ ] AC-14: `text.validate([]byte(\`{"text":"hello"}\`))` returns an Instance whose Config (after type assertion) has `Text == "hello"` and a nil error.
- [ ] AC-15: `text.validate([]byte(\`{"text":"  hello  "}\`))` returns Instance with `Text == "hello"`.
- [ ] AC-16: `text.validate([]byte(\`{"text":""}\`))` returns a non-nil error.
- [ ] AC-17: `text.validate([]byte(\`{"text":"   "}\`))` returns a non-nil error.
- [ ] AC-18: `text.validate([]byte("not-json"))` returns a non-nil error mentioning JSON.
- [ ] AC-19: `text.validate` with a 4097-character text returns a non-nil error mentioning the length cap.
- [ ] AC-20: `text.defaultConfig()` returns bytes that pass `text.validate` without error.
- [ ] AC-21: Rendering the text widget with `Text == "hello"` and writing the component to a buffer produces output containing the literal `"hello"`.
- [ ] AC-22: Rendering with `Text == "<script>"` produces HTML-escaped output (`&lt;script&gt;`) and no raw `<script>` substring.
- [ ] AC-23: Rendering produces no `style="..."` inline color attributes.
- [ ] AC-24: After importing `internal/widget/text`, `widget.Default().Get("text")` returns a registration with `ok == true`.
- [ ] AC-25: Tests confirm `widget.Default().List()` from a process that imports `_ "internal/widget/text"` includes a `text` Registration.
- [ ] AC-26: No new HTTP routes are registered. (Verified by reading `internal/widget/text/` for handler files -- there should be none.)
- [ ] AC-27: Existing view tests continue to pass without setting `Deps.Widgets`.

## Skills to Use

- `add-widget` -- for the per-widget conventions (HTML escape, theme inheritance, no global JS, package-per-widget layout).
- `green-bar` -- run before marking complete. Run `templ generate` first to compile the templ file.

## Test Requirements

1. All text-widget tests (`internal/widget/text/text_test.go`) construct registrations / widgets directly via the package; they do NOT need a `*Registry` for most assertions. The "Render through the registry" test is the one exception.

2. Use table-driven tests for the validator's input cases (one row per accepted form, one row per rejected form). Table tests follow the project pattern: a slice of structs with `name`, `input`, and either `want_text` (success cases) or `want_err_substring` (failure cases).

3. The "render produces output" tests use `templ.Component.Render(ctx, &buf)` (where `buf` is a `bytes.Buffer`). The package import is `bytes`; templ's `Component.Render` writes to an `io.Writer`.

4. The "Default contains text" tests rely on the test file importing the `text` package, which triggers `init()`. The test file's package declaration is `package text` (or `package text_test` if the test wants to consume the package as an external caller -- both are acceptable).

5. The integration test in `views/widget_wiring_test.go` is the "main.go is wired correctly" stand-in. Without this test, verifying AC-25 requires running the binary and inspecting the registry, which is heavier than necessary for CI. The test imports `internal/widget` and `_ "internal/widget/text"` (the same imports `main.go` will use), then calls `widget.Default().List()`.

6. Tests follow `.claude/rules/testing.md`: each test has a single failure reason; tests test contracts not implementations.

7. Run `go test -race ./internal/widget/...` to ensure the text widget's renderer is race-free (it should be -- the singleton is read-only and templ components are stateless).

## Definition of Done

- [ ] `internal/widget/text/text.go` created with `Type`, `MaxTextLength`, `Config`, `widgetImpl`, `singleton`, `Render`, `validate`, `defaultConfig`, `Registration`, and `init()`.
- [ ] `internal/widget/text/text.templ` created with `textComponent(body string)`.
- [ ] `templ generate` produces `internal/widget/text/text_templ.go` and the package compiles.
- [ ] `internal/widget/text/text_test.go` covers validator (happy + rejection), default-validates, render-contains-text, render-html-escapes, render-no-inline-colors, render-through-registry, default-contains-text after import.
- [ ] `views/routes.go` adds `Widgets *widget.Registry` to `Deps`.
- [ ] `main.go` blank-imports `internal/widget/text` and passes `widget.Default()` as `Widgets:` in the `views.Deps` literal.
- [ ] `views/widget_wiring_test.go` (or equivalent) verifies `widget.Default().List()` contains `text` after the relevant imports.
- [ ] All ACs (13-25, 26-27) pass.
- [ ] green-bar passes (gofmt, vet, build, test). Run `templ generate` before `green-bar`.
- [ ] `go test -race ./internal/widget/...` passes.
- [ ] No new third-party dependencies.
- [ ] No new HTTP routes registered. No new CSS file. No `static/css/widgets/` additions in this task (Screen Display owns visual polish).
