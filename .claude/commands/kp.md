# Plan

Create a detailed implementation plan based on exploration findings.

## Instructions

1. Read the card description, all comments, and any exploration findings
2. Read all files that will be modified to understand current state
3. Create a plan that covers:
   - **Context**: What problem this solves and why
   - **Changes**: File-by-file breakdown of modifications
   - **New files**: Any new files to create (with rationale)
   - **Tests**: What tests to add or modify
   - **Verification**: How to confirm the changes work (`pytest`, `pre-commit run --all-files`)
4. Update the card description with the plan using `mcp__kardbrd__update_card`
5. Post a summary comment on the card

## Guidelines

- Plans should be detailed enough that Sonnet can implement them without ambiguity
- Include specific function signatures, class names, and file paths
- Reference existing patterns in the codebase to follow
- Note any dependencies or ordering constraints between changes
- Include the exact test commands: `pytest` for tests, `pre-commit run --all-files` for lint
- Keep the plan focused — don't over-engineer or add unnecessary scope
