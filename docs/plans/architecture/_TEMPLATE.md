---
id: ARCH-XXX
title: "<Component/Feature Architecture>"
spec: SPEC-XXX
status: draft | approved | superseded
created: YYYY-MM-DD
author: architect
---

# <Component/Feature Architecture>

## Overview

One-paragraph summary of the technical approach.

## References

- Spec: `docs/plans/specs/<phase>/spec-<name>.md`
- Related ADRs: ADR-NNN
- Prerequisite architecture: ARCH-XXX (if any)

## Data Model

```go
// Define structs, relationships, and key types
type Example struct {
    ID   string
    Name string
}
```

## API Contract

### Endpoints

| Method | Path | Request Body | Response | Auth |
|--------|------|-------------|----------|------|
| GET    | /api/v1/... | - | ... | admin |

### Request/Response Examples

```json
// GET /api/v1/example
{
  "id": "abc123",
  "name": "example"
}
```

## Component Design

### Package Layout

Where new code lives:
- `internal/<package>/` -- business logic
- `api/v1/` -- HTTP handlers
- `views/` -- templ templates

### Interfaces

```go
// Key interfaces the implementation must satisfy
type ExampleService interface {
    Get(ctx context.Context, id string) (Example, error)
}
```

### Dependencies Between Components

How components wire together.

## Storage

Schema definitions, queries, migration details.

## Security Considerations

Authentication, authorization, input validation, secrets handling.

## Task Breakdown

This architecture decomposes into the following tasks:

1. TASK-NNN: <short description> -- (prerequisite: none)
2. TASK-NNN: <short description> -- (prerequisite: TASK-NNN)

## Alternatives Considered

What other approaches were evaluated and why this one was chosen.
