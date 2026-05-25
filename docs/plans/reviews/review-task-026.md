---
id: REVIEW-026
task: TASK-026
spec: SPEC-006
arch: ARCH-006
status: ACCEPT
reviewer: tester
reviewed: 2026-05-25
---

# Review: TASK-026 (Page edit view + widget instance admin UI)

## Summary

TASK-026 ships the final piece of the Screen Model: the per-page admin
surface at `/admin/screens/{id}/pages/{pageID}/edit` plus the four widget
admin routes (create, delete, move-up, move-down). The implementation
wires `pageEditPage` cleanly into the existing `screenMux`, extends
`screenMsgText` with the three widget-related flash codes, adds the
`widgetDisplayName` fallback helper, and exposes
`screens.Service.ListWidgetInstancesByPage` (the missing slice of
`GetScreenFull` that the page-edit handler needs).

I ran twenty-two adversarial probes across HTML escaping, cross-tenant
authorisation, path-param fuzz, concurrency, CSRF, role gating, and
end-to-end lifecycle. **The implementation held against every probe.**
The remaining residual notes are low-severity (dead error branches in
the widget handlers; redundant `GetPageByID` work in the page-edit GET).
None of them is worth a code change in this review.

My recommendation is **ACCEPT**.

Specifically I confirmed:

- AC-16 through AC-25 pass via the developer's existing tests plus my
  adversarial probes.
- HTML escaping holds for the widget config column (a custom widget
  whose default config contains `<script>` renders escaped), for the
  flash error query param, and for HTML-bearing registration metadata
  (`DisplayName`, `Description`).
- Cross-screen and cross-page URL routing all surface the typed
  `ErrPageNotFound` / `ErrWidgetNotFound` rather than 5xx or silent
  cross-tenant mutation.
- All four POST widget routes are CSRF-gated, are 403 for member
  identities, and are not dispatchable via GET.
- Concurrent `AddWidget` calls to the same page produce no 5xx (the
  TOCTOU race on `(page_id, position)` collapses into a friendly
  redirect on UNIQUE-constraint failure).
- Concurrent move-up / move-down across the same two widgets holds
  under `-race`, with no negative positions and no duplicates.
- `ListWidgetInstancesByPage` enforces the (screen, page) pair
  in-service (defence-in-depth), returns a non-nil empty slice for a
  page with no widgets, and emits rows in position-ASC order even
  when inserted out of order.
- The page-edit view renders the correct form actions, the widget
  type `<select>`, the empty state, the back-link, and the hero.

**No critical, high, or medium issues remain.** All twenty-two new
adversarial tests pass under `go test -race ./...`.

## AC coverage

| AC      | Description                                                                                                | Status | Evidence                                                                                  |
| ------- | ---------------------------------------------------------------------------------------------------------- | ------ | ----------------------------------------------------------------------------------------- |
| AC-16   | POST `.../widgets` `type=text` -> 302 `?msg=widget_added`; row has `type=text`, default config, `pos=max+1` | PASS   | `TestHandleWidgetCreate_HappyPath`                                                        |
| AC-17   | POST `.../widgets` `type=nonexistent` -> 302 `?error=Unknown+widget+type`; no row                          | PASS   | `TestHandleWidgetCreate_RejectsUnknownType`                                               |
| AC-18   | POST `.../widgets/{id}/delete` -> 302 `?msg=widget_deleted`; row gone                                      | PASS   | `TestHandleWidgetDelete_HappyPath`                                                        |
| AC-19   | Two widgets at positions 1 and 2; move-up on position 2 -> swapped                                         | PASS   | `TestHandleWidgetMoveUp_Swaps`                                                            |
| AC-20   | Persisted `text` default config passes `widget.Default().Validate("text", config)`                         | PASS   | `TestHandleWidgetCreate_DefaultConfigValidates`                                           |
| AC-24   | GET `.../pages/{pageID}/edit` for one-widget page renders DisplayName, position, `<pre>` config            | PASS   | `TestHandlePageEditForm_RendersWidgetList`                                                |
| AC-25   | GET `.../pages/{pageID}/edit` renders `<select name="type">` with every registered widget type             | PASS   | `TestHandlePageEditForm_ShowsWidgetTypeSelect`                                            |
| AC-26   | `GetScreenFull` on 2 pages / 3 widgets returns the full tree in position order                             | PASS   | `TestGetScreenFull_EndToEnd` (this task), `TestEndToEnd_ScreenPageWidgetLifecycle` (mine) |
| AC-21/22| Member 403 and CSRF rejection on every new widget route (re-verified at this layer)                        | PASS   | `TestWidgetRoutes_MemberIs403`, `TestWidgetRoutes_AllPOSTsRequireCSRF` (mine)             |

