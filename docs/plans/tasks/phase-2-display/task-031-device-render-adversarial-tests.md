---
id: TASK-031
title: "Adversarial tests for the device render handler: deleted-screen race, widget-render failure, malformed config row, identity confusion, live-reload boundaries"
spec: SPEC-007
arch: ARCH-007
status: ready
priority: p0
prerequisites: [TASK-030]
skills: [green-bar]
created: 2026-05-25
author: architect
---

# TASK-031: Adversarial tests for the device render handler

## Process Reference

Follow `docs/plans/PROCESS.md` for workflow conventions.
Read `.claude/CLAUDE.md` and relevant `.claude/rules/` files before starting.

## Objective

Lock in the device render handler's failure-mode behaviour with adversarial tests in a new file `views/device_adversarial_test.go`. The handler must NOT panic, MUST NOT take down the whole page on a single widget failure, MUST handle a deleted-Screen race cleanly, and MUST NOT honour a device's `?screen=` query parameter for cross-Screen access. This task does not modify production code -- if a test reveals a real bug, fix it as a small in-task patch, but the focus is on test coverage of the failure paths that TASK-030 deliberately deferred.

## Context

- TASK-030 ships the happy-path tests (rendering, rotator, live-reload, empty states, picker). This task ships the adversarial / failure-mode tests.
- The render handler's failure modes (per SPEC-007):
  - **R10b**: Device with non-nil `screen_id` whose Screen no longer exists -> render unassigned placeholder + info log line.
  - **R19, R20**: Widget render error -> emit `widget-error` placeholder + warn log line; surrounding widgets render normally.
  - **R21**: One bad widget MUST NOT cascade-fail subsequent widgets on the same page or subsequent pages.
  - **Implicit**: a device with `?screen=<id>` must NOT preview that Screen (only admins get the `?screen=` query parameter honoured).
- Use `db.OpenTestDB(t)` for isolated DBs. The test widget registry is built via `widget.NewRegistry()`; register the `text` widget plus a test-only "always-fails" widget that returns an error from `ValidateConfig`. Some failure modes (malformed config row, dangling widget type) are best simulated via direct SQL writes to bypass the screens.Service write-time validation (the spec calls this out -- the render-time second layer is what we are testing).

### Files to Read Before Starting

- `.claude/rules/testing.md` -- especially the "earn their existence" section.
- `views/device.go` -- the handler under test (output of TASK-030).
- `views/device_test.go` -- the happy-path tests built by TASK-030; mirror their helper setup.
- `internal/widget/registry.go` -- the `Registry.Render` contract and error shapes.
- `internal/widget/text/text.go` -- the existing widget; use it as the "good" widget on a mixed page.
- `internal/screens/service.go` -- `GetScreenFull`, `ErrScreenNotFound`.
- `docs/plans/specs/phase-2-display/spec-screen-display.md` -- requirements 10, 19-22, 30; AC-11, AC-20, AC-21.
- `docs/plans/architecture/phase-2-display/arch-screen-display.md` -- "Component Design > Page body rendering" section.

## Requirements

Write the following tests in a new file `views/device_adversarial_test.go`. All tests use `httptest.NewRecorder` and the existing identity-injection helpers from `views/device_test.go` (TASK-030 should have established these).

### 1. Deleted-Screen race -> unassigned placeholder

Build a device, create a screen, assign the device to the screen, THEN delete the screen via raw SQL (NOT via `screens.Service.DeleteScreen` -- that uses the FK cascade which would set `devices.screen_id` to NULL and skip the test target). The goal is to exercise the path where `device.ScreenID != nil` but `GetScreenFull` returns `ErrScreenNotFound`.

```go
// pseudocode
_, err := sqlDB.Exec("DELETE FROM screens WHERE id = ?", screenID)
// device.ScreenID is still set to screenID (the FK SET NULL is bypassed because
//  we issued the raw DELETE without the screens.Service path, which leaves the
//  devices.screen_id value pointing at a now-nonexistent screen). Actually, no:
//  the FK is enforced at INSERT/UPDATE time, but ON DELETE SET NULL fires
//  REGARDLESS of which DELETE issued it (it is a DB-layer behaviour). Use a
//  raw UPDATE to write a fictitious screen_id directly instead.
_, err := sqlDB.Exec("UPDATE devices SET screen_id = ? WHERE id = ?",
    "deadbeef00000000000000000000dead", deviceID)
```

