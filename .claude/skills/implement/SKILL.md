---
name: Implement
description: Use when the card has an approved implementation plan ready for execution.
user-invocable: true
---

# Implement

> **Violating the letter of the rules is violating the spirit of the rules.**

Execute the approved implementation plan task by task, following TDD discipline.

## Inputs (mandatory reads)

- **Attachment:** `implementation-plan.md` — the approved plan
- **Checklist:** "Implementation Tasks" — tracks progress
- All card comments (especially the approval comment)

## The TDD Iron Law

**NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST.**

For every task that involves production code:
1. **RED:** Write the test first. Run it. Watch it fail. If it passes, your test is wrong.
2. **GREEN:** Write the minimum production code to make the test pass. Run it. Watch it pass.
3. **REFACTOR:** Clean up. Run tests again. Still passing? Good. Commit.

If the task is purely configuration, documentation, or non-testable infrastructure, document why TDD doesn’t apply for this specific task.

## Process (per task)

1. Read the current task from the plan
2. Read the files that will be modified (verify they match what the plan expects)
3. Write the failing test (RED)
4. Run `uv run pytest` — confirm the new test fails and all other tests still pass
5. Write the production code (GREEN)
6. Run `uv run pytest` — confirm ALL tests pass
7. Run `uv run pre-commit run --all-files` — confirm lint passes
8. Commit with a descriptive message
9. Mark the checklist todo as complete: `kardbrd checklist complete <card_id> <todo_id>`
10. Move to the next task

## After All Tasks Complete

1. Run full test suite: `uv run pytest`
2. Run full lint: `uv run pre-commit run --all-files`
3. Read FULL output of both commands — not just exit code
4. If anything fails, fix it before claiming completion
5. Upload `implementation-report.md` attachment with:
   - What was implemented (task by task)
   - Any deviations from the plan and why
   - Test results (paste actual output)
   - Lint results (paste actual output)
6. Post comment: completion summary + "Ready for /review" + @mention requester

## Anti-Rationalization Guards

| Excuse | Why It’s Wrong |
|--------|----------------|
| "This is too simple to test" | Simple code still breaks. The test takes 30 seconds to write. Write it. |
| "I’ll write the tests after the code" | Tests that pass immediately prove nothing. You don’t know if your test actually validates the behavior. |
| "The plan is slightly wrong, I’ll adapt" | Minor adaptations are fine. Document them. Major deviations mean the plan needs updating — post a comment, don’t silently diverge. |
| "I already tested manually" | Manual testing isn’t repeatable. Write the automated test. |
| "All tests pass, I’m done" | Did you also run lint? Did you read the FULL output? Did you mark all checklist items? Did you upload the report? |
| "The existing tests cover this" | If existing tests cover it, your new test will pass immediately (RED fails). That means you need a MORE SPECIFIC test. |

## Red Flags

- Writing production code before the test
- Tests that pass on the first run (your test doesn’t test what you think)
- Committing without running both pytest and pre-commit
- Skipping checklist updates
- No implementation-report.md attachment at the end
- Qualifier language: "should work," "seems to pass," "probably fine"

## Verification Before Completion

1. Run `uv run pytest` — read FULL output, confirm all pass
2. Run `uv run pre-commit run --all-files` — read FULL output, confirm all pass
3. Verify implementation-report.md attachment exists
4. Verify ALL "Implementation Tasks" checklist items are marked complete
5. Verify completion comment was posted
6. Only then claim completion
