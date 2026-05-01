---
id: REVIEW-017
task: TASK-017
spec: SPEC-004
arch: ARCH-004
status: ACCEPT
reviewer: tester
reviewed: 2026-04-30
---

# Review: TASK-017 (Themes Service, Validators, Default Seed, CSS Helper)

## Summary

The Themes domain layer holds up under hostile probing. The validators are
strict whitelists for Name / Hex / Radius and a tight blacklist for the
font-family field; every dangerous-character probe (HTML break-out, control
characters including ESC and BEL, semicolons, braces, backslashes, embedded
nulls, leading-zero digits in foreign scripts, trailing newlines on hex,
multi-line names) was rejected. The CSS rendering helper is a pure
formatter as documented, deterministic across goroutines under `-race`, and
emits declarations in the spec'd order. The Service correctly wraps
`EnsureDefault` and `SetDefault` in transactions; under concurrent fan-out
the "exactly one default" invariant holds; ID generation is a 32-char hex
slice with no off-by-one collision risk; SQL injection probes via the name
field are rejected at the validator and (belt-and-braces) defused by the
parameterised query.

I tried hard to find a critical or high issue and could not. All tests
written for this review pass and exercise behaviour the developer's tests
left implicit: Update-with-colliding-name returns ErrDuplicateName,
EnsureDefault's seed values byte-equal `static/css/app.css`,
EnsureDefault and SetDefault survive concurrent fan-out, the CSS
declaration order is pinned, and SetDefault on a deleted theme returns
ErrThemeNotFound rather than panicking.

The only finding worth recording is a **low**-severity observation about
the SQL error-string match in `isUniqueNameViolation`. This is the same
fragile pattern the rest of the codebase uses; fixing it would touch
unrelated packages and is out of scope.

**Recommendation: ACCEPT.**

## AC coverage

| AC      | Description                                                                                  | Status | Evidence                                                                                          |
| ------- | -------------------------------------------------------------------------------------------- | ------ | ------------------------------------------------------------------------------------------------- |
| AC-1    | EnsureDefault on empty table inserts one row with documented values, IsDefault=1             | PASS   | `TestEnsureDefaultSeedsRow`, `TestEnsureDefaultColorsMatchStaticCSS`                              |
| AC-2    | EnsureDefault is idempotent: second call leaves table unchanged                              | PASS   | `TestEnsureDefaultIdempotent` (compares CreatedAt before/after)                                   |
| AC-3    | Create with whitespace-only name returns *ValidationError with Fields["name"] populated      | PASS   | `TestCreateRejectsBadName`                                                                        |
| AC-4    | Create with `theme<script>` returns *ValidationError                                         | PASS   | `TestCreateRejectsHtmlInjectionInName`                                                            |
| AC-5    | Create with 65-char name returns *ValidationError                                            | PASS   | `TestCreateRejectsLongName`                                                                       |
| AC-6    | Two Create calls with the same name -> second returns ErrDuplicateName                      | PASS   | `TestCreateDuplicateName`                                                                         |
| AC-7    | Create with ColorBg=#0b0d11 succeeds, returned value is `#0b0d11`                            | PASS   | `TestCreateAcceptsValidHex`                                                                       |
| AC-8    | Create with ColorBg=#FFF succeeds, returned value is lower-cased to `#fff`                   | PASS   | `TestCreateNormalisesUppercaseHex`, `TestCreateHappyPath`                                         |
| AC-9    | Create with ColorBg=`red` returns *ValidationError                                           | PASS   | `TestCreateRejectsBadHex/named_color`                                                             |
| AC-10   | Create with ColorBg=`rgb(...)` returns *ValidationError                                      | PASS   | `TestCreateRejectsBadHex/rgb_function`                                                            |
| AC-11   | Create with ColorBg=`#zzzzzz` returns *ValidationError                                       | PASS   | `TestCreateRejectsBadHex/non-hex_digits`                                                          |
| AC-12   | Create with FontFamily containing `;` and `<`/`>` returns *ValidationError                  | PASS   | `TestCreateRejectsBadFontFamily`                                                                  |
| AC-13   | Create with 257-char FontFamily returns *ValidationError                                     | PASS   | `TestCreateRejectsLongFontFamily`                                                                 |
| AC-14   | Create with Radius=`10px` succeeds, stored as `10px`                                         | PASS   | `TestCreateAcceptsRadius10px`                                                                     |
| AC-15   | Create with Radius=`10` (no unit) returns *ValidationError                                   | PASS   | `TestCreateRejectsRadiusWithoutUnit`                                                              |
| AC-16   | A -> SetDefault(B) -> SetDefault(A): exactly one default and it is A                         | PASS   | `TestSetDefaultBackToOriginal`                                                                    |
| AC-17   | SetDefault when inner UPDATE affects 0 rows -> ErrThemeNotFound, count unchanged             | PASS¹  | `TestSetDefaultPreservesStateOnNotFound`, `TestSetDefaultUnknownID` (post-condition only; see note) |
| AC-18   | Delete of default returns ErrCannotDeleteDefault, row still present                          | PASS   | `TestDeleteOfDefault`                                                                             |
| AC-19   | Delete of non-default removes row, GetDefault still returns original default                | PASS   | `TestDeleteOfNonDefaultPreservesDefault`                                                          |
| AC-20   | After AC-16, `SELECT COUNT(*) FROM themes WHERE is_default = 1` = 1                          | PASS   | `TestSetDefaultBackToOriginal` (asserts `CountDefaultThemes == 1`)                                |
| AC-28   | CSSVariables() contains every required custom-property name                                  | PASS   | `TestCSSVariablesContainsAllRequiredDeclarations`                                                 |
| AC-29   | CSSVariables() called twice returns byte-identical strings                                   | PASS   | `TestCSSVariablesDeterministic`                                                                   |
| AC-30   | When ColorAccent==`#7b93ff`, output contains literal `--accent: #7b93ff;`                    | PASS   | `TestCSSVariablesAccentSpecific`                                                                  |
| AC-31   | CSSVariables() output contains none of `<`, `>`, `</style>`                                  | PASS   | `TestCSSVariablesNoBreakoutCharacters`                                                            |
| AC-33   | EnsureDefault with Config{DefaultName:"onyx"} on empty table -> seeded name is `"onyx"`     | PASS   | `TestEnsureDefaultRespectsConfigName`                                                             |

