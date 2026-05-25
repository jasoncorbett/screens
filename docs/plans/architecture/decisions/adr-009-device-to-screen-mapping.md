---
id: ADR-009
title: "Device-to-Screen mapping via nullable devices.screen_id column (ON DELETE SET NULL)"
status: accepted
date: 2026-05-25
---

# ADR-009: Device-to-Screen mapping via nullable `devices.screen_id` column (ON DELETE SET NULL)

## Context

Phase 1 (Device Auth, SPEC-003) introduced the `devices` table: a registered display device has an ID, a name, a hashed bearer token, and timestamps. Devices authenticate; they don't (yet) have an assigned Screen. Phase 2 (Screen Model, SPEC-006) introduced the `screens` table: a Screen is a named dashboard with a theme reference and an ordered list of pages. Neither side currently knows about the other.

Screen Display (SPEC-007) needs the bridge. When a paired device lands on `/device/`, the render handler has to know which Screen to render for that device. Three design questions shake out:

1. **What is the schema shape of the mapping?** Options:
   - A nullable `screen_id TEXT` column on `devices` (the device "knows" its assigned Screen).
   - A separate `device_screen_assignments` join table.
   - A nullable `device_id TEXT` column on `screens` (the Screen "knows" which device displays it).
   - No persistent mapping; assignment lives entirely in admin-supplied per-request configuration (rejected as a category -- the device has to reload after power-loss with no human present, so the assignment has to persist somewhere).

2. **What is the FK behaviour when an admin deletes the referenced Screen?** Options:
   - `ON DELETE CASCADE`: delete the device row too.
   - `ON DELETE RESTRICT`: block the Screen delete until the device is reassigned.
   - `ON DELETE SET NULL`: leave the device row, clear its assignment.
   - No FK: silently orphan; check at render time.

3. **What is the cardinality?** Options:
   - 1:0..1 -- a device renders at most one Screen at a time; a Screen has zero or more devices.
   - 1:1 -- every device has a Screen; every Screen has at most one device.
   - N:M -- a device can render multiple Screens (rotating between Screens?); a Screen can be displayed on multiple devices.

The threat model is unchanged: admins can mutate the mapping; devices cannot (the assignment endpoint is admin-only with CSRF). The decision is about admin UX, schema simplicity, and the render-handler's failure modes.

## Decision

### Shape: a nullable `screen_id` column on `devices`.

The `devices` table gains a `screen_id TEXT NULL REFERENCES screens(id) ON DELETE SET NULL` column. Existing device rows get NULL (the migration is additive; no backfill). A device with `screen_id IS NULL` renders the "unassigned" placeholder; a device with a non-NULL `screen_id` renders that Screen.

### FK behaviour: `ON DELETE SET NULL`.

When an admin deletes a Screen, every device's `screen_id` referencing that Screen becomes NULL in the same transaction (atomic FK behaviour). The devices keep their identity and their tokens; they just go back to the "no Screen assigned" state, which the render handler already handles cleanly.

### Cardinality: 1:0..1.

A device renders at most one Screen at a time. A Screen has zero or more devices (no constraint on N). Two kitchen tablets can both display the "kitchen" Screen; that is the natural case.

### Why these three choices

- **Column over join table**: the cardinality is 1:0..1, so a column is the smallest representation. A join table would be either redundant (one row per device-screen pair, with a unique on device_id giving you the same property as a column) or misleading (suggesting N:M which we explicitly do not support). Schema simplicity wins.

