---
name: build
description: >
  Orchestrate the development loop. Reads current state of specs, architecture,
  and tasks, determines the next step, and spawns the appropriate agent
  (Architect, Developer, Tester). Use with: /build next, /build status,
  /build design <name>, /build develop <task>, /build test <task>,
  /build accept <task>.
user_invocable: true
---

# Build Orchestrator

This skill drives the Architect -> Developer -> Tester development loop.
After each agent step, pause and show the user the results for review before proceeding.

**Agents**:
- **Architect**: Writes the spec, designs the architecture, and creates task breakdowns (all in one pass)
- **Developer**: Implements tasks following project conventions
- **Tester**: Adversarial code reviewer that tries to break the implementation

## Branching

Each feature gets its own branch. The branch is created when design begins and used through the entire lifecycle.

**Branch naming**: Use the feature name in kebab-case. Follow `.claude/rules/git.md` -- no conventional-commit prefixes. Examples: `storage-engine`, `admin-auth`, `widget-system`.

**Branch lifecycle**:
1. `/build design <name>` -- creates the branch `<name>` from main, switches to it
2. `/build develop` -- commits on the same branch
3. `/build test` -- commits on the same branch
4. When all tasks are accepted, `/build accept` pushes the branch and opens a PR automatically

**Before creating a branch**: Check if one already exists for this feature. If the user is already on a feature branch, use it.

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
   - Features without specs -> can be sent to Architect via `/build design`
   - Tasks with status `ready` -> can be sent to Developer
   - Tasks with status `review` -> can be sent to Tester
   - Tasks with status `done` -> completed

### `/build next`

1. Run the status scan (above).
2. Automatically determine the highest-priority next action:
   - If any task is in `review` -> run Tester on it
   - If any task is `ready` (with prerequisites met) -> run Developer on it
   - If the roadmap has features without specs -> run Architect design on the next one
3. Tell the user what you're about to do and ask for confirmation before proceeding.
4. Spawn the appropriate agent.

### `/build design <feature-name>`

Invoke the Architect agent to write the spec, design the architecture, and create tasks -- all in one pass.

1. Read `docs/plans/roadmap.md` to find the feature and its phase.
2. Determine the next SPEC-NNN, ARCH-NNN, and TASK-NNN IDs by scanning existing files.
3. **Create a feature branch**: `git checkout -b <feature-name>` from main. If the branch already exists, switch to it.
4. Spawn the Architect agent:
   ```
   Agent(subagent_type="architect", prompt="
     Design feature: <feature-name>
     Phase: <phase>
     Next spec ID: SPEC-NNN
     Next architecture ID: ARCH-NNN
     Next task ID: TASK-NNN (increment for each task)
     Branch: <feature-name> (you are on this branch — commit your work here)

     Read these files first:
     - docs/plans/PROCESS.md
     - docs/plans/roadmap.md
     - docs/plans/specs/_TEMPLATE.md
     - docs/plans/architecture/_TEMPLATE.md
     - docs/plans/tasks/_TEMPLATE.md
     - docs/plans/specs/<phase>/PHASE.md
     - Any existing specs in docs/plans/specs/<phase>/
     - .claude/CLAUDE.md and all .claude/rules/ files
     - Existing source code (main.go, api/, views/, internal/)

     Do all three in one pass:
     1. Write the spec to: docs/plans/specs/<phase>/spec-<feature-name>.md
        Update the phase's PHASE.md with the new spec.
     2. Write architecture to: docs/plans/architecture/<phase>/arch-<name>.md
        Write ADRs if needed to: docs/plans/architecture/decisions/
     3. Write tasks to: docs/plans/tasks/<phase>/task-NNN-<name>.md

     After writing all files, stage and commit with a descriptive message
     following .claude/rules/git.md.
   ")
   ```
5. After the agent completes, show the user the spec ACs, architecture summary, and task list.
6. Tell the user: "Review the design above. When ready, run `/build next` or `/build develop <task-id>` to start implementation."

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
6. After the agent completes, **push the branch and open GitHub diff**:
   - Run `git push` (or `git push -u origin <branch>` if not yet tracking)
   - Run `open $(git remote get-url origin | sed 's/\.git$//' | sed 's|git@github.com:|https://github.com/|')/compare/main...<branch>` to open the diff in the browser
7. Tell the user: "Code pushed and GitHub diff opened. Review in GitHub, then run `/build test <task-id>` when ready."

