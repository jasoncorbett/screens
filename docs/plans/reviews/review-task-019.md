---
id: REVIEW-019
task: TASK-019
spec: SPEC-005
arch: ARCH-005
status: ACCEPT
reviewer: tester
reviewed: 2026-04-30
---

# Review: TASK-019 (Widget interface, registration struct, and registry)

## Summary

The widget contract package is small, idiomatic, and stands up to hostile
probing. The RWMutex split is correct: writes take the write lock; reads
take the read lock; user-supplied callbacks (`ValidateConfig`, `Widget.Render`)
run with the read lock RELEASED. That second property is the load-bearing
detail -- if the registry held the read lock across a callback, any
recursive call back into the registry would deadlock once a writer was
queued. I exercised this directly with three recursive-callback tests
(validator -> Get/List, renderer -> Get/List, validator -> Register on
the same registry) and all three completed cleanly. Concurrent-duplicate
registration is also race-free: 32 goroutines race to register the same
type, exactly one wins, the other 31 each see the documented "already
registered" error.

The exported surface matches what the architecture document commits to,
with no incidental exports. Errors include the type name in every
actionable branch (missing-field, duplicate, unknown-type, validator
failure). Error wrapping uses `%w` so callers can `errors.Is` and
`errors.As` the underlying validator error -- I confirmed this with both
a sentinel `errors.New(...)` (developer-supplied test) and a structured
`*validatorErr` type (new test). The `Render` path correctly skips
`reg.New()` when the validator returns an error, and `Validate`
forwards the validator's error verbatim. `List()` returns a fresh slice
on every call -- a hostile caller that zeroes every entry of the
returned slice cannot corrupt the registry's internal state.

I found NO critical, high, or medium issues that required source
changes. The 16 adversarial tests added in this review exercise
behaviour the developer's suite left implicit:

- the duplicate-Register error path is concurrency-safe (exactly one
  winner, all others get the documented error);
- mixed-traffic Get/List/Validate/Render against a registry that's
  simultaneously being written to is `-race` clean;
- recursive callbacks (validator/renderer/Register from inside user
  code) do not deadlock -- proves the lock is released before user code
  runs;
- the slice returned by `List()` is a true copy; caller mutation does
  not leak;
- a panicking validator propagates up to the caller (the documented
  behaviour, since validators MUST NOT panic per the spec);
- when the validator errors, `reg.New()` is never called;
- `nil` raw bytes and `nil` ctx pass through to the widget unchanged --
  the registry takes no opinion;
- the missing-field error messages all contain the registration's Type
  name, so a Phase 3 widget author can find the bug from logs alone;
- `errors.As` recovers a structured validator error through the
  `Render` wrapping, not just `errors.Is`-able sentinels;
- 32 goroutines hammering `Default()` in parallel see the same pointer
  -- the `sync.Once` guard works under contention;
- a 1MB raw blob passes through to the validator without modification;
- `reg.New` is called exactly once per `Render` (no caching).

The two low-severity observations are documentation notes: (1)
`Validate` does not overwrite `inst.Type` the way `Render` does, which
is a deliberate asymmetry but is not called out in the doc comment;
(2) the Type field accepts whitespace and unusual characters, which is
fine for the contract but worth flagging for the eventual spec on
widget-type naming. Neither rises to a fix-worthy bug.

**Recommendation: ACCEPT.**

## AC coverage

