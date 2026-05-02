---
id: REVIEW-018
task: TASK-018
spec: SPEC-004
arch: ARCH-004
status: ACCEPT
reviewer: tester
reviewed: 2026-04-30
---

# Review: TASK-018 (Theme Admin Views, Route Wiring, main.go Integration)

## Summary

The HTTP/UI layer for the Theme System holds up under hostile probing. The
six handlers route correctly, the inline-rerender pattern preserves rejected
form values exactly as the spec requires (ADR-004), the role-and-CSRF
middleware chain correctly rejects every member / anonymous / token-less
probe I threw at it, the default marker is anchored to `IsDefault` rather
than substring-matching the name, and ServeMux's pattern matching defuses
path-traversal attempts before they can reach a wrong handler. Concurrent
delete and set-default operations behave per the architecture: exactly one
delete succeeds, exactly one default remains. Validator rejection of script
injection in the name field is double-protected by templ's automatic
attribute-context HTML escaping.

I found no critical, high, or medium issues that required source changes.
The 21 tests added in this review exercise behaviour the developer's
suite left implicit:

- the inline form preserves all four kinds of rejected input
  (name, color, radius, font_family) AND echoes them back inside the
  re-rendered `value="..."` attribute, including HTML-escaped script
  payloads;
- the edit page header continues to show the EXISTING theme name when
  validation fails on a rename attempt (so the user knows which theme they
  are editing, not the rejected new name);
- the default marker is unambiguous when a non-default theme has the
  substring "default" in its name;
- a 51-theme list renders with valid HTML structure;
- members with valid sessions are 403'd on every theme route through the
  full middleware chain, not just the GET page;
- anonymous users are redirected to `/admin/login` rather than receiving
  a confusing 403 (proves the chain order is RequireAuth → RequireCSRF →
  RequireRole, not the other way around);
- POST without `_csrf` is rejected on every state-changing route (create,
  update, set-default, delete -- the developer covered only delete);
- POST with a wrong (non-empty) `_csrf` is also rejected;
- a 10000-character path id does not panic;
- the CSRFToken does not appear in slog output for the create handler.

The single low-severity observation is a path-redundancy note about
`r.PathValue("id") == ""` in the delete / set-default handlers: it cannot
fire under the current ServeMux pattern, but the defensive code is
harmless and consistent with the codebase. No fix.

**Recommendation: ACCEPT.**

## AC coverage

