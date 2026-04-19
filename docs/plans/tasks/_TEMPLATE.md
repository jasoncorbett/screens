---
id: TASK-NNN
title: "<Concise task description>"
spec: SPEC-XXX
arch: ARCH-XXX
status: ready | in-progress | review | done | blocked
priority: p0 | p1 | p2
prerequisites: []
skills: []
created: YYYY-MM-DD
author: architect
---

# TASK-NNN: <Concise task description>

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

One paragraph: what this task produces and why. Reference the spec and architecture doc for full context.

## Context

What already exists that this task builds on. Name specific files, functions, or patterns.

### Files to Read Before Starting

- `.claude/rules/<relevant>.md`
- `internal/<package>/<file>.go` -- existing pattern to follow
- `docs/plans/architecture/<phase>/arch-<name>.md` -- section X
- (prerequisite task outputs, if any)

## Requirements

Numbered list of exactly what must be implemented:

1. Create `internal/<package>/<file>.go` with ...
2. Add struct `Foo` with fields ...
3. Implement function `Bar(ctx context.Context, ...) error` that ...
4. Register endpoint `GET /api/v1/...` in `api/v1/routes.go`

## Acceptance Criteria

From the spec, filtered to just what this task covers:

- [ ] AC-1: ...
- [ ] AC-2: ...

## Skills to Use

- `add-endpoint` -- for creating HTTP handlers
- `green-bar` -- run before marking complete

## Test Requirements

What tests to write and what they verify:

1. Test that `Foo()` returns ... when given ...
2. Test that `GET /api/v1/...` returns 200 with ...
3. (Reference `.claude/rules/testing.md` for conventions)

## Definition of Done

- [ ] All requirements implemented
- [ ] All acceptance criteria tests pass
- [ ] green-bar passes (gofmt, vet, build, test)
- [ ] No new third-party dependencies added
- [ ] Code follows `.claude/rules/go-style.md`