| AC      | Description                                                                        | Status | Evidence                                                                  |
| ------- | ---------------------------------------------------------------------------------- | ------ | ------------------------------------------------------------------------- |
| AC-1    | `Widget` interface with single `Render(ctx, instance, theme) templ.Component`      | PASS   | `internal/widget/widget.go:31-40`; `go doc` shows one method              |
| AC-2    | `Registration` struct has Type, DisplayName, Description, New, DefaultConfig, ValidateConfig | PASS   | `internal/widget/registration.go:12-46`                            |
| AC-3    | `Instance` struct has ID, Type, Config                                             | PASS   | `internal/widget/widget.go:42-59`                                         |
| AC-4    | `NewRegistry().Register(validReg)` returns nil                                     | PASS   | `TestRegister_ThenGet`                                                    |
| AC-5    | Duplicate Register returns error containing type name and "already"                | PASS   | `TestRegister_DuplicateRejected`; `TestDuplicate_ErrorIncludesTypeName`   |
| AC-6    | `Get("nonexistent-type")` returns zero Registration and false                      | PASS   | `TestGet_UnknownType`                                                     |
| AC-7    | `Get("alpha")` after registering returns the registration and true                 | PASS   | `TestRegister_ThenGet`                                                    |
| AC-8    | `List()` returns sorted by Type                                                    | PASS   | `TestList_Ordering`                                                       |
| AC-9    | `Render(ctx, "alpha", validJSON, theme)` returns non-nil component, nil error      | PASS   | `TestRender_HappyPath`                                                    |
| AC-10   | `Render` with failing validator returns nil component, wrapped error               | PASS   | `TestRender_FailingValidator`; `TestRender_WrappedErrorUnwrapsToValidatorError` |
| AC-11   | `Render` of unknown type returns nil component, "unknown type" error               | PASS   | `TestRender_UnknownType`                                                  |
| AC-12   | Concurrent `Get` is `-race` clean                                                  | PASS   | `TestGet_ConcurrentReadsAreRaceFree`; `TestRegistry_ConcurrentMixedTraffic` extends to mixed traffic |

All 12 spec ACs scoped to this task pass. ACs 13-27 belong to TASK-020
(text widget + main.go wiring) and are out of scope here.

## Adversarial findings

### Findings that did NOT reveal a bug (the implementation held)

**Concurrent duplicate registration.** 32 goroutines race to register
type `"alpha"` on a fresh registry. The implementation uses `sync.Mutex`
write-lock around the duplicate-check + insert pair, which is the
correct pattern -- a naive RLock-then-upgrade would let multiple writers
through. Test asserts: exactly one goroutine returns nil; the other 31
return a non-nil error containing `"already"`. Pinned by
`TestRegister_DuplicateConcurrent`.

**Mixed-traffic concurrency under -race.** A registry has one stable
type pre-registered. 16 reader goroutines run 500 iterations each of
`Get("stable")` + `List()` + `Validate("stable", ...)` + `Render(ctx,
"stable", ...)`. Concurrently, 4 writer goroutines register 50 unique
types each. With `-race` enabled the run is clean across multiple
invocations. This proves: (1) the read path takes only `RLock`; (2)
the write path doesn't observe partially-constructed reader state; (3)
no mutation hides under the read lock. Test:
`TestRegistry_ConcurrentMixedTraffic`.

**Recursive callback from validator does not deadlock.** A validator
that calls back into `r.Get` and `r.List` on the same registry it's
running under completes cleanly. This proves the registry releases its
read lock before invoking `reg.ValidateConfig`. If the implementation
held the lock, this test would hang under `-timeout`. Pinned by
`TestRegistry_RecursiveCallFromValidator`.

**Recursive callback from renderer does not deadlock.** Same shape, but
the recursive call happens inside `Widget.Render`. Pinned by
`TestRegistry_RecursiveCallFromRenderer`.

**Recursive Register from inside a validator does not deadlock.** The
hardest case: a user's validator triggers a write-lock acquisition on
the same registry the validator is running under. If the outer Validate
held even a read lock across the call, the inner write would deadlock
against itself. The test passes; the inner registration is visible to a
follow-up `Get`. Pinned by `TestRegistry_RecursiveRegisterFromValidator`.

**`List()` returns a copy, not a view.** Three types are registered.
The test takes `List()`, zeroes every entry of the returned slice
(`reg = widget.Registration{}`), and calls `List()` again. The second
call returns the same three types in the same order. Confirms `List`
allocates a fresh slice per call (per the architecture). Pinned by
`TestList_ReturnsCopy`.

**Validator panic propagates.** A validator that calls `panic("...")`
does not crash the process silently inside the registry; the panic
propagates to the `Render` caller. The architecture is explicit:
"Validators MUST NOT panic". Recovery would mask a build-time bug, so
the registry deliberately doesn't recover. Pinned by
`TestRender_ValidatorPanicPropagates`. If a future spec changes the
contract to "registry recovers from misbehaving widget code", this test
should be inverted (it would then assert no panic AND a wrapping
error).

**`reg.New` not called when validator errors.** A validator returning
`errors.New("nope")` paired with a `New` that increments a counter:
`Render` is called once, validator returns error, counter stays at 0.
Confirms the documented call order (Validate-first, then New, then
Render) is enforced -- a buggy ordering that called New before
validating would surface here. Pinned by
`TestRender_NewNotCalledOnValidatorError`.

