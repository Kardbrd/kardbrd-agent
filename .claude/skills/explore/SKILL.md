---
name: Explore
description: Use when starting work on a new card or when the card topic needs codebase research before planning.
user-invocable: true
---

# Explore

> **Violating the letter of the rules is violating the spirit of the rules.**

Deep-dive exploration producing structured artifacts on the card — not just a comment.

## Outputs (mandatory, every time)

- **Attachment:** `exploration-<topic-slug>.md` — comprehensive analysis with:
  - Executive summary
  - Current state (what exists, with file paths and line numbers)
  - Relevant code paths
  - Architecture constraints and patterns to follow
  - Proposed approach or options with trade-offs
  - Open questions for the operator
- **Checklist:** "Exploration Findings" — one todo per actionable finding or decision point
- **Comment:** Summary of findings + recommendation for next step + @mention requester

## Process

1. Read the card description, all comments, and all attachments
2. Read linked cards and board context if referenced
3. Explore the codebase systematically — don’t just grep for keywords, read the actual code
4. For each relevant file: note the file path, line numbers, and what it does
5. If external resources are linked (repos, docs), fetch and analyze them
6. Draft the exploration attachment with ALL findings — never summarize away detail or omit
7. Upload attachment via `kardbrd attachment markdown <card_id> --filename "exploration-<topic>.md" --content "..."`
8. Create checklist via `kardbrd checklist create <card_id> "Exploration Findings"` then `kardbrd checklist add-todos <card_id> <checklist_id> "Finding 1" "Finding 2" ...`
9. Post summary comment via `kardbrd comment add <card_id> "..."`

## Anti-Rationalization Guards

| Excuse | Why It’s Wrong |
|--------|----------------|
| "I already know enough to plan" | You don’t. Read the code. Every exploration that skips reading produces a plan that misses constraints. |
| "This file isn’t relevant" | If you haven’t read it, you can’t know that. Read first, judge second. |
| "The comment summary is enough" | Comments are communication. The attachment is the artifact. Future skills read the attachment, not your comment. |
| "I’ll note the details in the plan instead" | The plan reads the exploration. If exploration is thin, the plan will be wrong. |
| "There’s too much code to read" | Then prioritize by relevance, but still read the key files. List what you skipped and why. |

## Red Flags

- Posting a comment without creating an attachment
- Exploration attachment under 2KB (you probably skimmed)
- No file paths or line numbers in findings
- No open questions (you’re not thinking critically)
- Skipping linked resources or related cards

## Verification Before Completion

1. Verify the attachment exists: `kardbrd attachment list <card_id>`
2. Verify the checklist exists with items: `kardbrd md card <card_id>` and confirm "Exploration Findings" checklist appears
3. Verify the comment was posted
4. Only then claim completion