- **Column on devices, not on screens**: the device "owns" the assignment (it is the device's render that consumes it). A column on screens would suggest "a Screen has *the* device that displays it" -- single device per Screen -- which is the wrong cardinality. It would also make "two devices on the same Screen" a schema impossibility, which is exactly the case we want to support.

- **SET NULL, not CASCADE**: deleting a Screen MUST NOT delete the device rows. Devices have identity (tokens, last-seen timestamps, audit trails) that outlives any Screen they once rendered. CASCADE would lose that information.

- **SET NULL, not RESTRICT**: the admin who deletes a Screen wants the Screen gone. RESTRICT would force them to first reassign every device displaying it -- that is the wrong UX, particularly when an admin is cleaning up an old / experimental Screen. The device-side failure mode is benign: the device shows the unassigned placeholder until the admin reassigns it.

- **SET NULL, not "no FK"**: relying on application-layer checks for "is this screen_id still valid?" is racy and adds a runtime check on every render. The FK at the DB layer is atomic, automatic, and cheap.

## Consequences

**Accepted trade-offs:**

- An admin who deletes a Screen by mistake silently un-assigns every device displaying it. The devices show the unassigned placeholder until the admin reassigns them. We accept this -- the alternative (RESTRICT) is a worse UX for the common-case admin who genuinely wants the Screen gone. A future cross-cutting "are you sure?" confirmation modal can address misclicks generally, not just for Screen deletes.
- The schema does not natively support "a device rotates between multiple Screens on a schedule". A future spec could add that with a separate join table (or with a "schedule" entity that selects which Screen is active at a given time-of-day). We accept the v1 limitation; no household use case has emerged.
- The schema does not natively support per-device per-Screen state ("this device's preferred starting page when rendering this Screen"). We accept this; the rotator is pure client-side and starts at page 1 every reload.
- The `auth.Device` Go struct grows a `ScreenID *string` field. Existing tests that construct `auth.Device` zero values still pass (nil pointer is the zero value); the change is additive.
- The migration uses `ALTER TABLE devices ADD COLUMN screen_id TEXT REFERENCES ...`. SQLite supports this syntax on existing tables provided the table is not subject to a complicating constraint that would force a rebuild. We confirm via `PRAGMA foreign_keys = ON` (already set by `db.Open`) that the constraint takes effect on subsequent INSERT / UPDATE statements; SQLite does not re-validate existing rows but existing rows all have NULL which trivially satisfies the FK.
- A `devices.screen_id` that becomes stale (e.g., due to a race or a future schema change that loosens the FK) is defended at the application layer: the render handler catches `screens.ErrScreenNotFound` from `GetScreenFull` and falls back to the unassigned placeholder, logging an info-level "device screen missing" line for operability.

**Benefits:**

- The mapping is one column. Schema diff is one line. Backfill is trivial (no backfill needed).
- "Two devices on the same Screen" is the natural case; no extra schema or code.
- "An admin deletes a Screen" Just Works: the FK SET NULL atomically un-assigns every device; the next render on each device shows the unassigned placeholder; no 500s, no orphan references.
- "An admin re-assigns a device to a different Screen" is one UPDATE.
- "A device boots up after power-loss" reads its own row, finds its `screen_id`, renders the assigned Screen. No human intervention.
- The render handler reads identity (device) → reads `device.ScreenID` → calls `GetScreenFull(*device.ScreenID)`. No cross-table lookup at render time beyond what `GetScreenFull` already does.
- Phase 5 admin UI ("which devices are displaying which Screen?") becomes a simple JOIN -- `SELECT d.name, s.name FROM devices d LEFT JOIN screens s ON s.id = d.screen_id ORDER BY ...`.
- The mapping is invisible to the widget interface and the rendering pipeline. Future per-Screen features (Page Backgrounds, Card Theming, Typography Roles) need not be aware of devices at all.

**Risks accepted:**

- An admin who deletes a Screen unintentionally loses the assignment on every device. The audit log (slog Info on the delete + the existing per-device assignment slog) captures what happened, but reverting requires the admin to manually re-assign each device. We accept this -- the operation is admin-only, CSRF-protected, and audit-logged; the "undo" workflow is "create a new Screen with the same contents and re-assign".
- The "one device, one Screen at a time" model means scheduled rotation (e.g., morning Screen vs evening Screen) requires either two devices or a future schedule-aware extension. We accept the limitation for v1.
- A future "multi-display" use case (one device with two physical screens, each showing a different Screen) is not directly supported. The household model does not include this; a future spec can address it if needed by adding a second column or a join table.
- The `ALTER TABLE ... ADD COLUMN ... REFERENCES ...` works in modern SQLite but is a slightly unusual migration shape. We verify in the migration test (`screens_schema_test.go` analogue) that the FK takes effect on new INSERTs.
- The FK enforcement only fires on insert / update; if a future bug or hand-edit introduces a stale `screen_id` value, the render handler's defensive `errors.Is(err, screens.ErrScreenNotFound)` branch catches it. We accept the redundancy; the cost is one branch.