**`nil` raw bytes pass through.** The registry forwards `nil` to the
validator unchanged; it does not pre-empt the validator's authority by
substituting an empty `[]byte{}` or rejecting the call early. This is
the right call: the validator is the single source of truth on what
constitutes a valid (or invalid) configuration. Pinned by
`TestRender_NilRawForwardedToValidator`.

**`nil` ctx passes through.** Same shape: the registry does not assert
`ctx != nil`; widgets are expected to honour cancellation if they need
it. A future "reject nil ctx" guard would be a deliberate contract
change, not an accident. Pinned by `TestRender_NilContextPassedThrough`.

**Errors include type name in every actionable branch.** Each
missing-field branch (`New`, `DefaultConfig`, `ValidateConfig`) and the
duplicate-type branch all surface the registration's Type in the error
string. A Phase 3 widget author misregistering their widget can find
the bug from logs alone -- they don't have to grep the call site.
Pinned by `TestRegister_ErrorsIncludeTypeName` and
`TestDuplicate_ErrorIncludesTypeName`. (Note: the empty-Type branch
doesn't include the type name because the type name *is* the empty
string. The error message says "Type must not be empty" instead, which
is just as actionable.)

**`errors.As` works through `Render`'s wrapping.** A structured
validator error type (`*validatorErr`) is correctly recovered by
`errors.As(err, &got)` after passing through the
`fmt.Errorf("widget %q: validate config: %w", ...)` wrapping. This
goes one step beyond the developer's `errors.Is` test. Pinned by
`TestRender_WrappedErrorUnwrapsToValidatorError`.

**`Default()` is concurrent-safe on first init.** 32 goroutines call
`widget.Default()` simultaneously. All return the same pointer; under
`-race` the run is clean. Confirms `sync.Once` correctly guards the
single write to `defaultReg`. Pinned by `TestDefault_ConcurrentFirstInit`.

**1MB JSON blob passes through.** The validator sees exactly the bytes
the caller passed. The registry does no buffering, no copying, no
truncation. Useful baseline for future widget specs that ferry larger
configs (e.g., a slideshow widget with embedded image URLs). Pinned by
`TestRender_LargeJSONBlob`.

**`reg.New` called once per `Render`.** Five sequential renders, five
calls to `New`. Confirms the architecture's "New is called per render"
contract; a future "cache the constructed widget" optimisation would
break this test, which is the desired behaviour (it would force a spec
amendment first). Pinned by `TestRender_NewCalledOncePerRender`.

**No incidental exports.** `go doc ./internal/widget/` lists exactly
the surface the architecture commits to: `MustRegister`, `Instance`,
`Registration`, `Registry`, `Default`, `NewRegistry`, `Widget`. No
helper types, no leaked internals. Future-proofing rests on this
surface staying tight; the current shape preserves that property.

**Doc comments on every exported identifier.** `widget.go`,
`registration.go`, `registry.go`, `default.go` all carry doc comments on
the package, every type, every method, and every exported function.
Verified by inspection.

**No third-party imports.** The package imports `context`, `fmt`,
`sort`, `sync` from stdlib plus `github.com/a-h/templ` and
`github.com/jasoncorbett/screens/internal/themes` -- both already
approved by the project. No new go.mod entries.

### Notes that did not warrant fixes

- **`Validate` does not overwrite `inst.Type`; only `Render` does.**
  This is a deliberate asymmetry per the architecture document:
  `Render` populates `inst.Type` so the widget renderer always sees a
  populated Type even if the validator forgot. `Validate` is called by
  Screen Model on the write path, where the caller already has the
  type name separately and is about to persist it as a discrete
  database column. The asymmetry is correct but is not called out in
  either method's doc comment. A future doc tweak ("Note: unlike
  Render, Validate does not populate Instance.Type ...") would
  improve discoverability. Severity: **low**, no fix.

- **`Register` accepts non-empty `Type` values containing whitespace,
  control characters, slashes, or other characters that may cause
  problems when the type identifier is later embedded in a URL path or
  database column.** The current contract is "Type must not be empty";
  the spec says "lowercase ASCII, no spaces" but the implementation
  does not enforce that. This is fine at this layer -- the spec is
  prescriptive guidance for widget authors, not a runtime contract --
  and tightening it now would be premature: TASK-020 will register
  `"text"` which is well-formed, and any Phase 3 widget that
  registers a malformed type will surface elsewhere (URL routing,
  display-name rendering). Severity: **low**, no fix. If Screen
  Display or the picker UI later wants stricter Type validation, the
  natural place is a helper exported alongside `Register`, not a
  silent rejection inside `Register` itself.

