---
name: Review
description: Use when implementation is complete and the branch needs code review before merge.
---

# Review

> **Violating the letter of the rules is violating the spirit of the rules.**

Three-stage code review: spec compliance, parallel specialized reviews via subagents, then synthesis.

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
5. Check for scope creep — changes that weren't in the plan
6. **Verdict:** PASS or FAIL with specific items

**If Stage 1 FAILS:** Stop. Post a comment listing the gaps. Do NOT proceed to Stage 2.

## Stage 2: Parallel Specialized Reviews

**Only proceed here if Stage 1 passed.**

Before launching subagents, run `uv run pytest` and `uv run pre-commit run --all-files` so test/lint
results are available as context for the subagents.

Determine which review perspectives are **relevant** to the card's changes. Skip perspectives that
have zero applicability (e.g., skip UX review for a pure backend refactor with no UI impact, skip
mobile UX for changes that don't touch any user-facing surface). If uncertain, include the
perspective — false negatives are worse than false positives.

Launch all relevant review subagents **in parallel** using the `Agent` tool. Each subagent receives
the same diff context (`git diff main...HEAD`) and the card's implementation plan/report.

### Review Perspectives

Each subagent MUST return a structured report with: perspective name, verdict (PASS/FAIL), issues
found (with severity: Critical/Important/Minor), and specific file:line references.

#### UX Review (kru)
**Relevance:** Changes that affect user-visible behavior, workflows, command output, error messages,
or interaction patterns.

Prompt the subagent to review for:
- User workflow impact — do changes break or degrade existing user interactions?
- Error message clarity — are errors actionable and understandable?
- Consistency — do new patterns match existing UX conventions in the codebase?
- Configuration ergonomics — are new options intuitive, well-named, well-documented?
- Output formatting — is CLI/log output readable and well-structured?
- Progressive disclosure — does complexity scale with user need?

#### Mobile UX Review (krup)
**Relevance:** Changes that affect rendering, layout, or interaction on small screens or touch
interfaces. Only applicable when changes touch UI templates, CSS, responsive layouts, or
client-facing web content.

Prompt the subagent to review for:
- Touch target sizing — are interactive elements large enough (min 44x44px)?
- Responsive behavior — do layouts work on narrow viewports (320px+)?
- Scroll and overflow — does content handle small screens without clipping?
- Performance on constrained devices — are assets optimized, lazy-loaded?
- Input handling — do forms work with virtual keyboards and autocomplete?
- Accessibility on mobile — proper ARIA, focus management, screen reader compat?

#### Security Review (krs)
**Relevance:** Always relevant when changes touch: subprocess execution, user input handling, file
system operations, network requests, authentication, token/secret handling, YAML/JSON parsing, or
WebSocket message processing.

Prompt the subagent to review for:
- Injection vectors — command injection via subprocess args, path traversal in file ops
- Secret exposure — tokens, API keys, or credentials in logs, error messages, or comments
- Input validation — WebSocket messages, YAML config, user-supplied paths validated before use
- Dependency security — new dependencies vetted, no known CVEs
- Async safety — race conditions, TOCTOU issues in concurrent operations
- Cleanup — temporary files, worktrees, MCP configs removed on session end
- Permissions — least-privilege principle in file creation, subprocess execution

#### Documentation Review (krd)
**Relevance:** Changes that add new features, change behavior, modify configuration, add/remove CLI
flags, or change API contracts.

Prompt the subagent to review for:
- CLAUDE.md accuracy — does it reflect the current state after changes?
- kardbrd.yml documentation — are new config options documented with examples?
- Code comments — are complex algorithms or non-obvious decisions explained?
- Docstrings — do public functions/classes have accurate docstrings?
- README/changelog — are user-facing changes reflected in docs?
- Inline help — do CLI commands have accurate `--help` text?

#### Code Quality Review (krc)
**Relevance:** Always relevant for any code change.

Prompt the subagent to review for:
- Correctness — does the code do what it claims?
- Style consistency — does it follow existing patterns in the codebase?
- Error handling — are failures handled gracefully? Async patterns correct?
- Edge cases — empty input, missing data, concurrent access, None values
- Naming — are variables, functions, classes named clearly and consistently?
- Complexity — are there unnecessary abstractions, premature generalizations?
- DRY violations — copy-pasted logic that should be shared
- Type safety — proper type hints, no `Any` where specific types are known

#### Test Quality Review (krt)
**Relevance:** Always relevant when changes include new or modified tests, or when new functionality
lacks test coverage.

Prompt the subagent to review for:
- Coverage — are new code paths tested? Are edge cases covered?
- Test correctness — do tests actually verify the right behavior?
- Test isolation — do tests depend on external state, ordering, or side effects?
- Mock accuracy — do mocks reflect real behavior, or do they mask bugs?
- Assertion quality — are assertions specific enough to catch regressions?
- Test naming — do test names describe the scenario and expected outcome?
- Negative tests — are failure modes and error paths tested?
- Async test patterns — proper use of `@pytest.mark.asyncio`, awaited assertions

### Subagent Dispatch

For each relevant perspective, use the `Agent` tool with:
- `description`: the perspective name (e.g., "Security review (krs)", "UX review (kru)")
- `prompt`: Include the full review criteria above, the diff context, implementation plan summary,
  and instruction to return a structured report

**Launch all relevant subagents in a single message** so they run in parallel.

Example dispatch pattern:
```
Agent({
  description: "Security review (krs)",
  prompt: "You are a security reviewer. Review the following changes for...[criteria]...\n\n
    Diff:\n[git diff output]\n\nReturn: perspective, verdict, issues with severity + file:line."
})
Agent({
  description: "Code quality review (krc)",
  prompt: "You are a code quality reviewer. Review the following changes for...[criteria]...\n\n
    Diff:\n[git diff output]\n\nReturn: perspective, verdict, issues with severity + file:line."
})
```

## Stage 3: Synthesis (GATE 2)

Collect all subagent reports and synthesize into a unified review.

1. Merge all issues into a single list, deduplicating across perspectives
2. Assign final severity to each issue:
   - **Critical:** Bugs, security vulnerabilities, data loss risks, broken functionality → Must fix
   - **Important:** Architecture problems, missing edge cases, inadequate tests → Should fix
   - **Minor:** Style nits, optimization opportunities, documentation → Can fix later
3. Determine overall verdict based on the most severe finding across all perspectives

## Outputs (mandatory, every time)

- **Attachment:** `code-review.md` — full structured review with:
  - Stage 1 results (spec compliance)
  - Per-perspective reports from Stage 2 (each subagent's findings)
  - Synthesized issue list with severity classification
  - File-by-file review notes
- **Checklist:** "Review Issues" — one todo per issue found (if any)
- **Comment:** Verdict + summary with perspective table:

```
| Perspective | Verdict | Issues |
|-------------|---------|--------|
| Security (krs) | PASS | 0 |
| Code Quality (krc) | FAIL | 2 critical |
| Test Quality (krt) | PASS | 1 minor |
```

Followed by one of:
  - **Approve:** "No blocking issues. Ready to merge."
  - **Request changes:** "Found N issues (X critical, Y important). See review attachment."
  - **Needs discussion:** "Architectural questions need human input. See review attachment."

## Anti-Rationalization Guards

| Excuse | Why It's Wrong |
|--------|----------------|
| "The code looks fine at a glance" | Read every changed line. A glance is not a review. |
| "Tests pass, so it works" | Tests can be wrong. Tests can be incomplete. Read the tests too. |
| "I wrote this code, I know it's correct" | Self-review is not review. Pretend you've never seen this code. |
| "This is a minor change, full review is overkill" | Minor changes cause major bugs. The review process is the same regardless of size. |
| "Stage 1 basically passed, I can skip straight to Stage 2" | "Basically passed" is not passed. List the specific gaps. |
| "I'll note the issues but approve anyway" | If there are Critical or Important issues, the verdict is Request Changes. Period. |
| "This perspective isn't relevant, I'll skip it" | If in doubt, include it. Justify every skip with specific reasoning in the review. |
| "I'll run the subagents sequentially to be safe" | Launch ALL relevant subagents in parallel. Sequential defeats the purpose. |

## Red Flags

- Approving with unresolved Critical or Important issues
- Skipping Stage 1 and going straight to specialized reviews
- Review attachment under 2KB (you skimmed)
- Not running pytest and pre-commit during review
- Running fewer than 2 review perspectives (at minimum, code quality + one other)
- Running subagents sequentially instead of in parallel
- Subagent report missing severity classifications or file:line references

## Verification Before Completion

1. Verify code-review.md attachment exists
2. Verify Stage 1 was completed
3. Verify all relevant specialized reviews were dispatched in parallel
4. Verify synthesized issue list covers all subagent findings
5. Verify pytest and pre-commit were run freshly during review
6. Verify comment with verdict and perspective summary table was posted
7. Only then claim completion
