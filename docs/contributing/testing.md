# Testing

## Running tests

```bash
# All tests
uv run pytest

# Single file
uv run pytest kardbrd_agent/tests/test_rules.py

# Specific test
uv run pytest kardbrd_agent/tests/test_integration.py::TestConcurrentProcessingIntegration

# Verbose output
uv run pytest -v

# Stop on first failure
uv run pytest -x
```

## Test architecture

Tests use **pytest** with **pytest-asyncio** for async test support. All async tests are decorated with `@pytest.mark.asyncio`.

### Fixtures

Shared fixtures are defined in `kardbrd_agent/tests/conftest.py`:

| Fixture | Description |
|---------|-------------|
| `git_repo` | Temporary git repository for worktree testing |
| `mock_kardbrd_client` | Mocked kardbrd API client |
| `mock_claude_result` | Pre-built `ExecutorResult` for testing |

### Test files

| File | Coverage |
|------|----------|
| `test_rules.py` | Rule engine matching, condition evaluation, validation |
| `test_executor.py` | Executor Protocol, output parsing, auth checking |
| `test_merge_workflow.py` | Merge state machine, conflict resolution |
| `test_integration.py` | End-to-end flows, concurrent processing |
| `test_worktree.py` | Worktree creation, symlinks, cleanup |
| `test_scheduler.py` | Cron schedule evaluation, card find-or-create |

## Async test patterns

All I/O-bound tests use async/await:

```python
import pytest

@pytest.mark.asyncio
async def test_executor_runs(mock_claude_result):
    result = await executor.execute(prompt="test", cwd=Path("/tmp"))
    assert result.success
```

## Fixtures example

```python
import pytest
from kardbrd_agent.executor import ExecutorResult

@pytest.fixture
def mock_claude_result():
    return ExecutorResult(
        success=True,
        result_text="Task completed successfully",
        session_id="test-session-123",
    )
```

## Pre-commit hooks

Linting and formatting run automatically on commit via pre-commit:

```bash
# Run all hooks manually
uv run pre-commit run --all-files

# Run ruff only
uv run pre-commit run ruff --all-files
```

Hooks include:

- **ruff** — linting and import sorting
- **yamllint** — YAML file validation
- **trailing-whitespace** — removes trailing whitespace
- **end-of-file-fixer** — ensures files end with newline

## Writing new tests

1. Place tests in `kardbrd_agent/tests/`
2. Name test files `test_<module>.py`
3. Use `@pytest.mark.asyncio` for async tests
4. Use fixtures from `conftest.py` where possible
5. Tests must pass before committing (`uv run pytest`)