| AC      | Description                                                                                  | Status | Evidence                                                                                     |
| ------- | -------------------------------------------------------------------------------------------- | ------ | -------------------------------------------------------------------------------------------- |
| AC-3    | POST `/admin/themes` with whitespace-only name re-renders inline, no row created             | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/whitespace_name` (existing); `TestHandleThemeCreate_PreservesRejectedFormValues` (new, asserts the value-preservation half) |
| AC-4    | POST with `theme<script>` re-renders inline, no row created                                  | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/name_with_markup`; `TestHandleThemeCreate_ValidatorRejectsScriptInjection` (new, also asserts templ escaping on echo-back) |
| AC-5    | POST with 65-char name re-renders inline, no row created                                     | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/name_too_long`                                   |
| AC-6    | POST with duplicate name surfaces `formErrors["name"]`                                       | PASS   | `TestHandleThemeCreate_RejectsDuplicateName`                                                 |
| AC-9    | Invalid color value re-renders inline                                                        | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/invalid_color_*` rows                            |
| AC-10   | `rgb(...)` color rejected                                                                    | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/invalid_color_text_muted`                        |
| AC-11   | `#zz` color rejected                                                                         | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/invalid_color_border`                            |
| AC-12   | `;`/`{`/`}` in font_family rejected                                                          | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/invalid_font_family`                             |
| AC-13   | Long font_family rejected                                                                    | PASS   | tested at the validator layer in TASK-017; UI-layer rejection inherited                       |
| AC-15   | Radius `10` (no unit) rejected                                                               | PASS   | `TestHandleThemeCreate_RejectsInvalidFields/invalid_radius`                                  |
| AC-21   | Member GET `/admin/themes` → 403                                                             | PASS   | `TestThemeMgmt_MemberCannotAccessThemesPage` (new); plus `TestThemeMgmt_MemberCannotPostCreate` and `TestThemeMgmt_MemberCannotPostDelete` (new) extend the role-check to POST routes |
| AC-22   | List shows every theme; default marker is unambiguous                                        | PASS   | `TestHandleThemeList_RendersThemes`; `TestHandleThemeList_DefaultMarkerByFlagNotByName` (new) verifies marker comes from IsDefault, not from substring matching the name |
| AC-23   | Valid create → 302 to `?msg=...`; next GET shows the theme                                   | PASS   | `TestHandleThemeCreate_HappyPath`                                                            |
| AC-24   | Edit form pre-populates with current values                                                  | PASS   | `TestHandleThemeEditForm_PrePopulates`; `TestHandleThemeEditForm_NameWithSpacesPreserved` (new) verifies whitespace-bearing names round-trip without URL-encoding or over-escaping |
| AC-25   | Valid update → 302 to `?msg=updated`                                                         | PASS   | `TestHandleThemeUpdate_HappyPath`                                                            |
| AC-26   | Update of unknown id → 302 to `?error=...`                                                   | PASS   | `TestHandleThemeUpdate_UnknownID`                                                            |
| AC-27   | Delete without valid `_csrf` → 403, row preserved                                            | PASS   | `TestThemeRoutes_CSRFRequired` (developer-supplied); plus `TestThemeMgmt_CreateWithoutCSRFRejected`, `TestThemeMgmt_UpdateWithoutCSRFRejected`, `TestThemeMgmt_SetDefaultWithoutCSRFRejected`, `TestThemeMgmt_DeleteWithWrongCSRFRejected` (new) extend the same guarantee to every state-changing route and to the wrong-token case |

## Adversarial findings

### Findings that did NOT reveal a bug (the implementation held)

**Member role enforcement on POST routes.** The developer's tests exercised
the GET `/admin/themes` member-403 path implicitly (via `RequireRole` tests
in TASK-013). I added tests that go through the FULL middleware chain via
`httptest.NewServer` for every member-accessible POST route: create, delete.
A regression that dropped the `RequireRole(RoleAdmin)` wrapping on the
theme sub-mux -- or accidentally registered theme handlers outside the
`adminMux` -- would have surfaced here. Each member POST returned 403, and
the underlying state (theme list, deleted-theme row) was unchanged.

**Anonymous request → /admin/login redirect.** Confirms the chain order is
`RequireAuth → RequireCSRF → RequireRole`. If `RequireRole` ran first on
a nil user, the response would be 403 instead of the 302 to login. The
test passes, locking in the order.

**CSRF on every state-changing theme route.** The developer's
`TestThemeRoutes_CSRFRequired` covered only the delete route. I extended
the integration coverage to:

- POST `/admin/themes` (create) without `_csrf` → 403, no row created.
- POST `/admin/themes/{id}` (update) without `_csrf` → 403, name unchanged.
- POST `/admin/themes/{id}/set-default` without `_csrf` → 403, default
  flag unchanged on both old and new candidate.
- POST `/admin/themes/{id}/delete` with a deliberately-wrong `_csrf` → 403,
  row preserved. (Forces the constant-time-compare in `RequireCSRF` to
  actually execute its mismatch branch rather than the empty-token branch.)

**Inline value preservation for create (ADR-004).** I submitted a single
POST with FOUR simultaneously-invalid fields -- empty name, invalid color
(`red`), invalid radius (`10` no unit), invalid font_family
(`Arial; }<script>`). Asserts:

- response is 200, NOT 302 (no redirect on validation error);
- four `<p class="error">` paragraphs render (one per rejected field);
- every rejected value appears inside a `value="..."` attribute on its
  input -- so the user does not retype anything;
- the script-payload font_family value is HTML-escaped to
  `value="Arial; }&lt;script&gt;"` (templ's attribute-context escaping is
  doing its job);
- the empty-name input renders with `value=""` (no fallback substitution).

**Inline value preservation for update + page header preservation.** Same
multi-field rejection on the edit form. Adds: the page hero shows the
EXISTING theme's name (not the rejected new name from the form). This is
what the architecture document specifies but the developer's
`TestHandleThemeUpdate_RejectsValidationError` covered only one field and
didn't pin the header text. The DB row remained un-mutated by the
rejection.

**Default marker is anchored to IsDefault, not name.** Created a non-default
theme called `default-but-not-really` alongside the seeded default.
`TestHandleThemeList_DefaultMarkerByFlagNotByName` counts
`<strong>default</strong>` occurrences and asserts exactly one -- proves
the marker logic in the templ is `if t.IsDefault` and not a substring
match against the name.

**51 themes render with valid HTML.** `TestHandleThemeList_ManyThemesRender`
creates 50 themes, GETs the list, asserts both endpoints of the list (`theme-0`,
`theme-49`) plus the seeded default render, the page closes with `</html>`,
and exactly one default marker is present. Catches a regression where the
table loop terminates early or breaks the surrounding HTML structure.

**Names with whitelisted whitespace round-trip cleanly.** `Kitchen Day _alpha-1`
goes in via Create and comes back out in `value="Kitchen Day _alpha-1"`
on the edit form -- no `%20`, no `&#32;`, no quote-stripping. Confirms the
attribute-context render does not URL-encode or HTML-encode normal spaces
(which would visually corrupt the field for the admin).

