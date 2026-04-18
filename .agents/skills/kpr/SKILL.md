---
name: PR
description: Use when implementation and review are complete and the branch needs a pull request created.
---

# PR (Pull Request)

> **Violating the letter of the rules is violating the spirit of the rules.**

Create a pull request from the current card branch to main.

## Inputs (mandatory reads)

- **Attachment:** `implementation-plan.md` — what was planned
- **Attachment:** `implementation-report.md` — what was built
- **Attachment:** `code-review.md` — review results (if exists)
- All card comments (for context and decisions)
- `git log main..HEAD` — commits on this branch
- `git diff main...HEAD` — full diff against main

## Prerequisites Check (GATE)

Before creating the PR, verify:

1. **Branch is not main** — you must be on a card branch
2. **Tests pass** — run `uv run pytest` and read FULL output
3. **Lint passes** — run `uv run pre-commit run --all-files` and read FULL output
4. **Commits exist** — `git log main..HEAD` shows at least one commit
5. **No uncommitted changes** — `git status` shows clean working tree. If there are uncommitted
   changes, commit them first with a descriptive message.

**If any prerequisite fails:** Stop. Post a comment explaining what failed. Do NOT create the PR.

## Process

1. Run `git log main..HEAD --oneline` to get the commit list
2. Run `git diff main...HEAD --stat` to get the change summary
3. Read card attachments (implementation-plan.md, implementation-report.md, code-review.md) for context
4. Read the card markdown for the card title and description
5. **Push the branch** to remote: `git push -u origin HEAD`
6. **Create the PR** using `gh pr create` with:
   - **Title:** Short, descriptive (under 72 chars) — derived from the card title or implementation
     summary
   - **Body:** Structured markdown with:
     - `## Summary` — 1-3 bullet points of what changed and why
     - `## Changes` — file-by-file or area-by-area breakdown
     - `## Test plan` — how the changes were verified
     - `## Card` — link to the kardbrd card
     - Footer: `🤖 Generated with [Claude Code](https://claude.ai/code)`

## PR Body Template

```markdown
## Summary
- [1-3 bullets: what changed and why]

## Changes
- [file-by-file or area-by-area breakdown]

## Test plan
- [x] All tests pass (`uv run pytest`)
- [x] Lint passes (`uv run pre-commit run --all-files`)
- [specific test scenarios if applicable]

## Card
[Card title](card_url)

🤖 Generated with [Claude Code](https://claude.ai/code)
```

## Outputs (mandatory, every time)

- **Pull request** created on GitHub via `gh pr create`
- **Comment on card:** PR URL + summary, mentioning the requester

Post the PR URL in the card comment so the user can review it.

## Anti-Rationalization Guards

| Excuse | Why It's Wrong |
|--------|----------------|
| "Tests probably pass, I ran them earlier" | Run them NOW. State changes between runs. |
| "It's a small change, the PR description can be brief" | Small PRs still need context. The reviewer wasn't in your head. |
| "I'll skip the card attachments, I know what I did" | The PR body should reflect the plan and review, not your memory. |
| "I'll push and create the PR in one step" | Push first, then create. If push fails, you need to handle it before creating the PR. |
| "The branch is already pushed, I can skip that step" | Verify it's up to date. `git push` is idempotent — run it anyway. |
| "Review wasn't done yet but the code is ready" | If review was requested as part of the workflow, the PR should note the review status. |

## Red Flags

- PR created without running tests and lint first
- PR body under 100 characters (you skimmed)
- No card link in the PR body
- Uncommitted changes left behind
- PR created against wrong base branch
- No comment posted on the card with the PR URL

## Verification Before Completion

1. Verify tests passed (read full output)
2. Verify lint passed (read full output)
3. Verify branch was pushed successfully
4. Verify PR was created (capture the URL from `gh pr create` output)
5. Verify PR body includes Summary, Changes, Test plan, and Card link
6. Verify card comment was posted with PR URL
7. Only then claim completion