AC-1 through AC-15 belong to TASK-025; this review re-verifies AC-21,
AC-22, AC-26 at the widget-route layer because TASK-026 extends the
same `screenMux`.

## Adversarial findings

### Findings that did NOT reveal a bug (the implementation held)

Each probe below is checked-in as a test that passes; each pins the
corresponding property so a future regression surfaces loudly.

**Cross-screen page-edit GET.** A page on screen A accessed via screen
B's URL is rejected with `?error=Page+not+found` (redirecting to
`/admin/screens/B/edit`). The defence-in-depth `WHERE id = ? AND
screen_id = ?` clause inside `GetPageByID` does its job; the handler
never reaches the widget list or the render. Pinned by
`TestHandlePageEditForm_CrossScreenRejected`.

**Cross-screen widget routes.** All four widget POST routes
(create / delete / move-up / move-down) on a (screen B, page A)
mismatch return 302 with `?error=Page+not+found` (since `AddWidget`,
`DeleteWidget`, and `swapWidget` all call `GetPageByID` first). The
widget on screen A survives the four cross-tenant probes. Pinned by
`TestWidgetHandlers_CrossScreenRejected`.

**Cross-page widget delete.** Two pages on the same screen; a widget
on page A is targeted via page B's URL. The sqlc query
`DeleteWidgetInstance` uses `WHERE id = ? AND page_id = ?`, so the
DELETE affects 0 rows and the service returns `ErrWidgetNotFound`.
The handler surfaces a friendly `?error=Widget+not+found` flash. The
widget on page A remains intact. Pinned by
`TestHandleWidgetDelete_CrossPageRejected`.

**HTML escaping of widget config bytes.** The `<pre><code>{ string(wi.Config) }</code></pre>`
expression in `pageEditPage` is rendered through templ's
`EscapeString`. I registered a custom widget whose default config
literally contains `<script>alert(1)</script>`, persisted an instance
via `AddWidget`, and rendered the page. The raw `<script>` does not
appear in the body; the escaped form (`&lt;script&gt;...`) does. The
XSS surface via the config column is closed. Pinned by
`TestHandlePageEditForm_WidgetConfigEscapesHTML`.

**HTML escaping of flash error.** A `?error=<script>alert(1)</script>`
query parameter on the page-edit URL is rendered escaped. Pinned by
`TestHandlePageEditForm_FlashEscapesHTML`.

**HTML escaping of registration `DisplayName` and `Description`.** A
custom registration carrying `<img src=x onerror=alert(1)>` as
DisplayName and `<b>bold</b>` as Description renders escaped inside
the `<option>` element. Future widget authors writing HTML in their
metadata cannot break the picker. Pinned by
`TestHandlePageEditForm_WidgetTypeSelectEscapesRegistration`.

**Path-param fuzz on pageID.** Five shapes (path traversal, SQL
metacharacters, null byte, unicode, 1KiB string) through
`handlePageEditForm` all return 302 with `?error=Page+not+found`, no
5xx, no log noise. Pinned by `TestHandlePageEditForm_BadPageIDs`.

**Path-param fuzz on widgetID.** Same five shapes through each of
delete / move-up / move-down (fifteen sub-cases total) return 302
with no 5xx. Pinned by `TestWidgetHandlers_BadWidgetIDs`.

**`ListWidgetInstancesByPage` cross-screen rejection.** Asking the
service for widgets on a (screenB, pageOnScreenA) pair returns
`ErrPageNotFound`. The service layer enforces the pair internally
before issuing the widget-list query. Pinned by
`TestService_ListWidgetInstancesByPage_CrossScreenRejected`.

**`ListWidgetInstancesByPage` empty-slice contract.** A page with no
widgets returns a non-nil, length-zero slice. Pinned by
`TestService_ListWidgetInstancesByPage_EmptySliceNonNil`.

