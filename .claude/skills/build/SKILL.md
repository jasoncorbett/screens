---
name: build
description: >
  Orchestrate the development loop. Reads current state of specs, architecture,
  and tasks, determines the next step, and spawns the appropriate agent
  (PM, Architect, Developer, Tester). Use with: /build next, /build status,
  /build spec <name>, /build architect <spec>, /build develop <task>,
  /build test <task>, /build accept <task>.
user_invocable: true
---

# Build Orchestrator

This skill drives the PM -> Architect -> Developer -> Tester development loop.
After each agent step, pause and show the user the results for review before proceeding.

## Branching

Each feature gets its own branch. The branch is created when the first spec is written and used throughout the entire lifecycle (spec -> architecture -> development -> testing).

**Branch naming**: Use the feature name in kebab-case. Follow `.claude/rules/git.md` — no conventional-commit prefixes. Examples: `storage-engine`, `admin-auth`, `widget-system`.

**Branch lifecycle**:
1. `/build spec <name>` — creates the branch `<name>` from main, switches to it
2. `/build architect` — commits on the same branch
3. `/build develop` — commits on the same branch
4. `/build test` — commits on the same branch
5. When all tasks for a spec are accepted, the branch is ready for merge/PR

**Before creating a branch**: Check if one already exists for this feature. If the user is already on a feature branch, use it.

**Telling agents about the branch**: Include the branch name in agent prompts so they know to commit on it. The PM agent creates the branch; subsequent agents just commit on the current branch.

## Commands

Parse the argument to determine the action:

### `/build status`

Scan the project state and report what's ready for the next step:

1. Show the current git branch.
2. Read all `PHASE.md` files in `docs/plans/specs/` to find the active phase.
3. Read all spec files (`spec-*.md`) and report their status.
4. Read all task files (`task-*.md`) and report their status.
5. Read all review files (`review-*.md`) and report their status.
6. Present a summary table showing what's actionable:
   - Specs with status `ready` -> can be sent to Architect
   - Tasks with status `ready` -> can be sent to Developer
   - Tasks with status `review` -> can be sent to Tester
   - Tasks with status `done` -> completed

Output format:
```
Branch: storage-engine
Phase 1: Foundation (active)
  SPEC-001 admin-auth         [accepted] -> 3 tasks
  SPEC-002 device-auth        [ready]    -> needs architect

  TASK-001 sqlite-storage     [done]
  TASK-002 session-middleware  [review]   -> needs tester
  TASK-003 admin-login-api    [ready]    -> needs developer
  TASK-004 admin-login-ui     [blocked]  -> waiting on TASK-003

Next action: /build test task-002
```

### `/build next`

1. Run the status scan (above).
2. Automatically determine the highest-priority next action:
   - If any task is in `review` -> run Tester on it
   - If any task is `ready` (with prerequisites met) -> run Developer on it
   - If any spec is `ready` but has no architecture -> run Architect on it
   - If the roadmap has features without specs -> run PM on the next one
3. Tell the user what you're about to do and ask for confirmation before proceeding.
4. Spawn the appropriate agent (see agent invocation below).

### `/build spec <feature-name>`

Invoke the PM agent to write a spec for the named feature.

1. Read `docs/plans/roadmap.md` to find the feature and its phase.
2. Read existing specs in that phase to determine the next SPEC-NNN ID.
3. **Create a feature branch**: `git checkout -b <feature-name>` from main. If the branch already exists, switch to it.
4. Spawn the PM agent:
   ```
   Agent(subagent_type="pm", prompt="
     Write a spec for: <feature-name>
     Phase: <phase>
     Next spec ID: SPEC-NNN
     Branch: <feature-name> (you are on this branch — commit your work here)
     
     Read these files first:
     - docs/plans/PROCESS.md
     - docs/plans/roadmap.md
     - docs/plans/specs/_TEMPLATE.md
     - docs/plans/specs/<phase>/PHASE.md
     - Any existing specs in docs/plans/specs/<phase>/
     
     Write the spec to: docs/plans/specs/<phase>/spec-<feature-name>.md
     Update the phase's PHASE.md with the new spec.
     
     After writing, stage and commit your files with a descriptive message
     following .claude/rules/git.md.
   ")
   ```
5. After the agent completes, show the user the spec content for review.
6. Tell the user: "Review the spec above. When ready, run `/build architect <spec-id>` to design the implementation."

### `/build architect <spec-id-or-filename>`

Invoke the Architect agent to design the implementation for a spec.

