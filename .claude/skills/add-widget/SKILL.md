---
name: add-widget
description: Scaffold a new widget type following the project's widget interface. Use when the user asks to add a widget (time, weather, calendar, etc.). Creates the widget implementation, templ component, registry entry, and tests. Read the widget architecture doc and .claude/rules/testing.md before applying.
---

# Add a widget

This skill scaffolds a new widget type that plugs into the widget system established in Phase 2. Read the widget interface architecture doc before starting.

## Before starting

1. Read the widget interface definition in `internal/widget/widget.go` (or wherever the interface is defined).
2. Read an existing widget implementation for the pattern to follow.
3. Read the widget registry to understand how widgets register themselves.

## Files to create or edit

1. **Widget implementation** -- `internal/widget/<type>.go`:
   - Implement the `Widget` interface (defined by the architecture).
   - Parse widget-specific configuration from JSON (`encoding/json`).
   - Validate configuration in a `Validate()` method or during construction.
   - If the widget fetches external data:
     - Accept an `*http.Client` (with timeouts set) via constructor or config.
     - Use `context.Context` for all external calls.
     - Handle errors gracefully -- display a fallback state, don't crash the page.
     - Consider caching with a TTL to avoid hammering external APIs.

2. **Templ component** -- `views/widgets/<type>.templ`:
   - Define a templ component that renders the widget's visual output.
   - Accept the widget's data/state as parameters.
   - Use the screen's theme CSS variables for styling.
   - Keep the component self-contained -- no global JS state.

3. **Registry entry** -- register the widget type:
   - Use an `init()` function to register with the widget registry.
   - Provide a unique type string (e.g., `"time"`, `"weather"`).
   - Register the factory function that creates instances from config.

4. **Configuration** -- if the widget needs global settings (API keys, default values):
   - Use the `add-config` skill to add environment-driven settings.
   - Widget-specific config goes in a sub-struct (e.g., `config.WeatherConfig`).
   - Per-instance config is stored as JSON in the database.

5. **Test** -- `internal/widget/<type>_test.go`:
   - Test configuration parsing and validation (valid config, invalid config, missing fields).
   - Test render output contains expected elements.
   - If the widget fetches external data, test with a `httptest.NewServer` mock.
   - Test the fallback/error state.
   - Follow `.claude/rules/testing.md`.

## After creating files

1. Run `templ generate` to compile the `.templ` file.
2. Run the `green-bar` skill. All four checks must pass.

## Do not

- Do not import third-party widget or charting libraries without explicit approval.
- Do not make external API calls without `context.Context` and timeouts.
- Do not store secrets (API keys) in the database -- use environment config.
- Do not add global JavaScript -- use htmx attributes for interactivity.
