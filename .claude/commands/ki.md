# Implement

Execute the implementation plan from the card description.

## Instructions

1. Read the card description to find the implementation plan
2. Read all attachments on the card for additional context
3. Implement changes file by file following the plan exactly
4. Run tests after each significant change: `pytest`
5. Run lint: `pre-commit run --all-files`
6. Fix any test failures or lint errors
7. Commit changes with a descriptive message (reference the card ID)
8. Post a comment summarizing what was implemented and any deviations from the plan

## Guidelines

- Follow the plan — don't add scope or refactor unrelated code
- Use existing patterns: `@pytest.mark.asyncio` for async tests, fixtures from `conftest.py`
- Test commands: `pytest` (all tests), `pytest path/to/test_file.py` (single file)
- Lint: `pre-commit run --all-files`
- Commit message format: Brief description of the change
- If the plan is missing or unclear, post a comment asking for clarification instead of guessing
- If tests fail and you can't fix them, report the failure in a comment
