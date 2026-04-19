---
phase: 1
title: "Foundation"
status: planning
milestone: "auth-and-storage"
---

# Phase 1: Foundation

## Goal

Establish persistent storage, admin authentication, and device authentication. At the end of this phase, admin users can log in and manage device tokens, and devices can authenticate to access protected endpoints. This is the security boundary all subsequent features build on.

## Specs in This Phase

| ID | Title | Status | Priority |
|----|-------|--------|----------|
| | Storage Engine | | p0 |
| | Admin Auth | | p0 |
| | Device Auth | | p0 |
| | Auth Middleware | | p0 |

## Dependencies

None -- this is the first phase.

## Exit Criteria

1. SQLite database initializes on startup with migration system
2. Admin can register (initial setup), log in, and log out
3. Admin can create and revoke device tokens
4. Device can authenticate with a token
5. Protected endpoints reject unauthenticated requests
6. All green-bar checks pass