**`ListWidgetInstancesByPage` position ordering.** Three widgets
inserted via raw sqlc with positions 3, 1, 2 (insertion-order
shuffled) come back sorted ASC by position. The SQL `ORDER BY
position` does its job. Pinned by
`TestService_ListWidgetInstancesByPage_PositionOrder`.

**Concurrent `AddWidget` is race-free.** Four concurrent POSTs to the
same page produce no 5xx. The `MaxWidgetPosition` + `CreateWidgetInstance`
sequence is NOT transactional, so two writers can both read max=K and
both try to insert at K+1. The UNIQUE(page_id, position) index fires
on the loser; the service surfaces a generic create-error which the
handler translates to "Could not add widget" (a friendly redirect, not
a 500). The race detector finds no shared-state mishandling. Final
state has no duplicate positions. Pinned by
`TestHandleWidgetCreate_ConcurrentAddRaceFree`.

**Concurrent reorder on adjacent widgets.** 40 concurrent moves
(20 MoveUp on the bottom widget + 20 MoveDown on the top widget)
produce a final state with no negative positions and no duplicates.
The negative-position swap inside a transaction does its job. The
race detector is clean. Pinned by `TestWidgetReorder_ConcurrentAcrossPages`.

**End-to-end lifecycle.** Through the real HTTP stack (`AddRoutes` +
`httptest.NewServer`), an admin walks the full loop: create screen ->
add page -> add widget -> verify widget renders in the page-edit
table -> add a second widget -> MoveUp the second one -> Delete the
now-top widget. Every step returns 302; the widget table shape
matches expectation at the end. Pinned by
`TestEndToEnd_ScreenPageWidgetLifecycle`.

**GET on POST-only widget routes is rejected.** Four routes (create,
delete, move-up, move-down) all 4xx on a GET request, never dispatch
to the handler. The ServeMux `POST /...` pattern blocks the GET so a
prefetcher / image-tag drive-by cannot trigger reorder. Pinned by
`TestWidgetRoutes_GETOnPostRouteRejected`.

**Member identity is 403 on every widget route.** Five URLs (one GET
+ four POSTs with valid `_csrf` so the rejection comes from
`RequireRole` and not CSRF) all 403 for a member identity. The
widget row survives. Pinned by `TestWidgetRoutes_MemberIs403`.

**Every widget POST is CSRF-gated.** Through the real chain, all four
widget POSTs without `_csrf` return 403; the widget remains intact.
Pinned by `TestWidgetRoutes_AllPOSTsRequireCSRF`.

**Unauthenticated page-edit GET redirects to /admin/login.** The
`RequireAuth` chain sends an unauthenticated visitor to `/admin/login`
rather than 403 or 500. The widget-admin URLs do not leak their
existence. Pinned by `TestWidgetRoutes_UnauthenticatedRedirectsToLogin`.

**Empty-state page-edit GET.** A page with zero widgets still renders
the "Page Settings" form, the empty "Widgets" table, and the
"Add Widget" form (with the type `<select>` populated). The back
link to the screen-edit page is present. Pinned by
`TestHandlePageEditForm_ZeroWidgetsRendersAddForm`.

**Page-edit form actions are correct.** The "Page Settings" form
POSTs to `/admin/screens/{screen}/pages/{page}` (the existing
TASK-025 page update endpoint). The "Add Widget" form POSTs to
`/admin/screens/{screen}/pages/{page}/widgets` (the create endpoint
this task adds). Pinned by `TestHandlePageEditForm_PageSettingsFormAction`.

**Page-edit hero is escape-safe.** The hero block renders "Edit Page:"
plus the page name (or `(no name)`), with a back link to "Back to
{screen.Name}". The page-name regex blocks markup at create time,
and the screen-name regex blocks it at create time; templ also
auto-escapes either string on render. Pinned by
`TestHandlePageEditForm_HeroIsEscapeSafe`.

**Widget handlers 403 on missing user context.** All five new
handlers (create, delete, move-up, move-down, page-edit) reject a
request whose context lacks a user (or, in the page-edit handler's
case, a session) with 403. The middleware chain populates both; the
handlers defend in depth. Pinned by
`TestWidgetHandlers_NoUserContextReturns403`.