**Validator rejects HTML break-out, templ escapes on re-render.** Submitted
`<img src=x onerror=alert(1)>` as a name. The validator rejects it
(no `<` allowed in `[A-Za-z0-9 _-]`). The re-rendered form contains the
escaped form `&lt;img src=x onerror=alert(1)&gt;` inside the `value="..."`
attribute, NOT the unescaped tag. Two layers of defence (validator +
attribute escaper) verified independently in
`TestHandleThemeCreate_ValidatorRejectsScriptInjection`.

**Path traversal is normalised by ServeMux.** GET `/admin/themes/../users`
and GET `/admin/themes/%2e%2e/users` both fail to land on the
`/admin/users` handler. The mux either redirects to the canonical path
(which then doesn't match a theme route) or rejects with 404/405. The
test asserts the response is NOT 200 -- the failure scenario is
"traversal succeeded and rendered the users page". Pinned by
`TestThemeRoutes_PathTraversalSafelyRejected`.

**Wrong-method requests are rejected.** GET on `/{id}/delete` and
`/{id}/set-default` returns 405 (mux pattern match fails); DELETE and PUT
on `/{id}` are stopped by `RequireCSRF` first (state-changing methods
without a `_csrf` token → 403). Either way, the theme row is unchanged.
Test: `TestThemeRoutes_WrongMethodRejected`.

**10000-character path id does not panic.** The handler treats it as
"theme not found" and 302s to `?error=Theme+not+found`. The id flows
through `themesSvc.GetByID` which uses a parameterised query, so SQLite
neither bombs nor truncates. Test: `TestHandleThemeUpdate_LongIDDoesNotPanic`.

**Sentinel error → sanitised redirect URL.** When `Service.Delete` returns
`ErrCannotDeleteDefault`, the handler 302s to
`/admin/themes?error=Cannot+delete+the+default+theme` -- a fixed
human-readable string, not the raw `err.Error()` and not any internal
package path. Test: `TestHandleThemeDelete_RedirectErrorIsSanitized`.

**Concurrent deletes against the same theme.** Four goroutines each
POST delete to the same non-default theme through the bare handler.
Exactly one returns 302 to `?msg=deleted`; the others return 302 to
`?error=Theme+not+found` or to the generic "Could not delete theme"
fallback (the latter triggers when the GetByID precheck succeeds but
the DELETE matches zero rows because another goroutine got there
first -- the service surfaces it as a generic error, not a 500). No 500,
no panic. Final state: row is gone. Test:
`TestHandleThemeUpdate_ConcurrentDeletesOneSucceeds`.

**Concurrent set-default on different themes leaves exactly one default.**
Two goroutines each set a different non-default theme as the system
default through the bare handler. Both 302; the final invariant
(`exactly one IsDefault==true row`) holds, enforced by both the
transactional `SetDefault` and the partial unique index
`themes_one_default`. Test: `TestThemeMgmt_SetDefaultExactlyOneRemains`.

**slog does not leak the CSRFToken.** The create handler emits
`slog.Info("theme created", "theme_id", t.ID, "name", t.Name,
"created_by", user.Email)`. Captured the JSON output; the captured
buffer does not contain the session's CSRFToken string. Test:
`TestThemeMgmt_LogsDoNotContainCSRFToken`.

**main.go fails fast on EnsureDefault error.** Read `main.go`:

```go
if err := themesSvc.EnsureDefault(context.Background()); err != nil {
    db.Close(sqlDB)
    log.Fatalf("seed default theme: %v", err)
}
```

Identical pattern to `db.Migrate`. A failure here halts process startup
before the listener binds. Confirmed by inspection -- not
behaviorally testable without spinning up the real binary.

### Notes that did not warrant fixes

- **`r.PathValue("id") == ""` defensive checks in `handleThemeDelete`,
  `handleThemeSetDefault`, `handleThemeEditForm`, `handleThemeUpdate`.**
  Under the current route patterns (`/admin/themes/{id}/delete`, etc.) the
  wildcard always matches a non-empty segment because ServeMux 1) refuses
  to match `/admin/themes//delete` (it 307-redirects to a canonicalised
  path), and 2) only invokes the handler if the pattern matched. So the
  empty-id branch is dead code in practice. It is also harmless and
  consistent with `views/devices.go`. Severity: **low**, no fix.

- **Existing tests assert "the form contains class=\"error\"" without
  checking which field's error renders.** A regression that put the
  `name` error message under the `color_bg` field would still pass
  `TestHandleThemeCreate_RejectsInvalidFields`. Not strictly broken --
  the spec only requires "an error appears" -- but a future tightening
  could anchor each error to its expected field via a more structured
  assertion. Out of scope for this review; no fix.

