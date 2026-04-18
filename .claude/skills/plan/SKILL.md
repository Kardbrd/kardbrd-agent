---
name: Plan
description: Use when exploration is complete and the card needs an implementation plan before coding.
user-invocable: true
---

# Plan

> **Violating the letter of the rules is violating the spirit of the rules.**

Create a detailed, unambiguous implementation plan from exploration findings. The plan must be concrete enough that another agent can implement it without asking questions.

## Inputs (mandatory reads)

- All card attachments (especially `exploration-*.md`)
- All card comments
- All files that will be modified (read them, don't guess their contents)

## Outputs (mandatory, every time)

- **Attachment:** `implementation-plan.md` — detailed plan with:
  - Context: what problem this solves and why
  - File-by-file breakdown of modifications (with current line numbers)
  - Specific function signatures, class names, data structures
  - New files to create (with rationale)
  - Test plan: what tests to write, in what order, expected behavior
  - Verification: exact commands to confirm changes work
  - Dependencies and ordering constraints between tasks
  - Each task must be a 2-5 minute bite-sized unit of work
- **Checklist:** "Implementation Tasks" — one todo per task from the plan, in execution order
- **Comment:** Plan summary + "Waiting for approval before implementing. Please reply to approve or request changes." + @mention requester

## Plan Quality Requirements

- NO "TBD" markers — every detail must be specified
- NO vague instructions like "add appropriate error handling" or "update tests accordingly"
- NO undefined references like "similar to Task N" — each task is self-contained
- Every task includes: what file, what function/class, what specifically changes, what the test looks like
- Include actual code snippets for non-obvious changes
- Include exact test commands: `uv run pytest` for tests, `uv run pre-commit run --all-files` for lint

## Process

1. Read ALL attachments on the card (exploration findings are your primary input)
2. Read ALL files that will be modified — verify current state matches exploration
3. Draft the plan with concrete, bite-sized tasks
4. Self-review the plan against the quality requirements above
5. Upload plan attachment
6. Create "Implementation Tasks" checklist with one todo per task
7. Post comment requesting approval
8. **STOP.** Do not proceed to implementation. The operator must approve via comment.

## Anti-Rationalization Guards

| Excuse | Why It’s Wrong |
|--------|----------------|
| "The exploration was thorough enough, I don’t need to re-read files" | Files may have changed. Re-read them. The plan must match current state. |
| "This task is obvious, I don’t need to spell it out" | If it’s obvious, spelling it out takes 30 seconds. If it’s not, you just created ambiguity. |
| "I can figure out the details during implementation" | That’s not planning, that’s procrastinating. The plan IS the details. |
| "TBD is fine for now" | No it isn’t. A plan with TBD is an incomplete plan. Finish it. |
| "I should just start implementing since the plan is clear in my head" | The plan goes on the card. The operator approves. You wait. This is not optional. |

## Red Flags

- Plan attachment under 3KB (you’re being vague)
- Fewer than 3 tasks in the checklist (you’re bundling too much per task)
- No test plan section
- Tasks without specific file paths and function names
- Starting implementation without approval comment
- No mention of verification commands

## Verification Before Completion

1. Verify plan attachment exists: `kardbrd attachment list <card_id>`
2. Verify "Implementation Tasks" checklist exists with items
3. Verify comment was posted requesting approval
4. Confirm you have NOT started any code changes
5. Only then claim completion
