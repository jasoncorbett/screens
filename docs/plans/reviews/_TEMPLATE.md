---
task: TASK-NNN
spec: SPEC-XXX
status: pass | fail | partial
tested-by: tester
date: YYYY-MM-DD
---

# Review: TASK-NNN

## Acceptance Criteria Coverage

| AC   | Description                    | Status | Notes          |
|------|--------------------------------|--------|----------------|
| AC-1 | When X, then Y                 | PASS   |                |
| AC-2 | Given A, when B, then C        | FAIL   | Returns 500    |

## Test Results

```
go test ./... output
```

## Green Bar

- gofmt: PASS / FAIL
- go vet: PASS / FAIL
- go build: PASS / FAIL
- go test: PASS / FAIL

## Issues Found

1. **ISSUE**: <description> -- Severity: high | medium | low
   - Reproduction: ...
   - Suggested fix: ...

## Recommendation

ACCEPT / REJECT

## Follow-Up Tasks Needed

- TASK-NNN: <description> (if rejecting or issues found)