¹ AC-17 note: the "row deleted between the lookup and the inner UPDATE" path
is genuinely hard to exercise with the test helper's pinned single
connection (BeginTx holds the only connection through Commit, so no other
goroutine can sneak a DELETE in between). The developer's test exercises
the equivalent post-condition: `SetDefault` on a never-existing id returns
`ErrThemeNotFound` and `CountDefaultThemes` is unchanged, which is the
property AC-17 actually asserts about external state. The branch in code
that returns `ErrThemeNotFound` from `RowsAffected == 0` is reachable but
not testable in this harness; it remains as defence-in-depth code rather
than a verified path. Documented here, not a fix-blocker.

## Adversarial findings

### Findings that did NOT reveal a bug (the implementation held)

**Validator stress: empty / whitespace / long / multi-line / control-byte
inputs.** Each validator was probed with empty strings, whitespace-only
strings (space, tab, multiple), 64-char boundary, 65/257-char overflow,
embedded `\n` / `\r` / `\t`, ANSI-escape-style 0x1b bytes, BEL (0x07),
embedded null (0x00), Unicode lookalikes (Cyrillic `а` rejects because
multibyte UTF-8 falls outside the ASCII regex character class), Unicode
digits (`#୦୦୦` -- regex `[0-9A-Fa-f]` is ASCII-only and rejects),
trailing newline on hex (`#FFFFFF\n` -- Go's regexp `$` matches end of
text by default and rejects), backslash, semicolon, braces, angle
brackets. All correctly classified.

**Multi-field validation accumulation.** `validateInput` runs every
validator and accumulates failures into `*ValidationError.Fields`. I
submitted an Input with five simultaneously-bad fields and confirmed
all five surface (`TestValidateInputAccumulatesErrors`). The
`Error()` method sorts the keys so log output is deterministic
(`TestValidationErrorMessageStable`).

**Whitespace handling on Name.** `Input{Name: "  My Theme  "}` passes
through validateInput with the surrounding whitespace stripped and the
inner space preserved (`TestValidateInputNormalises`). Hex values are
explicitly NOT trimmed -- pasting `"  #ffffff"` into a color field
fails the hex regex outright, surfacing the underlying tooling
problem rather than silently accepting (`TestValidateHex/leading_space_not_trimmed`).

**Radius edge cases.** Probed `"0"`, `"0px"`, `"010px"` (leading
zeros -- accepted; legal CSS), `"0em"`, `"0.0rem"`, `"5.5em"`,
`".5rem"` (rejected: no leading zero), `"-0px"` (rejected),
`"1e2px"` (rejected: scientific notation), `"00"` (rejected: no
unit), `"10px;"` (rejected: trailing junk), `"10pt"` (rejected:
unsupported unit). The regex anchor pair `^...$` keeps every reject
case airtight. The accepted-but-quirky `"010px"` is harmless legal
CSS and outside the spec's threat model.

