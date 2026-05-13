---
id: REVIEW-020
task: TASK-020
spec: SPEC-005
arch: ARCH-005
status: ACCEPT
reviewer: tester
reviewed: 2026-04-30
---

# Review: TASK-020 (Placeholder text widget + main.go and views.Deps wiring)

## Summary

The text widget is the contract validator for SPEC-005, and it works.
Every spec AC scoped to this task passes (AC-13 through AC-27). The
implementation is small enough to fit on one screen, and every line
matches the architecture document. The two structural pieces -- a
stateless singleton with a comma-ok defensive type assertion in
`Render`, and a four-step validator (unmarshal -> trim -> empty check
-> length cap) -- are exactly the shape the architecture commits to.

I tried to break it. I could not. Specifically:

- The 4096-byte boundary holds in both directions: 4096 accepted, 4097
  rejected, and the inclusive boundary is now pinned in
  `TestAdversarial_BoundaryAccept4096` so a future `>` -> `>=` edit
  surfaces as a test failure.
- Whitespace trim runs BEFORE the length cap, so a 4096-byte body
  padded with arbitrary whitespace is valid. The implementation
  documents this implicitly via field-order; the pin
  (`TestAdversarial_TrimRunsBeforeLengthCheck`) makes it a test-level
  contract.
- The cap is byte-based, not rune-based. 1024 four-byte emojis = 4096
  bytes (accepted); 1025 = 4100 bytes (rejected). The task document
  endorses this directly ("the cap is bytes, not lines"). The spec
  uses the word "characters" which is a mild ambiguity but the AC-19
  test assertion is byte-aligned (4097 ASCII chars = 4097 bytes), so
  this is a wording note, not a behavioural defect. Pinned by
  `TestAdversarial_LengthCapIsBytes`.
- Every malformed JSON shape I could think of is rejected: top-level
  null / array / number / string, empty input, `{}`, `{"text":null}`,
  `{"text":123}`, `{"text":[]}`, `{"text":true}`, trailing comma,
  unterminated string. Pinned in a 12-row table-driven test.
