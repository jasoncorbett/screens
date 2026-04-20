# Screens Development Process

This document defines the development workflow for the screens project. All task documents reference this file. Agents follow the workflow described here.

## Agent Roles

| Agent     | Input                       | Output                                     | Definition              |
|-----------|-----------------------------|--------------------------------------------|-------------------------|
| Architect | Feature name from roadmap   | Spec + architecture doc + task docs         | `.claude/agents/architect.md` |
| Developer | Task document               | Code + tests                                | `.claude/agents/developer.md` |
| Tester    | Task + implementation       | Adversarial review report + edge case tests | `.claude/agents/tester.md` |

## Workflow

```
Feature from Roadmap
      |
      v
  Architect -----> spec + arch + task-NNN-*.md  (one pass)
      |
      v
  Developer -----> code + tests (per task, in dependency order)
      |
      v
  Tester -----> adversarial review + edge case tests
      |
      v
  User reviews -----> ACCEPT (task done) or REJECT (developer revises)
```

### Concrete Invocation Sequence

1. **Design a feature**: Invoke the Architect agent with the feature name. The Architect reads the roadmap entry, writes a spec with acceptance criteria, designs the architecture, and breaks the work into task documents. All outputs are committed in one pass.

2. **Implement**: For each task in dependency order, invoke the Developer agent with the task path. The Developer reads the task, referenced architecture doc, and project rules, then implements, runs green-bar, updates task status to `review`, and commits. Code is pushed to GitHub for user review.

3. **Adversarial testing**: After each task implementation, invoke the Tester agent. The Tester actively tries to break the implementation: fuzzing inputs, testing concurrency, checking security, probing error paths. It writes edge case tests and produces a review report with ACCEPT/REJECT.

4. **Accept or reject**: Read the tester's report. If ACCEPT, mark the task `done` — this unblocks dependent tasks. If REJECT, the Developer fixes the issues and re-submits.

6. **Repeat** steps 3-5 until all tasks for the spec are `done`.

7. **Close the spec**: The PM marks the spec as `accepted` when all its acceptance criteria pass.

### Parallel Work

Tasks without mutual dependencies can be developed in parallel using separate worktrees. The Architect designs task dependency graphs to maximize parallelism. Phase 3 (Content Widgets) is the most parallelizable — all widget implementations are independent.

## Branching

Each feature gets its own git branch. The branch is created when the PM writes the first spec and is used through the entire lifecycle.

**Branch naming**: Feature name in kebab-case, no prefixes. Examples: `storage-engine`, `admin-auth`, `widget-system`. Follow `.claude/rules/git.md`.

**Branch lifecycle**:
1. PM creates branch from main when writing a spec
2. Architect commits architecture docs and tasks on the same branch
3. Developer commits code on the same branch
4. Tester commits review reports on the same branch
5. When all tasks for a spec are accepted, the branch is ready for merge/PR

**All agents commit their work**. Specs, architecture docs, tasks, code, tests, and reviews are all committed to the feature branch as they are produced.

## Task Lifecycle

Each task document tracks its status in frontmatter:

| Status        | Meaning                                          |
|---------------|--------------------------------------------------|
| `draft`       | Architect is still writing the task               |
| `ready`       | Available for a Developer to pick up              |
| `in-progress` | Developer is working on it                        |
| `review`      | Implementation complete, awaiting Tester          |
| `done`        | Tester approved, acceptance criteria met          |
| `blocked`     | Waiting on a prerequisite task or external factor |

## Conventions

All agents MUST:

1. Read `.claude/CLAUDE.md` before starting work
2. Read `.claude/rules/` files relevant to their changes
3. Follow the git conventions in `.claude/rules/git.md`
4. Run green-bar checks before considering code work complete
5. Never add third-party dependencies without explicit approval
6. Reference task IDs (e.g., TASK-001) when discussing work

## File Naming

| Document      | Pattern                          | Location                              |
|---------------|----------------------------------|---------------------------------------|
| Specs         | `spec-<kebab-case>.md`           | `docs/plans/specs/<phase>/`           |
| Architecture  | `arch-<kebab-case>.md`           | `docs/plans/architecture/<phase>/`    |
| Tasks         | `task-NNN-<kebab-case>.md`       | `docs/plans/tasks/<phase>/`           |
| Reviews       | `review-task-NNN.md`             | `docs/plans/reviews/`                 |
| ADRs          | `adr-NNN-<kebab-case>.md`        | `docs/plans/architecture/decisions/`  |
| Phase overview| `PHASE.md`                       | `docs/plans/specs/<phase>/`           |

Task numbers (NNN) are zero-padded and globally unique across all phases.