**Real font stacks pass.** Comma-separated double-quoted stacks (the
font-family blacklist excludes `;{}<>\` and bytes < 0x20 but
explicitly preserves comma, double-quote, single-quote, hyphen,
period, and space). `"SF Mono", "Fira Code", monospace` and
`-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui,
sans-serif` validate cleanly (`TestValidateFontFamilyAllowsRealFontStacks`).

**CSS injection through hand-constructed Theme.** The `CSSVariables()`
contract is "pure formatter, trusts validation". I constructed Theme
values that bypass validation and fed them through CSSVariables() --
the function does not escape, as documented. The barrier is the
validators, and the validators reject every dangerous character
needed to break out of `<style>...</style>` or terminate a CSS
declaration. There is no path from external user input to
CSSVariables() that bypasses validation: every Service method
(`Create`, `Update`, `EnsureDefault`) routes through `validateInput`
(or hard-coded constants for the seed). This was verified by
auditing every external entry point on the Service.

**CSS declaration order.** Pinned by `TestCSSVariablesDeclarationOrder`
to the exact order documented in the architecture doc:
`--bg, --surface, --border, --text, --text-muted, --accent, --radius,
--font-family, [--font-family-mono]`. The substring `--text:` is a
prefix of `--text-muted:`, so a sloppy substring search could give a
false positive; a dedicated test uses ordering-by-index to disambiguate
(`TestCSSVariablesTextOrderingDistinguishesMutedFromBare`).

**CSSVariables determinism under concurrency.** 32 goroutines all
calling `CSSVariables()` on a shared Theme value produce byte-identical
output (`TestCSSVariablesConcurrent`, run under `-race`). No data
race; `strings.Builder` is allocated fresh inside the method, so
concurrent calls do not share builder state.

**EnsureDefault concurrency.** 8 goroutines fanned out against a
fresh database. The transaction-wrapped count+insert keeps
`CountDefaultThemes == 1` and `len(List) == 1` after all goroutines
finish (`TestEnsureDefaultConcurrent`). `db.OpenTestDB(t)` pins
`MaxOpenConns(1)`, so SQLite serialises the connections behind the
pool, but the test still verifies that the application-layer logic
in EnsureDefault is race-clean (the `-race` flag would catch a
goroutine-visible mutation of `Service` fields or shared `*db.Queries`).

**SetDefault concurrency.** 6 goroutines each setting a different
theme as default. Whichever goroutine wins last leaves
`CountDefaultThemes == 1` (`TestSetDefaultConcurrent`). The
transaction in `SetDefault` is the safety net; the partial unique
index `themes_one_default` is the second line of defence -- if a
buggy refactor dropped the transaction, the index would surface a
hard SQL error rather than allowing two coexisting defaults.

**Update name collision.** Renaming theme B to match theme A's
existing name surfaces as `ErrDuplicateName`, the same error Create
returns (`TestUpdateRejectsDuplicateName`). The test also covers the
self-rename case (renaming a theme to its own current name is a
no-op and does not fire the UNIQUE constraint).

**Update / SetDefault on a deleted theme.** Both surface as
`ErrThemeNotFound`, never panic
(`TestUpdateOfDeletedThemeReturnsNotFound`,
`TestSetDefaultOnDeletedThemeReturnsNotFound`).

**SQL injection through Create's name.** The injection probe
`'); DROP TABLE themes;--` fails the validator (no apostrophe or
semicolon allowed in the name regex), and even if it had passed the
parameterised INSERT would have stored the metacharacters as literal
column data. `CountDefaultThemes` confirms the table survives
(`TestCreateRejectsSQLInjectionInName`).

**Generated ID collisions / off-by-one.** 1024 calls to `generateID()`
all produced unique 32-char hex strings (`TestGenerateIDUniqueness`).
The slice-to-32-chars pattern is borrowed verbatim from `internal/auth`
and produces 16 bytes of entropy.

**Default seed values match `static/css/app.css` byte-for-byte.**
`TestEnsureDefaultColorsMatchStaticCSS` pins every documented colour
plus the sans-serif and monospace stacks (Spec §18). If app.css
drifts or the constants in `service.go` are fat-fingered, the test
fails loudly.

**List on empty database returns non-nil empty slice.** Pinned by
`TestListEmptyReturnsNonNil` (developer-supplied; reproduced under my
review).

**Round-trip preservation.** Every field set on Create returns
identically through GetByID -- catches bugs where the SELECT/Scan
column order drifts from the INSERT (`TestCreateThenGetByIDPreservesEverything`).

### Notes that did not warrant fixes