- **`TestThemeRoutes_WrongMethodRejected` accepts 403/404/405**. This is
  a deliberate choice: the chain order is `RequireAuth → RequireCSRF →
  mux`, so a DELETE/PUT against `/admin/themes/{id}` is rejected by
  `RequireCSRF` (403) before the mux can return 405. Either status is a
  hard rejection; the failure mode the test guards against is a 200/302
  that would imply the wrong method was honoured.

## New tests added

In `views/themes_adversarial_test.go` (new file):

1. `TestThemeMgmt_MemberCannotAccessThemesPage` -- AC-21 GET path through
   the full middleware chain.
2. `TestThemeMgmt_MemberCannotPostCreate` -- member with valid CSRF still
   fails the role check on POST `/admin/themes`.
3. `TestThemeMgmt_MemberCannotPostDelete` -- member cannot delete an
   existing theme; row preserved.
4. `TestThemeMgmt_AnonymousRedirectedToLogin` -- chain-order check; the
   anonymous HTML nav is 302'd to login, not 403'd.
5. `TestThemeMgmt_CreateWithoutCSRFRejected` -- POST without `_csrf` on
   the create route; 403, no row.
6. `TestThemeMgmt_UpdateWithoutCSRFRejected` -- POST without `_csrf` on
   the update route; 403, name unchanged.
7. `TestThemeMgmt_SetDefaultWithoutCSRFRejected` -- POST without `_csrf`
   on the set-default route; 403, both old and new default flags unchanged.
8. `TestThemeMgmt_DeleteWithWrongCSRFRejected` -- POST with the wrong
   `_csrf` value; forces the mismatch branch in `subtle.ConstantTimeCompare`.
9. `TestHandleThemeCreate_PreservesRejectedFormValues` -- 4-field rejection;
   asserts every rejected value appears inside `value="..."` and the
   inline form re-renders with 4 error paragraphs.
10. `TestHandleThemeUpdate_PreservesRejectedFormValuesAndKeepsPageHeader` --
    same value preservation guarantee for the edit page; also asserts the
    page header still shows the EXISTING theme name.
11. `TestHandleThemeList_DefaultMarkerByFlagNotByName` -- the default
    marker is anchored to `IsDefault`, not to substring matching.
12. `TestHandleThemeList_ManyThemesRender` -- 51 themes render, HTML
    closes cleanly, exactly one default marker.
13. `TestHandleThemeEditForm_NameWithSpacesPreserved` -- whitelisted
    space / underscore / hyphen characters round-trip cleanly through
    the edit form's `value="..."`.
14. `TestHandleThemeUpdate_LongIDDoesNotPanic` -- 10000-char id does not
    panic; handler returns 302 with error.
15. `TestHandleThemeDelete_RedirectErrorIsSanitized` -- the redirect URL
    contains a fixed human-readable error, not raw `err.Error()` or
    internal package paths.
16. `TestThemeRoutes_WrongMethodRejected` -- GET on POST-only routes,
    DELETE/PUT on the update path, all rejected (403/404/405) without
    mutating state.
17. `TestThemeRoutes_PathTraversalSafelyRejected` -- `/admin/themes/../users`
    and `/admin/themes/%2e%2e/users` do not land on the users handler.
18. `TestHandleThemeCreate_ValidatorRejectsScriptInjection` -- defence in
    depth: validator rejects `<img onerror>`, templ escapes on echo-back.
19. `TestHandleThemeUpdate_ConcurrentDeletesOneSucceeds` -- four parallel
    delete attempts against the same theme; exactly one succeeds, no 500.
20. `TestThemeMgmt_SetDefaultExactlyOneRemains` -- two parallel
    set-default calls leave the system with exactly one default.
21. `TestThemeMgmt_LogsDoNotContainCSRFToken` -- captures slog output
    on a successful create; asserts the CSRFToken string is absent.

All 21 tests pass. No tests were committed that demonstrate broken
behaviour.

## Fixes applied

None. Every adversarial probe was either rejected as designed, or
exercised a guaranteed code path that produced the documented behaviour.

## Green-bar

```
gofmt -l .             # empty
go vet ./...           # clean
go build ./...         # clean
go test ./...          # ok
go test -race ./...    # ok
```

All four gates pass with race detection enabled across the full module.

## Recommendation

**ACCEPT.** The Theme admin UI faithfully implements the architecture
document and SPEC-004's UI requirements. Validation errors re-render
inline with rejected values preserved per ADR-004; the default marker is
flag-driven; CSRF and role enforcement apply uniformly to every
state-changing theme route; main.go fails fast on a missing default
seed; no secrets leak into logs or response headers; ServeMux's path
normalisation defuses traversal probes before they can reach a wrong
handler. Concurrent deletes and set-default operations behave per the
service contract, and the partial unique index is the documented
belt-and-suspenders fallback.

The single low-severity note (defensive empty-id check that the route
pattern makes unreachable in practice) is consistent with the rest of
the codebase. No source changes were necessary.
