---
id: TASK-028
title: "LIVE_RELOAD_SECONDS config setting + Deps wiring + README documentation"
spec: SPEC-007
arch: ARCH-007
status: ready
priority: p0
prerequisites: []
skills: [add-config, green-bar]
created: 2026-05-25
author: architect
---

# TASK-028: LIVE_RELOAD_SECONDS config setting + Deps wiring + README documentation

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add the `LIVE_RELOAD_SECONDS` env-driven config setting that controls the device's `<meta http-equiv="refresh">` interval. The setting is parsed in `internal/config/config.go`, validated (`>= 30`), threaded through `views.Deps`, wired in `main.go`, and documented in the top-level `README.md` configuration table. This task ships nothing user-facing; it is the config prerequisite for TASK-030 (the device render handler) which consumes the value.

## Context

- All env-driven configuration flows through `internal/config/config.go`. The existing `HTTPConfig` struct already holds HTTP-server-related settings (`Host`, `Port`, `ReadTimeout`, `WriteTimeout`, `ShutdownTimeout`). `LIVE_RELOAD_SECONDS` is a render-time interval served via HTTP, so `HTTPConfig` is its natural home.
- The existing helpers `env`, `envInt`, `envDuration`, `envBool` handle parsing.
- `Config.Validate()` accumulates errors via an `errs` slice; the existing pattern is `if c.HTTP.Port < 1 || c.HTTP.Port > 65535 { errs = append(errs, ...) }`. Mirror that for the new check.
- `views.Deps` is the dependency-injection struct passed to `views.AddRoutes`. The render handler (TASK-030) consumes the new value via `deps.LiveReloadSeconds`.
- The top-level `README.md` has a configuration table; add a row for the new setting.

### Files to Read Before Starting

- `.claude/rules/config.md` -- env-var conventions.
- `.claude/rules/go-style.md` -- stdlib-only.
- `.claude/skills/add-config/SKILL.md` -- the add-config workflow.
- `internal/config/config.go` -- the `Load()` and `Validate()` patterns to mirror.
- `views/routes.go` -- the `Deps` struct that gains the new field.
- `main.go` -- where `views.AddRoutes` is called with `&views.Deps{...}`.
- `README.md` -- the configuration table to extend.
- `docs/plans/specs/phase-2-display/spec-screen-display.md` -- requirement 25; AC-26, AC-27.
- `docs/plans/architecture/phase-2-display/arch-screen-display.md` -- "Configuration" section.

## Requirements

### Config field + parse

1. Edit `internal/config/config.go`:
   - Add `LiveReloadSeconds int` to `HTTPConfig` (placed after `ShutdownTimeout`).
   - In `Load()`, parse it in the `HTTPConfig{...}` block:
     ```go
     LiveReloadSeconds: envInt("LIVE_RELOAD_SECONDS", 60),
     ```
   - In `Validate()`, add the range check (alongside the existing HTTP_PORT range check):
     ```go
     if c.HTTP.LiveReloadSeconds < 30 {
         errs = append(errs, "LIVE_RELOAD_SECONDS must be at least 30 seconds")
     }
     ```
   - Add the new field to the `String()` method so debug logging stays useful (mirror the existing field interleaving).

### Deps field

2. Edit `views/routes.go`:
   - Add `LiveReloadSeconds int` to the `Deps` struct (placed near the existing field group; group by topic if needed, but a single trailing field is fine for one new value).

### main.go wiring

3. Edit `main.go` to thread the value through:
   ```go
   views.AddRoutes(mux, &views.Deps{
       ...
       Screens:           screensSvc,
       LiveReloadSeconds: cfg.HTTP.LiveReloadSeconds,
   })
   ```

### README documentation

4. Edit the configuration table in `README.md` to add a row for `LIVE_RELOAD_SECONDS`. The columns to populate are whatever the existing table uses (typically: name, default, description). The description should be one sentence, e.g.: "Interval in seconds between full-page reloads on the device renderer. Minimum 30 seconds. Default 60."

5. If the README has a separate section explaining the device-rendering subsystem, add a one-line note linking the env var to its effect; otherwise the table entry is sufficient.

## Acceptance Criteria

From SPEC-007:

- [ ] AC-26: When `LIVE_RELOAD_SECONDS=45` is set on startup, the config validation accepts it (it is >= 30).
- [ ] AC-27: When `LIVE_RELOAD_SECONDS=10` is set on startup, the config validation rejects it (below 30s floor) and the service fails to start.

Plus the task-internal ACs:

- [ ] AC-T1: When `LIVE_RELOAD_SECONDS` is not set, `cfg.HTTP.LiveReloadSeconds == 60` after `Load()`.
- [ ] AC-T2: `views.Deps.LiveReloadSeconds` receives the value from `cfg.HTTP.LiveReloadSeconds` in main.go.

## Skills to Use

- `add-config` -- the standard pattern for adding an env-driven config.
- `green-bar` -- run before marking review.

## Test Requirements

Tests live in `internal/config/config_test.go` (or a new file if patterns warrant). Mirror the existing tests' shape (typically table-driven cases for `Load()` and `Validate()` with `t.Setenv` for env-var-driven scenarios).

1. **Default**: with no env var set, after `Load()`, `cfg.HTTP.LiveReloadSeconds == 60` and `cfg.Validate() == nil`.

2. **Accepts valid value**: set `LIVE_RELOAD_SECONDS=45` via `t.Setenv`, call `Load()`, assert `cfg.HTTP.LiveReloadSeconds == 45` and validation passes.

3. **Rejects below floor**: set `LIVE_RELOAD_SECONDS=10`, call `Load()`, assert `cfg.Validate()` returns a non-nil error mentioning "LIVE_RELOAD_SECONDS" and "30".

4. **Accepts boundary**: `LIVE_RELOAD_SECONDS=30` passes validation; `LIVE_RELOAD_SECONDS=29` fails.

Existing config tests must continue to pass.

## Definition of Done

- [ ] `internal/config/config.go` has the new field, parser line, validation, and `String()` interleaving.
- [ ] `views/routes.go::Deps` has the new field.
- [ ] `main.go` threads the value through.
- [ ] `README.md` configuration table includes the new entry.
- [ ] All acceptance criteria tests pass.
- [ ] green-bar passes.
- [ ] No new third-party dependencies.
