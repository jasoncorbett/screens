---
id: ADR-001
title: "SQLite via modernc.org/sqlite as the storage engine"
status: accepted
date: 2026-04-18
---

# ADR-001: SQLite via modernc.org/sqlite as the storage engine

## Context

The screens service needs persistent storage for all domain data (admin accounts, device tokens, screens, themes, widgets). The project's design philosophy prioritizes minimal operational overhead -- a household dashboard should not require a separate database server.

The storage engine choice affects:
- Deployment complexity (external process vs. embedded)
- Dependency footprint (CGO vs. pure Go)
- Concurrency model (multi-writer vs. single-writer)
- Build portability (cross-compilation requirements)

Options considered:
1. **PostgreSQL/MySQL** -- full-featured but requires a separate server process, contradicting the single-binary philosophy.
2. **mattn/go-sqlite3** -- mature SQLite driver but requires CGO, complicating cross-compilation and CI.
3. **modernc.org/sqlite** -- pure-Go SQLite implementation, no CGO required, cross-compiles cleanly.
4. **Embedded key-value stores (bbolt, badger)** -- no SQL, would require building query and migration abstractions from scratch.

## Decision

Use **SQLite** as the storage engine via the **`modernc.org/sqlite`** pure-Go driver.

- SQLite is accessed through `database/sql` with the `sqlite` driver name.
- WAL (Write-Ahead Logging) mode is enabled for concurrent read performance.
- Foreign key enforcement is enabled via PRAGMA.
- The database defaults to a single file (`screens.db`) in the working directory.
- `sqlc` generates type-safe Go code from SQL queries as a build-time tool (no runtime dependency).

## Consequences

**Accepted trade-offs:**
- SQLite serializes writes via a single-writer model. This is acceptable for a household dashboard with low write volume.
- `modernc.org/sqlite` is slightly slower than the C-based `mattn/go-sqlite3` on benchmarks. The difference is negligible for this workload.
- `modernc.org/sqlite` adds a non-trivial number of transitive dependencies to `go.mod`. This is the cost of pure-Go SQLite.

**Benefits:**
- Zero external infrastructure: the database is a single file. Backup is a file copy.
- Pure Go: no CGO, clean cross-compilation, simpler CI.
- Standard `database/sql` interface: store implementations use familiar Go patterns.
- `sqlc` provides compile-time SQL validation and type-safe query methods without an ORM.
- In-memory SQLite (`":memory:"`) enables fast, isolated tests without filesystem access.
