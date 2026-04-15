# Merge Workflow

The merge workflow automates the process of getting agent-written code merged to main. It's a state machine that handles committing, rebasing, conflict resolution, testing, and squash merging.

## State machine

```
    ┌─────────────────┐
    │  Commit changes  │  Commit any uncommitted work in the worktree
    └────────┬────────┘
             │
             ▼
    ┌─────────────────┐
    │  Fetch & rebase  │  Fetch origin/main, rebase worktree branch
    └────────┬────────┘
             │
             ▼
    ┌─────────────────┐
    │ Resolve conflicts│  LLM-assisted conflict resolution (if needed)
    └────────┬────────┘
             │
             ▼
    ┌─────────────────┐
    │   Run tests      │  Execute test command, fix failures (loop)
    └────────┬────────┘
             │
             ▼
    ┌─────────────────┐
    │  Squash merge    │  Squash merge to main
    └────────┬────────┘
             │
             ▼
    ┌─────────────────┐
    │    Cleanup       │  Remove worktree and branch
    └─────────────────┘
```

## How it works

The merge workflow is orchestrated by the executor (Claude, Goose, or Codex) when triggered by a rule action. The executor handles both the git operations and the LLM-assisted steps (conflict resolution, test fixing) as part of its session.

## Conflict resolution

When a rebase produces conflicts, the workflow uses the LLM to resolve them:

1. MergeTools identifies conflicted files
2. The conflict context (both sides) is sent to the executor
3. The executor proposes resolutions
4. Resolutions are applied and the rebase continues
5. If conflicts persist, the process can loop with updated context

## Test loop

After a successful rebase, tests are run (if `AGENT_TEST_CMD` is configured):

1. Execute the test command
2. If tests fail, send failure output to the executor for fixing
3. Re-run tests after fixes
4. Loop up to a configurable number of attempts
5. If tests still fail after all attempts, report failure on the card

## Triggering

The merge workflow is triggered by rule actions. A typical setup:

```yaml
rules:
  - name: Merge on approval
    event: reaction_added
    emoji: "✅"
    require_label: Agent
    model: sonnet
    action: |
      Merge this branch to main. Run tests first and fix any failures.
      Use the merge workflow to squash merge.
```

## Squash merge

The final merge uses `git merge --squash` to collapse all worktree commits into a single commit on main. The commit message summarizes the changes made across all commits in the branch.