### Notes that did not warrant fixes (low severity)

Each of these is a UX wart, dead-code concern, or
codebase-wide pattern. None affects security, data integrity, or any
spec AC.

- **`ErrScreenNotFound` branch in widget handlers is dead.** All four
  widget handlers (`handleWidgetCreate`, `handleWidgetDelete`,
  `handleWidgetMoveUp`, `handleWidgetMoveDown`) include an
  `errors.Is(err, screens.ErrScreenNotFound)` branch. None of the
  underlying service methods (`AddWidget`, `DeleteWidget`,
  `MoveWidgetUp`, `MoveWidgetDown`) ever returns `ErrScreenNotFound` -
  they all call `GetPageByID` first, which only returns
  `ErrPageNotFound`. The branch is harmless but unreachable.
  **Severity: low**, no fix. A future cleanup could drop the
  branches, but they document intent and would survive if a future
  refactor decoupled page existence from screen existence in the
  service.

- **`ErrPageNotFound` branch in `handlePageEditForm`'s widgets call is
  dead.** `handlePageEditForm` calls `GetPageByID(screenID, pageID)`
  and then immediately calls `ListWidgetInstancesByPage(screenID,
  pageID)`. The second call internally calls `GetPageByID` again,
  which has already proven the page exists. The handler's
  `ErrPageNotFound` branch on the `ListWidgetInstancesByPage` return
  is therefore unreachable in practice. **Severity: low**, no fix.

- **Redundant `GetPageByID` call.** `handlePageEditForm` calls
  `GetPageByID` directly, then `ListWidgetInstancesByPage` (which
  calls `GetPageByID` internally). The view path does two
  `SELECT ... WHERE id = ? AND screen_id = ?` round-trips where one
  would suffice. SQLite handles this in microseconds; the redundancy
  is documented in the service's contract ("page existence is
  enforced inside the list method") rather than relied on. **Severity:
  low**, no fix.

- **Move-up button is rendered conditionally; move-down is always
  rendered.** The templ hides Move-up when `wi.Position == 1` but
  always renders Move-down, even on the bottom widget. The
  inconsistency mirrors TASK-025's page-level reorder buttons (which
  always render both). The service handles "already at the edge" as
  a no-op with a "Widget reordered." flash regardless. **Severity:
  low**, no fix. A future UX pass could disable both conditionally.

- **No-op move emits "Widget reordered." flash.** When MoveUp at the
  top is a no-op, the flash still reads "Widget reordered." This
  matches the documented behaviour in TASK-024 / TASK-025; the
  spec does not require a distinct "already at the edge" message.
  **Severity: low**, no fix.

- **`Render()` error is discarded.** `pageEditPage(...).Render(ctx,
  w)` ignores its returned error, matching the pattern across all of
  `views/`. **Severity: low**, no fix.

- **`<select name="type" required>` is empty when registry has no
  widgets.** If the registry has zero entries, the `<select>` renders
  with no `<option>` children. Browsers may not enforce `required` on
  an empty select; the user could submit an empty form which the
  handler maps to "Widget type is required". The fix would either
  emit an "(no widgets registered)" placeholder option or hide the
  form. Production seeds the registry with the `text` widget via
  init(), so this state only arises in tests. **Severity: low**, no
  fix.

## New tests added

In a new file `views/page_edit_adversarial_test.go` (22 adversarial
tests on top of the developer's 12 baseline tests in
`views/page_edit_test.go`):

1. `TestHandlePageEditForm_CrossScreenRejected` -- page on screen A
   accessed via screen B's URL returns Page not found.
2. `TestWidgetHandlers_CrossScreenRejected` -- four sub-cases for
   the widget POST routes; cross-screen probes all redirect.
3. `TestHandleWidgetDelete_CrossPageRejected` -- widget on page A
   targeted via page B returns Widget not found; the widget survives.
4. `TestHandlePageEditForm_WidgetConfigEscapesHTML` -- a custom
   widget's HTML-bearing default config renders escaped.
5. `TestHandlePageEditForm_FlashEscapesHTML` -- `?error=<script>...`
   on page-edit URL renders escaped.
6. `TestHandlePageEditForm_WidgetTypeSelectEscapesRegistration` --
   HTML-bearing `DisplayName` and `Description` in a registration
   render escaped inside the `<option>` element.
