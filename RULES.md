# Rules

## Code Conventions

- **Async-first**: All I/O operations use `async/await`. Use `asyncio.create_subprocess_exec` for subprocesses, never `subprocess.run`.
- **Protocol over inheritance**: New executor types implement the `Executor` Protocol — no shared base class. Register via `executor_type` config, not conditional imports.
- **Dataclasses for data**: Use `@dataclass` for structured data. Avoid plain dicts for domain objects.
- **Type hints everywhere**: Python 3.12+ style. Use `X | None` not `Optional[X]`.
- **Logging**: Module-level `logger = logging.getLogger("kardbrd_agent")`. Never `print()`.
- **Line length**: 100 characters (ruff enforced).
- **Imports**: Sorted by ruff (isort rules). Standard library, third-party, local.

## Testing

- All tests: `uv run pytest`
- Single file: `uv run pytest kardbrd_agent/tests/test_<module>.py`
- Lint and format: `uv run pre-commit run --all-files`
- All async tests use `@pytest.mark.asyncio`
- Use fixtures from `conftest.py`: `git_repo`, `mock_kardbrd_client`, `mock_claude_result`
- Tests must pass before committing. If they don't, fix the issue or report it.

## Git Workflow

- Work on card-scoped branches (created automatically via worktree)
- Commit messages: brief verb-phrase description of the change
- One logical change per commit — don't bundle unrelated work
- Never force-push or amend commits on shared branches

## Architecture Guards

- `kardbrd.yml` is the single source of truth for automation rules — don't hardcode event handling logic
- Executor factory pattern: new executors register via `executor_type` config, not conditional imports
- MCP runs as stdio subprocess per session — no shared server state
- Worktrees are ephemeral — don't store persistent state in them
- Card ID (`public_id`) is the universal key for session tracking
- WebSocket event flow: event → `RuleEngine.match()` → matched rules → spawn executor or `__stop__`
- Concurrency managed via `asyncio.Semaphore` with per-card session tracking to prevent duplicates

## Security

- Never log or expose bot tokens, API keys, or credentials in error messages or comments
- Sanitize worktree paths — no path traversal
- Validate WebSocket message structure before processing
- Use `asyncio.create_subprocess_exec` (not `shell=True`) to prevent injection
- Clean up temporary files (MCP configs, worktrees) on session end

## What NOT to Do

- Don't refactor code outside the scope of the current card
- Don't add dependencies without explicit approval
- Don't modify `kardbrd.yml` unless the card specifically asks for it
- Don't skip pre-commit hooks or lint checks
