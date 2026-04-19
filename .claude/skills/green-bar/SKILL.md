---
name: green-bar
description: Run the pre-commit green-bar checks (gofmt, vet, build, test) and report pass/fail. Use before every commit, or whenever the user asks to verify the project is in a clean state. If code touched concurrency primitives (goroutines, channels, sync.*, atomic), also run the race detector.
---

# Green-bar checks

Run these four commands from the repo root, in order. All must produce a clean result before a commit is acceptable.

1. `gofmt -l .` — must print nothing. Any listed file is unformatted; run `gofmt -w <file>` to fix.
2. `go vet ./...` — must exit 0 with no warnings.
3. `go build ./...` — must compile cleanly.
4. `go test ./...` — all tests must pass.

## When to also run `-race`

If the change touched any of: goroutines, channels, `sync.*`, `sync/atomic`, `context` cancellation paths, or HTTP handlers that share state, additionally run:

- `go test -race ./...`

## Reporting

Report each step's result on one line (pass / fail + first failing output). Do not summarize — the user wants to see exactly which gate failed. If everything is clean, a single line "green bar: all four checks pass" is enough.

## Do not

- Do not auto-fix vet or test failures without showing the user first.
- Do not skip a step because a prior run "just passed" — always run all four.
- Do not use `--no-verify` or any bypass if a pre-commit hook piggybacks on these.