- **The `defaultReg` global is never cleaned up between tests.** The
  developer's `TestMustRegister_PanicsOnDuplicate` registers a
  `__widget_test_dup__` type on the global. Because there is no
  reset hook, this leaks across the test binary. The architecture
  explicitly accepts this -- tests are supposed to use `NewRegistry()`,
  and the only `Default()`-touching test uses a clearly-test-only
  type that cannot collide with a real widget. Severity: **low**, no
  fix; matches the architecture's "tests don't reset the global"
  guidance.

## New tests added

In `internal/widget/adversarial_test.go` (new file):

1. `TestRegister_DuplicateConcurrent` -- 32 goroutines race; exactly
   one wins, others get the "already" error.
2. `TestRegistry_ConcurrentMixedTraffic` -- 16 readers x 500 iters
   (Get + List + Validate + Render) running against 4 writers
   registering 50 types each. `-race` clean.
3. `TestRegistry_RecursiveCallFromValidator` -- validator recursively
   calls Get / List on the same registry; no deadlock.
4. `TestRegistry_RecursiveCallFromRenderer` -- renderer recursively
   calls Get / List on the same registry; no deadlock.
5. `TestRegistry_RecursiveRegisterFromValidator` -- validator
   recursively calls Register on the same registry (writer from
   inside reader path); no deadlock and the inner registration
   becomes visible.
6. `TestList_ReturnsCopy` -- caller zeros the returned slice; next
   List call still returns the original three types in order.
7. `TestRender_ValidatorPanicPropagates` -- panicking validator
   propagates up; pins the documented "we don't recover" behaviour.
8. `TestRender_NewNotCalledOnValidatorError` -- when the validator
   errors, `reg.New` is never invoked; counter stays at 0.
9. `TestRender_NilRawForwardedToValidator` -- nil `[]byte` reaches the
   validator unchanged.
10. `TestRender_NilContextPassedThrough` -- nil ctx reaches the
    widget unchanged; the registry takes no opinion.
11. `TestRegister_ErrorsIncludeTypeName` -- table-driven; each
    missing-field branch's error includes the registration's Type.
12. `TestDuplicate_ErrorIncludesTypeName` -- the duplicate-type
    error includes the offending Type name.
13. `TestDefault_ConcurrentFirstInit` -- 32 goroutines hammer
    `Default()`; all see the same pointer; `-race` clean.
14. `TestRender_WrappedErrorUnwrapsToValidatorError` -- structured
    `*validatorErr` is recoverable via `errors.As` after the
    registry's `%w` wrapping.
15. `TestRender_LargeJSONBlob` -- 1MB raw blob reaches the validator
    with `len(raw)` intact.
16. `TestRender_NewCalledOncePerRender` -- 5 renders -> 5 calls to
    `reg.New`; pins "no caching" behaviour.

All 16 tests pass. No tests were committed that demonstrate broken
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
go test -race ./...    # ok
```

All four gates pass with race detection enabled across the full
module. The 16 new tests run in roughly 200ms total under `-race`.

## Recommendation

**ACCEPT.** The widget interface package implements the architecture
document exactly: a single-method `Widget` interface, a
plain-struct `Registration`, a map-backed `Registry` with an
`RWMutex` that releases the read lock before invoking user code, a
`Default()` singleton guarded by `sync.Once`, and a `MustRegister`
helper that panics on registration error. The exported surface is
tight and matches the spec's stability table. Concurrency is correct
under `-race` for both pure-read and mixed-traffic workloads. User
callbacks can recursively touch the registry (Get / List / Register)
without deadlocking. Errors include the offending Type name in every
actionable branch and use `%w` correctly so structured error types
are recoverable. No third-party dependencies; no incidental exports.

The two low-severity notes -- the deliberate `Validate` /`Render`
asymmetry around `inst.Type`, and the absence of structural Type
validation -- are observations about contract surface, not bugs.
Both are correctly handled by the architecture as written. No source
changes were necessary.

The contract is now stable for TASK-020 (placeholder text widget +
main.go wiring) to build on.
