# Task Development Context

This directory contains AI-actionable task documents for the screens project.

## Before working on any task

1. Read `/docs/plans/PROCESS.md` -- the master workflow document
2. Read `/.claude/CLAUDE.md` -- project architecture and conventions
3. Read all files in `/.claude/rules/` relevant to your changes
4. Read the task document itself, including all files listed in "Files to Read Before Starting"
5. Read the referenced architecture document for design context
6. Verify all prerequisite tasks are marked `done`

## Task status updates

Update the `status` field in the task's frontmatter as you progress:
- Set to `in-progress` when you start working
- Set to `review` when implementation is complete and green-bar passes
- Do not set to `done` -- the Tester agent does that after review

## Skills

Use skills as directed by each task's "Skills to Use" section. Common skills:
- `add-endpoint` -- scaffold HTTP handlers with route registration and tests
- `add-config` -- add environment-driven configuration settings
- `add-view` -- scaffold templ view pages with handlers
- `add-middleware` -- create HTTP middleware
- `add-store` -- create data access layer components
- `add-widget` -- scaffold widget type implementations
- `add-migration` -- add database schema migrations
- `green-bar` -- run pre-commit checks (always run before marking `review`)

## Conventions

- Follow `.claude/rules/go-style.md` for all Go code
- Follow `.claude/rules/http.md` for HTTP handlers and routing
- Follow `.claude/rules/testing.md` for test writing
- Follow `.claude/rules/config.md` for configuration changes
- Follow `.claude/rules/logging.md` for structured logging
- Follow `.claude/rules/git.md` for commits