7. `TestHandlePageEditForm_BadPageIDs` -- five shapes (traversal,
   SQL meta, null, unicode, 1KiB) on pageID all 302.
8. `TestWidgetHandlers_BadWidgetIDs` -- five shapes across three
   handlers (15 sub-cases) all 302.
9. `TestService_ListWidgetInstancesByPage_CrossScreenRejected` --
   service-level defence-in-depth on (screen, page).
10. `TestService_ListWidgetInstancesByPage_EmptySliceNonNil` --
    non-nil empty slice on no widgets.
11. `TestService_ListWidgetInstancesByPage_PositionOrder` -- out-of-
    order inserts come back ASC by position.
12. `TestHandleWidgetCreate_ConcurrentAddRaceFree` -- four parallel
    Add POSTs; no 5xx, no duplicates.
13. `TestWidgetReorder_ConcurrentAcrossPages` -- 40 parallel move-
    up/move-down on adjacent widgets; no negative positions, no
    duplicates, race-detector clean.
14. `TestEndToEnd_ScreenPageWidgetLifecycle` -- full HTTP stack
    walk: create screen -> add page -> add widget -> render page-
    edit -> add 2nd widget -> move-up -> delete.
15. `TestWidgetRoutes_GETOnPostRouteRejected` -- GET on the four
    POST-only widget routes returns 4xx.
16. `TestWidgetRoutes_MemberIs403` -- five URLs (one GET + four
    POSTs with valid `_csrf`) for a member identity all 403.
17. `TestWidgetRoutes_AllPOSTsRequireCSRF` -- four POST URLs
    without `_csrf` all 403; widget survives.
18. `TestWidgetRoutes_UnauthenticatedRedirectsToLogin` -- unauth
    request to page-edit redirects to login.
19. `TestHandlePageEditForm_ZeroWidgetsRendersAddForm` -- empty
    widget list still renders Page Settings + Add Widget chrome.
20. `TestHandlePageEditForm_PageSettingsFormAction` -- the page-
    settings form POSTs to the right URL, and the add-widget form
    POSTs to the right URL.
21. `TestHandlePageEditForm_HeroIsEscapeSafe` -- hero renders
    "Edit Page:" + name + back link safely.
22. `TestWidgetHandlers_NoUserContextReturns403` -- five handlers
    each return 403 on missing user context.

All twenty-two new tests pass under `go test -race`. No test
documents broken behaviour: each one pins a property the implementation
satisfies.

## Fixes applied

None. The implementation defended against every adversarial probe I
designed. No source files were edited.

## Green-bar

```
gofmt -l .                       # empty
go vet ./...                     # clean
go build ./...                   # clean
go test ./...                    # ok (all packages)
go test -race ./...              # ok (all packages)
templ generate                   # no diff
```

All four gates pass with race detection across the full module. The
new `views/page_edit_adversarial_test.go` adds roughly 800ms of test
time under `-race`. The committed `screens_templ.go` is already in
sync with `screens.templ` -- running `templ generate` produces no
diff.

## Recommendation

**ACCEPT.** Every spec AC scoped to TASK-026 (AC-16 through AC-20,
AC-24 through AC-26) passes. The route wiring honors the admin-only
middleware chain. CSRF gates every POST. Templ escapes the widget
config column, the flash messages, and the registration metadata.
Cross-screen / cross-page URL probes all surface typed errors and
friendly flashes rather than 5xx or silent cross-tenant mutation.
Concurrent add and reorder operations are race-safe and produce no
duplicate positions.

After this task, the Screen Model is feature-complete: an admin can
create a Screen, add Pages, add widget instances by type, reorder,
and delete -- all through the admin UI, exactly as SPEC-006 promised.
Screen Display (the next p0 spec in Phase 2) can consume
`GetScreenFull` and ship as an additive renderer on top of this
shape.

The residual low-severity notes (dead `ErrScreenNotFound` branches in
the widget handlers, redundant `GetPageByID` round-trip in
`handlePageEditForm`, always-rendered Move-down button) are
codebase-wide patterns or UX warts; none of them is worth a code
change in this review. They are pinned by the adversarial tests so a
future refactor surfaces them again.
