---
id: TASK-001
title: "Add database configuration"
spec: SPEC-001
arch: ARCH-001
status: done
priority: p0
prerequisites: []
skills: [add-config, green-bar]
created: 2026-04-18
author: architect
---

# TASK-001: Add database configuration

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Add database configuration fields to the existing `internal/config/config.go` so that the storage engine (TASK-002) can receive its settings through the standard config pattern. This is the first task in the storage engine feature and has no prerequisites.

## Context

The project's configuration pattern is established in `internal/config/config.go`. All settings are parsed from environment variables using helper functions (`env`, `envInt`, `envDuration`, `envBool`) with sensible defaults. The `Config` struct uses sub-structs (`HTTPConfig`, `LogConfig`) to group related settings. Validation accumulates errors in a slice.

### Files to Read Before Starting

- `.claude/rules/config.md` -- configuration conventions
- `.claude/rules/go-style.md` -- Go style conventions
- `.claude/rules/testing.md` -- test conventions
- `internal/config/config.go` -- existing config pattern to extend
- `docs/plans/architecture/phase-1-foundation/arch-storage-engine.md` -- "internal/config/config.go additions" section

## Requirements

1. Add a `DBConfig` sub-struct to `internal/config/config.go` with the following fields:
   - `Path` (string) -- path to the SQLite database file
   - `MaxOpenConns` (int) -- maximum number of open connections
   - `MaxIdleConns` (int) -- maximum number of idle connections
   - `ConnMaxLifetime` (time.Duration) -- maximum connection lifetime

2. Add a `DB DBConfig` field to the `Config` struct.

3. Parse the following environment variables in `Load()` using the existing helpers:
   - `DB_PATH` with default `"screens.db"`
   - `DB_MAX_OPEN_CONNS` with default `1`
   - `DB_MAX_IDLE_CONNS` with default `1`
   - `DB_CONN_MAX_LIFETIME` with default `0` (no limit)

4. Add validation in `Config.Validate()`:
   - `DB_PATH` must not be empty -- append `"DB_PATH must not be empty"` to the errors slice.

5. Update the README.md configuration table with the new environment variables. If README.md does not exist, create it with at minimum a configuration table that includes all existing and new env vars.

## Acceptance Criteria

- [ ] AC-8: When `DB_PATH` is set to an empty string, then config validation fails with a descriptive error.
- [ ] AC (partial): The `DBConfig` struct exists and is populated by `Load()` with correct defaults.

## Skills to Use

- `add-config` -- follow this skill's steps for adding the sub-struct, parsing, validation, and README update
- `green-bar` -- run before marking complete

## Test Requirements

1. Test that `Load()` returns correct default values for all DB fields when no env vars are set. Construct the expected `Config` value directly.
2. Test that `Validate()` returns an error when `DB.Path` is empty.
3. Test that `Validate()` passes when `DB.Path` is non-empty.
4. Use `t.Setenv` to test custom env var values for `DB_PATH` and `DB_MAX_OPEN_CONNS`.
5. Follow the table-driven test pattern from `.claude/rules/testing.md`.

## Definition of Done

- [ ] `DBConfig` sub-struct added with all four fields
- [ ] `Config.DB` field added and populated in `Load()`
- [ ] Validation rejects empty `DB_PATH`
- [ ] README.md configuration table updated
- [ ] Tests pass for default values and validation
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies added
- [ ] Code follows `.claude/rules/go-style.md`