(The FK CHECK is enabled; the UPDATE will fail with a FK violation. To bypass: temporarily disable FK via `PRAGMA foreign_keys = OFF` inside the test, do the UPDATE, then re-enable. This is acceptable in tests because we are deliberately simulating a stale state that the FK is supposed to prevent in production.)

Assert:
- GET `/device/` as that device returns 200.
- Body contains "No screen assigned" (the unassigned placeholder).
- A `slog.Info` line with key "device screen missing" was emitted (can be verified by using `slog.New(slog.NewTextHandler(buf, nil))` and asserting on `buf.String()`).

Maps to: SPEC-007 R10b, AC-11.

### 2. Widget render error -> inline placeholder, surrounding widgets unaffected

Build a screen with one page that contains THREE widgets in this order: (1) a valid `text` widget, (2) a widget whose type is `text-broken` (registered in the test registry with a `ValidateConfig` that always returns an error), (3) another valid `text` widget. Render `/device/`. Assert:
- 200.
- Body contains the bodies of BOTH valid widgets.
- Body contains exactly one `widget-error` element (between them).
- The `widget-error` element references the type `text-broken`.
- A `slog.Warn` line with key "widget render failed" was emitted.

Maps to: SPEC-007 R19-R21, AC-20.

### 3. Unknown widget type -> inline placeholder

Add a widget instance via the registry-bypassing direct SQL: `INSERT INTO widget_instances (id, page_id, type, config, position) VALUES (?, ?, 'nonexistent', '{}', 1)`. This bypasses the `screens.Service.AddWidget` validator and lands a row with a type no registry can resolve. Render. Assert:
- 200.
- Body contains a `widget-error` placeholder.
- `slog.Warn` line emitted.

Maps to: SPEC-007 AC-20.

### 4. Malformed config bytes -> inline placeholder

Direct SQL: `INSERT INTO widget_instances (id, page_id, type, config, position) VALUES (?, ?, 'text', 'not-json', 1)`. The `text` widget's validator rejects malformed JSON. Render. Assert:
- 200.
- Body contains a `widget-error` placeholder.
- Body does NOT contain the literal `not-json`.
- `slog.Warn` line emitted with the type `text`.

Maps to: SPEC-007 AC-21.

### 5. Multiple bad widgets do not cascade-fail

Build a screen with TWO pages. Page 1 has one bad widget (type `text-broken`). Page 2 has one valid `text` widget. Render. Assert:
- 200.
- Body contains two `<section class="page-container">` elements.
- Page 1's section contains a `widget-error` placeholder.
- Page 2's section contains the valid `text` widget's body. (i.e., the failure on page 1 did not bleed into page 2.)

Maps to: SPEC-007 R21.

### 6. Device with `?screen=<id>` is ignored -> renders the device's actual assigned screen

Build a device assigned to Screen A. Create a Screen B with a different name. GET `/device/?screen=<Screen-B-id>` as that device. Assert:
- 200.
- Body contains Screen A's name (NOT Screen B's name).
- Body does NOT contain Screen B's name.

This pins the identity-vs-query-parameter precedence: a device cannot preview an arbitrary Screen by appending `?screen=`. (The handler branches on identity FIRST; admins are the only branch that reads `?screen=`.)

Maps to: SPEC-007 R11 / R12, security note in Non-Functional > Security.

### 7. Anonymous request -> 401 or login redirect

Send a GET to `/device/` with NO admin session cookie and NO device cookie / bearer. Through the `RequireAuth` middleware chain, this should produce either a 302 to `/admin/login` (HTML navigation) or a 401 (non-HTML). Assert one of those, and assert the body does NOT contain any Screen content.

This pins that the auth chain is correctly in front of the new render handler.

Maps to: SPEC-007 NFR Security; SPEC-003 AC-19 / AC-20 carried forward.

### 8. computeReload / computeRotation boundary cases (already in TASK-030 but pinned here for the failure dimension)

Table-driven test on the helper functions directly:

| rotation | reloadCfg | expected |
|----------|-----------|----------|
| 0        | 60        | 30 (rotation gets defaulted to 30; reload is max(30, 60) = 60; so actually 60) -- pick one and assert |
| 5        | 10        | 30 (reload below floor; rotation < floor; result = 30) |
| 3600     | 60        | 3600 (rotation wins) |
| 30       | 30        | 30 |
| 30       | 31        | 31 |

