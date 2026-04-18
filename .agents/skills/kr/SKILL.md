---
name: Review
description: Use when implementation is complete and the branch needs code review before merge.
---

# Review

> **Violating the letter of the rules is violating the spirit of the rules.**

Two-stage code review: first verify spec compliance (right thing?), then assess code quality (built well?).

## Inputs (mandatory reads)

- **Attachment:** `implementation-plan.md` — what was supposed to be built
- **Attachment:** `implementation-report.md` — what was actually built
- **Attachment:** `exploration-*.md` — original analysis
- All card comments
- `git diff main...HEAD` — actual code changes

## Stage 1: Spec Compliance (GATE 1)

**Question: Did we build the right thing?**

1. Read the implementation plan
2. Read the implementation report
3. Compare `git diff main...HEAD` against the plan requirements
4. For EVERY task in the plan, verify:
   - Was it implemented?
   - Does the implementation match what was specified?
   - Are there any gaps or missing pieces?
5. Check for scope creep — changes that weren’t in the plan
6. **Verdict:** PASS or FAIL with specific items

**If Stage 1 FAILS:** Stop. Post a comment listing the gaps. Do NOT proceed to Stage 2.

## Stage 2: Code Quality (GATE 2)

**Question: Did we build it well?**

Only proceed here if Stage 1 passed.

1. Review each changed file for:
   - **Correctness:** Does the code do what it claims?
   - **Tests:** Are tests adequate? Do they test the right things?
   - **Style:** Does it follow existing patterns in the codebase?
   - **Security:** (kardbrd-agent specific)
     - No shell injection via subprocess args
     - Bot tokens not logged or exposed
     - Temporary files cleaned up
     - WebSocket messages validated
     - Worktree paths sanitized
   - **Error handling:** Are failures handled gracefully? Async patterns correct?
   - **Edge cases:** What happens with empty input, missing data, concurrent access?
2. Run `uv run pytest` — verify all tests pass
3. Run `uv run pre-commit run --all-files` — verify lint passes
4. **Verdict:** PASS or FAIL with specific issues

## Issue Classification

- **Critical:** Bugs, security vulnerabilities, data loss risks, broken functionality → Must fix before merge
- **Important:** Architecture problems, missing edge cases, inadequate tests → Should fix before merge
- **Minor:** Style nits, optimization opportunities, documentation → Can fix later

## Outputs (mandatory, every time)

- **Attachment:** `code-review.md` — full structured review with:
  - Stage 1 results (spec compliance)
  - Stage 2 results (code quality)
  - Issue list with severity classification
  - File-by-file review notes
- **Checklist:** "Review Issues" — one todo per issue found (if any)
- **Comment:** Verdict + summary. One of:
  - **Approve:** "No blocking issues. Ready to merge."
  - **Request changes:** "Found N issues (X critical, Y important). See review attachment."
  - **Needs discussion:** "Architectural questions need human input. See review attachment."

## Anti-Rationalization Guards

| Excuse | Why It’s Wrong |
|--------|----------------|
| "The code looks fine at a glance" | Read every changed line. A glance is not a review. |
| "Tests pass, so it works" | Tests can be wrong. Tests can be incomplete. Read the tests too. |
| "I wrote this code, I know it’s correct" | Self-review is not review. Pretend you’ve never seen this code. |
| "This is a minor change, full review is overkill" | Minor changes cause major bugs. The review process is the same regardless of size. |
| "Stage 1 basically passed, I can skip straight to Stage 2" | "Basically passed" is not passed. List the specific gaps. |
| "I’ll note the issues but approve anyway" | If there are Critical or Important issues, the verdict is Request Changes. Period. |

## Red Flags

- Approving with unresolved Critical or Important issues
- Skipping Stage 1 and going straight to code quality
- Review attachment under 2KB (you skimmed)
- Not running pytest and pre-commit during review
- No mention of security checklist items

## Verification Before Completion

1. Verify code-review.md attachment exists
2. Verify both stages were completed (or Stage 2 was explicitly skipped due to Stage 1 failure)
3. Verify pytest and pre-commit were run freshly during review
4. Verify comment with verdict was posted
5. Only then claim completion
