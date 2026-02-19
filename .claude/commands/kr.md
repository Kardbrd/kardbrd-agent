# Review

Full code review of changes on the card's branch.

## Instructions

1. Read the card description and comments for context
2. Check `git diff main...HEAD` to see all changes on this branch
3. Review each changed file for:
   - **Correctness**: Does the code do what the card describes?
   - **Tests**: Are there adequate tests? Run `pytest` to verify they pass
   - **Style**: Does it follow existing patterns? Run `pre-commit run --all-files`
   - **Security**: Check for subprocess injection, credential exposure, WebSocket auth issues
   - **Error handling**: Are failures handled gracefully? Are async patterns correct?
4. Post a structured review comment with:
   - Summary of changes
   - Issues found (if any), categorized by severity
   - Suggestions for improvement
   - Verdict: approve, request changes, or needs discussion

## Security Checklist (kardbrd-agent specific)

- [ ] No shell injection via `asyncio.create_subprocess_exec` args
- [ ] Bot tokens not logged or exposed in error messages
- [ ] Temporary files (MCP configs) cleaned up properly
- [ ] WebSocket messages validated before processing
- [ ] No unbounded data in prompts or comments
- [ ] Worktree paths sanitized (no path traversal)

## Guidelines

- Be constructive — suggest fixes, not just problems
- Reference specific lines when commenting on code
- Check that new code has corresponding tests
- Verify the branch is clean: no debug prints, no commented-out code
