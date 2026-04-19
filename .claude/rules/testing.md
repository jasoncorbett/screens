# Testing

## Philosophy

Write tests that **earn their existence**. Every test should either catch a real bug, document a non-obvious behavior, or guard against a specific regression. If you can't articulate why a test would fail, don't write it.

### Write tests for:
- Business logic with branching paths (happy path + meaningful edge cases)
- Error handling and failure modes
- Any function where a subtle bug would cause silent data corruption or incorrect output
- Behaviors that are non-obvious from the function signature alone

### Do not write tests for:
- Simple getters/setters or pure pass-through functions
- Framework or stdlib behavior you don't own
- Implementations — test behavior and outcomes, not internal mechanics
- Every possible input permutation when a few representative cases cover the same ground

### What makes a good test:
- It has a single, clear failure reason — when it fails, you know exactly what broke
- It reads like documentation — the test name and structure explain the invariant being protected
- It tests the contract (inputs → outputs), not the implementation details
- It would catch a real bug if someone introduced one
- Do not verify a function was called, but rather verify it returned the right data or wrote the right data to the database

**Coverage is not the goal.** A 40% coverage suite with sharp, meaningful tests is worth more than an 80% suite padded with assertions that verify `result != nil`.

When in doubt, write fewer tests and make them count.

## Conventions

- Use the standard `testing` package only. No `testify`, no `gomega`, no third-party assertion libraries.
- Prefer **table-driven tests**:
  ```go
  tests := []struct {
      name string
      in   string
      want int
  }{ /* ... */ }
  for _, tt := range tests {
      t.Run(tt.name, func(t *testing.T) {
          got := thing(tt.in)
          if got != tt.want {
              t.Errorf("thing(%q) = %d, want %d", tt.in, got, tt.want)
          }
      })
  }
  ```
- HTTP handlers: test with `net/http/httptest.NewRecorder` for unit tests and `httptest.NewServer` for integration tests that exercise the real mux.
- Use `t.Helper()` in test helpers and `t.Cleanup(...)` for teardown. Mark independent subtests with `t.Parallel()` when safe.
- Configuration in tests: construct `config.Config` values directly in the test rather than setting env vars. If env must be exercised, use `t.Setenv` so cleanup is automatic.
- File location: keep `_test.go` files alongside the code they cover, in the same package. Use `package foo_test` only when the test truly needs to consume the package as an external caller.
- Target: `go test ./...` passes; `go test -race ./...` passes for anything concurrent.

## Verification

When verifying a bug fix, test that the **specific reported problem** no longer occurs — don't just confirm the happy path produces some output.

- Define the expected result before running the test, then compare against it (e.g. "15 days requested = 15 transaction files expected, got X").
- If the bug was "X is missing," explicitly check that X is now present.
- A passing happy path is not a fix confirmation — reproduce the original failing scenario and show it now succeeds.