- **`isUniqueNameViolation` uses substring matching** on
  `"UNIQUE constraint failed: themes.name"`. The modernc.org/sqlite
  driver does not expose a stable error code through `errors.Is`,
  so the codebase's existing convention (mirrored from
  `internal/auth/auth.go`) is to grep the error string. If
  modernc.org/sqlite changed its error format, this would silently
  fall through to the generic `fmt.Errorf("create theme: %w", err)`
  branch and the admin would see "could not create theme" instead
  of a duplicate-name field error. Severity: **low**, project-wide
  consistency is more valuable than diverging here. No fix.
- **`validateInput` double-trims `Name`** -- once at the field
  level and again inside `validateName`. Idempotent; the duplicated
  trim is wasted CPU on an already-cheap path. Cosmetic. No fix.
- **AC-17 mid-transaction-deletion path is unreachable in tests**
  (see AC table note). The `RowsAffected == 0 -> ErrThemeNotFound`
  branch in `SetDefault` is correct in form; it just cannot be
  forced by the test helper. Not a fix-blocker.
- **`Delete` `n == 0` branch returns a generic error** (with a
  fmt.Errorf message, no sentinel). The branch is documented as
  unreachable in practice (the `WHERE id = ? AND is_default = 0`
  clause combined with the application-layer `IsDefault` check
  closes the window). No fix.

## New tests added

In `internal/themes/adversarial_test.go` (new file):

- `TestUpdateRejectsDuplicateName` -- locks in the contract that a
  rename collision returns `ErrDuplicateName` (the same error
  Create returns); also exercises the self-rename no-op case.
- `TestEnsureDefaultColorsMatchStaticCSS` -- byte-for-byte pin of
  every seeded value against the documented constants from
  `static/css/app.css` (Spec §18).
- `TestEnsureDefaultConcurrent` -- 8-goroutine fan-out probe of the
  EnsureDefault transaction; asserts `CountDefaultThemes == 1` and
  `len(List) == 1` afterwards. Run with `-race` to surface any
  Service-field mutation race.
- `TestSetDefaultConcurrent` -- 6-goroutine fan-out probe of
  SetDefault, each goroutine targeting a different theme.
  Asserts `CountDefaultThemes == 1` no matter which goroutine wins.
- `TestCSSVariablesConcurrent` -- 32 goroutines call
  `CSSVariables()` on a shared Theme; asserts byte-identical output
  to a pre-computed reference. Catches a future refactor that
  shared a builder across calls.
- `TestCSSVariablesDeclarationOrder` -- pins the exact ordering
  of declarations against the architecture-documented sequence.
- `TestCSSVariablesTextOrderingDistinguishesMutedFromBare` -- guards
  the `--text:` / `--text-muted:` prefix-overlap trap so a typo
  refactor surfaces here.
- `TestGenerateIDUniqueness` -- 1024 calls, all unique, all 32 chars.
- `TestCreateRejectsSQLInjectionInName` -- classic injection probe
  through the name field; rejected at the validator and the table
  survives.
- `TestUpdateOfDeletedThemeReturnsNotFound` -- Update on a deleted
  id surfaces `ErrThemeNotFound`, never panics.
- `TestSetDefaultOnDeletedThemeReturnsNotFound` -- SetDefault on a
  deleted id surfaces `ErrThemeNotFound`.
- `TestCreateRejectsControlCharacterInFontFamily` -- bytes 0x00,
  0x07, 0x1b, 0x1f all rejected by `validateFontFamily` (the
  `c < 0x20` clause). Catches a future refactor that loosens the
  control-character check to "newline only".
- `TestValidateFontFamilyAllowsRealFontStacks` -- the boundary case:
  the blacklist must not over-reject real-world stacks. Exercises
  comma-separated double-quoted stacks, the macOS system stack, a
  single-quoted stack, and a bare family name.
- `TestCreateThenGetByIDPreservesEverything` -- full-field round-trip
  catches a SELECT/Scan column-order drift bug.

## Fixes applied

None. The implementation passed every adversarial probe.

## Green-bar

```
gofmt -l .             # empty
go vet ./...           # clean
go build ./...         # clean
go test ./...          # ok
go test -race ./...    # ok
```

All four gates pass with race detection enabled.

## Recommendation

**ACCEPT.** The Themes domain layer is correct, defensive, and
matches the architecture document precisely:

- validators are strict whitelists (Name, Hex, Radius) plus a
  tight blacklist (FontFamily) and accumulate per-field errors;
- `CSSVariables()` is pure, deterministic, and ordered;
- the seed values match `static/css/app.css` byte-for-byte;
- `EnsureDefault` and `SetDefault` are transaction-protected and
  hold their invariants under concurrent fan-out;
- ID generation is collision-free in practice and uniformly
  32 characters;
- SQL injection probes are rejected at the validator and defused
  at the parameterised query layer.

The single low-severity note (substring-based duplicate-name
detection) is consistent with the rest of the codebase and not
worth diverging on. No source changes were necessary.