### `/build test <task-id-or-filename>`

Invoke the adversarial Tester agent to try to break the implementation.

1. Locate the task file. Verify status is `review`.
2. Read the task and its referenced spec for acceptance criteria.
3. **Verify branch**: Confirm you're on the correct feature branch.
4. Spawn the Tester agent:
   ```
   Agent(subagent_type="tester", prompt="
     Adversarially test this task implementation:
     Task: <task-path>
     Spec: <spec-path> (from task frontmatter)
     Branch: <current-branch> (you are on this branch — commit your work here)

     Read these files first:
     - The task document
     - The spec document (for acceptance criteria)
     - The architecture document (for intended design)
     - All files created or modified by the task
     - .claude/rules/testing.md

     Your job is to TRY TO BREAK the implementation:
     1. Verify ACs pass (baseline)
     2. Fuzz inputs: empty strings, huge strings, unicode, SQL metacharacters
     3. Test concurrency if shared state exists (go test -race)
     4. Check security: auth bypass, error message leakage, SQL injection
     5. Test error paths: missing data, DB unavailable, duplicate operations
     6. Write tests for every issue you find or suspect

     When you find a bug (critical, high, or medium severity):
     - FIX the bug in the source code, then write a test that proves the fix.
     - NEVER write a test that merely confirms a bug exists. Every test you
       commit must pass. A test that documents broken behavior is useless —
       fix the code first, then test the corrected behavior.

     Low-severity findings can be noted in the review without a fix.

     Write review to: docs/plans/reviews/review-task-NNN.md
     REJECT only if a critical/high issue could NOT be fixed without
     rearchitecting. Otherwise ACCEPT (with fixes applied).
     
     After writing fixes, tests, and the review, run green-bar to verify
     everything passes. Stage and commit with a descriptive message
     following .claude/rules/git.md.
   ")
   ```
4. After the agent completes, **push and show the review report**.
   - Run `git push`
5. If ACCEPT: tell the user to run `/build accept <task-id>`.
   If REJECT: show what's broken and suggest `/build develop <task-id>` to fix.

### `/build accept <task-id-or-filename>`

Mark a task as done after the user approves.

1. Locate the task file.
2. Update the task frontmatter status from `review` to `done`.
3. Update the spec frontmatter status to `accepted` if all its tasks are now `done`.
4. Commit the status change.
5. Check if this unblocks any other tasks (tasks listing this one as a prerequisite).
6. Report what was accepted and what's now unblocked.
7. If all tasks for a spec are `done`, **create a pull request**:
   a. Push the branch: `git push -u origin <branch>`.
   b. Read the spec to extract the title, problem statement, and acceptance criteria.
   c. Collect the list of completed tasks (id + title) from the task files.
   d. Create the PR using `gh pr create` with this format:
      ```
      gh pr create --title "<spec-title>" --body "$(cat <<'EOF'
      ## Summary

      <Problem statement from spec — first 2-3 sentences.>

      ## Acceptance Criteria

      <List of ACs from the spec, copied as-is.>

      ## Tasks

      <Bullet list: TASK-NNN: title (for each task belonging to this spec).>
      EOF
      )"
      ```
   e. Report the PR URL to the user.
8. Suggest the next action: `/build next` or `/build status`.

## State Reading

To determine current state, read frontmatter from all files matching:
- `docs/plans/specs/**/spec-*.md` -- spec status
- `docs/plans/architecture/**/arch-*.md` -- architecture status
- `docs/plans/tasks/**/task-*.md` -- task status, prerequisites
- `docs/plans/reviews/review-*.md` -- review verdicts

Parse the YAML frontmatter between `---` markers to extract `status`, `id`, `prerequisites`, `spec`, etc.

## Review Protocol

After EVERY agent step, you MUST:
1. Show the user a concise summary of what the agent produced
2. Show the key content (spec ACs, architecture decisions, files changed, test results)
3. Suggest the next `/build` command
4. Wait for the user to proceed -- do NOT automatically run the next step

## Error Handling

- If a task's prerequisites aren't met: report which prerequisites are blocking and their current status
- If green-bar fails: show the failures and suggest `/build develop <task-id>` to fix
- If the tester REJECTs: show the issues and suggest `/build develop <task-id>` to revise
- If a spec file or task file isn't found: show available files and suggest the correct path
