# screens

A screen dashboard style management service

## Architecture

A minimal Go HTTP service built on the standard library. No frameworks, no routers beyond `net/http.ServeMux`, no DI containers. Configuration is environment-driven with sensible defaults; logging is structured via `log/slog`.

Module path: `github.com/jasoncorbett/screens`. Internal imports use `github.com/jasoncorbett/screens/internal/...`; `internal/` is off-limits to external consumers.

## Layout

- `main.go` — entrypoint, HTTP server wiring, graceful shutdown
- `api/` — route registration and handlers; versioned subpackages under `api/v1/`
- `cmd/` — auxiliary CLI tools (e.g. `cmd/version`)
- `internal/config/` — env-based configuration loading
- `internal/logging/` — slog setup (JSON in prod, colorized in dev)
- `internal/version/` — build-time version info

- `views/` — HTML view handlers using templ; route registration mirrors `api/`
- `static/` — embedded static assets (CSS, JS); served with gzip when available
- `static-handler.go` — gzip-aware embedded file server


## Rules

Project conventions live in `.claude/rules/`. Read the relevant file before making changes in that area:

- [Go style](rules/go-style.md) — stdlib-only, idiomatic Go
- [HTTP & routing](rules/http.md) — `net/http` patterns, handler conventions
- [Logging](rules/logging.md) — `log/slog` usage
- [Configuration](rules/config.md) — env-var conventions
- [Testing](rules/testing.md) — test conventions and bug-fix verification
- [Git](rules/git.md) — commit message and branch-naming conventions

When introducing a new cross-cutting convention, add it as `.claude/rules/<topic>.md` and link it here so the structure stays discoverable.

## Running locally

- `go run .` starts the server. Configuration is env-driven; `.env` is auto-loaded in dev mode.
- Default bind address and log level come from `internal/config`; see the top-level `README.md` configuration table for the full list.
- Health endpoint: `GET /health`.

- Run `templ generate` before building to compile `.templ` files to Go code.
- Run `go generate ./...` to create gzipped static assets for production.
- Demo page: `GET /`.


## Quick rules (top-level reminders)

- Return errors; never panic in request paths. Accept `context.Context` as the first parameter on I/O or cancellable functions.
- Never log secrets, auth headers, or PII. Never commit `.env` or credential files.
- Standard library only — no third-party dependencies without explicit approval.

## Before committing

Run the green-bar checks — all must pass:

- `gofmt -l .` (empty output)
- `go vet ./...`
- `go build ./...`
- `go test ./...` (and `go test -race ./...` for anything concurrent)

Then follow [Git](rules/git.md) for commit message and branch conventions.

## Development Workflow

The project uses a structured agent-driven development process. See [docs/plans/PROCESS.md](../docs/plans/PROCESS.md) for the full workflow and [docs/plans/roadmap.md](../docs/plans/roadmap.md) for the phased feature plan.

### Agents

Agent definitions live in `.claude/agents/`. Each agent has a specific role in the development loop:

- [PM](agents/pm.md) — produces specs with acceptance criteria from feature requests
- [Architect](agents/architect.md) — designs technical implementation, produces architecture docs and task breakdowns
- [Developer](agents/developer.md) — implements tasks following project conventions and skills
- [Tester](agents/tester.md) — validates implementations against acceptance criteria, produces review reports

### Skills

Reusable skill definitions live in `.claude/skills/`:

- [add-endpoint](skills/add-endpoint/SKILL.md) — scaffold HTTP API handlers
- [add-view](skills/add-view/SKILL.md) — scaffold templ view pages
- [add-config](skills/add-config/SKILL.md) — add env-driven configuration settings
- [add-middleware](skills/add-middleware/SKILL.md) — create HTTP middleware
- [add-store](skills/add-store/SKILL.md) — create data access layer components
- [add-widget](skills/add-widget/SKILL.md) — scaffold widget type implementations
- [add-migration](skills/add-migration/SKILL.md) — add database schema migrations
- [green-bar](skills/green-bar/SKILL.md) — pre-commit checks (gofmt, vet, build, test)

### Planning Documents

- `docs/plans/PROCESS.md` — master workflow document
- `docs/plans/roadmap.md` — phased feature roadmap
- `docs/plans/specs/` — feature specifications (PRDs)
- `docs/plans/architecture/` — technical architecture documents and ADRs
- `docs/plans/tasks/` — AI-actionable task documents
- `docs/plans/reviews/` — test/review reports

## Template variables

`screens`, `A screen dashboard style management service`, and `github.com/jasoncorbett/screens` are Backstage scaffolder placeholders rendered at project-creation time. In a rendered project they are already substituted; do not treat them as live variables.