(The exact expected values follow from `computeReload(computeRotation(rot), cfg)`. Pick a stable set, assert.)

Maps to: SPEC-007 R16, AC-14, AC-26, AC-27.

### 9. Cache-Control headers on every successful render path

Parameterised: for each of (unassigned device render, device-with-screen render, admin picker render, admin preview render, admin screen-not-found render), assert `Cache-Control` and `Pragma` response headers are correctly set. Catches a regression where the header-set call is missed on one branch.

Maps to: SPEC-007 R17, AC-12.

### 10. Empty `?screen=` query parameter for admin -> picker

Admin GETs `/device/?screen=` (literally empty value). Assert the response is the picker page, not a "Screen not found" card. This is because `r.URL.Query().Get("screen") == ""` (an empty value); the handler's `if screenID == "" { render picker }` branch should match. Pins the parse behaviour.

## Acceptance Criteria

From SPEC-007:

- [ ] AC-11: Device whose Screen was deleted between requests -> 200 with "No screen assigned" placeholder.
- [ ] AC-20: Unknown widget type -> `widget-error` placeholder + surrounding widgets still render + `slog.Warn`.
- [ ] AC-21: Malformed config row -> `widget-error` placeholder + 200 response.

Plus the task-internal:

- [ ] AC-T1: Three widgets on a page (good, bad, good) -> both good ones render; bad one shows placeholder.
- [ ] AC-T2: Bad widget on page 1 does not affect rendering of page 2.
- [ ] AC-T3: Device with `?screen=<other-id>` query parameter -> renders the device's assigned Screen, NOT the queried one.
- [ ] AC-T4: Anonymous GET `/device/` -> 302 or 401 (not 200, not 500).
- [ ] AC-T5: Empty `?screen=` parameter for an admin -> picker page.
- [ ] AC-T6: All success-path response headers include `Cache-Control: no-store, must-revalidate` and `Pragma: no-cache`.

## Skills to Use

- `green-bar` -- run before marking review.

## Test Requirements

All new tests live in `views/device_adversarial_test.go`. Helpers:

- A `newTestRegistryWithBroken(t *testing.T) *widget.Registry` helper that builds a fresh registry, registers the `text` widget AND a `text-broken` widget whose `ValidateConfig` always returns an error. The broken widget's `DefaultConfig` returns an empty `{}` byte slice.

- A `insertRawWidget(t *testing.T, sqlDB *sql.DB, pageID, typeName, configJSON string, position int)` helper that does the direct INSERT bypassing the service's validation, returning the new widget ID (generated via the same `auth.GenerateToken[:32]` primitive used elsewhere).

- A `captureSlog(t *testing.T) (*bytes.Buffer, func())` helper that installs a slog handler writing to the buffer for the duration of the test, returning the buffer + a teardown.

Each test:
1. Open a test DB; run migrations (automatic via `db.OpenTestDB`).
2. Build the services (themes, screens with the test registry).
3. Build the test fixtures (devices, screens, pages, widgets) -- via service for valid rows, via direct SQL for adversarial rows.
4. Build an `httptest.NewRecorder` + a request with the identity context populated.
5. Invoke `handleDeviceRender(...)` directly (NOT through the full middleware chain unless the test explicitly needs it; the auth chain is tested separately).
6. Assert response code, body substrings, headers, and slog output.

Follow `.claude/rules/testing.md`. The bar is "each test has a single clear failure reason; the assertion language tells the next person what invariant is at stake".

## Definition of Done

- [ ] `views/device_adversarial_test.go` exists with all 10 tests listed above (some grouped via table-driven cases where natural).
- [ ] All tests pass on first run against the TASK-030 implementation. If a test surfaces a real production bug, document it in the task (or via a tiny fix patch), but the focus is test coverage.
- [ ] The helpers (`newTestRegistryWithBroken`, `insertRawWidget`, `captureSlog`) are minimal and reused across tests.
- [ ] `go test -race ./views/...` passes. (No race conditions are intentionally introduced, but the test surface touches `sync` indirectly via slog.)
- [ ] green-bar passes.
- [ ] No new third-party dependencies.
- [ ] No raw config bytes or error messages are logged at warn / error level by the production code; the tests verify this where it matters (the widget-error placeholder must NOT contain the config bytes or the error message).