- HTML escape via templ holds for `<script>`, `<img onerror=...>`,
  `"><svg/onload=...>`, pre-escaped entities (`&amp;` -> `&amp;amp;`),
  and bare `</div>` (which is the canonical "break out of the parent
  div" attack against this widget's specific markup). None of the raw
  forms appear in the rendered output.
- The render path is concurrent-safe: 32 goroutines x 50 renders each
  on the singleton, `-race` clean.
- `Registration().New()` returns the same singleton pointer across
  calls (architectural requirement).
- A mistyped Config (someone bypasses the validator and stuffs
  `instance.Config = "wrong"`) does NOT panic. It renders an empty
  themed div. Visible-but-harmless, exactly the comma-ok branch's job.
- The CSS class attribute is exactly `class="widget widget-text"`. No
  inline `style="..."` attribute. No leaked classes.
- `widget.Default().Get("text")` returns a registration whose
  Type/DisplayName/Description match the values returned by
  `Registration()` directly, and all three function fields are
  non-nil. The init() side-effect is real.
- The wiring test in `views/widget_wiring_test.go` is a stand-in for
  "main.go is wired correctly" -- it blank-imports the same path
  main.go uses and confirms the registry contains `text`. I verified
  the corresponding line is present in `main.go:23` and that
  `Widgets: widget.Default()` is passed into `views.Deps` (line 100).

I found NO critical, high, or medium issues that required source
changes. 13 adversarial tests (47 sub-tests when counting the
table-driven cases) were added in this review; all pass. No tests
were committed that document broken behaviour.

**Recommendation: ACCEPT.**

## AC coverage

| AC      | Description                                                                       | Status | Evidence                                                                |
| ------- | --------------------------------------------------------------------------------- | ------ | ----------------------------------------------------------------------- |
| AC-13   | `widget.Default().List()` (with text imported) contains `text`                    | PASS   | `TestDefaultRegistryContainsText`; `TestDefaultContainsTextAfterImport` |
| AC-14   | `validate({"text":"hello"})` returns Instance with Text=="hello", nil err         | PASS   | `TestValidateAccepts/plain_text`                                        |
| AC-15   | `validate({"text":"  hello  "})` returns Text=="hello" (trimmed)                  | PASS   | `TestValidateAccepts/trims_surrounding_whitespace`                      |
| AC-16   | `validate({"text":""})` returns non-nil error                                     | PASS   | `TestValidateRejects/empty_string`                                      |
| AC-17   | `validate({"text":"   "})` returns non-nil error                                  | PASS   | `TestValidateRejects/whitespace-only`                                   |
| AC-18   | `validate("not-json")` returns non-nil error mentioning JSON                      | PASS   | `TestValidateRejects/not_JSON`                                          |
| AC-19   | `validate` with 4097-char text returns error mentioning length cap                | PASS   | `TestValidateRejects/exceeds_length_cap`                                |
| AC-20   | `defaultConfig()` bytes pass `validate` cleanly                                   | PASS   | `TestDefaultConfigValidates`                                            |
| AC-21   | Render with Text=="hello" produces output containing literal "hello"              | PASS   | `TestRenderContainsText`                                                |
| AC-22   | Render with Text=="<script>" HTML-escapes; no raw `<script>` substring            | PASS   | `TestRenderHTMLEscapes`; `TestAdversarial_HTMLEntitiesAreEscaped` (5x)  |
| AC-23   | Rendered HTML contains no `style="..."` inline color attribute                    | PASS   | `TestRenderNoInlineStyle`                                               |
| AC-24   | After importing `internal/widget/text`, `widget.Default().Get("text")` ok==true   | PASS   | `TestDefaultContainsTextAfterImport`                                    |
| AC-25   | `widget.Default().List()` from a process that imports text contains text reg     | PASS   | `TestDefaultRegistryContainsText` in `views/widget_wiring_test.go`      |
| AC-26   | No new HTTP routes registered                                                     | PASS   | `internal/widget/text/` has no handler files; `views/routes.go` adds a Deps field only |
| AC-27   | Existing view tests still pass without setting `Deps.Widgets`                     | PASS   | `go test ./views/...` clean (50+ existing tests untouched)              |

All 15 spec ACs scoped to this task pass. ACs 1-12 belong to TASK-019
(already accepted in REVIEW-019).

## Adversarial findings

### Findings that did NOT reveal a bug (the implementation held)

**Inclusive 4096-byte upper boundary.** The developer's `tooLong` test
covers 4097 bytes (rejected). I added a test for exactly 4096 bytes
(accepted). Both sides of the cliff are now pinned, so a future
`>` -> `>=` edit is a visible regression. Pinned by
`TestAdversarial_BoundaryAccept4096`.

**Whitespace trim runs before length cap.** The validator order is:
unmarshal -> trim -> empty check -> length cap. A 4096-byte body
padded with 100 leading + 100 trailing spaces is valid because trim
runs first. This matches the spec ("trim before storing") and the
implementation's source ordering. Pinned by
`TestAdversarial_TrimRunsBeforeLengthCheck`.

**Length cap is byte-based, not rune-based.** 1024 four-byte emojis
(🎉) = 4096 bytes -- accepted. 1025 = 4100 bytes -- rejected. The task
document explicitly endorses byte-counting ("the cap is bytes, not
lines"). The spec text uses the word "characters" but AC-19's literal
test (4097-char ASCII string) coincides with 4097 bytes, so the
discrepancy is wording-level. Pinned by
`TestAdversarial_LengthCapIsBytes`. If a future spec amendment
switches to rune-based counting, that test will need to be inverted
along with the validator code.

**Twelve malformed JSON shapes all reject cleanly.** The validator
handles `null`, `[]`, `42`, `"hello"`, `{}`, `{"text":null}`,
`{"text":123}`, `{"text":[]}`, `{"text":true}`, empty input,
trailing-comma JSON, and an unterminated string. Each returns a
non-nil error and never panics. Pinned by
`TestAdversarial_RejectsMalformedJSONShapes` (12 sub-tests).

**Nil bytes do not panic.** `validate(nil)` returns the documented
"invalid JSON" error path. Pinned by `TestAdversarial_NilBytesRejected`.

**Unknown extra JSON fields are ignored.** Go's `json.Unmarshal`
default behaviour applies: `{"text":"hello","foo":"bar"}` is accepted
and `cfg.Text == "hello"`. This matters for forward-compatibility --
a future schema migration that adds optional fields can ship without
breaking old configs. Pinned by
`TestAdversarial_UnknownExtraFieldsAccepted`.

**1MB malicious blob rejected.** Constructing a JSON document with a
1,048,576-character `text` field fails the cap. The validator allocates
the full string before checking, but the renderer never sees it.
Pinned by `TestAdversarial_OneMegBlobRejectedByCap`.

**HTML escape covers every realistic injection vector.** Five
specific attack strings round-trip through templ's `EscapeString`:
`<script>alert(1)</script>` (the obvious one), `<img onerror=alert(1)>`
(attribute-based), `"><svg/onload=alert(1)>` (which would break out of
a quoted attribute if the widget put the body in an attribute -- the
text widget does not, but the test pins the safer behaviour),
pre-escaped `&amp;` (correctly re-escapes to `&amp;amp;`), and bare
`</div>` (which would close the widget's containing div early if not
escaped). Each case asserts both the escaped form is present AND the
raw form is absent. Pinned by `TestAdversarial_HTMLEntitiesAreEscaped`.

**`Registration().New()` is the documented singleton.** Two calls
return the same `*widgetImpl` pointer. The architecture commits to
"stateless widget types may always return the same singleton"; this
test pins that the text widget actually does. A future "make Render
stateful" refactor that allocates per-call would fail this test.
Pinned by `TestAdversarial_NewReturnsSingleton`.

**Concurrent Render is race-free.** 32 goroutines x 50 renders each
through the singleton; under `-race` the run is clean across multiple
invocations. The widget is provably stateless -- no field reads, no
field writes inside `Render`. Pinned by
`TestAdversarial_ConcurrentRenderRaceFree`.

**Mistyped Config does not panic.** A caller that bypasses
`ValidateConfig` and stuffs `instance.Config = "not a Config"` does
NOT crash the renderer. The comma-ok type assertion swallows the
mismatch; the zero-value `Config` (empty Text) flows into
`textComponent`; the output is `<div class="widget widget-text"></div>`.
Visible-but-harmless. Pinned by
`TestAdversarial_RenderWithMistypedConfigDoesNotPanic`.

**Exact CSS class attribute and div shape.** The output is exactly
`<div class="widget widget-text">{escaped body}</div>`. No additional
classes, no inline styles, no nested elements. Pinned by
`TestAdversarial_RenderProducesExactCSSClasses`.

**Default registry registration matches Registration() byte-for-byte.**
The Type/DisplayName/Description fields, plus the three function-field
non-nil checks, all match between `widget.Default().Get("text")` and
`Registration()`. If a future refactor accidentally splits the global
init() registration from the test-time helper, this test catches the
drift. Pinned by `TestAdversarial_DefaultRegistrationFieldsMatch`.

**`templ generate` is idempotent.** Running it twice on a clean tree
shows `updates=0` both times, confirming the templ-generated file is
in sync with the `.templ` source. The generated file is gitignored
per `.gitignore`'s `*_templ.go` pattern.

**No third-party imports beyond what's approved.** The text package
imports `context`, `encoding/json`, `fmt`, `strings` from stdlib plus
`github.com/a-h/templ`, `github.com/jasoncorbett/screens/internal/themes`,
and `github.com/jasoncorbett/screens/internal/widget` -- all already
approved.

**Doc comments on every exported identifier.** `Type`, `MaxTextLength`,
`Config`, `Registration` (the function), and the package itself all
have doc comments. The unexported `widgetImpl`, `singleton`,
`validate`, and `defaultConfig` also have explanatory comments since
the reasoning (e.g., "stateless; one global value is enough", "the
error is intentionally discarded") is non-obvious.

**`views.Deps.Widgets` is a pointer; existing tests construct
`Deps{...}` literals without it and continue to compile and pass.**
Verified by `go test ./views/...` -- 50+ existing tests run clean.

### Notes that did not warrant fixes (low severity)

- **Spec uses "characters" but implementation uses bytes.** SPEC-005
  requirement 17 says "Reject text values longer than 4096 characters".
  The implementation enforces a byte cap (`len(cfg.Text) > MaxTextLength`).
  The task document explicitly endorses byte-counting. AC-19's literal
  test (4097 ASCII chars) coincides with 4097 bytes, so the test
  passes either way. This is a wording note in the spec, not a
  behavioural defect. Severity: **low**, no fix. If a future spec
  wants rune-counting (because emoji-heavy text feels short-changed
  to the admin), that's a deliberate amendment; the test
  `TestAdversarial_LengthCapIsBytes` makes the change visible.

- **Embedded NUL bytes (transported via JSON's ` ` escape) are
  accepted.** The validator does not strip control characters; templ
  passes the NUL through to the rendered HTML. NUL inside an HTML
  text node is technically illegal in HTML5 (the parser is required
  to replace it with U+FFFD), so browsers self-heal. This is not an
  XSS vector -- the surrounding HTML escape still applies -- but a
  pedantic admin who pastes a string from a binary protocol gets
  silent character mangling. Severity: **low**, no fix. The task did
  not ask for a control-character whitelist, and adding one now
  would push a stylistic decision ahead of the spec.

- **The `TestDefaultRegistryContainsText` integration test in
  `views/widget_wiring_test.go` is not strictly testing main.go's
  blank import -- the test file blank-imports the package itself, so
  the test would pass even if main.go's import were removed.** This
  is documented in the task ("This is a stand-in for [the binary's
  registry] -- since `init()` runs at import time, importing both
  packages in the test file is equivalent."). The test is a smoke
  test: it asserts the import-then-registry chain works at all,
  which is what AC-25 actually requires. A stricter test would
  parse `main.go` and check for the literal import line, which is
  brittle. Severity: **low**, no fix. The wiring is also covered by
  `go build ./...` -- if `main.go` breaks, the build fails.

- **A widget with `instance.Config = nil` renders an empty themed
  div, identical to `instance.Config = "wrong-type"`.** Both go
  through the comma-ok branch and produce a zero-value Config. This
  is fine in practice (the validator guarantees the right type
  reaches Render in production code) but the empty-div output is
  identical for two distinct misuse scenarios. A defensive renderer
  could log a warning on mistype, but the architecture explicitly
  says "Render MUST NOT panic" and accepts the silent-on-misuse
  trade-off. Severity: **low**, no fix.

## New tests added

In `internal/widget/text/adversarial_test.go` (new file, 13 top-level
tests with 12 sub-tests inside `TestAdversarial_RejectsMalformedJSONShapes`
and 5 inside `TestAdversarial_HTMLEntitiesAreEscaped`):

1. `TestAdversarial_BoundaryAccept4096` -- pins the inclusive upper
   bound (4096 bytes accepted).
2. `TestAdversarial_TrimRunsBeforeLengthCheck` -- pins the trim-then-
   measure ordering (padded 4096-byte body accepted).
3. `TestAdversarial_LengthCapIsBytes` -- pins byte-based cap (1024
   emojis accepted, 1025 rejected).
4. `TestAdversarial_RejectsMalformedJSONShapes` -- 12 rows: top-level
   null/array/number/string, `{}`, `{"text":null/123/[]/true}`, empty
   input, trailing comma, unterminated string.
5. `TestAdversarial_NilBytesRejected` -- nil input does not panic and
   returns the documented JSON-error.
6. `TestAdversarial_UnknownExtraFieldsAccepted` -- forward-compat
   property: unknown keys in JSON are ignored.
7. `TestAdversarial_OneMegBlobRejectedByCap` -- 1MB text body rejected
   by length cap.
8. `TestAdversarial_HTMLEntitiesAreEscaped` -- 5 attack-string rows
   verifying both "escaped form present" and "raw form absent" for
   `<script>`, `<img onerror=...>`, `"><svg/onload=...>`, `&amp;`,
   and `</div>`.
9. `TestAdversarial_NewReturnsSingleton` -- two `New()` calls return
   the same pointer.
10. `TestAdversarial_ConcurrentRenderRaceFree` -- 32 goroutines x 50
    renders each, `-race` clean.
11. `TestAdversarial_RenderWithMistypedConfigDoesNotPanic` -- comma-ok
    branch produces empty themed div without panicking.
12. `TestAdversarial_RenderProducesExactCSSClasses` -- exact div shape
    and class attribute.
13. `TestAdversarial_DefaultRegistrationFieldsMatch` -- registry
    contents match `Registration()` return value.

All 13 tests pass. No tests were committed that demonstrate broken
behaviour.

## Fixes applied

None. Every adversarial probe was either rejected as designed, or
exercised a guaranteed code path that produced the documented
behaviour. The implementation faithfully follows the architecture
document.

## Green-bar

```
gofmt -l .             # empty
go vet ./...           # clean
go build ./...         # clean
go test ./...          # ok
go test -race ./...    # ok (text package: 1.4s)
```

All four gates pass with race detection enabled across the full
module. The 13 new tests run in roughly 30ms total under `-race`.
`templ generate` is idempotent (`updates=0`).

## Recommendation

**ACCEPT.** The text widget implements every spec requirement and
every architecture commitment. The validator is total, strict,
bounded, and deterministic; the renderer is stateless, concurrent-
safe, and inherits templ's HTML escape; the registration self-
installs in `init()` and `main.go` blank-imports the package. The
`views.Deps.Widgets` field is added with a nil-safe zero value, and
all 50+ existing view tests continue to pass without setting it.

The four low-severity notes -- the spec's "characters" / impl's
"bytes" wording mismatch, the NUL-byte pass-through, the wiring
test's reliance on the test file's own blank import rather than
main.go's, and the silent-on-mistype renderer -- are observations
about the contract surface, not bugs. All are correctly handled by
the architecture as written; none rise to fix-worthy severity.

The contract is now fully validated end-to-end. SPEC-005 is complete:
TASK-019 shipped the interface + registry, TASK-020 ships the
placeholder text widget that proves the contract works. Phase 3
widgets (time, weather, calendar, etc.) can now be authored
independently against this stable surface.