1. Locate the spec file (by ID or filename).
2. Read the spec to understand what needs designing.
3. Determine the next TASK-NNN and ARCH-NNN IDs by scanning existing files.
4. **Verify branch**: Confirm you're on the correct feature branch. If not, switch to it.
5. Spawn the Architect agent:
   ```
   Agent(subagent_type="architect", prompt="
     Design the architecture and create task breakdown for:
     Spec: <spec-path>
     Phase: <phase>
     Next architecture ID: ARCH-NNN
     Next task ID: TASK-NNN (increment for each task)
     Branch: <current-branch> (you are on this branch — commit your work here)
     
     Read these files first:
     - docs/plans/PROCESS.md
     - docs/plans/architecture/_TEMPLATE.md
     - docs/plans/tasks/_TEMPLATE.md
     - The spec file itself
     - .claude/CLAUDE.md and all .claude/rules/ files
     - Existing source code (main.go, api/, views/, internal/)
     
     Write architecture to: docs/plans/architecture/<phase>/arch-<name>.md
     Write tasks to: docs/plans/tasks/<phase>/task-NNN-<name>.md
     Write ADRs if needed to: docs/plans/architecture/decisions/
     
     After writing all files, stage and commit with a descriptive message
     following .claude/rules/git.md. Example:
     'design storage engine architecture (ARCH-001, TASK-001 through TASK-004)'
   ")
   ```
6. After the agent completes, show the user the architecture doc and task list.
7. Tell the user: "Review the architecture and tasks above. When ready, run `/build next` or `/build develop <task-id>` to start implementation."

### `/build develop <task-id-or-filename>`

Invoke the Developer agent to implement a task.

1. Locate the task file.
2. Verify status is `ready` and all prerequisites are `done`. If not, report the blocker and stop.
3. **Verify branch**: Confirm you're on the correct feature branch.
4. Read the task to understand what's needed.
5. Spawn the Developer agent:
   ```
   Agent(subagent_type="developer", prompt="
     Implement this task:
     Task: <task-path>
     Branch: <current-branch> (you are on this branch — commit your work here)
     
     Read these files first:
     - The task document (contains 'Files to Read Before Starting')
     - docs/plans/PROCESS.md
     - .claude/CLAUDE.md and all .claude/rules/ files
     - The referenced architecture document
     
     Follow the task's requirements exactly.
     Use the skills listed in the task's 'Skills to Use' section.
     Run green-bar before marking complete.
     Update the task status to 'review' when done.
     Commit with a descriptive message following .claude/rules/git.md.
   ")
   ```
6. After the agent completes, show the user what was implemented (files changed, test results).
7. Tell the user: "Review the implementation above. When ready, run `/build test <task-id>` to validate."

### `/build test <task-id-or-filename>`

Invoke the Tester agent to validate a task implementation.

1. Locate the task file. Verify status is `review`.
2. Read the task and its referenced spec for acceptance criteria.
3. **Verify branch**: Confirm you're on the correct feature branch.
4. Spawn the Tester agent:
   ```
   Agent(subagent_type="tester", prompt="
     Validate this task implementation:
     Task: <task-path>
     Spec: <spec-path> (from task frontmatter)
     Branch: <current-branch> (you are on this branch — commit your work here)
     
     Read these files first:
     - The task document
     - The spec document (for acceptance criteria)
     - All files created or modified by the task
     - docs/plans/PROCESS.md
     - docs/plans/reviews/_TEMPLATE.md
     - .claude/rules/testing.md
     
     Map every acceptance criterion to PASS/FAIL.
     Write missing tests if ACs lack coverage.
     Run go test ./... and green-bar.
     Write review to: docs/plans/reviews/review-task-NNN.md
     Recommend ACCEPT or REJECT.
     
     After writing the review, stage and commit with a descriptive message
     following .claude/rules/git.md.
   ")
   ```
4. After the agent completes, show the user the review report.
5. If ACCEPT: tell the user to run `/build accept <task-id>`.
   If REJECT: show what needs fixing and suggest `/build develop <task-id>` again.

### `/build accept <task-id-or-filename>`

Mark a task as done after the user approves the tester's review.

1. Locate the task file.
2. Update the task frontmatter status from `review` to `done`.
3. Commit the status change.
4. Check if this unblocks any other tasks (tasks listing this one as a prerequisite).
5. Report what was accepted and what's now unblocked.
6. If all tasks for a spec are `done`, note that the spec can be marked `accepted` and the branch is ready for merge/PR.
7. Suggest the next action: `/build next` or `/build status`.

## State Reading

To determine current state, read frontmatter from all files matching:
- `docs/plans/specs/**/spec-*.md` — spec status
- `docs/plans/architecture/**/arch-*.md` — architecture status
- `docs/plans/tasks/**/task-*.md` — task status, prerequisites
- `docs/plans/reviews/review-*.md` — review verdicts

Parse the YAML frontmatter between `---` markers to extract `status`, `id`, `prerequisites`, `spec`, etc.

## Review Protocol

After EVERY agent step, you MUST:
1. Show the user a concise summary of what the agent produced
2. Show the key content (spec ACs, architecture decisions, files changed, test results)
3. Suggest the next `/build` command
4. Wait for the user to proceed — do NOT automatically run the next step

## Error Handling

- If a task's prerequisites aren't met: report which prerequisites are blocking and their current status
- If green-bar fails: show the failures and suggest `/build develop <task-id>` to fix
- If the tester REJECTs: show the issues and suggest `/build develop <task-id>` to revise
- If a spec file or task file isn't found: show available files and suggest the correct path
