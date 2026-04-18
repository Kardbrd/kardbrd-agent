---
name: Ship
description: Use when the PR is approved and ready to merge to main.
user-invocable: true
---

# Ship (Merge to Main)

> **Violating the letter of the rules is violating the spirit of the rules.**

Merge the pull request to main and clean up.

## Inputs (mandatory reads)

- **PR URL or number** — from card comments or `gh pr list --head $(git branch --show-current)`
- **Card comments** — for approval context
- `git status` — verify clean working tree
- `gh pr view` — verify PR state and checks

## Prerequisites Check (GATE)

Before merging, verify ALL of the following:

1. **PR exists** — find the PR for the current branch using
   `gh pr list --head $(git branch --show-current) --json number,url,state`
2. **PR is open** — state must be "OPEN", not already merged or closed
3. **CI checks pass** — `gh pr checks` shows all checks passing. If checks are still running, wait
   and re-check (do NOT merge with pending checks).
4. **No merge conflicts** — `gh pr view --json mergeable` shows the PR is mergeable
5. **Tests pass locally** — run `uv run pytest` and read FULL output
6. **Lint passes locally** — run `uv run pre-commit run --all-files` and read FULL output

**If any prerequisite fails:** Stop. Post a comment explaining what failed. Do NOT merge.

## Process

1. Find the PR: `gh pr list --head $(git branch --show-current) --json number,url,title,state`
2. Verify PR status: `gh pr checks <number>`
3. Run local verification: `uv run pytest` and `uv run pre-commit run --all-files`
4. **Squash merge** the PR:
   ```
   gh pr merge <number> --squash --delete-branch
   ```
   - `--squash` — clean single-commit history on main
   - `--delete-branch` — clean up the feature branch after merge
5. Verify the merge succeeded: `gh pr view <number> --json state`
6. Post completion comment on card

## Merge Strategy

Always use **squash merge** (`--squash`). This produces:
- Clean single-commit history on main
- The squash commit message includes the PR title and number
- Feature branch is deleted automatically with `--delete-branch`

Do NOT use regular merge or rebase merge unless explicitly requested.

## Outputs (mandatory, every time)

- **PR merged** via `gh pr merge --squash --delete-branch`
- **Comment on card:** Merge confirmation with:
  - PR number and URL
  - Merge commit SHA (from `gh pr view --json mergeCommit`)
  - "Shipped to main" confirmation
  - @mention requester

## Anti-Rationalization Guards

| Excuse | Why It's Wrong |
|--------|----------------|
| "CI is probably green, it was green last time I checked" | Check NOW. Someone may have pushed, or a flaky test may have failed. |
| "I'll merge without waiting for checks" | Never. Broken main is worse than a delayed merge. |
| "Tests pass locally, CI doesn't matter" | CI runs in a clean environment. Local-only green is not sufficient. |
| "I'll skip the local test run, CI covers it" | Local tests catch issues before merge. Run both. |
| "The PR was approved, so it's safe to merge" | Approval is necessary but not sufficient. Checks must also pass. |
| "I'll do a regular merge instead of squash" | Squash unless explicitly told otherwise. Clean history matters. |
| "I'll keep the branch, might need it later" | Delete it. Branches are cheap to recreate. Stale branches are clutter. |

## Red Flags

- Merging with failing or pending CI checks
- Merging without running local tests and lint
- Using regular merge instead of squash
- Not deleting the feature branch after merge
- No comment posted on the card confirming the merge
- Merging a PR that's already merged or closed

## Verification Before Completion

1. Verify PR was found for the current branch
2. Verify all CI checks passed
3. Verify local tests and lint passed
4. Verify merge completed successfully (PR state is "MERGED")
5. Verify feature branch was deleted
6. Verify card comment was posted with merge confirmation
7. Only then claim completion
